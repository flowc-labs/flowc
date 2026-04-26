// Package index maintains an in-memory mirror of Ready resources from the
// Store, plus reverse indexes for "what depends on X?" queries and an
// ownership map tracking which xDS resource names belong to which
// deployment.
//
// Translators consume the indexer to look up dependent resources during
// translation without making per-call store lookups. The indexer is
// populated by a Watch consumer (typically the reconciler) that calls
// Apply for each event, and Bootstrap once at startup.
//
// All resources held here are by definition Ready — the K8s store backend's
// projectability filter only emits events for Ready resources, so by the
// time something lands in the indexer it has been validated and its
// references have resolved.
package index

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	flowcv1alpha1 "github.com/flowc-labs/flowc/api/v1alpha1"
	"github.com/flowc-labs/flowc/internal/flowc/store"
	"github.com/flowc-labs/flowc/internal/flowc/xds/cache"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// kindAPI is the StoredResource Kind value for flowc API CRs. Promoted
// to a constant because it appears in several lookup paths
// (case-switching, APIPolicy targetRef matching).
const kindAPI = "API"

// AffectedTask describes a translation the dispatcher should run as a
// result of a store event. The Kind is always one of the kinds that has
// a translator (today: Gateway, Deployment); other kinds (Listener, API,
// APIPolicy) translate into AffectedTasks for the kinds that do.
type AffectedTask struct {
	// Kind is the translator to invoke. "Gateway" or "Deployment".
	Kind string

	// Name identifies the owner resource of that Kind.
	Name string

	// Deletion is true when the upstream event was a delete of the named
	// resource (not just an event that affects it). Drives whether the
	// translator runs in upsert or remove mode.
	Deletion bool

	// NodeID is populated for Gateway deletes so the translator can call
	// cache.RemoveNode without needing the (now-deleted) Gateway resource
	// to be present in the indexer. Empty for all other tasks.
	NodeID string
}

// Indexer is the in-memory projection consumed by translators.
type Indexer struct {
	mu  sync.RWMutex
	log *logger.EnvoyLogger

	// Primary indexes — typed v1alpha1 objects, keyed by name.
	gateways    map[string]*flowcv1alpha1.Gateway
	listeners   map[string]*flowcv1alpha1.Listener
	apis        map[string]*flowcv1alpha1.API
	deployments map[string]*flowcv1alpha1.Deployment
	apiPolicies map[string]*flowcv1alpha1.APIPolicy

	// Reverse indexes — for invalidation lookup ("who depends on X?").
	listenersByGateway     map[string][]string // gw → []listener
	deploymentsByGateway   map[string][]string // gw → []deployment
	deploymentsByAPI       map[string][]string // api → []deployment
	deploymentsByListener  map[string][]string // listener → []deployment
	apiPoliciesByTargetAPI map[string][]string // api → []apiPolicy

	// Ownership: nodeID → depName → xDS names actually pushed.
	// Populated by RecordOwnership after the reconciler finishes a
	// successful translate+publish. Read on Deployment delete to know
	// which xDS resources to undeploy.
	ownership map[string]map[string]cache.ResourceNames
}

// New constructs an empty indexer. Call Bootstrap before processing
// Watch events to populate it from the store.
func New(log *logger.EnvoyLogger) *Indexer {
	return &Indexer{
		log:                    log,
		gateways:               make(map[string]*flowcv1alpha1.Gateway),
		listeners:              make(map[string]*flowcv1alpha1.Listener),
		apis:                   make(map[string]*flowcv1alpha1.API),
		deployments:            make(map[string]*flowcv1alpha1.Deployment),
		apiPolicies:            make(map[string]*flowcv1alpha1.APIPolicy),
		listenersByGateway:     make(map[string][]string),
		deploymentsByGateway:   make(map[string][]string),
		deploymentsByAPI:       make(map[string][]string),
		deploymentsByListener:  make(map[string][]string),
		apiPoliciesByTargetAPI: make(map[string][]string),
		ownership:              make(map[string]map[string]cache.ResourceNames),
	}
}

