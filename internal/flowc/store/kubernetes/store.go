// Package kubernetes implements a Store backed by Kubernetes CRDs.
//
// Reads (Get/List/Watch) are served from a controller-runtime informer cache
// that mirrors the cluster. Writes (Put/Delete) go straight to the K8s API;
// the cache is updated asynchronously when the informer observes the change
// and a corresponding WatchEvent is fanned out to Watch subscribers.
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	toolscache "k8s.io/client-go/tools/cache"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	flowcv1alpha1 "github.com/flowc-labs/flowc/api/v1alpha1"
	storepkg "github.com/flowc-labs/flowc/internal/flowc/store"
)

const (
	defaultNamespace = "default"
	watchBufferSize  = 64
)

var apiVersion = flowcv1alpha1.GroupVersion.Group + "/" + flowcv1alpha1.GroupVersion.Version

// Store is a Store implementation backed by Kubernetes CRDs.
type Store struct {
	client    client.Client
	cache     ctrlcache.Cache
	namespace string

	watchersMu sync.Mutex
	watchers   []*watcher
}

type watcher struct {
	filter storepkg.WatchFilter
	ch     chan storepkg.WatchEvent
	ctx    context.Context
}

// New constructs a K8s-backed Store. The caller owns the cache lifecycle and
// must start it (and wait for initial sync) before serving reads.
func New(ctx context.Context, c client.Client, cch ctrlcache.Cache, namespace string) (*Store, error) {
	if c == nil {
		return nil, fmt.Errorf("client is required")
	}
	if cch == nil {
		return nil, fmt.Errorf("cache is required")
	}
	if namespace == "" {
		namespace = defaultNamespace
	}

	s := &Store{
		client:    c,
		cache:     cch,
		namespace: namespace,
	}

	if err := s.registerInformers(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// NewFromManager wraps a controller-runtime Manager's client and cache.
// Reconcilers registered on the same Manager will share the underlying
// informer cache with this Store — there is one set of informers per process.
//
// The caller must still start the Manager (mgr.Start) and ideally wait for
// mgr.GetCache().WaitForCacheSync before issuing reads through the Store.
func NewFromManager(ctx context.Context, mgr manager.Manager, namespace string) (*Store, error) {
	if mgr == nil {
		return nil, fmt.Errorf("manager is required")
	}
	return New(ctx, mgr.GetClient(), mgr.GetCache(), namespace)
}

// registerInformers wires an event handler into the informer cache for every
// supported kind. Must be called before the cache starts so handlers observe
// the initial list; registration after start is also supported by
// controller-runtime but the initial stream is then lost.
func (s *Store) registerInformers(ctx context.Context) error {
	for _, kind := range supportedKinds() {
		k := kind // per-iteration capture for closures
		entry := kindRegistry[k]
		inf, err := s.cache.GetInformer(ctx, entry.Object())
		if err != nil {
			return fmt.Errorf("get informer for %s: %w", k, err)
		}
		_, err = inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				s.onInformerEvent(k, storepkg.WatchEventPut, obj, nil)
			},
			UpdateFunc: func(oldObj, newObj any) {
				s.onInformerEvent(k, storepkg.WatchEventPut, newObj, oldObj)
			},
			DeleteFunc: func(obj any) {
				// Informer may wrap deletes in DeletedFinalStateUnknown when a watch
				// was missed; unwrap so we still emit a usable event.
				if tomb, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
					obj = tomb.Obj
				}
				s.onInformerEvent(k, storepkg.WatchEventDelete, obj, nil)
			},
		})
		if err != nil {
			return fmt.Errorf("add handler for %s: %w", k, err)
		}
	}
	return nil
}

// Get returns a resource from the informer cache.
func (s *Store) Get(ctx context.Context, key storepkg.ResourceKey) (*storepkg.StoredResource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, ok := kindRegistry[key.Kind]
	if !ok {
		return nil, storepkg.ErrNotFound
	}
	obj := entry.Object()
	if err := s.cache.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: key.Name}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, storepkg.ErrNotFound
		}
		return nil, err
	}
	return objectToStored(key.Kind, obj)
}

