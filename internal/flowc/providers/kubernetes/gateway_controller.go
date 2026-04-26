package kubernetes

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	phasePending      = "Pending"
	phaseProvisioning = "Provisioning"
	phaseReady        = "Ready"
	phaseFailed       = "Failed"

	reasonProvisioning = "ProvisioningInProgress"
	reasonReady        = "AllReplicasReady"
	reasonFailed       = "ProvisioningFailed"
)

// GatewayReconciler materialises each Gateway CR into an Envoy Deployment +
// Service + bootstrap ConfigMap, and updates .status as the data plane comes
// up. Cleanup on deletion is handled by owner references.
type GatewayReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// XDSHost / XDSPort form the xDS address baked into each proxy's
	// bootstrap config. The host must be reachable from data plane pods.
	XDSHost string
	XDSPort int32

	// EnvoyImage is the Envoy container image reference.
	EnvoyImage string

	// EnvoyImagePullPolicy is applied to the Envoy container.
	EnvoyImagePullPolicy corev1.PullPolicy

	// EnvoyAdminPort is where Envoy's admin/health endpoint listens.
	EnvoyAdminPort int32
}

// +kubebuilder:rbac:groups=flowc.io,resources=gateways,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=gateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=gateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=flowc.io,resources=listeners,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile brings the cluster state in line with the Gateway CR. Deletion
// is handled by Kubernetes garbage collection via the owner references
// SetControllerReference installs on every provisioned object, so there is
// no explicit finalizer logic here.
func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var gw flowcv1alpha1.Gateway
	if err := r.Get(ctx, req.NamespacedName, &gw); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !gw.DeletionTimestamp.IsZero() {
		// Owner references take care of Deployment/Service/ConfigMap cleanup.
		return ctrl.Result{}, nil
	}

	listeners, err := r.listListenersForGateway(ctx, &gw)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("list listeners for gateway %s: %w", gw.Name, err)
	}

	if err := r.reconcileConfigMap(ctx, &gw); err != nil {
		return ctrl.Result{}, r.markFailed(ctx, &gw, fmt.Errorf("configmap: %w", err))
	}

	deploy, err := r.reconcileDeployment(ctx, &gw, listeners)
	if err != nil {
		return ctrl.Result{}, r.markFailed(ctx, &gw, fmt.Errorf("deployment: %w", err))
	}

	if err := r.reconcileService(ctx, &gw, listeners); err != nil {
		return ctrl.Result{}, r.markFailed(ctx, &gw, fmt.Errorf("service: %w", err))
	}

	if err := r.updateStatus(ctx, &gw, deploy); err != nil {
		log.Error(err, "failed to update Gateway status", "name", gw.Name)
		return ctrl.Result{}, err
	}

	// Re-queue once while the Deployment rolls out so status flips from
	// Provisioning to Ready without waiting for the next external event.
	if deploy.Status.ReadyReplicas == 0 {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers the Gateway controller and its watches.
// Listeners are watched with an index so a Listener change enqueues the
// parent Gateway (and only that Gateway).
func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("gateway").
		For(&flowcv1alpha1.Gateway{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Watches(
			&flowcv1alpha1.Listener{},
			handler.EnqueueRequestsFromMapFunc(r.listenerToGateway),
		).
		Complete(r)
}

// listenerToGateway maps a Listener event to a reconcile of the Gateway the
// listener points at. A Listener with no gatewayRef is skipped.
func (r *GatewayReconciler) listenerToGateway(_ context.Context, obj client.Object) []ctrl.Request {
	l, ok := obj.(*flowcv1alpha1.Listener)
	if !ok || l.Spec.GatewayRef == "" {
		return nil
	}
	return []ctrl.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: l.Namespace,
			Name:      l.Spec.GatewayRef,
		},
	}}
}

// listListenersForGateway returns the Listeners in gw's namespace whose
// spec.gatewayRef names gw.
func (r *GatewayReconciler) listListenersForGateway(ctx context.Context, gw *flowcv1alpha1.Gateway) ([]flowcv1alpha1.Listener, error) {
	var all flowcv1alpha1.ListenerList
	if err := r.List(ctx, &all, client.InNamespace(gw.Namespace)); err != nil {
		return nil, err
	}
	var matched []flowcv1alpha1.Listener
	for i := range all.Items {
		if all.Items[i].Spec.GatewayRef == gw.Name {
			matched = append(matched, all.Items[i])
		}
	}
	return matched, nil
}