// Bootstrap fills the indexer from the current state of the store. Call
// once on startup, before processing the first Watch event. Subsequent
// duplicate Apply calls (when a Watch event echoes a List result) are
// safe — Apply is idempotent.
func (i *Indexer) Bootstrap(ctx context.Context, s store.Store) error {
	for _, kind := range []string{"Gateway", "Listener", kindAPI, "Deployment", "APIPolicy"} {
		items, err := s.List(ctx, store.ListFilter{Kind: kind})
		if err != nil {
			return fmt.Errorf("list %s: %w", kind, err)
		}
		for _, res := range items {
			i.Apply(store.WatchEvent{Type: store.WatchEventPut, Resource: res})
		}
	}
	return nil
}

// Apply incorporates a single store event and returns the translation
// tasks affected by it. The returned tasks should be debounced and
// dispatched by the caller.
//
// For events that cause the affected resource itself to need translation
// (Gateway, Deployment), one task with Deletion set per the event type.
// For events that affect dependents (API, APIPolicy, Listener), one
// task per dependent — never a task for the source kind itself.
func (i *Indexer) Apply(event store.WatchEvent) []AffectedTask {
	res := event.Resource
	if res == nil {
		return nil
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	switch res.Meta.Kind {
	case "Gateway":
		return i.applyGateway(event)
	case "Listener":
		return i.applyListener(event)
	case kindAPI:
		return i.applyAPI(event)
	case "Deployment":
		return i.applyDeployment(event)
	case "APIPolicy":
		return i.applyAPIPolicy(event)
	default:
		return nil
	}
}

// --- Per-kind appliers ---

func (i *Indexer) applyGateway(event store.WatchEvent) []AffectedTask {
	name := event.Resource.Meta.Name
	if event.Type == store.WatchEventDelete {
		// Capture NodeID before removing the gateway so the translator can
		// clear the right node's snapshot without needing the indexer to
		// still hold the deleted gateway.
		var nodeID string
		if old, ok := i.gateways[name]; ok {
			nodeID = old.Spec.NodeID
		}
		delete(i.gateways, name)
		return []AffectedTask{{Kind: "Gateway", Name: name, Deletion: true, NodeID: nodeID}}
	}
	gw, err := decodeGateway(event.Resource)
	if err != nil {
		i.warn("decode Gateway", name, err)
		return nil
	}
	i.gateways[name] = gw
	return []AffectedTask{{Kind: "Gateway", Name: name}}
}

func (i *Indexer) applyListener(event store.WatchEvent) []AffectedTask {
	name := event.Resource.Meta.Name
	if event.Type == store.WatchEventDelete {
		old, ok := i.listeners[name]
		if !ok {
			return nil
		}
		delete(i.listeners, name)
		removeFromIndex(i.listenersByGateway, old.Spec.GatewayRef, name)
		return []AffectedTask{{Kind: "Gateway", Name: old.Spec.GatewayRef}}
	}
	l, err := decodeListener(event.Resource)
	if err != nil {
		i.warn("decode Listener", name, err)
		return nil
	}
	if old, exists := i.listeners[name]; exists && old.Spec.GatewayRef != l.Spec.GatewayRef {
		removeFromIndex(i.listenersByGateway, old.Spec.GatewayRef, name)
	}
	i.listeners[name] = l
	addToIndex(i.listenersByGateway, l.Spec.GatewayRef, name)
	return []AffectedTask{{Kind: "Gateway", Name: l.Spec.GatewayRef}}
}

func (i *Indexer) applyAPI(event store.WatchEvent) []AffectedTask {
	name := event.Resource.Meta.Name
	if event.Type == store.WatchEventDelete {
		delete(i.apis, name)
		// Cascading Deployment deletes (when their controller flips them
		// to not-Ready due to the missing API) handle xDS cleanup.
		return nil
	}
	api, err := decodeAPI(event.Resource)
	if err != nil {
		i.warn("decode API", name, err)
		return nil
	}
	i.apis[name] = api
	// Re-translate every dependent deployment so spec changes (e.g. new
	// OpenAPI routes) propagate even when the deployments themselves
	// haven't changed.
	return i.deploymentTasksFor(i.deploymentsByAPI[name])
}

func (i *Indexer) applyDeployment(event store.WatchEvent) []AffectedTask {
	name := event.Resource.Meta.Name
	if event.Type == store.WatchEventDelete {
		old, ok := i.deployments[name]
		if !ok {
			return nil
		}
		delete(i.deployments, name)
		removeFromIndex(i.deploymentsByGateway, old.Spec.Gateway.Name, name)
		removeFromIndex(i.deploymentsByAPI, old.Spec.APIRef, name)
		if old.Spec.Gateway.Listener != "" {
			removeFromIndex(i.deploymentsByListener, old.Spec.Gateway.Listener, name)
		}
		return []AffectedTask{{Kind: "Deployment", Name: name, Deletion: true}}
	}
	dep, err := decodeDeployment(event.Resource)
	if err != nil {
		i.warn("decode Deployment", name, err)
		return nil
	}
	if old, exists := i.deployments[name]; exists {
		if old.Spec.Gateway.Name != dep.Spec.Gateway.Name {
			removeFromIndex(i.deploymentsByGateway, old.Spec.Gateway.Name, name)
		}
		if old.Spec.APIRef != dep.Spec.APIRef {
			removeFromIndex(i.deploymentsByAPI, old.Spec.APIRef, name)
		}
		if old.Spec.Gateway.Listener != dep.Spec.Gateway.Listener {
			removeFromIndex(i.deploymentsByListener, old.Spec.Gateway.Listener, name)
		}
	}
	i.deployments[name] = dep
	addToIndex(i.deploymentsByGateway, dep.Spec.Gateway.Name, name)
	addToIndex(i.deploymentsByAPI, dep.Spec.APIRef, name)
	if dep.Spec.Gateway.Listener != "" {
		addToIndex(i.deploymentsByListener, dep.Spec.Gateway.Listener, name)
	}
	return []AffectedTask{{Kind: "Deployment", Name: name}}
}

func (i *Indexer) applyAPIPolicy(event store.WatchEvent) []AffectedTask {
	name := event.Resource.Meta.Name
	if event.Type == store.WatchEventDelete {
		old, ok := i.apiPolicies[name]
		if !ok {
			return nil
		}
		delete(i.apiPolicies, name)
		if old.Spec.TargetRef.Kind == kindAPI {
			removeFromIndex(i.apiPoliciesByTargetAPI, old.Spec.TargetRef.Name, name)
			return i.deploymentTasksFor(i.deploymentsByAPI[old.Spec.TargetRef.Name])
		}
		return nil
	}
	pol, err := decodeAPIPolicy(event.Resource)
	if err != nil {
		i.warn("decode APIPolicy", name, err)
		return nil
	}
	if old, exists := i.apiPolicies[name]; exists && old.Spec.TargetRef.Kind == kindAPI &&
		old.Spec.TargetRef.Name != pol.Spec.TargetRef.Name {
		removeFromIndex(i.apiPoliciesByTargetAPI, old.Spec.TargetRef.Name, name)
	}
	i.apiPolicies[name] = pol
	if pol.Spec.TargetRef.Kind == kindAPI {
		addToIndex(i.apiPoliciesByTargetAPI, pol.Spec.TargetRef.Name, name)
		return i.deploymentTasksFor(i.deploymentsByAPI[pol.Spec.TargetRef.Name])
	}
	return nil
}

func (i *Indexer) deploymentTasksFor(names []string) []AffectedTask {
	if len(names) == 0 {
		return nil
	}
	out := make([]AffectedTask, 0, len(names))
	for _, n := range names {
		out = append(out, AffectedTask{Kind: "Deployment", Name: n})
	}
	return out
}

// --- Lookups (read-locked, return defensively-shared pointers) ---
//
// Returned pointers point at the indexer's own copies; callers must treat
// them as read-only. The indexer never mutates a stored object after Apply
// (it overwrites the map entry with a fresh decoded copy on each event).

func (i *Indexer) GetGateway(name string) (*flowcv1alpha1.Gateway, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	v, ok := i.gateways[name]
	return v, ok
}

func (i *Indexer) GetListener(name string) (*flowcv1alpha1.Listener, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	v, ok := i.listeners[name]
	return v, ok
}

func (i *Indexer) GetAPI(name string) (*flowcv1alpha1.API, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	v, ok := i.apis[name]
	return v, ok
}

func (i *Indexer) GetDeployment(name string) (*flowcv1alpha1.Deployment, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	v, ok := i.deployments[name]
	return v, ok
}

func (i *Indexer) Gateways() []*flowcv1alpha1.Gateway {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := make([]*flowcv1alpha1.Gateway, 0, len(i.gateways))
	for _, v := range i.gateways {
		out = append(out, v)
	}
	return out
}

func (i *Indexer) ListenersForGateway(gw string) []*flowcv1alpha1.Listener {
	i.mu.RLock()
	defer i.mu.RUnlock()
	names := i.listenersByGateway[gw]
	out := make([]*flowcv1alpha1.Listener, 0, len(names))
	for _, n := range names {
		if v, ok := i.listeners[n]; ok {
			out = append(out, v)
		}
	}
	return out
}

func (i *Indexer) DeploymentsForGateway(gw string) []*flowcv1alpha1.Deployment {
	i.mu.RLock()
	defer i.mu.RUnlock()
	names := i.deploymentsByGateway[gw]
	out := make([]*flowcv1alpha1.Deployment, 0, len(names))
	for _, n := range names {
		if v, ok := i.deployments[n]; ok {
			out = append(out, v)
		}
	}
	return out
}

func (i *Indexer) DeploymentsForAPI(api string) []*flowcv1alpha1.Deployment {
	i.mu.RLock()
	defer i.mu.RUnlock()
	names := i.deploymentsByAPI[api]
	out := make([]*flowcv1alpha1.Deployment, 0, len(names))
	for _, n := range names {
		if v, ok := i.deployments[n]; ok {
			out = append(out, v)
		}
	}
	return out
}

func (i *Indexer) DeploymentsForListener(listener string) []*flowcv1alpha1.Deployment {
	i.mu.RLock()
	defer i.mu.RUnlock()
	names := i.deploymentsByListener[listener]
	out := make([]*flowcv1alpha1.Deployment, 0, len(names))
	for _, n := range names {
		if v, ok := i.deployments[n]; ok {
			out = append(out, v)
		}
	}
	return out
}

func (i *Indexer) APIPoliciesForAPI(api string) []*flowcv1alpha1.APIPolicy {
	i.mu.RLock()
	defer i.mu.RUnlock()
	names := i.apiPoliciesByTargetAPI[api]
	out := make([]*flowcv1alpha1.APIPolicy, 0, len(names))
	for _, n := range names {
		if v, ok := i.apiPolicies[n]; ok {
			out = append(out, v)
		}
	}
	return out
}

// --- Ownership tracking ---

// RecordOwnership stores the xDS resource names produced by a successful
// translation of a single deployment, so a future delete can call
// cache.UnDeployAPI with exactly the names that were pushed.
func (i *Indexer) RecordOwnership(nodeID, depName string, names cache.ResourceNames) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ownership[nodeID] == nil {
		i.ownership[nodeID] = make(map[string]cache.ResourceNames)
	}
	i.ownership[nodeID][depName] = names
}

