package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	flowcv1alpha1 "github.com/flowc-labs/flowc/api/v1alpha1"
)

const (
	conditionAPIResolved      = "APIResolved"
	conditionGatewayResolved  = "GatewayResolved"
	conditionListenerResolved = "ListenerResolved"

	phaseDeployed = "Deployed"

	reasonAPIRefMissing      = "APIRefMissing"
	reasonAPINotFound        = "APINotFound"
	reasonAPINotReady        = "APINotReady"
	reasonAPIResolved        = "APIResolved"
	reasonGatewayRefMissing  = "GatewayRefMissing"
	reasonGatewayNotFound    = "GatewayNotFound"
	reasonGatewayNotAccepted = "GatewayNotAccepted"
	reasonGatewayResolved    = "GatewayResolved"
	reasonListenerNotFound   = "ListenerNotFound"
	reasonListenerAmbiguous  = "ListenerAmbiguous"
	reasonListenerNoneForGW  = "NoListenersForGateway"
	reasonListenerWrongGW    = "ListenerTargetsDifferentGateway"
	reasonListenerResolved   = "ListenerResolved"
	reasonDeploymentReady    = "Deployed"
	reasonDeploymentBlocked  = "DependenciesNotReady"
	reasonDeploymentInvalid  = "InvalidSpec"
)

// DeploymentReconciler validates Deployment CRs that bind an API to a
// Gateway+Listener. Resolution of the referenced API/Gateway/Listener happens
// here so the xDS reconciler can take a Deployment from the store at face
// value once status.phase == Deployed.
//
// No downstream resources are owned: the xDS reconciler is what eventually
// projects this binding into Envoy config.
type DeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// NewDeploymentReconciler is the canonical constructor; mirrors
// NewAPIReconciler so SetupAll wires both the same way.
func NewDeploymentReconciler(c client.Client, scheme *runtime.Scheme) *DeploymentReconciler {
	return &DeploymentReconciler{Client: c, Scheme: scheme}
}

// +kubebuilder:rbac:groups=flowc.io,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=deployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=apis;gateways;listeners,verbs=get;list;watch

// Reconcile validates the Deployment and writes a status snapshot. Deletion
// is a no-op — there's nothing to clean up here; the xDS reconciler observes
// the store delete and removes the corresponding Envoy resources.
func (r *DeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var dep flowcv1alpha1.Deployment
	if err := r.Get(ctx, req.NamespacedName, &dep); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !dep.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	desired := r.deriveStatus(ctx, &dep)
	if deploymentStatusEqual(dep.Status, desired) {
		return ctrl.Result{}, nil
	}
	dep.Status = desired
	if err := r.Status().Update(ctx, &dep); err != nil {
		log.Error(err, "Failed to update Deployment status", "name", dep.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers the Deployment controller and the watches that
// make ref changes re-validate dependent Deployments.
func (r *DeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("deployment").
		For(&flowcv1alpha1.Deployment{}).
		Watches(&flowcv1alpha1.API{}, handler.EnqueueRequestsFromMapFunc(r.apiToDeployments)).
		Watches(&flowcv1alpha1.Gateway{}, handler.EnqueueRequestsFromMapFunc(r.gatewayToDeployments)).
		Watches(&flowcv1alpha1.Listener{}, handler.EnqueueRequestsFromMapFunc(r.listenerToDeployments)).
		Complete(r)
}

// apiToDeployments enqueues every Deployment in the same namespace whose
// spec.apiRef names the changed API.
func (r *DeploymentReconciler) apiToDeployments(ctx context.Context, obj client.Object) []ctrl.Request {
	api, ok := obj.(*flowcv1alpha1.API)
	if !ok {
		return nil
	}
	return r.deploymentsMatching(ctx, api.Namespace, func(d *flowcv1alpha1.Deployment) bool {
		return d.Spec.APIRef == api.Name
	})
}

// gatewayToDeployments enqueues Deployments referencing the changed Gateway.
func (r *DeploymentReconciler) gatewayToDeployments(ctx context.Context, obj client.Object) []ctrl.Request {
	gw, ok := obj.(*flowcv1alpha1.Gateway)
	if !ok {
		return nil
	}
	return r.deploymentsMatching(ctx, gw.Namespace, func(d *flowcv1alpha1.Deployment) bool {
		return d.Spec.Gateway.Name == gw.Name
	})
}

// listenerToDeployments enqueues Deployments whose listener resolution could
// change as a result of this Listener event. That includes deployments that
// name this listener explicitly *and* deployments that auto-resolve against
// the same gateway (because adding/removing a listener can flip the
// auto-resolve outcome between unique / missing / ambiguous).
func (r *DeploymentReconciler) listenerToDeployments(ctx context.Context, obj client.Object) []ctrl.Request {
	l, ok := obj.(*flowcv1alpha1.Listener)
	if !ok || l.Spec.GatewayRef == "" {
		return nil
	}
	return r.deploymentsMatching(ctx, l.Namespace, func(d *flowcv1alpha1.Deployment) bool {
		if d.Spec.Gateway.Name != l.Spec.GatewayRef {
			return false
		}
		// Explicit name match, or auto-resolve mode (listener field empty).
		return d.Spec.Gateway.Listener == "" || d.Spec.Gateway.Listener == l.Name
	})
}

// deploymentsMatching is the shared filter+enqueue used by every watch.
// Errors are logged but swallowed: a missed enqueue gets corrected on the
// next periodic resync.
func (r *DeploymentReconciler) deploymentsMatching(ctx context.Context, namespace string, pred func(*flowcv1alpha1.Deployment) bool) []ctrl.Request {
	var list flowcv1alpha1.DeploymentList
	if err := r.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		logf.FromContext(ctx).Error(err, "List Deployments for watch fan-out failed", "namespace", namespace)
		return nil
	}
	var out []ctrl.Request
	for i := range list.Items {
		d := &list.Items[i]
		if !pred(d) {
			continue
		}
		out = append(out, ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: d.Namespace, Name: d.Name},
		})
	}
	return out
}

