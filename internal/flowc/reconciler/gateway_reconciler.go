package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	v1alpha1 "github.com/flowc-labs/flowc/api/v1alpha1"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/resource/store"
	"github.com/flowc-labs/flowc/internal/flowc/server/models"
	"github.com/flowc-labs/flowc/internal/flowc/xds/cache"
	listenerbuilder "github.com/flowc-labs/flowc/internal/flowc/xds/resources/listener"
	"github.com/flowc-labs/flowc/internal/flowc/xds/translator"
	"github.com/flowc-labs/flowc/pkg/types"
)

// listenerInfo is a helper struct pairing a listener name with its spec.
type listenerInfo struct {
	name string
	spec v1alpha1.ListenerSpec
}

// translateDeployment translates a single deployment into xDS resources.
// It loads the API, finds the listener, parses the spec to IR,
// converts to model types, resolves strategies, and runs the translator.
// Note: the translator produces clusters + routes only (no listeners).
// Listeners are generated at the reconciler level by buildListeners.
func (r *Reconciler) translateDeployment(
	ctx context.Context,
	gwStored *store.StoredResource,
	gwSpec *v1alpha1.GatewaySpec,
	depStored *store.StoredResource,
	depSpec *v1alpha1.DeploymentSpec,
	listeners []listenerInfo,
) (*translator.XDSResources, error) {
	nodeID := gwSpec.NodeID

	// Load the referenced API
	apiStored, err := r.store.Get(ctx, store.ResourceKey{Kind: "API", Name: depSpec.APIRef})
	if err != nil {
		return nil, fmt.Errorf("API %q not found: %w", depSpec.APIRef, err)
	}
	var apiSpec v1alpha1.APISpec
	if err := json.Unmarshal(apiStored.SpecJSON, &apiSpec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal API spec: %w", err)
	}

	// Resolve listener ref (auto-select if omitted and unambiguous)
	listenerRef := depSpec.Gateway.Listener
	if listenerRef == "" {
		if len(listeners) == 0 {
			return nil, fmt.Errorf("no listeners found for gateway %q", gwStored.Meta.Name)
		}
		if len(listeners) > 1 {
			return nil, fmt.Errorf("multiple listeners found for gateway %q; spec.gateway.listener is required", gwStored.Meta.Name)
		}
		listenerRef = listeners[0].name
	}

	// Find the listener for this deployment
	var listener *listenerInfo
	for i := range listeners {
		if listeners[i].name == listenerRef {
			listener = &listeners[i]
			break
		}
	}
	if listener == nil {
		return nil, fmt.Errorf("listener %q not found", listenerRef)
	}

	// Resolve hostname: use first hostname from listener, or "*"
	hostname := "*"
	if len(listener.spec.Hostnames) > 0 {
		hostname = listener.spec.Hostnames[0]
	}

	// Parse the API spec to IR (transient)
	var irAPI *ir.API
	if apiSpec.SpecContent != "" {
		apiType := ir.APIType(apiSpec.APIType)
		if apiType == "" {
			apiType = ir.APITypeREST
		}
		parsed, err := r.parserRegistry.Parse(ctx, apiType, []byte(apiSpec.SpecContent))
		if err != nil {
			return nil, fmt.Errorf("failed to parse spec: %w", err)
		}
		irAPI = parsed
		irAPI.Metadata.BasePath = normalizeBasePath(apiSpec.Context)
	}

	// Build models for the existing translator
	modelDeployment := toModelDeployment(depStored.Meta.Name, apiStored.Meta.Name, &apiSpec)
	modelGateway := toModelGateway(gwStored.Meta.Name, gwSpec, gwStored.Meta.Labels)
	modelListener := toModelListener(listener.name, &listener.spec)
	modelVHost := &models.GatewayVirtualHost{
		ID:         hostname,
		ListenerID: listener.name,
		Name:       hostname,
		Hostname:   hostname,
	}

	// Resolve strategies (3-level precedence: API > Gateway > Builtin)
	resolver := translator.NewConfigResolver(nil, v1StrategyToTypes(gwSpec.Defaults), r.logger)
	resolvedConfig := resolver.Resolve(v1StrategyToTypes(depSpec.Strategy))

	factory := translator.NewStrategyFactory(translator.DefaultTranslatorOptions(), r.logger)
	strategies, err := factory.CreateStrategySet(resolvedConfig, modelDeployment)
	if err != nil {
		return nil, fmt.Errorf("strategy creation failed: %w", err)
	}

	compositeTranslator, err := translator.NewCompositeTranslator(strategies, translator.DefaultTranslatorOptions(), r.logger)
	if err != nil {
		return nil, fmt.Errorf("translator creation failed: %w", err)
	}

	compositeTranslator.SetTranslationContext(&translator.TranslationContext{
		Gateway:     modelGateway,
		Listener:    modelListener,
		VirtualHost: modelVHost,
	})

	xdsResources, err := compositeTranslator.Translate(ctx, modelDeployment, irAPI, nodeID)
	if err != nil {
		return nil, fmt.Errorf("translation failed: %w", err)
	}

	return xdsResources, nil
}