// List returns resources from the informer cache. Unsupported kinds return an
// empty list (matching MemoryStore semantics).
func (s *Store) List(ctx context.Context, filter storepkg.ListFilter) ([]*storepkg.StoredResource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	kinds := supportedKinds()
	if filter.Kind != "" {
		if _, ok := kindRegistry[filter.Kind]; !ok {
			return nil, nil
		}
		kinds = []string{filter.Kind}
	}

	var result []*storepkg.StoredResource
	for _, kind := range kinds {
		entry := kindRegistry[kind]
		listObj := entry.List()
		if err := s.cache.List(ctx, listObj, client.InNamespace(s.namespace)); err != nil {
			return nil, fmt.Errorf("list %s: %w", kind, err)
		}
		items, err := meta.ExtractList(listObj)
		if err != nil {
			return nil, fmt.Errorf("extract %s list: %w", kind, err)
		}
		for _, item := range items {
			cobj, ok := item.(client.Object)
			if !ok {
				continue
			}
			res, err := objectToStored(kind, cobj)
			if err != nil {
				continue
			}
			if !matchesLabels(res.Meta.Labels, filter.Labels) {
				continue
			}
			result = append(result, res)
		}
	}
	return result, nil
}

// Put writes a resource to the K8s API. Create-or-update semantics, using the
// live client (not the cache) to decide which path to take.
//
// Spec and metadata are written via a normal Update. If the caller provided a
// non-empty StatusJSON, status is written via a separate Status().Update()
// call — CRDs in this project have +kubebuilder:subresource:status, so the
// .status field is only writable through the subresource endpoint.
func (s *Store) Put(ctx context.Context, res *storepkg.StoredResource, opts storepkg.PutOptions) (*storepkg.StoredResource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, ok := kindRegistry[res.Meta.Kind]
	if !ok {
		return nil, storepkg.ErrInvalidResource
	}

	existing := entry.Object()
	key := client.ObjectKey{Namespace: s.namespace, Name: res.Meta.Name}
	err := s.client.Get(ctx, key, existing)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get existing %s/%s: %w", res.Meta.Kind, res.Meta.Name, err)
	}

	if apierrors.IsNotFound(err) {
		return s.createResource(ctx, res, opts, entry)
	}

	// Optimistic concurrency: if caller supplied a revision, require it to
	// match the existing ResourceVersion.
	if opts.ExpectedRevision != 0 {
		currentRV := metaRevisionFromObject(existing)
		if currentRV != opts.ExpectedRevision {
			return nil, &storepkg.RevisionConflictError{
				Key:      res.Key(),
				Expected: opts.ExpectedRevision,
				Actual:   currentRV,
			}
		}
	}

	return s.updateResource(ctx, res, opts, existing)
}

// createResource handles the not-found branch of Put. Spec goes in via
// Create; any status the caller supplied lands via a follow-up
// Status().Update() since CRDs here use the status subresource.
func (s *Store) createResource(ctx context.Context, res *storepkg.StoredResource, opts storepkg.PutOptions, entry kindEntry) (*storepkg.StoredResource, error) {
	obj := entry.Object()
	if err := applyStoredToObject(res, obj, s.namespace, opts, "" /* no RV on create */); err != nil {
		return nil, err
	}
	if err := s.client.Create(ctx, obj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, storepkg.ErrAlreadyExists
		}
		return nil, fmt.Errorf("create %s/%s: %w", res.Meta.Kind, res.Meta.Name, err)
	}

	if hasStatus(res.StatusJSON) {
		if err := applyStatusToObject(res.StatusJSON, obj); err != nil {
			return nil, err
		}
		if err := s.client.Status().Update(ctx, obj); err != nil {
			return nil, fmt.Errorf("status update after create %s/%s: %w", res.Meta.Kind, res.Meta.Name, err)
		}
	}
	return objectToStored(res.Meta.Kind, obj)
}

