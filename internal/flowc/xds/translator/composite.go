package translator

import (
	"context"
	"fmt"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/server/models"
	"github.com/flowc-labs/flowc/internal/flowc/xds/resources/listener"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// TranslationContext contains the resolved gateway hierarchy for a deployment.
// This provides context about where the API is being deployed within the gateway hierarchy.
type TranslationContext struct {
	// Gateway is the target gateway (physical Envoy proxy)
	Gateway *models.Gateway

	// Listener is the target listener (port binding)
	Listener *models.Listener

	// VirtualHost is the target virtual host (SNI-based hostname routing)
	VirtualHost *models.GatewayVirtualHost
}

// CompositeTranslator orchestrates multiple strategies to generate xDS resources
// It implements the Translator interface while delegating to specialized strategies
type CompositeTranslator struct {
	// Strategy set
	strategies *StrategySet

	// Options
	options *TranslatorOptions

	// Logger
	logger *logger.EnvoyLogger

	// Translation context (optional, set when translating with hierarchy)
	translationContext *TranslationContext
}

// NewCompositeTranslator creates a new composite translator
func NewCompositeTranslator(strategies *StrategySet, options *TranslatorOptions, log *logger.EnvoyLogger) (*CompositeTranslator, error) {
	if strategies == nil {
		return nil, fmt.Errorf("strategies cannot be nil")
	}

	// Validate required strategies
	if err := strategies.Validate(); err != nil {
		return nil, fmt.Errorf("invalid strategy set: %w", err)
	}

	if options == nil {
		options = DefaultTranslatorOptions()
	}

	// Ensure optional strategies have no-op implementations if nil
	if strategies.LoadBalancing == nil {
		strategies.LoadBalancing = &NoOpLoadBalancingStrategy{}
	}
	if strategies.Retry == nil {
		strategies.Retry = &NoOpRetryStrategy{}
	}
	if strategies.RateLimit == nil {
		strategies.RateLimit = &NoOpRateLimitStrategy{}
	}
	if strategies.Observability == nil {
		strategies.Observability = &NoOpObservabilityStrategy{}
	}

	return &CompositeTranslator{
		strategies: strategies,
		options:    options,
		logger:     log,
	}, nil
}

// SetTranslationContext sets the translation context for gateway hierarchy-aware translation.
// This should be called before Translate() when deploying to a specific environment.
func (t *CompositeTranslator) SetTranslationContext(ctx *TranslationContext) {
	t.translationContext = ctx
}

// GetTranslationContext returns the current translation context.
func (t *CompositeTranslator) GetTranslationContext() *TranslationContext {
	return t.translationContext
}

// Name returns the translator name
func (t *CompositeTranslator) Name() string {
	return fmt.Sprintf("composite[deployment=%s,route=%s,lb=%s,retry=%s]",
		t.strategies.Deployment.Name(),
		t.strategies.RouteMatch.Name(),
		t.strategies.LoadBalancing.Name(),
		t.strategies.Retry.Name(),
	)
}

// Validate validates the deployment
func (t *CompositeTranslator) Validate(deployment *models.APIDeployment, irAPI *ir.API) error {
	// Validate with deployment strategy (most critical)
	return t.strategies.Deployment.Validate(deployment)
}

// Translate converts a deployment into xDS resources
func (t *CompositeTranslator) Translate(ctx context.Context, deployment *models.APIDeployment, irAPI *ir.API, nodeID string) (*XDSResources, error) {
	if err := t.Validate(deployment, irAPI); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	if t.logger != nil {
		t.logger.WithFields(map[string]interface{}{
			"translator":          t.Name(),
			"deployment":          deployment.ID,
			"deployment_strategy": t.strategies.Deployment.Name(),
			"route_strategy":      t.strategies.RouteMatch.Name(),
			"lb_strategy":         t.strategies.LoadBalancing.Name(),
			"retry_strategy":      t.strategies.Retry.Name(),
		}).Info("Starting xDS translation with composite strategy")
	}

	// PHASE 1: Generate clusters using deployment strategy
	clusters, err := t.strategies.Deployment.GenerateClusters(ctx, deployment)
	if err != nil {
		return nil, fmt.Errorf("cluster generation failed: %w", err)
	}

	if t.logger != nil {
		t.logger.WithFields(map[string]interface{}{
			"clusters_count": len(clusters),
		}).Debug("Generated clusters")
	}

	// PHASE 2: Apply load balancing strategy to clusters
	for _, cluster := range clusters {
		if err := t.strategies.LoadBalancing.ConfigureCluster(cluster, deployment); err != nil {
			return nil, fmt.Errorf("load balancing configuration failed for cluster %s: %w", cluster.Name, err)
		}
	}

	// PHASE 3: Generate routes using IR
	routes, err := t.generateRoutes(ctx, deployment, irAPI, clusters)
	if err != nil {
		return nil, fmt.Errorf("route generation failed: %w", err)
	}

	if t.logger != nil {
		t.logger.WithFields(map[string]interface{}{
			"route_configs_count": len(routes),
		}).Debug("Generated routes")
	}

	// PHASE 4: Apply retry strategy to routes
	for _, routeConfig := range routes {
		for _, vhost := range routeConfig.VirtualHosts {
			for _, route := range vhost.Routes {
				if err := t.strategies.Retry.ConfigureRetry(route, deployment); err != nil {
					return nil, fmt.Errorf("retry configuration failed: %w", err)
				}
			}
		}
	}

	// PHASE 5: Generate listeners (if needed)
	var listeners []*listenerv3.Listener
	if t.shouldGenerateListener(deployment) {
		listeners = append(listeners, t.generateListener(deployment, routes))
	}

	// PHASE 6: Apply rate limiting to listeners
	for _, l := range listeners {
		if err := t.strategies.RateLimit.ConfigureRateLimit(l, deployment); err != nil {
			return nil, fmt.Errorf("rate limit configuration failed: %w", err)
		}
	}

	// PHASE 7: Apply observability configuration
	if len(listeners) > 0 {
		if err := t.strategies.Observability.ConfigureObservability(listeners[0], clusters, deployment); err != nil {
			return nil, fmt.Errorf("observability configuration failed: %w", err)
		}
	}

	if t.logger != nil {
		t.logger.WithFields(map[string]interface{}{
			"clusters":  len(clusters),
			"routes":    len(routes),
			"listeners": len(listeners),
		}).Info("Successfully completed xDS translation")
	}

	return &XDSResources{
		Clusters:  clusters,
		Routes:    routes,
		Listeners: listeners,
		Endpoints: nil, // Typically not needed for LOGICAL_DNS clusters
	}, nil
}

// generateRoutes creates route configurations from IR
func (t *CompositeTranslator) generateRoutes(ctx context.Context, deployment *models.APIDeployment, irAPI *ir.API, clusters []*clusterv3.Cluster) ([]*routev3.RouteConfiguration, error) {
	if irAPI == nil || len(irAPI.Endpoints) == 0 {
		// No spec or no endpoints — generate a catch-all prefix route
		// that proxies everything under the context path to the upstream.
		clusterNames := t.strategies.Deployment.GetClusterNames(deployment)
		if len(clusterNames) == 0 {
			return []*routev3.RouteConfiguration{}, nil
		}
		basePath := deployment.Context
		if basePath == "" {
			basePath = "/"
		}
		if basePath[0] != '/' {
			basePath = "/" + basePath
		}
		routeAction := &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{
				Cluster: clusterNames[0],
			},
		}
		if basePath != "/" {
			routeAction.PrefixRewrite = "/"
		}
		routeName := t.getRouteConfigName(deployment)
		routeConfig := &routev3.RouteConfiguration{
			Name: routeName,
			VirtualHosts: []*routev3.VirtualHost{
				{
					Name:    t.generateVirtualHostName(deployment),
					Domains: t.getDomains(deployment),
					Routes: []*routev3.Route{
						{
							Match: &routev3.RouteMatch{
								PathSpecifier: &routev3.RouteMatch_Prefix{
									Prefix: basePath,
								},
							},
							Action: &routev3.Route_Route{Route: routeAction},
						},
					},
				},
			},
		}
		return []*routev3.RouteConfiguration{routeConfig}, nil
	}

	// Get cluster names from deployment strategy
	clusterNames := t.strategies.Deployment.GetClusterNames(deployment)
	if len(clusterNames) == 0 {
		return nil, fmt.Errorf("no cluster names returned from deployment strategy")
	}

	// Primary cluster is the first one (or only one for basic deployments)
	primaryCluster := clusterNames[0]

	var xdsRoutes []*routev3.Route

	// Get base path from metadata
	basePath := t.getBasePath(deployment, irAPI)

	// Create routes for each IR endpoint
	for _, endpoint := range irAPI.Endpoints {
		// Build the full path with gateway basepath prefix
		fullPath := basePath + endpoint.Path.Pattern

		// Use route match strategy to create matcher
		routeMatch := t.strategies.RouteMatch.CreateMatcher(fullPath, endpoint.Method, &endpoint)

		// Create route with primary cluster as destination.
		// PrefixRewrite strips the basePath so the upstream sees the
		// original API path (e.g., /httpbin/get → /get).
		routeAction := &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{
				Cluster: primaryCluster,
			},
		}
		if basePath != "" && basePath != "/" {
			routeAction.PrefixRewrite = TruncatePathParams(endpoint.Path.Pattern)
		}

		route := &routev3.Route{
			Match:  routeMatch,
			Action: &routev3.Route_Route{Route: routeAction},
		}

		xdsRoutes = append(xdsRoutes, route)
	}

	// Create route configuration with environment-aware name
	// Route config name must match what the listener expects: route_{listenerID}_{environmentName}
	routeName := t.getRouteConfigName(deployment)
	routeConfig := &routev3.RouteConfiguration{
		Name: routeName,
		VirtualHosts: []*routev3.VirtualHost{
			{
				Name:    t.generateVirtualHostName(deployment),
				Domains: t.getDomains(deployment),
				Routes:  xdsRoutes,
			},
		},
	}

	return []*routev3.RouteConfiguration{routeConfig}, nil
}

