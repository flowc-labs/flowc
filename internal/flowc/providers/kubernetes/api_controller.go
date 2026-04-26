package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	flowcv1alpha1 "github.com/flowc-labs/flowc/api/v1alpha1"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
)

const (
	conditionSpecParsed = "SpecParsed"

	reasonAccepted          = "Validated"
	reasonInvalidSpec       = "InvalidSpec"
	reasonParsed            = "Parsed"
	reasonParseError        = "ParseError"
	reasonNoSpecContent     = "NoSpecContent"
	reasonUnsupportedType   = "UnsupportedAPIType"
	reasonPolicyRefMissing  = "PolicyRefMissing"
	reasonPoliciesResolved  = "PoliciesResolved"
	reasonAPIReady          = "APIReady"
	reasonAPIDependencyMiss = "DependencyMissing"

	apiKind = "API"
)

// APIReconciler validates flowc API CRs and writes status + conditions back
// to the API. It owns no downstream resources — the K8s store still picks up
// every API CR via informers, but informer consumers (e.g. the xDS reconciler)
// gate on .status to decide whether the API is fit to project into Envoy
// config. Running here (leader-only) keeps validation work off the replicas.
type APIReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// parsers translates spec.specContent into ir.API based on spec.apiType.
	// Built once at construction so program caches inside parsers warm up
	// across reconciles.
	parsers *ir.ParserRegistry
}

// NewAPIReconciler constructs an APIReconciler with the default parser
// registry (OpenAPI today; gRPC/GraphQL/AsyncAPI stubs in future).
func NewAPIReconciler(c client.Client, scheme *runtime.Scheme) *APIReconciler {
	return &APIReconciler{
		Client:  c,
		Scheme:  scheme,
		parsers: ir.DefaultParserRegistry(),
	}
}

// +kubebuilder:rbac:groups=flowc.io,resources=apis,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=apis/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=apipolicies,verbs=get;list;watch

// Reconcile validates the API CR and writes a status snapshot. Deletion is a
// no-op: there are no owned resources, and the CR's status disappears with it.
func (r *APIReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var api flowcv1alpha1.API
	if err := r.Get(ctx, req.NamespacedName, &api); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !api.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	desired := r.deriveStatus(ctx, &api)

	if apiStatusEqual(api.Status, desired) {
		return ctrl.Result{}, nil
	}
	api.Status = desired
	if err := r.Status().Update(ctx, &api); err != nil {
		log.Error(err, "Failed to update API status", "name", api.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers the API controller and watches APIPolicy CRs so
// changes to a policy targeting an API re-validate the API.
func (r *APIReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("api").
		For(&flowcv1alpha1.API{}).
		Watches(
			&flowcv1alpha1.APIPolicy{},
			handler.EnqueueRequestsFromMapFunc(r.apiPolicyToAPI),
		).
		Complete(r)
}

// apiPolicyToAPI maps an APIPolicy event back to the API named by its
// targetRef. APIPolicies targeting other kinds (Deployment, etc.) are ignored.
func (r *APIReconciler) apiPolicyToAPI(_ context.Context, obj client.Object) []ctrl.Request {
	p, ok := obj.(*flowcv1alpha1.APIPolicy)
	if !ok || p.Spec.TargetRef.Kind != apiKind || p.Spec.TargetRef.Name == "" {
		return nil
	}
	return []ctrl.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: p.Namespace,
			Name:      p.Spec.TargetRef.Name,
		},
	}}
}

