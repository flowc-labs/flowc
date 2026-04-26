package translator

import (
	"context"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/models"
)

// =============================================================================
// STRATEGY INTERFACES
// Each interface handles a specific concern in xDS generation
// =============================================================================

// DeploymentStrategy handles cluster generation based on deployment patterns
// This is the core strategy that determines how many clusters and which routing pattern
type DeploymentStrategy interface {
	// GenerateClusters creates clusters for the deployment
	GenerateClusters(ctx context.Context, deployment *models.APIDeployment) ([]*clusterv3.Cluster, error)

	// GetClusterNames returns the names of clusters that will be created
	// This is useful for route generation to know which clusters to route to
	GetClusterNames(deployment *models.APIDeployment) []string

	// Name returns the strategy name
	Name() string

	// Validate checks if the deployment is valid for this strategy
	Validate(deployment *models.APIDeployment) error
}

// RouteMatchStrategy handles how routes are matched (prefix, exact, regex, etc.)
type RouteMatchStrategy interface {
	// CreateMatcher creates a route matcher for the given path and method
	CreateMatcher(path, method string, endpoint *ir.Endpoint) *routev3.RouteMatch

	// Name returns the strategy name
	Name() string
}

// LoadBalancingStrategy handles load balancing configuration for clusters
type LoadBalancingStrategy interface {
	// ConfigureCluster applies load balancing settings to a cluster
	ConfigureCluster(cluster *clusterv3.Cluster, deployment *models.APIDeployment) error

	// Name returns the strategy name
	Name() string
}

// RetryStrategy handles retry policy configuration
type RetryStrategy interface {
	// ConfigureRetry applies retry policy to a route
	ConfigureRetry(route *routev3.Route, deployment *models.APIDeployment) error

	// Name returns the strategy name
	Name() string
}

// RateLimitStrategy handles rate limiting configuration
type RateLimitStrategy interface {
	// ConfigureRateLimit applies rate limiting to listeners/routes
	ConfigureRateLimit(listener *listenerv3.Listener, deployment *models.APIDeployment) error

	// Name returns the strategy name
	Name() string
}

// ObservabilityStrategy handles tracing, metrics, and logging configuration
type ObservabilityStrategy interface {
	// ConfigureObservability applies observability settings to listener/cluster
	ConfigureObservability(listener *listenerv3.Listener, clusters []*clusterv3.Cluster, deployment *models.APIDeployment) error

	// Name returns the strategy name
	Name() string
}

// =============================================================================
// STRATEGY COLLECTIONS
// Groups related strategies together
// =============================================================================

// StrategySet contains all strategies needed for xDS generation
type StrategySet struct {
	Deployment    DeploymentStrategy
	RouteMatch    RouteMatchStrategy
	LoadBalancing LoadBalancingStrategy
	Retry         RetryStrategy
	RateLimit     RateLimitStrategy
	Observability ObservabilityStrategy
}

// Validate checks if all required strategies are present
func (s *StrategySet) Validate() error {
	if s.Deployment == nil {
		return ErrMissingStrategy("deployment")
	}
	if s.RouteMatch == nil {
		return ErrMissingStrategy("route_match")
	}
	// Other strategies can be nil (will use no-op implementations)
	return nil
}

// =============================================================================
// NO-OP STRATEGIES
// Default implementations that do nothing
// =============================================================================

// NoOpLoadBalancingStrategy does nothing (use cluster defaults)
type NoOpLoadBalancingStrategy struct{}

func (s *NoOpLoadBalancingStrategy) ConfigureCluster(cluster *clusterv3.Cluster, deployment *models.APIDeployment) error {
	return nil // No changes
}

func (s *NoOpLoadBalancingStrategy) Name() string {
	return "noop-loadbalancing"
}

// NoOpRetryStrategy does nothing (no retry policy)
type NoOpRetryStrategy struct{}

func (s *NoOpRetryStrategy) ConfigureRetry(route *routev3.Route, deployment *models.APIDeployment) error {
	return nil // No retry policy
}

func (s *NoOpRetryStrategy) Name() string {
	return "noop-retry"
}

// NoOpRateLimitStrategy does nothing (no rate limiting)
type NoOpRateLimitStrategy struct{}

func (s *NoOpRateLimitStrategy) ConfigureRateLimit(listener *listenerv3.Listener, deployment *models.APIDeployment) error {
	return nil // No rate limiting
}

func (s *NoOpRateLimitStrategy) Name() string {
	return "noop-ratelimit"
}

// NoOpObservabilityStrategy does nothing (no observability config)
type NoOpObservabilityStrategy struct{}

func (s *NoOpObservabilityStrategy) ConfigureObservability(listener *listenerv3.Listener, clusters []*clusterv3.Cluster, deployment *models.APIDeployment) error {
	return nil // No observability config
}

func (s *NoOpObservabilityStrategy) Name() string {
	return "noop-observability"
}
