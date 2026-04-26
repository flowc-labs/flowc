// Package cache wraps the go-control-plane snapshot cache with the small
// set of operations the FlowC translation pipeline actually needs.
//
// Two distinct write paths exist:
//
//   - DeployAPI / UnDeployAPI: per-deployment merge + remove. Operates on
//     clusters / endpoints / routes, never touches listeners. Used by the
//     dispatch package's DeploymentTranslator.
//
//   - ReplaceSnapshot: full-snapshot replace including listeners. Used by
//     the dispatch package's GatewayTranslator for full gateway rebuilds
//     (Gateway events, Listener events, startup).
//
// Listeners are intentionally gateway-scoped — they live on Snapshot, not
// APIDeployment. A single deployment never publishes or removes a listener.
package cache

import (
	"context"
	"fmt"
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// ConfigManager manages xDS configuration snapshots per Envoy node.
type ConfigManager struct {
	cache  cachev3.SnapshotCache
	logger *logger.EnvoyLogger
}

// NewConfigManager creates a new configuration manager.
func NewConfigManager(cache cachev3.SnapshotCache, log *logger.EnvoyLogger) *ConfigManager {
	return &ConfigManager{
		cache:  cache,
		logger: log,
	}
}

// UpdateSnapshot updates the configuration snapshot for a given node ID.
// Validates internal consistency before installing.
func (cm *ConfigManager) UpdateSnapshot(nodeID string, snapshot *cachev3.Snapshot) error {
	if err := snapshot.Consistent(); err != nil {
		return fmt.Errorf("snapshot inconsistent: %w", err)
	}
	if err := cm.cache.SetSnapshot(context.Background(), nodeID, snapshot); err != nil {
		return fmt.Errorf("failed to set snapshot: %w", err)
	}
	cm.logger.Infof("Updated snapshot for node %s", nodeID)
	return nil
}

// GetSnapshot retrieves the current snapshot for a given node ID.
func (cm *ConfigManager) GetSnapshot(nodeID string) (*cachev3.Snapshot, error) {
	snapshot, err := cm.cache.GetSnapshot(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot for node %s: %w", nodeID, err)
	}
	concrete, ok := snapshot.(*cachev3.Snapshot)
	if !ok {
		return nil, fmt.Errorf("snapshot is not of type *cachev3.Snapshot")
	}
	return concrete, nil
}

// CreateEmptySnapshot creates an empty snapshot for a node.
func (cm *ConfigManager) CreateEmptySnapshot(nodeID string) (*cachev3.Snapshot, error) {
	snapshot, err := cachev3.NewSnapshot(
		"0",
		map[resourcev3.Type][]types.Resource{
			resourcev3.ClusterType:  {},
			resourcev3.EndpointType: {},
			resourcev3.ListenerType: {},
			resourcev3.RouteType:    {},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot: %w", err)
	}
	return snapshot, nil
}

// APIDeployment carries the xDS resources owned by a single API deployment.
// Used with DeployAPI (merge into snapshot) and analogously with
// UnDeployAPI (remove by name). Has no Listeners field — listeners are
// gateway-scoped and live on Snapshot.
type APIDeployment struct {
	Clusters  []*clusterv3.Cluster
	Endpoints []*endpointv3.ClusterLoadAssignment
	Routes    []*routev3.RouteConfiguration
}

// Snapshot is the complete xDS resource set for one node, used by
// ReplaceSnapshot for full gateway rebuilds. Includes listeners since
// rebuilds reconstruct the entire snapshot including the listener layer.
type Snapshot struct {
	Clusters  []*clusterv3.Cluster
	Endpoints []*endpointv3.ClusterLoadAssignment
	Listeners []*listenerv3.Listener
	Routes    []*routev3.RouteConfiguration
}

// DeployAPI merges a single deployment's clusters / endpoints / routes
// into the node's existing snapshot. Dedup by name means re-deploying the
// same deployment replaces (rather than duplicates) its xDS resources.
// Listeners pass through unchanged from the previous snapshot.
func (cm *ConfigManager) DeployAPI(nodeID string, deployment *APIDeployment) error {
	snapshot, err := cm.GetSnapshot(nodeID)
	if err != nil {
		snapshot, err = cm.CreateEmptySnapshot(nodeID)
		if err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}
	}

	resources := make(map[resourcev3.Type][]types.Resource)

	// Dedup clusters by name.
	clusterMap := make(map[string]types.Resource)
	for _, res := range snapshot.GetResources(resourcev3.ClusterType) {
		if c, ok := res.(*clusterv3.Cluster); ok {
			clusterMap[c.Name] = res
		}
	}
	for _, c := range deployment.Clusters {
		clusterMap[c.Name] = c
	}
	clusterResources := make([]types.Resource, 0, len(clusterMap))
	for _, res := range clusterMap {
		clusterResources = append(clusterResources, res)
	}
	resources[resourcev3.ClusterType] = clusterResources

	// Dedup endpoints by ClusterName.
	endpointMap := make(map[string]types.Resource)
	for _, res := range snapshot.GetResources(resourcev3.EndpointType) {
		if e, ok := res.(*endpointv3.ClusterLoadAssignment); ok {
			endpointMap[e.ClusterName] = res
		}
	}
	for _, e := range deployment.Endpoints {
		endpointMap[e.ClusterName] = e
	}
	endpointResources := make([]types.Resource, 0, len(endpointMap))
	for _, res := range endpointMap {
		endpointResources = append(endpointResources, res)
	}
	resources[resourcev3.EndpointType] = endpointResources

	// Dedup routes by name.
	routeMap := make(map[string]types.Resource)
	for _, res := range snapshot.GetResources(resourcev3.RouteType) {
		if r, ok := res.(*routev3.RouteConfiguration); ok {
			routeMap[r.Name] = res
		}
	}
	for _, r := range deployment.Routes {
		routeMap[r.Name] = r
	}
	routeResources := make([]types.Resource, 0, len(routeMap))
	for _, res := range routeMap {
		routeResources = append(routeResources, res)
	}
	resources[resourcev3.RouteType] = routeResources

	// Listeners pass through untouched — they're owned by the gateway-
	// scoped path (ReplaceSnapshot), never published per-deployment.
	resources[resourcev3.ListenerType] = convertResourceMap(snapshot.GetResources(resourcev3.ListenerType))

	// Monotonic timestamp version: count-based versions can go backwards
	// on resource removal and cause Envoy to skip updates.
	newVersion := fmt.Sprintf("%d", time.Now().UnixNano())
	newSnapshot, err := cachev3.NewSnapshot(newVersion, resources)
	if err != nil {
		return fmt.Errorf("failed to create new snapshot: %w", err)
	}
	return cm.UpdateSnapshot(nodeID, newSnapshot)
}

// ResourceNames identifies the named xDS resources owned by a single API
// deployment. Returned by translators after a successful deploy and
// passed to UnDeployAPI to remove just those resources from a node's
// snapshot. Endpoints uses cluster names because xDS keys endpoints by
// their ClusterName field.
type ResourceNames struct {
	Clusters  []string
	Endpoints []string // by ClusterName
	Routes    []string
}

// UnDeployAPI removes named clusters, endpoints, and routes from a node's
// snapshot. Listeners are deliberately untouched — their lifecycle is
// owned by the gateway-level translator (full snapshot rebuild on
// Listener events), not by per-deployment publish/unpublish.
//
// Removal is idempotent: missing names are silently skipped, missing
// snapshots return nil.
func (cm *ConfigManager) UnDeployAPI(nodeID string, names ResourceNames) error {
	snapshot, err := cm.GetSnapshot(nodeID)
	if err != nil {
		return nil
	}

	dropClusters := stringSet(names.Clusters)
	dropEndpoints := stringSet(names.Endpoints)
	dropRoutes := stringSet(names.Routes)

	resources := make(map[resourcev3.Type][]types.Resource)

	keepClusters := make([]types.Resource, 0)
	for _, res := range snapshot.GetResources(resourcev3.ClusterType) {
		c, ok := res.(*clusterv3.Cluster)
		if !ok {
			keepClusters = append(keepClusters, res)
			continue
		}
		if _, drop := dropClusters[c.Name]; drop {
			continue
		}
		keepClusters = append(keepClusters, res)
	}
	resources[resourcev3.ClusterType] = keepClusters

	keepEndpoints := make([]types.Resource, 0)
	for _, res := range snapshot.GetResources(resourcev3.EndpointType) {
		e, ok := res.(*endpointv3.ClusterLoadAssignment)
		if !ok {
			keepEndpoints = append(keepEndpoints, res)
			continue
		}
		if _, drop := dropEndpoints[e.ClusterName]; drop {
			continue
		}
		keepEndpoints = append(keepEndpoints, res)
	}
	resources[resourcev3.EndpointType] = keepEndpoints

	keepRoutes := make([]types.Resource, 0)
	for _, res := range snapshot.GetResources(resourcev3.RouteType) {
		r, ok := res.(*routev3.RouteConfiguration)
		if !ok {
			keepRoutes = append(keepRoutes, res)
			continue
		}
		if _, drop := dropRoutes[r.Name]; drop {
			continue
		}
		keepRoutes = append(keepRoutes, res)
	}
	resources[resourcev3.RouteType] = keepRoutes

	resources[resourcev3.ListenerType] = convertResourceMap(snapshot.GetResources(resourcev3.ListenerType))

	newVersion := fmt.Sprintf("%d", time.Now().UnixNano())
	newSnapshot, err := cachev3.NewSnapshot(newVersion, resources)
	if err != nil {
		return fmt.Errorf("failed to create new snapshot: %w", err)
	}
	return cm.UpdateSnapshot(nodeID, newSnapshot)
}

// ReplaceSnapshot sets the node's snapshot to exactly the provided
// resources. Used for full gateway rebuilds where the dispatcher has
// re-translated every deployment plus every listener for that gateway.
func (cm *ConfigManager) ReplaceSnapshot(nodeID string, snap *Snapshot) error {
	resources := make(map[resourcev3.Type][]types.Resource)

	clusters := make([]types.Resource, 0, len(snap.Clusters))
	for _, c := range snap.Clusters {
		clusters = append(clusters, c)
	}
	resources[resourcev3.ClusterType] = clusters

	endpoints := make([]types.Resource, 0, len(snap.Endpoints))
	for _, e := range snap.Endpoints {
		endpoints = append(endpoints, e)
	}
	resources[resourcev3.EndpointType] = endpoints

	listeners := make([]types.Resource, 0, len(snap.Listeners))
	for _, l := range snap.Listeners {
		listeners = append(listeners, l)
	}
	resources[resourcev3.ListenerType] = listeners

	routes := make([]types.Resource, 0, len(snap.Routes))
	for _, r := range snap.Routes {
		routes = append(routes, r)
	}
	resources[resourcev3.RouteType] = routes

	newVersion := fmt.Sprintf("%d", time.Now().UnixNano())
	newSnapshot, err := cachev3.NewSnapshot(newVersion, resources)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}
	return cm.UpdateSnapshot(nodeID, newSnapshot)
}

// RemoveNode drops all configuration for a given node ID. Used when a
// Gateway is deleted.
func (cm *ConfigManager) RemoveNode(nodeID string) {
	cm.cache.ClearSnapshot(nodeID)
	cm.logger.Infof("Removed configuration for node %s", nodeID)
}

// ListNodes returns the set of node IDs that have a snapshot installed.
func (cm *ConfigManager) ListNodes() []string {
	return cm.cache.GetStatusKeys()
}

// --- helpers ---

func stringSet(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, i := range items {
		out[i] = struct{}{}
	}
	return out
}

func convertResourceMap(resourceMap map[string]types.Resource) []types.Resource {
	resources := make([]types.Resource, 0, len(resourceMap))
	for _, res := range resourceMap {
		resources = append(resources, res)
	}
	return resources
}
