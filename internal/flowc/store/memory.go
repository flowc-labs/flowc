package store

import (
	"context"
	"sync"
	"time"
)

const watchBufferSize = 64

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu        sync.RWMutex
	resources map[ResourceKey]*StoredResource

	watchersMu sync.Mutex
	watchers   []*watcher
}

type watcher struct {
	filter WatchFilter
	ch     chan WatchEvent
	ctx    context.Context
}

// NewMemoryStore creates a new in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		resources: make(map[ResourceKey]*StoredResource),
	}
}

func (s *MemoryStore) Get(ctx context.Context, key ResourceKey) (*StoredResource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	res, ok := s.resources[key]
	if !ok {
		return nil, ErrNotFound
	}
	return res.Clone(), nil
}

func (s *MemoryStore) Put(ctx context.Context, res *StoredResource, opts PutOptions) (*StoredResource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := res.Key()
	existing, exists := s.resources[key]
	now := time.Now()

	if exists {
		// Optimistic concurrency check
		if opts.ExpectedRevision != 0 && existing.Meta.Revision != opts.ExpectedRevision {
			return nil, &RevisionConflictError{
				Key:      key,
				Expected: opts.ExpectedRevision,
				Actual:   existing.Meta.Revision,
			}
		}

		// Ownership check
		if opts.ManagedBy != "" && existing.Meta.ManagedBy != "" && existing.Meta.ManagedBy != opts.ManagedBy {
			policy := existing.Meta.ConflictPolicy
			if policy == "" {
				policy = ConflictStrict
			}
			switch policy {
			case ConflictStrict:
				return nil, &OwnershipConflictError{
					Key:          key,
					CurrentOwner: existing.Meta.ManagedBy,
					AttemptedBy:  opts.ManagedBy,
				}
			case ConflictTakeover:
				// Allow — ownership transfers below
			case ConflictWarn:
				// Allow — caller may log the warning
			}
		}

		stored := res.Clone()
		stored.Meta.Revision = existing.Meta.Revision + 1
		stored.Meta.CreatedAt = existing.Meta.CreatedAt
		stored.Meta.UpdatedAt = now
		if opts.ManagedBy != "" {
			stored.Meta.ManagedBy = opts.ManagedBy
		}
		s.resources[key] = stored

		s.notify(WatchEvent{
			Type:        WatchEventPut,
			Resource:    stored.Clone(),
			OldResource: existing.Clone(),
		})

		return stored.Clone(), nil
	}

	// New resource
	stored := res.Clone()
	stored.Meta.Revision = 1
	stored.Meta.CreatedAt = now
	stored.Meta.UpdatedAt = now
	if opts.ManagedBy != "" {
		stored.Meta.ManagedBy = opts.ManagedBy
	}
	s.resources[key] = stored

	s.notify(WatchEvent{
		Type:     WatchEventPut,
		Resource: stored.Clone(),
	})

	return stored.Clone(), nil
}

func (s *MemoryStore) Delete(ctx context.Context, key ResourceKey, opts DeleteOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.resources[key]
	if !ok {
		return ErrNotFound
	}

	if opts.ExpectedRevision != 0 && existing.Meta.Revision != opts.ExpectedRevision {
		return &RevisionConflictError{
			Key:      key,
			Expected: opts.ExpectedRevision,
			Actual:   existing.Meta.Revision,
		}
	}

	delete(s.resources, key)

	s.notify(WatchEvent{
		Type:        WatchEventDelete,
		Resource:    existing.Clone(),
		OldResource: existing.Clone(),
	})

	return nil
}

func (s *MemoryStore) List(ctx context.Context, filter ListFilter) ([]*StoredResource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*StoredResource
	for _, res := range s.resources {
		if !matchesListFilter(res, filter) {
			continue
		}
		result = append(result, res.Clone())
	}
	return result, nil
}

func (s *MemoryStore) Watch(ctx context.Context, filter WatchFilter) (<-chan WatchEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ch := make(chan WatchEvent, watchBufferSize)
	w := &watcher{filter: filter, ch: ch, ctx: ctx}

	s.watchersMu.Lock()
	s.watchers = append(s.watchers, w)
	s.watchersMu.Unlock()

	// Cleanup on context cancel
	go func() {
		<-ctx.Done()
		s.watchersMu.Lock()
		defer s.watchersMu.Unlock()
		for i, ww := range s.watchers {
			if ww == w {
				s.watchers = append(s.watchers[:i], s.watchers[i+1:]...)
				break
			}
		}
		close(ch)
	}()

	return ch, nil
}

// notify fans out an event to all matching watchers.
// Must be called with s.mu held (write lock).
func (s *MemoryStore) notify(event WatchEvent) {
	s.watchersMu.Lock()
	defer s.watchersMu.Unlock()

	for _, w := range s.watchers {
		if w.ctx.Err() != nil {
			continue
		}
		if !matchesWatchFilter(event, w.filter) {
			continue
		}
		select {
		case w.ch <- event:
		default:
			// Drop if buffer full — consumer too slow
		}
	}
}

func matchesListFilter(res *StoredResource, f ListFilter) bool {
	if f.Kind != "" && res.Meta.Kind != f.Kind {
		return false
	}
	for k, v := range f.Labels {
		if res.Meta.Labels[k] != v {
			return false
		}
	}
	return true
}

func matchesWatchFilter(event WatchEvent, f WatchFilter) bool {
	res := event.Resource
	if f.Kind != "" && res.Meta.Kind != f.Kind {
		return false
	}
	return true
}