// buildListeners generates xDS listeners at the gateway level.
// One xDS listener is created per physical listener resource, with a filter
// chain for each hostname that has at least one successfully translated
// route configuration.
func (r *Reconciler) buildListeners(
	listeners []listenerInfo,
	activeRoutes map[string]struct{}, // set of route config names that were successfully generated
) []*cache.ListenerWithName {
	var results []*cache.ListenerWithName

	for _, l := range listeners {
		hostnames := l.spec.Hostnames
		if len(hostnames) == 0 {
			hostnames = []string{"*"}
		}

		var filterChains []*listenerbuilder.FilterChainConfig
		for _, hostname := range hostnames {
			routeName := fmt.Sprintf("route_%s_%s", l.name, hostname)
			if _, ok := activeRoutes[routeName]; !ok {
				continue // No successful deployment for this hostname
			}
			filterChains = append(filterChains, &listenerbuilder.FilterChainConfig{
				Name:            hostname,
				Hostname:        hostname,
				RouteConfigName: routeName,
			})
		}

		if len(filterChains) == 0 {
			continue
		}

		addr := l.spec.Address
		if addr == "" {
			addr = "0.0.0.0"
		}

		config := &listenerbuilder.ListenerConfig{
			Name:         fmt.Sprintf("listener_%d", l.spec.Port),
			Port:         l.spec.Port,
			Address:      addr,
			FilterChains: filterChains,
			HTTP2:        l.spec.HTTP2,
		}

		xdsListener, err := listenerbuilder.CreateListenerWithFilterChains(config)
		if err != nil {
			r.logger.WithFields(map[string]any{
				"listener": l.name,
				"error":    err.Error(),
			}).Error("Failed to create xDS listener")
			continue
		}

		results = append(results, &cache.ListenerWithName{Listener: xdsListener})
	}

	return results
}

