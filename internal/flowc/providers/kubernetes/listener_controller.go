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
	reasonListenerInvalidSpec = "InvalidSpec"
	reasonListenerReady       = "Ready"
	reasonListenerBlocked     = "DependenciesNotReady"
)

// ListenerReconciler validates Listener CRs and writes status. The xDS
// store bridge gates on Ready=True, so a Listener doesn't appear in the
// projectable view (and won't get its xDS listener built by the
// dispatch GatewayTranslator) until this controller marks it Ready.
//
// Owns no downstream resources — the xDS dispatch path reads listener
// CRs from the indexer to build per-port Envoy listeners during gateway
// rebuilds.
type ListenerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// NewListenerReconciler constructs the reconciler with all dependencies
// injected. Mirrors the shape of NewAPIReconciler / NewDeploymentReconciler.
func NewListenerReconciler(c client.Client, scheme *runtime.Scheme) *ListenerReconciler {
	return &ListenerReconciler{Client: c, Scheme: scheme}
}

// +kubebuilder:rbac:groups=flowc.io,resources=listeners,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=listeners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=gateways,verbs=get;list;watch

// Reconcile validates the Listener and writes a status snapshot. Deletion
// is a no-op — there are no owned resources, and the CR's status
// disappears with it.
func (r *ListenerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var l flowcv1alpha1.Listener
	if err := r.Get(ctx, req.NamespacedName, &l); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !l.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	desired := r.deriveStatus(ctx, &l)
	if listenerStatusEqual(l.Status, desired) {
		return ctrl.Result{}, nil
	}
	l.Status = desired
	if err := r.Status().Update(ctx, &l); err != nil {
		log.Error(err, "Failed to update Listener status", "name", l.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers the controller and the watches that make
// Gateway changes re-validate dependent Listeners.
func (r *ListenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("listener").
		For(&flowcv1alpha1.Listener{}).
		Watches(&flowcv1alpha1.Gateway{}, handler.EnqueueRequestsFromMapFunc(r.gatewayToListeners)).
		Complete(r)
}

// gatewayToListeners enqueues every Listener that references the changed
// Gateway. List-and-filter; bounded by listeners-per-namespace.
func (r *ListenerReconciler) gatewayToListeners(ctx context.Context, obj client.Object) []ctrl.Request {
	gw, ok := obj.(*flowcv1alpha1.Gateway)
	if !ok {
		return nil
	}
	var listeners flowcv1alpha1.ListenerList
	if err := r.List(ctx, &listeners, client.InNamespace(gw.Namespace)); err != nil {
		logf.FromContext(ctx).Error(err, "List Listeners for watch fan-out", "namespace", gw.Namespace)
		return nil
	}
	var out []ctrl.Request
	for i := range listeners.Items {
		if listeners.Items[i].Spec.GatewayRef != gw.Name {
			continue
		}
		out = append(out, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: listeners.Items[i].Namespace,
				Name:      listeners.Items[i].Name,
			},
		})
	}
	return out
}

// deriveStatus runs validation + ref resolution and folds the outcome
// into a fresh ListenerStatus value.
func (r *ListenerReconciler) deriveStatus(ctx context.Context, l *flowcv1alpha1.Listener) flowcv1alpha1.ListenerStatus {
	out := flowcv1alpha1.ListenerStatus{
		Phase:      phasePending,
		Conditions: l.Status.Conditions,
	}

	// 1. Field validation.
	if err := validateListenerSpec(&l.Spec); err != nil {
		out.Phase = phaseFailed
		out.Conditions = setCondition(out.Conditions, falseCond(flowcv1alpha1.ConditionAccepted, reasonListenerInvalidSpec, err.Error()))
		out.Conditions = setCondition(out.Conditions, falseCond(flowcv1alpha1.ConditionReady, reasonListenerInvalidSpec, err.Error()))
		return out
	}
	out.Conditions = setCondition(out.Conditions, trueCond(flowcv1alpha1.ConditionAccepted, reasonAccepted, "Spec fields validated"))

	// 2. Resolve Gateway reference.
	gwCond := r.resolveListenerGateway(ctx, l)
	out.Conditions = setCondition(out.Conditions, gwCond)

	// 3. Roll up Ready: gateway present and Ready ⇒ listener Ready.
	if gwCond.Status == metav1.ConditionTrue {
		out.Phase = phaseReady
		out.Conditions = setCondition(out.Conditions, trueCond(flowcv1alpha1.ConditionReady, reasonListenerReady, "Listener is ready"))
	} else {
		out.Phase = phasePending
		out.Conditions = setCondition(out.Conditions, falseCond(flowcv1alpha1.ConditionReady, reasonListenerBlocked, gwCond.Message))
	}
	return out
}

// validateListenerSpec checks structural requirements that aren't already
// enforced by kubebuilder markers. Port range and other field-level
// constraints are handled by the API server via the markers; this catches
// the semantic gaps.
func validateListenerSpec(spec *flowcv1alpha1.ListenerSpec) error {
	if strings.TrimSpace(spec.GatewayRef) == "" {
		return errors.New("spec.gatewayRef is required")
	}
	// Port bounds are enforced by kubebuilder markers (1..65535) but
	// re-check defensively in case markers are bypassed (e.g., direct
	// REST writes via memory store mode in the future).
	if spec.Port == 0 || spec.Port > 65535 {
		return fmt.Errorf("spec.port must be in [1, 65535] (got %d)", spec.Port)
	}
	return nil
}

// resolveListenerGateway fetches the referenced Gateway and decides the
// GatewayResolved condition. Same shape as DeploymentReconciler's
// equivalent — gates on Gateway.Accepted (spec valid) so the Listener can
// flip Ready before Envoy comes up, removing a startup race where Gateway
// rebuilds briefly produced empty listener sets while Gateway.Ready flipped.
func (r *ListenerReconciler) resolveListenerGateway(ctx context.Context, l *flowcv1alpha1.Listener) metav1.Condition {
	var gw flowcv1alpha1.Gateway
	err := r.Get(ctx, types.NamespacedName{Namespace: l.Namespace, Name: l.Spec.GatewayRef}, &gw)
	switch {
	case apierrors.IsNotFound(err):
		return falseCond(conditionGatewayResolved, reasonGatewayNotFound, fmt.Sprintf("Gateway %q not found in namespace %q", l.Spec.GatewayRef, l.Namespace))
	case err != nil:
		return falseCond(conditionGatewayResolved, reasonGatewayNotFound, fmt.Sprintf("get Gateway %q: %v", l.Spec.GatewayRef, err))
	}
	if !acceptedTrue(gw.Status.Conditions) {
		return falseCond(conditionGatewayResolved, reasonGatewayNotAccepted, fmt.Sprintf("Gateway %q is not Accepted", gw.Name))
	}
	return trueCond(conditionGatewayResolved, reasonGatewayResolved, fmt.Sprintf("Gateway %q is Accepted", gw.Name))
}

// listenerStatusEqual is the equivalent of statusEqual for ListenerStatus.
// Used to skip no-op writes that would otherwise churn the status
// subresource and trigger reconciliation loops.
func listenerStatusEqual(a, b flowcv1alpha1.ListenerStatus) bool {
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
	return true
}