// deriveStatus runs all validation steps and folds them into a single
// APIStatus value. Pure aside from the targeted-policy lookup.
func (r *APIReconciler) deriveStatus(ctx context.Context, api *flowcv1alpha1.API) flowcv1alpha1.APIStatus {
	out := flowcv1alpha1.APIStatus{
		Phase:      phasePending,
		Conditions: api.Status.Conditions,
	}

	// 1. Field validation.
	if err := validateAPISpec(&api.Spec); err != nil {
		out.Phase = phaseFailed
		out.Conditions = setCondition(out.Conditions, metav1.Condition{
			Type:    flowcv1alpha1.ConditionAccepted,
			Status:  metav1.ConditionFalse,
			Reason:  reasonInvalidSpec,
			Message: err.Error(),
		})
		out.Conditions = setCondition(out.Conditions, metav1.Condition{
			Type:    flowcv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  reasonInvalidSpec,
			Message: err.Error(),
		})
		return out
	}
	out.Conditions = setCondition(out.Conditions, metav1.Condition{
		Type:    flowcv1alpha1.ConditionAccepted,
		Status:  metav1.ConditionTrue,
		Reason:  reasonAccepted,
		Message: "Spec fields validated",
	})

	// 2. Spec parsing (only when content is supplied).
	parsed, parseCond := r.parseSpec(ctx, &api.Spec)
	out.Conditions = setCondition(out.Conditions, parseCond)
	if parsed != nil {
		out.ParsedInfo = parsed
	}

	// 3. Cross-ref: APIPolicies targeting this API must all be Accepted.
	policyMsg, policyOK, err := r.checkTargetingPolicies(ctx, api)
	if err != nil {
		out.Phase = phasePending
		out.Conditions = setCondition(out.Conditions, metav1.Condition{
			Type:    flowcv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  reasonAPIDependencyMiss,
			Message: fmt.Sprintf("Failed to list APIPolicies: %v", err),
		})
		return out
	}

	// 4. Roll up Ready.
	parseFatal := parseCond.Status == metav1.ConditionFalse && parseCond.Reason == reasonParseError
	switch {
	case parseFatal:
		out.Phase = phaseFailed
		out.Conditions = setCondition(out.Conditions, metav1.Condition{
			Type:    flowcv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  reasonParseError,
			Message: parseCond.Message,
		})
	case !policyOK:
		out.Phase = phasePending
		out.Conditions = setCondition(out.Conditions, metav1.Condition{
			Type:    flowcv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  reasonPolicyRefMissing,
			Message: policyMsg,
		})
	default:
		out.Phase = phaseReady
		out.Conditions = setCondition(out.Conditions, metav1.Condition{
			Type:    flowcv1alpha1.ConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  reasonAPIReady,
			Message: policyMsg,
		})
	}
	return out
}

// validateAPISpec checks the structural requirements that admission webhooks
// don't already guarantee. Kubebuilder markers handle enums and bounds; this
// catches semantic gaps like duplicate policy IDs.
func validateAPISpec(spec *flowcv1alpha1.APISpec) error {
	if strings.TrimSpace(spec.Version) == "" {
		return errors.New("spec.version is required")
	}
	if strings.TrimSpace(spec.Context) == "" {
		return errors.New("spec.context is required")
	}
	if !strings.HasPrefix(spec.Context, "/") {
		return fmt.Errorf("spec.context must start with '/' (got %q)", spec.Context)
	}
	if strings.TrimSpace(spec.Upstream.Host) == "" {
		return errors.New("spec.upstream.host is required")
	}
	if spec.Upstream.Port == 0 || spec.Upstream.Port > 65535 {
		return fmt.Errorf("spec.upstream.port must be in [1, 65535] (got %d)", spec.Upstream.Port)
	}

	seen := make(map[string]struct{}, len(spec.PolicyChain))
	for i, p := range spec.PolicyChain {
		if strings.TrimSpace(p.ID) == "" {
			return fmt.Errorf("spec.policyChain[%d].id is required", i)
		}
		if strings.TrimSpace(p.PolicyType) == "" {
			return fmt.Errorf("spec.policyChain[%d].policyType is required", i)
		}
		if _, dup := seen[p.ID]; dup {
			return fmt.Errorf("spec.policyChain has duplicate id %q", p.ID)
		}
		seen[p.ID] = struct{}{}
	}
	return nil
}

// parseSpec attempts to parse spec.specContent through the IR parser
// registry. Returns the derived ParsedInfo (nil on any non-success outcome)
// and a SpecParsed condition describing what happened.
func (r *APIReconciler) parseSpec(ctx context.Context, spec *flowcv1alpha1.APISpec) (*flowcv1alpha1.ParsedInfo, metav1.Condition) {
	if strings.TrimSpace(spec.SpecContent) == "" {
		return nil, metav1.Condition{
			Type:    conditionSpecParsed,
			Status:  metav1.ConditionTrue,
			Reason:  reasonNoSpecContent,
			Message: "No spec content supplied",
		}
	}

	apiType := ir.APIType(spec.APIType)
	if apiType == "" {
		// Auto-detect placeholder: only OpenAPI is wired today, so REST is
		// the only sensible default. Replace with real sniffing once more
		// parsers register.
		apiType = ir.APITypeREST
	}

	parser, err := r.parsers.GetParser(apiType)
	if err != nil {
		return nil, metav1.Condition{
			Type:    conditionSpecParsed,
			Status:  metav1.ConditionFalse,
			Reason:  reasonUnsupportedType,
			Message: fmt.Sprintf("No parser registered for apiType %q", apiType),
		}
	}

	parsed, err := parser.Parse(ctx, []byte(spec.SpecContent))
	if err != nil {
		return nil, metav1.Condition{
			Type:    conditionSpecParsed,
			Status:  metav1.ConditionFalse,
			Reason:  reasonParseError,
			Message: err.Error(),
		}
	}

	info := parsedInfoFromIR(parsed)
	return info, metav1.Condition{
		Type:    conditionSpecParsed,
		Status:  metav1.ConditionTrue,
		Reason:  reasonParsed,
		Message: fmt.Sprintf("Parsed %d endpoint(s)", len(parsed.Endpoints)),
	}
}