// reconcileGatewayFromStored performs the full xDS reconciliation for a single gateway.
// It translates every deployment from scratch and replaces the entire xDS snapshot.
func (r *Reconciler) reconcileGatewayFromStored(ctx context.Context, gwStored *store.StoredResource) error {
	var gwSpec v1alpha1.GatewaySpec
	if err := json.Unmarshal(gwStored.SpecJSON, &gwSpec); err != nil {
		return fmt.Errorf("unmarshal gateway spec: %w", err)
	}

	nodeID := gwSpec.NodeID

	r.logger.WithFields(map[string]any{
		"gateway": gwStored.Meta.Name,
		"nodeId":  nodeID,
	}).Info("Reconciling gateway")

	// Load all listeners referencing this gateway
	allListeners, err := r.store.List(ctx, store.ListFilter{Kind: "Listener"})
	if err != nil {
		return fmt.Errorf("list listeners: %w", err)
	}
	var listeners []listenerInfo
	for _, l := range allListeners {
		var lSpec v1alpha1.ListenerSpec
		if err := json.Unmarshal(l.SpecJSON, &lSpec); err != nil {
			continue
		}
		if lSpec.GatewayRef == gwStored.Meta.Name {
			listeners = append(listeners, listenerInfo{name: l.Meta.Name, spec: lSpec})
		}
	}

	// Load all deployments referencing this gateway
	allDeployments, err := r.store.List(ctx, store.ListFilter{Kind: "Deployment"})
	if err != nil {
		return fmt.Errorf("list deployments: %w", err)
	}

	type depInfo struct {
		stored *store.StoredResource
		spec   v1alpha1.DeploymentSpec
	}
	var deployments []depInfo
	for _, d := range allDeployments {
		var dSpec v1alpha1.DeploymentSpec
		if err := json.Unmarshal(d.SpecJSON, &dSpec); err != nil {
			continue
		}
		if dSpec.Gateway.Name == gwStored.Meta.Name {
			deployments = append(deployments, depInfo{stored: d, spec: dSpec})
		}
	}

	// Single pass: translate each deployment, accumulate clusters + routes
	cacheDeployment := &cache.APIDeployment{}
	activeRoutes := make(map[string]struct{}) // route config names with successful translations

	for _, dep := range deployments {
		xds, err := r.translateDeployment(ctx, gwStored, &gwSpec, dep.stored, &dep.spec, listeners)
		if err != nil {
			r.updateDeploymentStatus(ctx, dep.stored.Meta.Name, "Failed", err.Error())
			continue
		}

		cacheDeployment.Clusters = append(cacheDeployment.Clusters, xds.Clusters...)
		cacheDeployment.Endpoints = append(cacheDeployment.Endpoints, xds.Endpoints...)
		cacheDeployment.Routes = append(cacheDeployment.Routes, xds.Routes...)

		// Track which route config names were successfully generated
		for _, rc := range xds.Routes {
			activeRoutes[rc.Name] = struct{}{}
		}

		r.updateDeploymentStatus(ctx, dep.stored.Meta.Name, "Deployed", "")
	}

	// Generate listeners at the gateway level
	for _, lw := range r.buildListeners(listeners, activeRoutes) {
		cacheDeployment.Listeners = append(cacheDeployment.Listeners, lw.Listener)
	}

	// Replace the entire snapshot
	if err := r.configManager.ReplaceSnapshot(nodeID, cacheDeployment); err != nil {
		return fmt.Errorf("replace xDS snapshot: %w", err)
	}

	// Update gateway status
	r.updateGatewayStatus(ctx, gwStored.Meta.Name, "Ready")

	r.logger.WithFields(map[string]any{
		"gateway":     gwStored.Meta.Name,
		"deployments": len(deployments),
		"clusters":    len(cacheDeployment.Clusters),
		"routes":      len(cacheDeployment.Routes),
		"listeners":   len(cacheDeployment.Listeners),
	}).Info("Gateway reconciliation complete")

	return nil
}