// GetOwnership returns the recorded names for a deployment on a known
// node. Use OwnershipForDeployment when the nodeID isn't known up front.
func (i *Indexer) GetOwnership(nodeID, depName string) (cache.ResourceNames, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if perDep, ok := i.ownership[nodeID]; ok {
		names, ok := perDep[depName]
		return names, ok
	}
	return cache.ResourceNames{}, false
}

// OwnershipForDeployment finds the node a deployment was published to and
// the names it owns there. Used on Deployment Delete, where the deployment
// is already gone from the primary index and we can't read its
// spec.gateway.name to derive the node. Linear scan over nodes; N is
// expected to be small (one per gateway).
func (i *Indexer) OwnershipForDeployment(depName string) (nodeID string, names cache.ResourceNames, ok bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	for nID, perDep := range i.ownership {
		if n, exists := perDep[depName]; exists {
			return nID, n, true
		}
	}
	return "", cache.ResourceNames{}, false
}

// ClearOwnership removes the deployment's entry; called after a successful
// UnDeployAPI on the cache.
func (i *Indexer) ClearOwnership(nodeID, depName string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if perDep, ok := i.ownership[nodeID]; ok {
		delete(perDep, depName)
		if len(perDep) == 0 {
			delete(i.ownership, nodeID)
		}
	}
}