// parsedInfoFromIR projects the rich ir.API down to the small status snapshot
// we expose on the API CR. Paths are deduplicated; servers are URL-only.
func parsedInfoFromIR(api *ir.API) *flowcv1alpha1.ParsedInfo {
	info := &flowcv1alpha1.ParsedInfo{
		Title:   api.Metadata.Title,
		Version: api.Metadata.Version,
	}
	if info.Title == "" {
		info.Title = api.Metadata.Name
	}

	seen := make(map[string]struct{}, len(api.Endpoints))
	for _, ep := range api.Endpoints {
		p := ep.Path.Pattern
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		info.Paths = append(info.Paths, p)
	}

	for _, s := range api.Servers {
		if s.URL != "" {
			info.Servers = append(info.Servers, s.URL)
		}
	}
	return info
}

// checkTargetingPolicies lists APIPolicies in the API's namespace and
// inspects those whose targetRef points at this API. Returns:
//   - msg: human-readable summary for the Ready condition
//   - ok:  true when every targeting policy is Accepted (or there are none)
//   - err: non-nil only on List failure
func (r *APIReconciler) checkTargetingPolicies(ctx context.Context, api *flowcv1alpha1.API) (string, bool, error) {
	var policies flowcv1alpha1.APIPolicyList
	if err := r.List(ctx, &policies, client.InNamespace(api.Namespace)); err != nil {
		return "", false, err
	}

	var (
		matched   int
		unhealthy []string
	)
	for i := range policies.Items {
		p := &policies.Items[i]
		if p.Spec.TargetRef.Kind != apiKind || p.Spec.TargetRef.Name != api.Name {
			continue
		}
		matched++
		if !apiPolicyAccepted(p) {
			unhealthy = append(unhealthy, p.Name)
		}
	}

	switch {
	case matched == 0:
		return "No APIPolicies target this API", true, nil
	case len(unhealthy) > 0:
		return fmt.Sprintf("%d/%d targeting APIPolicies not Accepted: %s", len(unhealthy), matched, strings.Join(unhealthy, ", ")), false, nil
	default:
		return fmt.Sprintf("%d targeting APIPolicies Accepted", matched), true, nil
	}
}

// apiPolicyAccepted treats a policy as healthy when it carries no Ready/
// Accepted condition (controller hasn't run yet — give it benefit of the
// doubt) or the condition is True.
func apiPolicyAccepted(p *flowcv1alpha1.APIPolicy) bool {
	if len(p.Status.Conditions) == 0 {
		return true
	}
	for _, c := range p.Status.Conditions {
		if c.Type != flowcv1alpha1.ConditionAccepted && c.Type != flowcv1alpha1.ConditionReady {
			continue
		}
		if c.Status == metav1.ConditionFalse {
			return false
		}
	}
	return true
}

// apiStatusEqual is the equivalent of statusEqual for APIStatus. ParsedInfo
// is compared field-by-field; nil vs. empty-but-non-nil are treated as equal
// to avoid pointless writes.
func apiStatusEqual(a, b flowcv1alpha1.APIStatus) bool {
	if a.Phase != b.Phase {
		return false
	}
	if len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for i := range a.Conditions {
		ac, bc := a.Conditions[i], b.Conditions[i]
		if ac.Type != bc.Type || ac.Status != bc.Status || ac.Reason != bc.Reason || ac.Message != bc.Message {
			return false
		}
	}
	return parsedInfoEqual(a.ParsedInfo, b.ParsedInfo)
}

func parsedInfoEqual(a, b *flowcv1alpha1.ParsedInfo) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Title != b.Title || a.Version != b.Version {
		return false
	}
	return stringSlicesEqual(a.Paths, b.Paths) && stringSlicesEqual(a.Servers, b.Servers)
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