// updateResource handles the existing-object branch of Put. If spec or
// metadata changed, we issue a regular Update; if the caller also provided
// status, we issue a Status().Update() after.
func (s *Store) updateResource(ctx context.Context, res *storepkg.StoredResource, opts storepkg.PutOptions, existing client.Object) (*storepkg.StoredResource, error) {
	if err := applyStoredToObject(res, existing, s.namespace, opts, existing.GetResourceVersion()); err != nil {
		return nil, err
	}
	if err := s.client.Update(ctx, existing); err != nil {
		if apierrors.IsConflict(err) {
			return nil, &storepkg.RevisionConflictError{
				Key:      res.Key(),
				Expected: opts.ExpectedRevision,
				Actual:   metaRevisionFromObject(existing),
			}
		}
		return nil, fmt.Errorf("update %s/%s: %w", res.Meta.Kind, res.Meta.Name, err)
	}

	if hasStatus(res.StatusJSON) {
		if err := applyStatusToObject(res.StatusJSON, existing); err != nil {
			return nil, err
		}
		if err := s.client.Status().Update(ctx, existing); err != nil {
			if apierrors.IsConflict(err) {
				return nil, &storepkg.RevisionConflictError{
					Key:      res.Key(),
					Expected: opts.ExpectedRevision,
					Actual:   metaRevisionFromObject(existing),
				}
			}
			return nil, fmt.Errorf("status update %s/%s: %w", res.Meta.Kind, res.Meta.Name, err)
		}
	}
	return objectToStored(res.Meta.Kind, existing)
}

// Delete removes a resource from the K8s API.
func (s *Store) Delete(ctx context.Context, key storepkg.ResourceKey, opts storepkg.DeleteOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, ok := kindRegistry[key.Kind]
	if !ok {
		return storepkg.ErrNotFound
	}

	obj := entry.Object()
	obj.SetName(key.Name)
	obj.SetNamespace(s.namespace)

	deleteOpts := []client.DeleteOption{}
	if opts.ExpectedRevision != 0 {
		// Fetch first to validate revision; K8s DeleteOptions.Preconditions
		// supports resourceVersion matching but only via the raw client-go
		// client. We can get the same effect by checking in the cache.
		existing := entry.Object()
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: key.Name}, existing); err != nil {
			if apierrors.IsNotFound(err) {
				return storepkg.ErrNotFound
			}
			return err
		}
		currentRV := metaRevisionFromObject(existing)
		if currentRV != opts.ExpectedRevision {
			return &storepkg.RevisionConflictError{
				Key:      key,
				Expected: opts.ExpectedRevision,
				Actual:   currentRV,
			}
		}
	}

	if err := s.client.Delete(ctx, obj, deleteOpts...); err != nil {
		if apierrors.IsNotFound(err) {
			return storepkg.ErrNotFound
		}
		return fmt.Errorf("delete %s/%s: %w", key.Kind, key.Name, err)
	}
	return nil
}