// getRouteConfigName returns the route configuration name that matches the listener's expectation.
// When listeners/environments are created, they expect route configs named: route_{listenerID}_{environmentName}
func (t *CompositeTranslator) getRouteConfigName(deployment *models.APIDeployment) string {
	// If we have translation context, use the environment-aware naming
	if t.translationContext != nil && t.translationContext.Listener != nil && t.translationContext.VirtualHost != nil {
		return fmt.Sprintf("route_%s_%s", t.translationContext.Listener.ID, t.translationContext.VirtualHost.Name)
	}

	// Fallback to default name (backward compatibility)
	return "flowc_default_route"
}

// generateListener creates a listener with environment-aware SNI filter chains.
// This requires translation context to be set via SetTranslationContext().
func (t *CompositeTranslator) generateListener(deployment *models.APIDeployment, routes []*routev3.RouteConfiguration) *listenerv3.Listener {
	// Translation context is required for environment-based deployments
	if t.translationContext == nil || t.translationContext.VirtualHost == nil || t.translationContext.Listener == nil {
		t.logger.Error("Translation context is required but not set; cannot generate listener")
		// Return nil - this will be caught in the Translate method
		return nil
	}

	listenerName := fmt.Sprintf("listener_%d", t.translationContext.Listener.Port)
	routeName := routes[0].Name // Use first route config name

	// Create listener with SNI filter chain for the environment
	config := &listener.ListenerConfig{
		Name:    listenerName,
		Port:    t.translationContext.Listener.Port,
		Address: t.translationContext.Listener.Address,
		HTTP2:   t.translationContext.Listener.HTTP2,
		FilterChains: []*listener.FilterChainConfig{
			{
				Name:            t.translationContext.VirtualHost.Name,
				Hostname:        t.translationContext.VirtualHost.Hostname,
				HTTPFilters:     t.translationContext.VirtualHost.HTTPFilters,
				RouteConfigName: routeName,
				TLS:             convertTLSConfig(t.translationContext.Listener.TLS),
			},
		},
	}

	l, err := listener.CreateListenerWithFilterChains(config)
	if err != nil {
		t.logger.WithError(err).Error("Failed to create listener with filter chains")
		return nil
	}
	return l
}

