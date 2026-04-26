package dispatch

import (
	"context"
	"fmt"

	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	flowcv1alpha1 "github.com/flowc-labs/flowc/api/v1alpha1"
	"github.com/flowc-labs/flowc/internal/flowc/index"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/xds/cache"
	listenerbuilder "github.com/flowc-labs/flowc/internal/flowc/xds/resources/listener"
	"github.com/flowc-labs/flowc/internal/flowc/xds/translator"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// GatewayTranslator handles full-snapshot rebuilds of a gateway. Used
// for Gateway and Listener events (which are routed here by the indexer)
// and for the startup full reconcile. It translates every deployment on
// the gateway, builds listeners from the gateway's Listeners, and calls
// cache.ReplaceSnapshot to atomically swap the node's snapshot.
//
// On Delete it clears the node's snapshot and ownership entries.
type GatewayTranslator struct {
	indexer *index.Indexer
	cache   *cache.ConfigManager
	parsers *ir.ParserRegistry
	options *translator.TranslatorOptions
	log     *logger.EnvoyLogger
}

// NewGatewayTranslator constructs the translator with all dependencies
// injected.
func NewGatewayTranslator(
	idx *index.Indexer,
	cm *cache.ConfigManager,
	parsers *ir.ParserRegistry,
	log *logger.EnvoyLogger,
) *GatewayTranslator {
	return &GatewayTranslator{
		indexer: idx,
		cache:   cm,
		parsers: parsers,
		options: translator.DefaultTranslatorOptions(),
		log:     log,
	}
}

// Kind returns the dispatch kind name.
func (t *GatewayTranslator) Kind() string { return "Gateway" }

// Translate routes Put/Delete tasks to the appropriate handler.
func (t *GatewayTranslator) Translate(ctx context.Context, task index.AffectedTask) error {
	if task.Deletion {
		return t.handleDelete(ctx, task)
	}
	return t.handlePut(ctx, task)
}

// handlePut runs a full rebuild for the gateway: translates every
// deployment, builds listeners from listener CRs, calls ReplaceSnapshot.
// Per-deployment translation failures are logged and skipped — they
// don't fail the whole gateway. Successful deployments are recorded in
// the indexer's ownership map.
func (t *GatewayTranslator) handlePut(ctx context.Context, task index.AffectedTask) error {
	gw, ok := t.indexer.GetGateway(task.Name)
	if !ok {
		// Gateway removed after Apply; the Delete task will follow.
		return nil
	}
	nodeID := gw.Spec.NodeID

	listeners := t.indexer.ListenersForGateway(task.Name)
	deployments := t.indexer.DeploymentsForGateway(task.Name)

	snap := &cache.Snapshot{}
	perDepNames := make(map[string]cache.ResourceNames, len(deployments))
	activeRoutes := make(map[string]struct{})

	for _, dep := range deployments {
		xds, err := translateOne(ctx, dep, t.indexer, t.parsers, t.options, t.log)
		if err != nil {
			// Per-deployment failure: log and skip; the deployment
			// will retry on its next Watch event.
			if t.log != nil {
				t.log.WithFields(map[string]any{
					"gateway":    task.Name,
					"deployment": dep.Name,
					"error":      err.Error(),
				}).Error("Skipping deployment in gateway rebuild")
			}
			continue
		}
		snap.Clusters = append(snap.Clusters, xds.Clusters...)
		snap.Endpoints = append(snap.Endpoints, xds.Endpoints...)
		snap.Routes = append(snap.Routes, xds.Routes...)
		for _, rc := range xds.Routes {
			activeRoutes[rc.Name] = struct{}{}
		}
		perDepNames[dep.Name] = resourceNamesFromXDS(xds)
	}

	// Ensure every (listener, hostname) the listener layer will reference
	// has a matching RouteConfiguration in the snapshot. Without this, the
	// cold-start case (Listener Ready before any Deployment provides
	// routes) would push a listener with an RDS reference to a name that
	// doesn't exist in the snapshot and snapshot.Consistent() would
	// reject it. Real routes from later DeployAPI calls dedup by name
	// onto these placeholders, so once a deployment's routes show up the
	// placeholder is silently replaced.
	for _, l := range listeners {
		hostnames := l.Spec.Hostnames
		if len(hostnames) == 0 {
			hostnames = []string{"*"}
		}
		for _, hostname := range hostnames {
			routeName := fmt.Sprintf("route_%s_%s", l.Name, hostname)
			if _, ok := activeRoutes[routeName]; ok {
				continue
			}
			snap.Routes = append(snap.Routes, placeholderRouteConfig(routeName, hostname))
			activeRoutes[routeName] = struct{}{}
		}
	}

	snap.Listeners = t.buildListeners(listeners)

	if err := t.cache.ReplaceSnapshot(nodeID, snap); err != nil {
		return fmt.Errorf("replace snapshot for gateway %q: %w", task.Name, err)
	}

	// Replace ownership for this node atomically: clear then re-record.
	// Old entries for deployments no longer on this gateway disappear.
	t.indexer.ClearOwnershipForNode(nodeID)
	for depName, names := range perDepNames {
		t.indexer.RecordOwnership(nodeID, depName, names)
	}

	if t.log != nil {
		t.log.WithFields(map[string]any{
			"gateway":     task.Name,
			"deployments": len(deployments),
			"clusters":    len(snap.Clusters),
			"routes":      len(snap.Routes),
			"listeners":   len(snap.Listeners),
		}).Info("Gateway snapshot rebuilt")
	}
	return nil
}

// handleDelete drops the node's snapshot and ownership entries. NodeID
// comes from the AffectedTask (captured by the indexer at delete time
// since the gateway is no longer in the indexer).
func (t *GatewayTranslator) handleDelete(_ context.Context, task index.AffectedTask) error {
	if task.NodeID == "" {
		// Never knew the gateway (no NodeID captured); nothing to clear.
		return nil
	}
	t.cache.RemoveNode(task.NodeID)
	t.indexer.ClearOwnershipForNode(task.NodeID)
	return nil
}

// buildListeners constructs xDS listeners from Listener CRs. One xDS
// listener per Listener CR, one filter chain per hostname.
//
// Naming convention: listeners are `listener_<port>`, route-config
// references are `route_<listenerName>_<hostname>` to match what the
// composite translator emits for routes (and what handlePut backfills
// with placeholder route configs when no deployment supplies routes
// yet — see the placeholder pass above).
func (t *GatewayTranslator) buildListeners(listeners []*flowcv1alpha1.Listener) []*listenerv3.Listener {
	results := make([]*listenerv3.Listener, 0, len(listeners))
	for _, l := range listeners {
		hostnames := l.Spec.Hostnames
		if len(hostnames) == 0 {
			hostnames = []string{"*"}
		}

		filterChains := make([]*listenerbuilder.FilterChainConfig, 0, len(hostnames))
		for _, hostname := range hostnames {
			filterChains = append(filterChains, &listenerbuilder.FilterChainConfig{
				Name:            hostname,
				Hostname:        hostname,
				RouteConfigName: fmt.Sprintf("route_%s_%s", l.Name, hostname),
			})
		}

		addr := l.Spec.Address
		if addr == "" {
			addr = "0.0.0.0"
		}

		config := &listenerbuilder.ListenerConfig{
			Name:         fmt.Sprintf("listener_%d", l.Spec.Port),
			Port:         l.Spec.Port,
			Address:      addr,
			FilterChains: filterChains,
			HTTP2:        l.Spec.HTTP2,
		}
		xdsListener, err := listenerbuilder.CreateListenerWithFilterChains(config)
		if err != nil {
			if t.log != nil {
				t.log.WithFields(map[string]any{
					"listener": l.Name,
					"error":    err.Error(),
				}).Error("Failed to build xDS listener")
			}
			continue
		}
		results = append(results, xdsListener)
	}
	return results
}

// placeholderRouteConfig emits a RouteConfiguration with a single empty
// VirtualHost. Used to satisfy snapshot.Consistent() when a Listener's
// hostname has no deployment-emitted routes yet — every listener filter
// chain RDS reference must resolve to a RouteConfig in the snapshot.
// DeployAPI's dedup-by-name silently replaces the placeholder when a
// deployment publishes a real route config with the same name.
func placeholderRouteConfig(name, domain string) *routev3.RouteConfiguration {
	return &routev3.RouteConfiguration{
		Name: name,
		VirtualHosts: []*routev3.VirtualHost{
			{
				Name:    "placeholder",
				Domains: []string{domain},
			},
		},
	}
}
