package dispatch

import (
	"context"
	"fmt"
	"time"

	flowcv1alpha1 "github.com/flowc-labs/flowc/api/v1alpha1"
	"github.com/flowc-labs/flowc/internal/flowc/index"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/models"
	"github.com/flowc-labs/flowc/internal/flowc/xds/cache"
	"github.com/flowc-labs/flowc/internal/flowc/xds/translator"
	"github.com/flowc-labs/flowc/pkg/logger"
	"github.com/flowc-labs/flowc/pkg/types"
)

// translateOne resolves a single Deployment's dependencies from the
// indexer (API, Gateway, Listener) and runs the strategy-based composite
// translator to produce xDS resources. Used by both DeploymentTranslator
// (single call) and GatewayTranslator (one call per deployment in a
// gateway's full rebuild).
//
// All inputs are read from the indexer — the translator path performs no
// store reads. Returned XDSResources have nil Listeners; listener
// publication is gateway-translator's responsibility.
func translateOne(
	ctx context.Context,
	dep *flowcv1alpha1.Deployment,
	idx *index.Indexer,
	parsers *ir.ParserRegistry,
	options *translator.TranslatorOptions,
	log *logger.EnvoyLogger,
) (*translator.XDSResources, error) {
	api, ok := idx.GetAPI(dep.Spec.APIRef)
	if !ok {
		return nil, fmt.Errorf("API %q not in indexer", dep.Spec.APIRef)
	}
	gw, ok := idx.GetGateway(dep.Spec.Gateway.Name)
	if !ok {
		return nil, fmt.Errorf("gateway %q not in indexer", dep.Spec.Gateway.Name)
	}

	// Resolve listener: explicit name takes precedence; otherwise
	// auto-resolve when the gateway has exactly one listener.
	var listener *flowcv1alpha1.Listener
	if explicit := dep.Spec.Gateway.Listener; explicit != "" {
		l, ok := idx.GetListener(explicit)
		if !ok {
			return nil, fmt.Errorf("listener %q not in indexer", explicit)
		}
		if l.Spec.GatewayRef != gw.Name {
			return nil, fmt.Errorf("listener %q targets gateway %q, not %q", explicit, l.Spec.GatewayRef, gw.Name)
		}
		listener = l
	} else {
		listeners := idx.ListenersForGateway(gw.Name)
		switch len(listeners) {
		case 0:
			return nil, fmt.Errorf("gateway %q has no listeners", gw.Name)
		case 1:
			listener = listeners[0]
		default:
			return nil, fmt.Errorf("gateway %q has %d listeners; spec.gateway.listener is required", gw.Name, len(listeners))
		}
	}

	hostname := "*"
	if len(listener.Spec.Hostnames) > 0 {
		hostname = listener.Spec.Hostnames[0]
	}

	// Parse spec content into IR if present. Translator works without it
	// (catch-all prefix route), so absence is fine.
	var irAPI *ir.API
	if api.Spec.SpecContent != "" {
		apiType := ir.APIType(api.Spec.APIType)
		if apiType == "" {
			apiType = ir.APITypeREST
		}
		parsed, err := parsers.Parse(ctx, apiType, []byte(api.Spec.SpecContent))
		if err != nil {
			return nil, fmt.Errorf("parse API spec: %w", err)
		}
		parsed.Metadata.BasePath = normalizeBasePath(api.Spec.Context)
		irAPI = parsed
	}

	// Build the legacy model objects the strategy framework expects.
	modelDep := toModelDeployment(dep.Name, api.Name, &api.Spec)
	modelGw := toModelGateway(gw.Name, &gw.Spec, gw.Labels)
	modelListener := toModelListener(listener.Name, &listener.Spec)
	modelVHost := &models.GatewayVirtualHost{
		ID:         hostname,
		ListenerID: listener.Name,
		Name:       hostname,
		Hostname:   hostname,
	}

	// 3-level strategy precedence: builtin < gateway defaults < per-API.
	resolver := translator.NewConfigResolver(nil, v1StrategyToTypes(gw.Spec.Defaults), log)
	resolvedConfig := resolver.Resolve(v1StrategyToTypes(dep.Spec.Strategy))

	factory := translator.NewStrategyFactory(options, log)
	strategies, err := factory.CreateStrategySet(resolvedConfig, modelDep)
	if err != nil {
		return nil, fmt.Errorf("strategy creation: %w", err)
	}

	composite, err := translator.NewCompositeTranslator(strategies, options, log)
	if err != nil {
		return nil, fmt.Errorf("composite translator creation: %w", err)
	}
	composite.SetTranslationContext(&translator.TranslationContext{
		Gateway:     modelGw,
		Listener:    modelListener,
		VirtualHost: modelVHost,
	})

	return composite.Translate(ctx, modelDep, irAPI, gw.Spec.NodeID)
}