// deriveStatus runs all checks and folds them into a fresh DeploymentStatus.
// xdsSnapshotVersion is preserved from the existing status so we don't stomp
// on what the xDS reconciler wrote.
func (r *DeploymentReconciler) deriveStatus(ctx context.Context, dep *flowcv1alpha1.Deployment) flowcv1alpha1.DeploymentStatus {
	out := flowcv1alpha1.DeploymentStatus{
		Phase:              phasePending,
		Conditions:         dep.Status.Conditions,
		XDSSnapshotVersion: dep.Status.XDSSnapshotVersion,
	}

	// 1. Field validation.
	if err := validateDeploymentSpec(&dep.Spec); err != nil {
		out.Phase = phaseFailed
		out.Conditions = setCondition(out.Conditions, falseCond(flowcv1alpha1.ConditionAccepted, reasonDeploymentInvalid, err.Error()))
		out.Conditions = setCondition(out.Conditions, falseCond(flowcv1alpha1.ConditionReady, reasonDeploymentInvalid, err.Error()))
		return out
	}
	out.Conditions = setCondition(out.Conditions, trueCond(flowcv1alpha1.ConditionAccepted, reasonAccepted, "Spec fields validated"))

	// 2. Resolve API.
	apiCond := r.resolveAPI(ctx, dep)
	out.Conditions = setCondition(out.Conditions, apiCond)

	// 3. Resolve Gateway.
	gwCond := r.resolveGateway(ctx, dep)
	out.Conditions = setCondition(out.Conditions, gwCond)

	// 4. Resolve Listener (depends on Gateway being known, but we still try
	//    even if Gateway lookup failed so the user sees both gaps at once).
	lstCond := r.resolveListener(ctx, dep)
	out.Conditions = setCondition(out.Conditions, lstCond)

	// 5. Roll up Ready.
	allTrue := apiCond.Status == metav1.ConditionTrue &&
		gwCond.Status == metav1.ConditionTrue &&
		lstCond.Status == metav1.ConditionTrue
	if allTrue {
		out.Phase = phaseDeployed
		out.Conditions = setCondition(out.Conditions, trueCond(flowcv1alpha1.ConditionReady, reasonDeploymentReady, "All references resolved"))
	} else {
		out.Phase = phasePending
		out.Conditions = setCondition(out.Conditions, falseCond(flowcv1alpha1.ConditionReady, reasonDeploymentBlocked, summarizeBlockers(apiCond, gwCond, lstCond)))
	}
	return out
}

