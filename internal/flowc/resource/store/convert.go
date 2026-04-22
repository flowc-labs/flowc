package store

import (
	"maps"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AnnotationManagedBy      = "flowc.io/managed-by"
	AnnotationConflictPolicy = "flowc.io/conflict-policy"
	AnnotationUpdatedAt      = "flowc.io/updated-at"
)

// StoreMetaToObjectMeta converts a StoreMeta to a metav1.ObjectMeta.
func StoreMetaToObjectMeta(sm StoreMeta) metav1.ObjectMeta {
	om := metav1.ObjectMeta{
		Name:              sm.Name,
		Namespace:         "default",
		Labels:            sm.Labels,
		ResourceVersion:   strconv.FormatInt(sm.Revision, 10),
		CreationTimestamp: metav1.NewTime(sm.CreatedAt),
	}

	// Build annotations from StoreMeta fields + existing annotations
	annotations := make(map[string]string)
	maps.Copy(annotations, sm.Annotations)
	if sm.ManagedBy != "" {
		annotations[AnnotationManagedBy] = sm.ManagedBy
	}
	if sm.ConflictPolicy != "" {
		annotations[AnnotationConflictPolicy] = sm.ConflictPolicy
	}
	if !sm.UpdatedAt.IsZero() {
		annotations[AnnotationUpdatedAt] = sm.UpdatedAt.Format(time.RFC3339)
	}
	if len(annotations) > 0 {
		om.Annotations = annotations
	}

	return om
}

// ObjectMetaToStoreMeta converts a metav1.ObjectMeta to a StoreMeta.
func ObjectMetaToStoreMeta(kind string, om metav1.ObjectMeta) StoreMeta {
	sm := StoreMeta{
		Kind:   kind,
		Name:   om.Name,
		Labels: om.Labels,
	}

	if om.ResourceVersion != "" {
		sm.Revision, _ = strconv.ParseInt(om.ResourceVersion, 10, 64)
	}

	if !om.CreationTimestamp.IsZero() {
		sm.CreatedAt = om.CreationTimestamp.Time
	}

	// Extract FlowC-specific annotations
	if om.Annotations != nil {
		sm.ManagedBy = om.Annotations[AnnotationManagedBy]
		sm.ConflictPolicy = om.Annotations[AnnotationConflictPolicy]
		if updatedAt, ok := om.Annotations[AnnotationUpdatedAt]; ok {
			sm.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		}

		// Copy remaining annotations (excluding FlowC ones)
		remaining := make(map[string]string)
		for k, v := range om.Annotations {
			if k != AnnotationManagedBy && k != AnnotationConflictPolicy && k != AnnotationUpdatedAt {
				remaining[k] = v
			}
		}
		if len(remaining) > 0 {
			sm.Annotations = remaining
		}
	}

	return sm
}
