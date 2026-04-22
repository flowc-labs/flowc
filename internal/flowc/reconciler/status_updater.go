package reconciler

import (
	"context"
	"encoding/json"
	"time"

	"github.com/flowc-labs/flowc/internal/flowc/resource/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// deploymentStatus is a local type for marshaling deployment status.
type deploymentStatus struct {
	Phase      string             `json:"phase,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// gatewayStatus is a local type for marshaling gateway status.
type gatewayStatus struct {
	Phase      string             `json:"phase,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// updateDeploymentStatus updates the status of a deployment resource.
func (r *Reconciler) updateDeploymentStatus(ctx context.Context, depName string, phase, message string) {
	key := store.ResourceKey{Kind: "Deployment", Name: depName}
	stored, err := r.store.Get(ctx, key)
	if err != nil {
		r.logger.WithFields(map[string]any{
			"deployment": depName,
			"error":      err.Error(),
		}).Error("Failed to get deployment for status update")
		return
	}

	// Unmarshal existing status (if any)
	var status deploymentStatus
	if stored.StatusJSON != nil {
		_ = json.Unmarshal(stored.StatusJSON, &status)
	}

	status.Phase = phase

	reason := "Reconciled"
	condStatus := metav1.ConditionTrue
	if phase == "Failed" {
		reason = "ReconcileFailed"
		condStatus = metav1.ConditionFalse
	}

	now := metav1.NewTime(time.Now())
	setCondition(&status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})

	statusJSON, err := json.Marshal(status)
	if err != nil {
		r.logger.WithError(err).Error("Failed to marshal deployment status")
		return
	}

	stored.StatusJSON = statusJSON
	_, err = r.store.Put(ctx, stored, store.PutOptions{
		ExpectedRevision: stored.Meta.Revision,
	})
	if err != nil {
		r.logger.WithFields(map[string]any{
			"deployment": depName,
			"error":      err.Error(),
		}).Warn("Failed to update deployment status (may have been modified)")
	}
}

// updateGatewayStatus updates the status of a gateway resource.
func (r *Reconciler) updateGatewayStatus(ctx context.Context, gwName string, phase string) {
	key := store.ResourceKey{Kind: "Gateway", Name: gwName}
	stored, err := r.store.Get(ctx, key)
	if err != nil {
		return
	}

	var status gatewayStatus
	if stored.StatusJSON != nil {
		_ = json.Unmarshal(stored.StatusJSON, &status)
	}

	status.Phase = phase

	now := metav1.NewTime(time.Now())
	setCondition(&status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		LastTransitionTime: now,
	})

	statusJSON, err := json.Marshal(status)
	if err != nil {
		r.logger.WithError(err).Error("Failed to marshal gateway status")
		return
	}

	stored.StatusJSON = statusJSON
	if _, err := r.store.Put(ctx, stored, store.PutOptions{
		ExpectedRevision: stored.Meta.Revision,
	}); err != nil {
		r.logger.WithFields(map[string]any{
			"gateway": gwName,
			"error":   err.Error(),
		}).Warn("Failed to update gateway status (may have been modified)")
	}
}

// setCondition updates or adds a condition in a condition list.
func setCondition(conditions *[]metav1.Condition, cond metav1.Condition) {
	for i, c := range *conditions {
		if c.Type == cond.Type {
			(*conditions)[i] = cond
			return
		}
	}
	*conditions = append(*conditions, cond)
}
