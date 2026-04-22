package store

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"
)

// StoreMeta is the metadata envelope for stored resources.
type StoreMeta struct {
	Kind           string            `json:"kind"`
	Name           string            `json:"name"`
	Revision       int64             `json:"revision"`
	ManagedBy      string            `json:"managedBy,omitempty"`
	ConflictPolicy string            `json:"conflictPolicy,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

// ResourceKey is the unique identity of a resource: (Kind, Name).
type ResourceKey struct {
	Kind string
	Name string
}

// String returns a human-readable key representation.
func (k ResourceKey) String() string {
	return fmt.Sprintf("%s/%s", k.Kind, k.Name)
}

// Key returns the ResourceKey for this StoreMeta.
func (m *StoreMeta) Key() ResourceKey {
	return ResourceKey{Kind: m.Kind, Name: m.Name}
}

// Conflict policy constants.
const (
	ConflictStrict   = "strict"
	ConflictWarn     = "warn"
	ConflictTakeover = "takeover"
)

// StoredResource is the kind-agnostic envelope stored in the store.
type StoredResource struct {
	Meta       StoreMeta       `json:"metadata"`
	SpecJSON   json.RawMessage `json:"spec"`
	StatusJSON json.RawMessage `json:"status,omitempty"`
}

// Key returns the resource key for this stored resource.
func (s *StoredResource) Key() ResourceKey {
	return s.Meta.Key()
}

// Clone returns a deep copy of the stored resource.
func (s *StoredResource) Clone() *StoredResource {
	c := &StoredResource{
		Meta: s.Meta,
	}
	if s.SpecJSON != nil {
		c.SpecJSON = make(json.RawMessage, len(s.SpecJSON))
		copy(c.SpecJSON, s.SpecJSON)
	}
	if s.StatusJSON != nil {
		c.StatusJSON = make(json.RawMessage, len(s.StatusJSON))
		copy(c.StatusJSON, s.StatusJSON)
	}
	if s.Meta.Labels != nil {
		c.Meta.Labels = make(map[string]string, len(s.Meta.Labels))
		maps.Copy(c.Meta.Labels, s.Meta.Labels)
	}
	if s.Meta.Annotations != nil {
		c.Meta.Annotations = make(map[string]string, len(s.Meta.Annotations))
		maps.Copy(c.Meta.Annotations, s.Meta.Annotations)
	}
	return c
}

// PutOptions controls the behavior of Store.Put.
type PutOptions struct {
	ExpectedRevision int64
	ManagedBy        string
}

// DeleteOptions controls the behavior of Store.Delete.
type DeleteOptions struct {
	ExpectedRevision int64
}

// ListFilter selects which resources to return from Store.List.
type ListFilter struct {
	Kind   string
	Labels map[string]string
}

// WatchEventType indicates whether a resource was written or deleted.
type WatchEventType string

const (
	WatchEventPut    WatchEventType = "PUT"
	WatchEventDelete WatchEventType = "DELETE"
)

// WatchEvent represents a change to a stored resource.
type WatchEvent struct {
	Type        WatchEventType
	Resource    *StoredResource
	OldResource *StoredResource
}

// WatchFilter selects which events to receive.
type WatchFilter struct {
	Kind string
}

// Store is the desired-state store abstraction.
type Store interface {
	Get(ctx context.Context, key ResourceKey) (*StoredResource, error)
	Put(ctx context.Context, res *StoredResource, opts PutOptions) (*StoredResource, error)
	Delete(ctx context.Context, key ResourceKey, opts DeleteOptions) error
	List(ctx context.Context, filter ListFilter) ([]*StoredResource, error)
	Watch(ctx context.Context, filter WatchFilter) (<-chan WatchEvent, error)
}