// reconcileConfigMap creates or updates the bootstrap ConfigMap used by the
// Envoy container. Data is overwritten on every reconcile; metadata is only
// set on first create.
func (r *GatewayReconciler) reconcileConfigMap(ctx context.Context, gw *flowcv1alpha1.Gateway) error {
	desired, err := buildConfigMap(gw, r.XDSHost, r.XDSPort, r.EnvoyAdminPort)
	if err != nil {
		return err
	}
	if err := ctrl.SetControllerReference(gw, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner ref on ConfigMap: %w", err)
	}

	var current corev1.ConfigMap
	key := client.ObjectKeyFromObject(desired)
	err = r.Get(ctx, key, &current)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return fmt.Errorf("get ConfigMap: %w", err)
	}

	if configMapEqual(&current, desired) {
		return nil
	}
	current.Data = desired.Data
	current.Labels = desired.Labels
	current.OwnerReferences = desired.OwnerReferences
	return r.Update(ctx, &current)
}

// reconcileDeployment returns the live Deployment (post-apply) so the caller
// can read .Status for phase transitions.
func (r *GatewayReconciler) reconcileDeployment(ctx context.Context, gw *flowcv1alpha1.Gateway, listeners []flowcv1alpha1.Listener) (*appsv1.Deployment, error) {
	desired := buildDeployment(gw, listeners, r.EnvoyImage, r.EnvoyImagePullPolicy, r.EnvoyAdminPort)
	if err := ctrl.SetControllerReference(gw, desired, r.Scheme); err != nil {
		return nil, fmt.Errorf("set owner ref on Deployment: %w", err)
	}

	var current appsv1.Deployment
	key := client.ObjectKeyFromObject(desired)
	err := r.Get(ctx, key, &current)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, desired); err != nil {
			return nil, err
		}
		return desired, nil
	case err != nil:
		return nil, fmt.Errorf("get Deployment: %w", err)
	}

	// Preserve the server-assigned replica count if the user has scaled the
	// Deployment manually; otherwise drive spec toward desired.
	desired.Spec.Replicas = current.Spec.Replicas
	if deploymentEqual(&current, desired) {
		return &current, nil
	}
	current.Spec = desired.Spec
	current.Labels = desired.Labels
	current.OwnerReferences = desired.OwnerReferences
	if err := r.Update(ctx, &current); err != nil {
		return nil, err
	}
	return &current, nil
}

func (r *GatewayReconciler) reconcileService(ctx context.Context, gw *flowcv1alpha1.Gateway, listeners []flowcv1alpha1.Listener) error {
	desired := buildService(gw, listeners, r.EnvoyAdminPort)
	if err := ctrl.SetControllerReference(gw, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner ref on Service: %w", err)
	}

	var current corev1.Service
	key := client.ObjectKeyFromObject(desired)
	err := r.Get(ctx, key, &current)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return fmt.Errorf("get Service: %w", err)
	}

	// ClusterIP is immutable; keep whatever the API assigned.
	desired.Spec.ClusterIP = current.Spec.ClusterIP
	desired.Spec.ClusterIPs = current.Spec.ClusterIPs
	if serviceEqual(&current, desired) {
		return nil
	}
	current.Spec.Ports = desired.Spec.Ports
	current.Spec.Selector = desired.Spec.Selector
	current.Spec.Type = desired.Spec.Type
	current.Labels = desired.Labels
	current.OwnerReferences = desired.OwnerReferences
	return r.Update(ctx, &current)
}

// updateStatus derives phase + Ready condition from the Deployment's
// current replica counts.
func (r *GatewayReconciler) updateStatus(ctx context.Context, gw *flowcv1alpha1.Gateway, deploy *appsv1.Deployment) error {
	desired := deriveStatus(deploy)
	if statusEqual(gw.Status, desired) {
		return nil
	}
	gw.Status = desired
	return r.Status().Update(ctx, gw)
}

// markFailed writes a Failed phase + condition, surfacing the provisioning
// error to kubectl. The returned error is the input error so the caller can
// return it verbatim from Reconcile.
func (r *GatewayReconciler) markFailed(ctx context.Context, gw *flowcv1alpha1.Gateway, cause error) error {
	conds := setCondition(gw.Status.Conditions, acceptedCondition())
	conds = setCondition(conds, metav1.Condition{
		Type:    flowcv1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  reasonFailed,
		Message: cause.Error(),
	})
	newStatus := flowcv1alpha1.GatewayStatus{
		Phase:      phaseFailed,
		Conditions: conds,
	}
	if statusEqual(gw.Status, newStatus) {
		return cause
	}
	gw.Status = newStatus
	if err := r.Status().Update(ctx, gw); err != nil {
		logf.FromContext(ctx).Error(err, "failed to write Failed status on Gateway", "name", gw.Name)
	}
	return cause
}