// resourceNamesFromXDS extracts the names from a translation result so
// they can be recorded in the indexer's ownership map and later passed to
// cache.UnDeployAPI on delete.
func resourceNamesFromXDS(xds *translator.XDSResources) cache.ResourceNames {
	out := cache.ResourceNames{
		Clusters:  make([]string, 0, len(xds.Clusters)),
		Endpoints: make([]string, 0, len(xds.Endpoints)),
		Routes:    make([]string, 0, len(xds.Routes)),
	}
	for _, c := range xds.Clusters {
		out.Clusters = append(out.Clusters, c.Name)
	}
	for _, e := range xds.Endpoints {
		out.Endpoints = append(out.Endpoints, e.ClusterName)
	}
	for _, r := range xds.Routes {
		out.Routes = append(out.Routes, r.Name)
	}
	return out
}

// --- Model conversion helpers (ported from reconciler/gateway_reconciler.go) ---
//
// These adapt v1alpha1 CRDs to the legacy models package the strategy
// framework was built against. They'll move when the legacy reconciler
// path is deleted at cutover; duplicated here so the new dispatch package
// is self-contained while both paths coexist.

func toModelDeployment(depName, apiName string, apiSpec *flowcv1alpha1.APISpec) *models.APIDeployment {
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

func toModelGateway(name string, spec *flowcv1alpha1.GatewaySpec, labels map[string]string) *models.Gateway {
	return &models.Gateway{
		ID:       name,
		NodeID:   spec.NodeID,
		Name:     name,
		Status:   models.GatewayStatusConnected,
		Defaults: v1StrategyToTypes(spec.Defaults),
		Labels:   labels,
	}
}

func toModelListener(name string, spec *flowcv1alpha1.ListenerSpec) *models.Listener {
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

func v1StrategyToTypes(cfg *flowcv1alpha1.StrategyConfig) *types.StrategyConfig {
	if cfg == nil {
		return nil
	}
	out := &types.StrategyConfig{}
	if cfg.Deployment != nil {
		out.Deployment = &types.DeploymentStrategyConfig{Type: cfg.Deployment.Type}
	}
	if cfg.RouteMatching != nil {
		out.RouteMatching = &types.RouteMatchStrategyConfig{
			Type:          cfg.RouteMatching.Type,
			CaseSensitive: cfg.RouteMatching.CaseSensitive,
		}
	}
	if cfg.LoadBalancing != nil {
		out.LoadBalancing = &types.LoadBalancingStrategyConfig{Type: cfg.LoadBalancing.Type}
	}
	if cfg.Retry != nil {
		out.Retry = &types.RetryStrategyConfig{
			Type:       cfg.Retry.Type,
			MaxRetries: cfg.Retry.MaxRetries,
			RetryOn:    cfg.Retry.RetryOn,
		}
	}
	if cfg.RateLimit != nil {
		out.RateLimit = &types.RateLimitStrategyConfig{
			Type:              cfg.RateLimit.Type,
			RequestsPerMinute: cfg.RateLimit.RequestsPerMinute,
			BurstSize:         cfg.RateLimit.BurstSize,
		}
	}
	return out
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