// upsertDeploymentResources translates a single deployment and merges its
// resources into the existing gateway snapshot via DeployAPI (additive upsert
// with dedup). Used when only one deployment changed.
func (r *Reconciler) upsertDeploymentResources(ctx context.Context, gatewayName, depName string) error {
	// Load the gateway
	gwStored, err := r.store.Get(ctx, store.ResourceKey{Kind: "Gateway", Name: gatewayName})
	if err != nil {
		return fmt.Errorf("get gateway %q: %w", gatewayName, err)
	}
	var gwSpec v1alpha1.GatewaySpec
	if err := json.Unmarshal(gwStored.SpecJSON, &gwSpec); err != nil {
		return fmt.Errorf("unmarshal gateway spec: %w", err)
	}

	// Load the deployment
	depStored, err := r.store.Get(ctx, store.ResourceKey{Kind: "Deployment", Name: depName})
	if err != nil {
		return fmt.Errorf("get deployment %q: %w", depName, err)
	}
	var depSpec v1alpha1.DeploymentSpec
	if err := json.Unmarshal(depStored.SpecJSON, &depSpec); err != nil {
		return fmt.Errorf("unmarshal deployment spec: %w", err)
	}

	// Load listeners for context
	allListeners, err := r.store.List(ctx, store.ListFilter{Kind: "Listener"})
	if err != nil {
		return fmt.Errorf("list listeners: %w", err)
	}
	var listeners []listenerInfo
	for _, l := range allListeners {
		var lSpec v1alpha1.ListenerSpec
		if err := json.Unmarshal(l.SpecJSON, &lSpec); err != nil {
			continue
		}
		if lSpec.GatewayRef == gatewayName {
			listeners = append(listeners, listenerInfo{name: l.Meta.Name, spec: lSpec})
		}
	}

	// Translate this single deployment
	xds, err := r.translateDeployment(ctx, gwStored, &gwSpec, depStored, &depSpec, listeners)
	if err != nil {
		r.updateDeploymentStatus(ctx, depName, "Failed", err.Error())
		return fmt.Errorf("translate deployment %q: %w", depName, err)
	}

	// Build the activeRoutes set. We need to include both the new deployment's
	// routes AND all existing deployments' routes for the affected listener, so
	// the rebuilt listener has filter chains for all active hostnames.
	activeRoutes := make(map[string]struct{})
	for _, rc := range xds.Routes {
		activeRoutes[rc.Name] = struct{}{}
	}

	// Find other deployments on the same listener to include their route names
	allDeployments, err := r.store.List(ctx, store.ListFilter{Kind: "Deployment"})
	if err != nil {
		return fmt.Errorf("list deployments: %w", err)
	}
	for _, d := range allDeployments {
		var dSpec v1alpha1.DeploymentSpec
		if err := json.Unmarshal(d.SpecJSON, &dSpec); err != nil {
			continue
		}
		if dSpec.Gateway.Name == gatewayName && dSpec.Gateway.Listener == depSpec.Gateway.Listener {
			// Find the listener to get hostnames
			for _, l := range listeners {
				if l.name == dSpec.Gateway.Listener {
					hostnames := l.spec.Hostnames
					if len(hostnames) == 0 {
						hostnames = []string{"*"}
					}
					for _, h := range hostnames {
						routeName := fmt.Sprintf("route_%s_%s", l.name, h)
						activeRoutes[routeName] = struct{}{}
					}
					break
				}
			}
		}
	}

	// Build the listener for the affected listener resource (with all its hostnames)
	listenerResults := r.buildListeners(listeners, activeRoutes)

	// Merge into existing snapshot via DeployAPI (dedup handles replacements)
	cacheDeployment := &cache.APIDeployment{
		Clusters:  xds.Clusters,
		Endpoints: xds.Endpoints,
		Routes:    xds.Routes,
	}
	for _, lw := range listenerResults {
		cacheDeployment.Listeners = append(cacheDeployment.Listeners, lw.Listener)
	}

	if err := r.configManager.DeployAPI(gwSpec.NodeID, cacheDeployment); err != nil {
		r.updateDeploymentStatus(ctx, depName, "Failed", fmt.Sprintf("deploy to xDS cache: %v", err))
		return fmt.Errorf("deploy to xDS cache: %w", err)
	}

	r.updateDeploymentStatus(ctx, depName, "Deployed", "")

	r.logger.WithFields(map[string]any{
		"gateway":    gatewayName,
		"deployment": depName,
		"clusters":   len(xds.Clusters),
		"routes":     len(xds.Routes),
		"listeners":  len(cacheDeployment.Listeners),
	}).Info("Single deployment upsert complete")

	return nil
}

// removeDeploymentResources handles a deployment deletion by falling back to
// a full gateway rebuild. The deleted deployment is already gone from the store
// so it simply won't appear in the new snapshot.
func (r *Reconciler) removeDeploymentResources(ctx context.Context, gatewayName string) error {
	return r.reconcileGateway(ctx, gatewayName)
}