// deriveStatus maps Deployment health to a GatewayStatus value. Accepted is
// True once the controller has reached this point — provisioning errors are
// surfaced via markFailed, never via Accepted. Ready tracks Envoy replica
// health independently so dependents that only need spec validity (Listener,
// Deployment) can become Ready without waiting for the Envoy pod to come up.
func deriveStatus(deploy *appsv1.Deployment) flowcv1alpha1.GatewayStatus {
	specReplicas := int32(1)
	if deploy.Spec.Replicas != nil {
		specReplicas = *deploy.Spec.Replicas
	}
	ready := deploy.Status.ReadyReplicas

	conds := setCondition(nil, acceptedCondition())

	switch {
	case ready >= specReplicas && specReplicas > 0:
		conds = setCondition(conds, metav1.Condition{
			Type:    flowcv1alpha1.ConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  reasonReady,
			Message: fmt.Sprintf("%d/%d replicas ready", ready, specReplicas),
		})
		return flowcv1alpha1.GatewayStatus{Phase: phaseReady, Conditions: conds}
	case deploy.Status.Replicas > 0:
		conds = setCondition(conds, metav1.Condition{
			Type:    flowcv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  reasonProvisioning,
			Message: fmt.Sprintf("%d/%d replicas ready", ready, specReplicas),
		})
		return flowcv1alpha1.GatewayStatus{Phase: phaseProvisioning, Conditions: conds}
	default:
		conds = setCondition(conds, metav1.Condition{
			Type:    flowcv1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  reasonProvisioning,
			Message: "waiting for replicas",
		})
		return flowcv1alpha1.GatewayStatus{Phase: phasePending, Conditions: conds}
	}
}

// acceptedCondition is the canonical Accepted=True condition for a Gateway.
// The Gateway controller has no separate spec-validation step today, so
// Accepted is asserted as soon as we reach status writing.
func acceptedCondition() metav1.Condition {
	return metav1.Condition{
		Type:    flowcv1alpha1.ConditionAccepted,
		Status:  metav1.ConditionTrue,
		Reason:  reasonAccepted,
		Message: "Spec accepted",
	}
}

// setCondition adds or replaces a condition by type, preserving
// LastTransitionTime when Status is unchanged.
func setCondition(existing []metav1.Condition, c metav1.Condition) []metav1.Condition {
	if c.LastTransitionTime.IsZero() {
		c.LastTransitionTime = metav1.Now()
	}
	out := make([]metav1.Condition, 0, len(existing)+1)
	replaced := false
	for _, e := range existing {
		if e.Type == c.Type {
			if e.Status == c.Status {
				c.LastTransitionTime = e.LastTransitionTime
			}
			out = append(out, c)
			replaced = true
			continue
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, c)
	}
	return out
}

// --- Shallow equality helpers: enough to skip no-op Update calls.

func configMapEqual(a, b *corev1.ConfigMap) bool {
	return mapsEqual(a.Data, b.Data) && mapsEqual(a.Labels, b.Labels) && ownerRefsEqual(a.OwnerReferences, b.OwnerReferences)
}

func deploymentEqual(a, b *appsv1.Deployment) bool {
	if !mapsEqual(a.Labels, b.Labels) || !ownerRefsEqual(a.OwnerReferences, b.OwnerReferences) {
		return false
	}
	if len(a.Spec.Template.Spec.Containers) != len(b.Spec.Template.Spec.Containers) {
		return false
	}
	// Compare image + args + ports which are the fields we drive.
	ac, bc := a.Spec.Template.Spec.Containers[0], b.Spec.Template.Spec.Containers[0]
	if ac.Image != bc.Image || strings.Join(ac.Args, " ") != strings.Join(bc.Args, " ") {
		return false
	}
	if len(ac.Ports) != len(bc.Ports) {
		return false
	}
	for i := range ac.Ports {
		if ac.Ports[i].ContainerPort != bc.Ports[i].ContainerPort || ac.Ports[i].Name != bc.Ports[i].Name {
			return false
		}
	}
	return true
}

func serviceEqual(a, b *corev1.Service) bool {
	if !mapsEqual(a.Labels, b.Labels) || !ownerRefsEqual(a.OwnerReferences, b.OwnerReferences) {
		return false
	}
	if !mapsEqual(a.Spec.Selector, b.Spec.Selector) || a.Spec.Type != b.Spec.Type {
		return false
	}
	if len(a.Spec.Ports) != len(b.Spec.Ports) {
		return false
	}
	for i := range a.Spec.Ports {
		if a.Spec.Ports[i].Port != b.Spec.Ports[i].Port || a.Spec.Ports[i].Name != b.Spec.Ports[i].Name {
			return false
		}
	}
	return true
}

func statusEqual(a, b flowcv1alpha1.GatewayStatus) bool {
	if a.Phase != b.Phase || len(a.Conditions) != len(b.Conditions) {
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

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func ownerRefsEqual(a, b []metav1.OwnerReference) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(r metav1.OwnerReference) string {
		return r.APIVersion + "|" + r.Kind + "|" + r.Name + "|" + string(r.UID) + "|" + strconv.FormatBool(r.Controller != nil && *r.Controller)
	}
	for i := range a {
		if key(a[i]) != key(b[i]) {
			return false
		}
	}
	return true
}
