package translator

import (
	"context"
	"fmt"
	"regexp"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/models"
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
		t.logger.WithFields(map[string]any{
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
		t.logger.WithFields(map[string]any{
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
	routes, err := t.generateRoutes(deployment, irAPI)
	if err != nil {
		return nil, fmt.Errorf("route generation failed: %w", err)
	}

	if t.logger != nil {
		t.logger.WithFields(map[string]any{
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

	// Listeners are gateway-scoped and built by the dispatch package's
	// GatewayTranslator from Listener CRs. The per-deployment translation
	// here only contributes clusters / endpoints / routes; rate-limit and
	// observability strategies that operated on listeners no longer have
	// a target at this layer and are skipped — they'll need to be
	// reworked when actually implemented (today's strategies are no-ops).

	if t.logger != nil {
		t.logger.WithFields(map[string]any{
			"clusters": len(clusters),
			"routes":   len(routes),
		}).Info("Successfully completed xDS translation")
	}

	return &XDSResources{
		Clusters: clusters,
		Routes:   routes,
		// Listeners and Endpoints are unused at this layer; left nil.
	}, nil
}

// generateRoutes creates route configurations from IR
func (t *CompositeTranslator) generateRoutes(deployment *models.APIDeployment, irAPI *ir.API) ([]*routev3.RouteConfiguration, error) {
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
		// Match: PathSeparatedPrefix matches at path-segment boundaries
		// (so /httpbingo doesn't false-match /httpbin) and is invalid for
		// basePath "/", so we fall back to Prefix at the root.
		//
		// Rewrite: RegexRewrite (not PrefixRewrite). PrefixRewrite="/"
		// produces /httpbin/get → "/" + "/get" = "//get", which httpbin's
		// mux canonicalizes via a 301. PrefixRewrite="" is indistinguishable
		// from "unset" in proto3 (Envoy skips the rewrite entirely). The
		// regex pattern `^<basePath>/?` consumes the basePath plus an
		// optional trailing slash, so substituting "/" gives /get for
		// /httpbin/get and /httpbin/ → / and bare /httpbin → /.
		var match *routev3.RouteMatch
		if basePath == "/" {
			match = &routev3.RouteMatch{
				PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: basePath},
			}
		} else {
			match = &routev3.RouteMatch{
				PathSpecifier: &routev3.RouteMatch_PathSeparatedPrefix{PathSeparatedPrefix: basePath},
			}
			routeAction.RegexRewrite = &matcherv3.RegexMatchAndSubstitute{
				Pattern: &matcherv3.RegexMatcher{
					Regex: "^" + regexp.QuoteMeta(basePath) + "/?",
				},
				Substitution: "/",
			}
		}
		routeName := t.getRouteConfigName()
		routeConfig := &routev3.RouteConfiguration{
			Name: routeName,
			VirtualHosts: []*routev3.VirtualHost{
				{
					Name:    t.generateVirtualHostName(deployment),
					Domains: t.getDomains(deployment),
					Routes: []*routev3.Route{
						{
							Match:  match,
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
	routeName := t.getRouteConfigName()
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

// getRouteConfigName returns the route configuration name. The naming
// scheme `route_<listenerID>_<virtualHostName>` matches what
// dispatch/gateway.go::buildListeners points its filter chains at, so
// route configs and listener filter chains line up by construction.
//
// translationContext is set by translateOne in dispatch/translate.go
// before calling Translate; nil context here is a programming error and
// will panic. There's no fallback path because this translator is only
// used through the dispatch flow.
func (t *CompositeTranslator) getRouteConfigName() string {
	return fmt.Sprintf("route_%s_%s", t.translationContext.Listener.ID, t.translationContext.VirtualHost.Name)
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