// ClearOwnershipForNode wipes the ownership map for an entire node, used
// when a Gateway is deleted and its node's snapshot is dropped.
func (i *Indexer) ClearOwnershipForNode(nodeID string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.ownership, nodeID)
}

// --- helpers ---

func (i *Indexer) warn(op, name string, err error) {
	if i.log == nil {
		return
	}
	i.log.WithFields(map[string]any{
		"name":  name,
		"error": err.Error(),
	}).Warn(op)
}

func addToIndex(idx map[string][]string, key, val string) {
	if key == "" {
		return
	}
	if slices.Contains(idx[key], val) {
		return
	}
	idx[key] = append(idx[key], val)
}

func removeFromIndex(idx map[string][]string, key, val string) {
	if key == "" {
		return
	}
	cur, ok := idx[key]
	if !ok {
		return
	}
	for n, existing := range cur {
		if existing == val {
			idx[key] = append(cur[:n], cur[n+1:]...)
			if len(idx[key]) == 0 {
				delete(idx, key)
			}
			return
		}
	}
}

// --- decoders: StoredResource → typed v1alpha1 object ---

func decodeGateway(r *store.StoredResource) (*flowcv1alpha1.Gateway, error) {
	obj := &flowcv1alpha1.Gateway{}
	applyMeta(r, &obj.Name, &obj.Labels, &obj.Annotations)
	if err := unmarshalSpecStatus(r, &obj.Spec, &obj.Status); err != nil {
		return nil, err
	}
	return obj, nil
}