// --- Conversion helpers: api/v1 types -> models types (for xDS translator) ---

func toModelDeployment(depName string, apiName string, apiSpec *v1alpha1.APISpec) *models.APIDeployment {
	now := time.Now()
	return &models.APIDeployment{
		ID:      depName,
		Name:    apiName,
		Version: apiSpec.Version,
		Context: apiSpec.Context,
		Metadata: types.FlowCMetadata{
			Name:    apiName,
			Version: apiSpec.Version,
			Context: apiSpec.Context,
			APIType: apiSpec.APIType,
			Upstream: types.UpstreamConfig{
				Host:    apiSpec.Upstream.Host,
				Port:    apiSpec.Upstream.Port,
				Scheme:  apiSpec.Upstream.Scheme,
				Timeout: apiSpec.Upstream.Timeout,
			},
			Gateway: types.GatewayConfig{
				NodeID: "", // filled via translation context
			},
		},
		UpdatedAt: now,
	}
}

func toModelGateway(name string, spec *v1alpha1.GatewaySpec, labels map[string]string) *models.Gateway {
	return &models.Gateway{
		ID:       name,
		NodeID:   spec.NodeID,
		Name:     name,
		Status:   models.GatewayStatusConnected,
		Defaults: v1StrategyToTypes(spec.Defaults),
		Labels:   labels,
	}
}

func toModelListener(name string, spec *v1alpha1.ListenerSpec) *models.Listener {
	ml := &models.Listener{
		ID:        name,
		GatewayID: spec.GatewayRef,
		Port:      spec.Port,
		Address:   spec.Address,
		HTTP2:     spec.HTTP2,
	}
	if ml.Address == "" {
		ml.Address = "0.0.0.0"
	}
	if spec.TLS != nil {
		ml.TLS = &models.TLSConfig{
			CertPath:          spec.TLS.CertPath,
			KeyPath:           spec.TLS.KeyPath,
			CAPath:            spec.TLS.CAPath,
			RequireClientCert: spec.TLS.RequireClientCert,
			MinVersion:        spec.TLS.MinVersion,
			CipherSuites:      spec.TLS.CipherSuites,
		}
	}
	return ml
}

// v1StrategyToTypes converts a v1alpha1.StrategyConfig to a types.StrategyConfig.
func v1StrategyToTypes(cfg *v1alpha1.StrategyConfig) *types.StrategyConfig {
	if cfg == nil {
		return nil
	}
	result := &types.StrategyConfig{}
	if cfg.Deployment != nil {
		result.Deployment = &types.DeploymentStrategyConfig{Type: cfg.Deployment.Type}
	}
	if cfg.RouteMatching != nil {
		result.RouteMatching = &types.RouteMatchStrategyConfig{
			Type:          cfg.RouteMatching.Type,
			CaseSensitive: cfg.RouteMatching.CaseSensitive,
		}
	}
	if cfg.LoadBalancing != nil {
		result.LoadBalancing = &types.LoadBalancingStrategyConfig{Type: cfg.LoadBalancing.Type}
	}
	if cfg.Retry != nil {
		result.Retry = &types.RetryStrategyConfig{
			Type:       cfg.Retry.Type,
			MaxRetries: cfg.Retry.MaxRetries,
			RetryOn:    cfg.Retry.RetryOn,
		}
	}
	if cfg.RateLimit != nil {
		result.RateLimit = &types.RateLimitStrategyConfig{
			Type:              cfg.RateLimit.Type,
			RequestsPerMinute: cfg.RateLimit.RequestsPerMinute,
			BurstSize:         cfg.RateLimit.BurstSize,
		}
	}
	return result
}

func normalizeBasePath(path string) string {
	if path == "" || path == "/" {
		return ""
	}
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	if path[0] != '/' {
		path = "/" + path
	}
	return path
}

func unmarshalJSON(data json.RawMessage, v any) error {
	return json.Unmarshal(data, v)
}