// Watch registers a subscriber. Events are fanned out from the informer
// callbacks registered in registerInformers.
func (s *Store) Watch(ctx context.Context, filter storepkg.WatchFilter) (<-chan storepkg.WatchEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ch := make(chan storepkg.WatchEvent, watchBufferSize)
	w := &watcher{filter: filter, ch: ch, ctx: ctx}

	s.watchersMu.Lock()
	s.watchers = append(s.watchers, w)
	s.watchersMu.Unlock()

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

// onInformerEvent translates an informer callback into a Store WatchEvent
// for fan-out to subscribers — but only for resources whose Ready condition
// is True (the projectability gate). The bridge collapses raw informer
// events into a clean stream:
//
//   - not-Ready → Ready          : emit Put   (resource entered xDS view)
//   - Ready     → not-Ready      : emit Delete (resource left xDS view)
//   - Ready     → Ready, spec changed : emit Put   (content update)
//   - Ready     → Ready, spec unchanged: drop      (status churn only)
//   - not-Ready → not-Ready      : drop          (irrelevant to xDS)
//   - delete of previously Ready : emit Delete
//   - delete of never-Ready      : drop
//
// Readiness is determined from the object's status.conditions[Ready]; the
// last-known status carried on Delete events tells us whether the resource
// had been in the projectable view, so no separate "mirrored" set is
// needed to track it.
func (s *Store) onInformerEvent(kind string, eventType storepkg.WatchEventType, obj, oldObj any) {
	cobj, ok := obj.(client.Object)
	if !ok {
		return
	}

	if eventType == storepkg.WatchEventDelete {
		if !isReady(cobj) {
			// Wasn't in the projectable view; consumers don't know about it.
			return
		}
		res, err := objectToStored(kind, cobj)
		if err != nil {
			return
		}
		s.notify(storepkg.WatchEvent{Type: storepkg.WatchEventDelete, Resource: res})
		return
	}

	// PUT path: Add (oldObj == nil) or Update (oldObj != nil) from informer.
	nowReady := isReady(cobj)
	var oldReady bool
	var oldTyped client.Object
	if oldObj != nil {
		if t, ok := oldObj.(client.Object); ok {
			oldTyped = t
			oldReady = isReady(t)
		}
	}

	switch {
	case nowReady && !oldReady:
		// Newly Ready (or first Add of an already-Ready resource).
		res, err := objectToStored(kind, cobj)
		if err != nil {
			return
		}
		s.notify(storepkg.WatchEvent{Type: storepkg.WatchEventPut, Resource: res})

	case oldReady && !nowReady:
		// Lost Ready — consumers should drop it.
		res, err := objectToStored(kind, cobj)
		if err != nil {
			return
		}
		s.notify(storepkg.WatchEvent{Type: storepkg.WatchEventDelete, Resource: res})

	case oldReady && nowReady:
		// Still Ready — only emit on real spec changes.
		if !specChanged(oldTyped, cobj) {
			return
		}
		res, err := objectToStored(kind, cobj)
		if err != nil {
			return
		}
		event := storepkg.WatchEvent{Type: storepkg.WatchEventPut, Resource: res}
		if oldRes, err := objectToStored(kind, oldTyped); err == nil {
			event.OldResource = oldRes
		}
		s.notify(event)

	default:
		// !oldReady && !nowReady — never in the projectable view, still isn't.
	}
}

// isReady reports whether obj's status carries a Ready condition set to
// True. The K8s store bridge gates xDS-bound events on this — only Ready
// resources flow to consumers.
func isReady(obj client.Object) bool {
	data, err := json.Marshal(obj)
	if err != nil {
		return false
	}
	var s struct {
		Status struct {
			Conditions []metav1.Condition `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return false
	}
	for _, c := range s.Status.Conditions {
		if c.Type == flowcv1alpha1.ConditionReady {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

// specChanged reports whether two objects' .spec differ by JSON. Used in
// the Ready→Ready transition to suppress events caused only by status
// updates that don't change xDS-relevant content.
func specChanged(oldObj, newObj client.Object) bool {
	if oldObj == nil {
		return true
	}
	oldData, err := json.Marshal(oldObj)
	if err != nil {
		return true
	}
	newData, err := json.Marshal(newObj)
	if err != nil {
		return true
	}
	var oldEnv, newEnv struct {
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(oldData, &oldEnv); err != nil {
		return true
	}
	if err := json.Unmarshal(newData, &newEnv); err != nil {
		return true
	}
	return string(oldEnv.Spec) != string(newEnv.Spec)
}

func (s *Store) notify(event storepkg.WatchEvent) {
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
			// Drop if buffer full — consumer too slow.
		}
	}
}

// objectToStored converts a typed CRD object into a StoredResource. It uses
// JSON round-tripping so Spec/Status survive as RawMessage without needing a
// per-kind accessor.
func objectToStored(kind string, obj client.Object) (*storepkg.StoredResource, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", kind, err)
	}
	var envelope struct {
		Spec   json.RawMessage `json:"spec"`
		Status json.RawMessage `json:"status"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal %s envelope: %w", kind, err)
	}

	om := metav1.ObjectMeta{
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		ResourceVersion:   obj.GetResourceVersion(),
		Labels:            obj.GetLabels(),
		Annotations:       obj.GetAnnotations(),
		CreationTimestamp: obj.GetCreationTimestamp(),
	}

	return &storepkg.StoredResource{
		Meta:       storepkg.ObjectMetaToStoreMeta(kind, om),
		SpecJSON:   envelope.Spec,
		StatusJSON: envelope.Status,
	}, nil
}

// applyStoredToObject writes a StoredResource's metadata and spec onto a
// typed object via a JSON round-trip. Preserves ResourceVersion when supplied
// (required for Update).
func applyStoredToObject(res *storepkg.StoredResource, obj client.Object, namespace string, opts storepkg.PutOptions, resourceVersion string) error {
	metadata := map[string]any{
		"name":      res.Meta.Name,
		"namespace": namespace,
	}
	if res.Meta.Labels != nil {
		metadata["labels"] = res.Meta.Labels
	}
	annotations := mergeAnnotations(res, opts)
	if len(annotations) > 0 {
		metadata["annotations"] = annotations
	}
	if resourceVersion != "" {
		metadata["resourceVersion"] = resourceVersion
	}

	full := map[string]any{
		"apiVersion": apiVersion,
		"kind":       res.Meta.Kind,
		"metadata":   metadata,
	}
	if len(res.SpecJSON) > 0 {
		full["spec"] = res.SpecJSON
	}

	data, err := json.Marshal(full)
	if err != nil {
		return fmt.Errorf("marshal desired %s/%s: %w", res.Meta.Kind, res.Meta.Name, err)
	}
	if err := json.Unmarshal(data, obj); err != nil {
		return fmt.Errorf("unmarshal desired %s/%s: %w", res.Meta.Kind, res.Meta.Name, err)
	}
	return nil
}

// hasStatus returns true when the caller-supplied StatusJSON actually
// contains something worth writing. We treat nil, empty, and the bare JSON
// literal `null` as "no status provided".
func hasStatus(statusJSON []byte) bool {
	if len(statusJSON) == 0 {
		return false
	}
	trimmed := string(statusJSON)
	return trimmed != "null"
}

// applyStatusToObject unmarshals `{"status": ...}` onto obj, replacing only
// the Status field. Relies on json.Unmarshal's behaviour of leaving fields
// absent from the JSON untouched.
func applyStatusToObject(statusJSON []byte, obj client.Object) error {
	wrap := map[string]json.RawMessage{"status": json.RawMessage(statusJSON)}
	data, err := json.Marshal(wrap)
	if err != nil {
		return fmt.Errorf("marshal status wrapper: %w", err)
	}
	if err := json.Unmarshal(data, obj); err != nil {
		return fmt.Errorf("unmarshal status: %w", err)
	}
	return nil
}

func mergeAnnotations(res *storepkg.StoredResource, opts storepkg.PutOptions) map[string]string {
	out := make(map[string]string, len(res.Meta.Annotations)+2)
	maps.Copy(out, res.Meta.Annotations)
	managedBy := opts.ManagedBy
	if managedBy == "" {
		managedBy = res.Meta.ManagedBy
	}
	if managedBy != "" {
		out[storepkg.AnnotationManagedBy] = managedBy
	}
	if res.Meta.ConflictPolicy != "" {
		out[storepkg.AnnotationConflictPolicy] = res.Meta.ConflictPolicy
	}
	return out
}

func metaRevisionFromObject(obj client.Object) int64 {
	om := metav1.ObjectMeta{ResourceVersion: obj.GetResourceVersion()}
	return storepkg.ObjectMetaToStoreMeta("", om).Revision
}

func matchesLabels(actual, want map[string]string) bool {
	for k, v := range want {
		if actual[k] != v {
			return false
		}
	}
	return true
}

func matchesWatchFilter(event storepkg.WatchEvent, f storepkg.WatchFilter) bool {
	if f.Kind == "" {
		return true
	}
	res := event.Resource
	if res == nil {
		return false
	}
	return res.Meta.Kind == f.Kind
}