func decodeListener(r *store.StoredResource) (*flowcv1alpha1.Listener, error) {
	obj := &flowcv1alpha1.Listener{}
	applyMeta(r, &obj.Name, &obj.Labels, &obj.Annotations)
	if err := unmarshalSpecStatus(r, &obj.Spec, &obj.Status); err != nil {
		return nil, err
	}
	return obj, nil
}

func decodeAPI(r *store.StoredResource) (*flowcv1alpha1.API, error) {
	obj := &flowcv1alpha1.API{}
	applyMeta(r, &obj.Name, &obj.Labels, &obj.Annotations)
	if err := unmarshalSpecStatus(r, &obj.Spec, &obj.Status); err != nil {
		return nil, err
	}
	return obj, nil
}

func decodeDeployment(r *store.StoredResource) (*flowcv1alpha1.Deployment, error) {
	obj := &flowcv1alpha1.Deployment{}
	applyMeta(r, &obj.Name, &obj.Labels, &obj.Annotations)
	if err := unmarshalSpecStatus(r, &obj.Spec, &obj.Status); err != nil {
		return nil, err
	}
	return obj, nil
}

func decodeAPIPolicy(r *store.StoredResource) (*flowcv1alpha1.APIPolicy, error) {
	obj := &flowcv1alpha1.APIPolicy{}
	applyMeta(r, &obj.Name, &obj.Labels, &obj.Annotations)
	if err := unmarshalSpecStatus(r, &obj.Spec, &obj.Status); err != nil {
		return nil, err
	}
	return obj, nil
}

func applyMeta(r *store.StoredResource, name *string, labels, annotations *map[string]string) {
	*name = r.Meta.Name
	if r.Meta.Labels != nil {
		*labels = r.Meta.Labels
	}
	if r.Meta.Annotations != nil {
		*annotations = r.Meta.Annotations
	}
}

func unmarshalSpecStatus(r *store.StoredResource, spec, status any) error {
	if len(r.SpecJSON) > 0 {
		if err := json.Unmarshal(r.SpecJSON, spec); err != nil {
			return fmt.Errorf("unmarshal spec: %w", err)
		}
	}
	if len(r.StatusJSON) > 0 {
		// Best effort — status decoding failure shouldn't poison the indexer.
		_ = json.Unmarshal(r.StatusJSON, status)
	}
	return nil
}