// convertTLSConfig converts models.TLSConfig to listener.TLSConfig
func convertTLSConfig(tlsConfig *models.TLSConfig) *listener.TLSConfig {
	if tlsConfig == nil {
		return nil
	}
	return &listener.TLSConfig{
		CertPath:          tlsConfig.CertPath,
		KeyPath:           tlsConfig.KeyPath,
		CAPath:            tlsConfig.CAPath,
		RequireClientCert: tlsConfig.RequireClientCert,
		MinVersion:        tlsConfig.MinVersion,
		CipherSuites:      tlsConfig.CipherSuites,
	}
}

// shouldGenerateListener determines if a listener should be generated for this deployment.
// In the hierarchical gateway model, listeners are managed separately at the listener/environment level,
// not during API deployment. API deployments only generate routes (RDS).
func (t *CompositeTranslator) shouldGenerateListener(deployment *models.APIDeployment) bool {
	// Never generate listeners during API deployment - they're managed at the gateway/listener/environment level
	// TODO: Implement listener management in ListenerService and EnvironmentService
	return false
}

// generateVirtualHostName creates a virtual host name
func (t *CompositeTranslator) generateVirtualHostName(deployment *models.APIDeployment) string {
	if deployment.Metadata.Gateway.VirtualHost.Name != "" {
		return deployment.Metadata.Gateway.VirtualHost.Name
	}
	return fmt.Sprintf("%s-%s-vhost", deployment.Name, deployment.Version)
}

// getDomains returns the domains for the virtual host
func (t *CompositeTranslator) getDomains(deployment *models.APIDeployment) []string {
	if len(deployment.Metadata.Gateway.VirtualHost.Domains) > 0 {
		return deployment.Metadata.Gateway.VirtualHost.Domains
	}
	return []string{"*"} // Default to wildcard
}

// getBasePath returns the gateway base path for this API
func (t *CompositeTranslator) getBasePath(deployment *models.APIDeployment, irAPI *ir.API) string {
	// First try IR metadata
	if irAPI != nil && irAPI.Metadata.BasePath != "" {
		return irAPI.Metadata.BasePath
	}
	// Fallback to deployment context
	if deployment.Context != "" {
		path := deployment.Context
		if len(path) > 0 && path[0] != '/' {
			path = "/" + path
		}
		if len(path) > 1 && path[len(path)-1] == '/' {
			path = path[:len(path)-1]
		}
		// Root context means no prefix — endpoint paths already start with /
		if path == "/" {
			return ""
		}
		return path
	}
	return "" // Default to no prefix (endpoint paths already include leading /)
}