func validateDeploymentSpec(spec *flowcv1alpha1.DeploymentSpec) error {
	if strings.TrimSpace(spec.APIRef) == "" {
		return errors.New("spec.apiRef is required")
	}
	if strings.TrimSpace(spec.Gateway.Name) == "" {
		return errors.New("spec.gateway.name is required")
	}
	return nil
}

// resolveAPI fetches the referenced API and decides the APIResolved condition.
func (r *DeploymentReconciler) resolveAPI(ctx context.Context, dep *flowcv1alpha1.Deployment) metav1.Condition {
	if dep.Spec.APIRef == "" {
		return falseCond(conditionAPIResolved, reasonAPIRefMissing, "spec.apiRef is empty")
	}
	var api flowcv1alpha1.API
	err := r.Get(ctx, types.NamespacedName{Namespace: dep.Namespace, Name: dep.Spec.APIRef}, &api)
	switch {
	case apierrors.IsNotFound(err):
		return falseCond(conditionAPIResolved, reasonAPINotFound, fmt.Sprintf("API %q not found in namespace %q", dep.Spec.APIRef, dep.Namespace))
	case err != nil:
		return falseCond(conditionAPIResolved, reasonAPINotFound, fmt.Sprintf("get API %q: %v", dep.Spec.APIRef, err))
	}
	if !readyTrue(api.Status.Phase, api.Status.Conditions) {
		return falseCond(conditionAPIResolved, reasonAPINotReady, fmt.Sprintf("API %q is not Ready (phase=%q)", api.Name, api.Status.Phase))
	}
	return trueCond(conditionAPIResolved, reasonAPIResolved, fmt.Sprintf("API %q is Ready", api.Name))
}

// resolveGateway fetches the referenced Gateway and decides the
// GatewayResolved condition. Gates on Gateway.Accepted (spec valid), not
// Gateway.Ready (Envoy pod up): xDS translation only needs spec validity, and
// gating on Ready creates a startup race where Listeners briefly disappear
// from the snapshot while Gateway.Ready flips on Envoy boot.
func (r *DeploymentReconciler) resolveGateway(ctx context.Context, dep *flowcv1alpha1.Deployment) metav1.Condition {
	if dep.Spec.Gateway.Name == "" {
		return falseCond(conditionGatewayResolved, reasonGatewayRefMissing, "spec.gateway.name is empty")
	}
	var gw flowcv1alpha1.Gateway
	err := r.Get(ctx, types.NamespacedName{Namespace: dep.Namespace, Name: dep.Spec.Gateway.Name}, &gw)
	switch {
	case apierrors.IsNotFound(err):
		return falseCond(conditionGatewayResolved, reasonGatewayNotFound, fmt.Sprintf("Gateway %q not found in namespace %q", dep.Spec.Gateway.Name, dep.Namespace))
	case err != nil:
		return falseCond(conditionGatewayResolved, reasonGatewayNotFound, fmt.Sprintf("get Gateway %q: %v", dep.Spec.Gateway.Name, err))
	}
	if !acceptedTrue(gw.Status.Conditions) {
		return falseCond(conditionGatewayResolved, reasonGatewayNotAccepted, fmt.Sprintf("Gateway %q is not Accepted", gw.Name))
	}
	return trueCond(conditionGatewayResolved, reasonGatewayResolved, fmt.Sprintf("Gateway %q is Accepted", gw.Name))
}

// resolveListener handles both modes: an explicit listener name, or
// auto-resolution when the Gateway has exactly one Listener. Returns the
// ListenerResolved condition.
func (r *DeploymentReconciler) resolveListener(ctx context.Context, dep *flowcv1alpha1.Deployment) metav1.Condition {
	gwName := dep.Spec.Gateway.Name
	if gwName == "" {
		return falseCond(conditionListenerResolved, reasonGatewayRefMissing, "cannot resolve listener without spec.gateway.name")
	}

	if explicit := dep.Spec.Gateway.Listener; explicit != "" {
		var l flowcv1alpha1.Listener
		err := r.Get(ctx, types.NamespacedName{Namespace: dep.Namespace, Name: explicit}, &l)
		switch {
		case apierrors.IsNotFound(err):
			return falseCond(conditionListenerResolved, reasonListenerNotFound, fmt.Sprintf("Listener %q not found in namespace %q", explicit, dep.Namespace))
		case err != nil:
			return falseCond(conditionListenerResolved, reasonListenerNotFound, fmt.Sprintf("get Listener %q: %v", explicit, err))
		}
		if l.Spec.GatewayRef != gwName {
			return falseCond(conditionListenerResolved, reasonListenerWrongGW, fmt.Sprintf("Listener %q targets Gateway %q, not %q", explicit, l.Spec.GatewayRef, gwName))
		}
		return trueCond(conditionListenerResolved, reasonListenerResolved, fmt.Sprintf("Listener %q resolved", explicit))
	}

	// Auto-resolve: list listeners in this namespace targeting our gateway.
	var listeners flowcv1alpha1.ListenerList
	if err := r.List(ctx, &listeners, client.InNamespace(dep.Namespace)); err != nil {
		return falseCond(conditionListenerResolved, reasonListenerNotFound, fmt.Sprintf("list Listeners: %v", err))
	}
	var matches []string
	for i := range listeners.Items {
		if listeners.Items[i].Spec.GatewayRef == gwName {
			matches = append(matches, listeners.Items[i].Name)
		}
	}
	switch len(matches) {
	case 0:
		return falseCond(conditionListenerResolved, reasonListenerNoneForGW, fmt.Sprintf("Gateway %q has no Listeners", gwName))
	case 1:
		return trueCond(conditionListenerResolved, reasonListenerResolved, fmt.Sprintf("Auto-resolved to Listener %q", matches[0]))
	default:
		return falseCond(conditionListenerResolved, reasonListenerAmbiguous, fmt.Sprintf("Gateway %q has %d Listeners (%s); set spec.gateway.listener to disambiguate", gwName, len(matches), strings.Join(matches, ", ")))
	}
}

// readyTrue is the rule "this referenced resource is fit to depend on": the
// owning controller has marked it Ready, both via .status.phase and the Ready
// condition. Both checks are required because phase alone is a coarse summary
// and conditions[Ready] is the source of truth.
func readyTrue(phase string, conditions []metav1.Condition) bool {
	if phase != phaseReady {
		return false
	}
	for _, c := range conditions {
		if c.Type == flowcv1alpha1.ConditionReady {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

// acceptedTrue is the rule "this referenced resource has a valid spec". Use
// this when only spec-level dependencies matter (xDS translation, ref
// resolution) — Gateway.Accepted flips True as soon as the spec validates,
// without waiting for the data plane (Envoy pod) to come up.
func acceptedTrue(conditions []metav1.Condition) bool {
	for _, c := range conditions {
		if c.Type == flowcv1alpha1.ConditionAccepted {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func summarizeBlockers(conds ...metav1.Condition) string {
	var blockers []string
	for _, c := range conds {
		if c.Status != metav1.ConditionTrue {
			blockers = append(blockers, fmt.Sprintf("%s=%s", c.Type, c.Reason))
		}
	}
	if len(blockers) == 0 {
		return "no blockers"
	}
	return strings.Join(blockers, ", ")
}

// trueCond / falseCond are tiny wrappers so the per-step code reads as a
// list of decisions rather than condition struct boilerplate.
func trueCond(typ, reason, msg string) metav1.Condition {
	return metav1.Condition{Type: typ, Status: metav1.ConditionTrue, Reason: reason, Message: msg}
}

func falseCond(typ, reason, msg string) metav1.Condition {
	return metav1.Condition{Type: typ, Status: metav1.ConditionFalse, Reason: reason, Message: msg}
}

// deploymentStatusEqual mirrors statusEqual / apiStatusEqual. xDS snapshot
// version is part of the comparison so a no-op write doesn't churn the API.
func deploymentStatusEqual(a, b flowcv1alpha1.DeploymentStatus) bool {
	if a.Phase != b.Phase || a.XDSSnapshotVersion != b.XDSSnapshotVersion {
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
	return true
}
