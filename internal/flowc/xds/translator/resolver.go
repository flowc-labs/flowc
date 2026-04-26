package translator

import (
	"fmt"
	"time"

	"github.com/flowc-labs/flowc/internal/flowc/models"
	"github.com/flowc-labs/flowc/pkg/logger"
	"github.com/flowc-labs/flowc/pkg/types"
)

// ConfigResolver resolves xDS strategy configuration with precedence:
// 1. Built-in defaults (code)
// 2. Profile defaults (gateway profile)
// 3. Gateway-wide defaults (gateway config)
// 4. Per-API config (flowc.yaml) - HIGHEST PRECEDENCE
type ConfigResolver struct {
	builtinDefaults *types.StrategyConfig
	profileDefaults *types.StrategyConfig
	gatewayDefaults *types.StrategyConfig
	logger          *logger.EnvoyLogger
}

// NewConfigResolver creates a new config resolver.
// profileDefaults may be nil if the gateway does not reference a profile.
func NewConfigResolver(profileDefaults, gatewayDefaults *types.StrategyConfig, log *logger.EnvoyLogger) *ConfigResolver {
	return &ConfigResolver{
		builtinDefaults: DefaultStrategyConfig(),
		profileDefaults: profileDefaults,
		gatewayDefaults: gatewayDefaults,
		logger:          log,
	}
}

// Resolve resolves the final configuration by applying precedence rules
func (r *ConfigResolver) Resolve(apiConfig *types.StrategyConfig) *types.StrategyConfig {
	resolved := &types.StrategyConfig{}

	// Resolve each strategy configuration
	resolved.Deployment = r.resolveDeployment(apiConfig)
	resolved.RouteMatching = r.resolveRouteMatching(apiConfig)
	resolved.LoadBalancing = r.resolveLoadBalancing(apiConfig)
	resolved.Retry = r.resolveRetry(apiConfig)
	resolved.RateLimit = r.resolveRateLimit(apiConfig)
	resolved.Observability = r.resolveObservability(apiConfig)

	if r.logger != nil {
		r.logger.WithFields(map[string]any{
			"deployment_type": resolved.Deployment.Type,
			"route_type":      resolved.RouteMatching.Type,
			"lb_type":         resolved.LoadBalancing.Type,
			"retry_type":      resolved.Retry.Type,
			"ratelimit_type":  resolved.RateLimit.Type,
		}).Debug("Resolved xDS strategy configuration")
	}

	return resolved
}

// resolveDeployment resolves deployment strategy config
func (r *ConfigResolver) resolveDeployment(apiConfig *types.StrategyConfig) *types.DeploymentStrategyConfig {
	// Precedence: API > Gateway > Profile > Builtin
	if apiConfig != nil && apiConfig.Deployment != nil {
		return apiConfig.Deployment
	}
	if r.gatewayDefaults != nil && r.gatewayDefaults.Deployment != nil {
		return r.gatewayDefaults.Deployment
	}
	if r.profileDefaults != nil && r.profileDefaults.Deployment != nil {
		return r.profileDefaults.Deployment
	}
	return r.builtinDefaults.Deployment
}

// resolveRouteMatching resolves route matching strategy config
func (r *ConfigResolver) resolveRouteMatching(apiConfig *types.StrategyConfig) *types.RouteMatchStrategyConfig {
	if apiConfig != nil && apiConfig.RouteMatching != nil {
		return apiConfig.RouteMatching
	}
	if r.gatewayDefaults != nil && r.gatewayDefaults.RouteMatching != nil {
		return r.gatewayDefaults.RouteMatching
	}
	if r.profileDefaults != nil && r.profileDefaults.RouteMatching != nil {
		return r.profileDefaults.RouteMatching
	}
	return r.builtinDefaults.RouteMatching
}

// resolveLoadBalancing resolves load balancing strategy config
func (r *ConfigResolver) resolveLoadBalancing(apiConfig *types.StrategyConfig) *types.LoadBalancingStrategyConfig {
	if apiConfig != nil && apiConfig.LoadBalancing != nil {
		return apiConfig.LoadBalancing
	}
	if r.gatewayDefaults != nil && r.gatewayDefaults.LoadBalancing != nil {
		return r.gatewayDefaults.LoadBalancing
	}
	if r.profileDefaults != nil && r.profileDefaults.LoadBalancing != nil {
		return r.profileDefaults.LoadBalancing
	}
	return r.builtinDefaults.LoadBalancing
}

// resolveRetry resolves retry strategy config
func (r *ConfigResolver) resolveRetry(apiConfig *types.StrategyConfig) *types.RetryStrategyConfig {
	if apiConfig != nil && apiConfig.Retry != nil {
		return apiConfig.Retry
	}
	if r.gatewayDefaults != nil && r.gatewayDefaults.Retry != nil {
		return r.gatewayDefaults.Retry
	}
	if r.profileDefaults != nil && r.profileDefaults.Retry != nil {
		return r.profileDefaults.Retry
	}
	return r.builtinDefaults.Retry
}

// resolveRateLimit resolves rate limiting strategy config
func (r *ConfigResolver) resolveRateLimit(apiConfig *types.StrategyConfig) *types.RateLimitStrategyConfig {
	if apiConfig != nil && apiConfig.RateLimit != nil {
		return apiConfig.RateLimit
	}
	if r.gatewayDefaults != nil && r.gatewayDefaults.RateLimit != nil {
		return r.gatewayDefaults.RateLimit
	}
	if r.profileDefaults != nil && r.profileDefaults.RateLimit != nil {
		return r.profileDefaults.RateLimit
	}
	return r.builtinDefaults.RateLimit
}

// resolveObservability resolves observability strategy config
func (r *ConfigResolver) resolveObservability(apiConfig *types.StrategyConfig) *types.ObservabilityStrategyConfig {
	if apiConfig != nil && apiConfig.Observability != nil {
		return apiConfig.Observability
	}
	if r.gatewayDefaults != nil && r.gatewayDefaults.Observability != nil {
		return r.gatewayDefaults.Observability
	}
	if r.profileDefaults != nil && r.profileDefaults.Observability != nil {
		return r.profileDefaults.Observability
	}
	return r.builtinDefaults.Observability
}

// StrategyFactory creates strategy instances from configuration
type StrategyFactory struct {
	options *TranslatorOptions
	logger  *logger.EnvoyLogger
}

// NewStrategyFactory creates a new strategy factory
func NewStrategyFactory(options *TranslatorOptions, log *logger.EnvoyLogger) *StrategyFactory {
	if options == nil {
		options = DefaultTranslatorOptions()
	}
	return &StrategyFactory{
		options: options,
		logger:  log,
	}
}

// CreateStrategySet creates a complete strategy set from configuration
func (f *StrategyFactory) CreateStrategySet(config *types.StrategyConfig, deployment *models.APIDeployment) (*StrategySet, error) {
	if config == nil {
		config = DefaultStrategyConfig()
	}

	// Create deployment strategy
	deploymentStrategy, err := f.createDeploymentStrategy(config.Deployment)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment strategy: %w", err)
	}

	// Create route matching strategy
	routeMatchStrategy, err := f.createRouteMatchStrategy(config.RouteMatching)
	if err != nil {
		return nil, fmt.Errorf("failed to create route match strategy: %w", err)
	}

	// Create load balancing strategy
	loadBalancingStrategy, err := f.createLoadBalancingStrategy(config.LoadBalancing)
	if err != nil {
		return nil, fmt.Errorf("failed to create load balancing strategy: %w", err)
	}

	// Create retry strategy
	retryStrategy, err := f.createRetryStrategy(config.Retry)
	if err != nil {
		return nil, fmt.Errorf("failed to create retry strategy: %w", err)
	}

	// Create rate limit strategy
	rateLimitStrategy, err := f.createRateLimitStrategy(config.RateLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to create rate limit strategy: %w", err)
	}

	// Create observability strategy
	observabilityStrategy, err := f.createObservabilityStrategy(config.Observability)
	if err != nil {
		return nil, fmt.Errorf("failed to create observability strategy: %w", err)
	}

	return &StrategySet{
		Deployment:    deploymentStrategy,
		RouteMatch:    routeMatchStrategy,
		LoadBalancing: loadBalancingStrategy,
		Retry:         retryStrategy,
		RateLimit:     rateLimitStrategy,
		Observability: observabilityStrategy,
	}, nil
}

// createDeploymentStrategy creates a deployment strategy from config
func (f *StrategyFactory) createDeploymentStrategy(config *types.DeploymentStrategyConfig) (DeploymentStrategy, error) {
	if config == nil {
		config = &types.DeploymentStrategyConfig{Type: "basic"}
	}

	switch config.Type {
	case "basic", "":
		return NewBasicDeploymentStrategy(f.options, f.logger), nil

	case "canary":
		if config.Canary == nil {
			return nil, ErrStrategyConfigMissing("canary")
		}
		return NewCanaryDeploymentStrategy(config.Canary, f.options, f.logger), nil

	case "blue-green":
		if config.BlueGreen == nil {
			return nil, ErrStrategyConfigMissing("blue-green")
		}
		return NewBlueGreenDeploymentStrategy(config.BlueGreen, f.options, f.logger), nil

	default:
		return nil, ErrInvalidStrategyType("deployment", config.Type)
	}
}

// createRouteMatchStrategy creates a route matching strategy from config
func (f *StrategyFactory) createRouteMatchStrategy(config *types.RouteMatchStrategyConfig) (RouteMatchStrategy, error) {
	if config == nil {
		config = &types.RouteMatchStrategyConfig{Type: "prefix", CaseSensitive: true}
	}

	switch config.Type {
	case "prefix", "":
		return NewPrefixRouteMatchStrategy(config.CaseSensitive), nil

	case "exact":
		return NewExactRouteMatchStrategy(config.CaseSensitive), nil

	case "regex":
		return NewRegexRouteMatchStrategy(config.CaseSensitive), nil

	case "header-versioned":
		return NewHeaderVersionedRouteMatchStrategy(config.VersionHeader, config.CaseSensitive), nil

	default:
		return nil, ErrInvalidStrategyType("route_match", config.Type)
	}
}

// createLoadBalancingStrategy creates a load balancing strategy from config
func (f *StrategyFactory) createLoadBalancingStrategy(config *types.LoadBalancingStrategyConfig) (LoadBalancingStrategy, error) {
	if config == nil {
		config = &types.LoadBalancingStrategyConfig{Type: "round-robin"}
	}

	switch config.Type {
	case "round-robin", "":
		return NewRoundRobinLoadBalancingStrategy(), nil

	case "least-request":
		return NewLeastRequestLoadBalancingStrategy(config.ChoiceCount), nil

	case "random":
		return NewRandomLoadBalancingStrategy(), nil

	case "consistent-hash":
		return NewConsistentHashLoadBalancingStrategy(config.HashOn, config.HeaderName, config.CookieName), nil

	case "locality-aware":
		// Locality-aware wraps another strategy
		baseStrategy, err := f.createBaseLoadBalancingStrategy(config)
		if err != nil {
			return nil, err
		}
		return NewLocalityAwareLoadBalancingStrategy(baseStrategy), nil

	default:
		return nil, ErrInvalidStrategyType("load_balancing", config.Type)
	}
}

// createBaseLoadBalancingStrategy creates base strategy for locality-aware
//
//nolint:unparam // config reserved for future config-driven base-strategy selection
func (f *StrategyFactory) createBaseLoadBalancingStrategy(config *types.LoadBalancingStrategyConfig) (LoadBalancingStrategy, error) {
	// Default to round-robin for base
	return NewRoundRobinLoadBalancingStrategy(), nil
}

// createRetryStrategy creates a retry strategy from config
func (f *StrategyFactory) createRetryStrategy(config *types.RetryStrategyConfig) (RetryStrategy, error) {
	if config == nil {
		config = &types.RetryStrategyConfig{Type: "conservative"}
	}

	switch config.Type {
	case "none":
		return &NoOpRetryStrategy{}, nil

	case "conservative", "":
		return NewConservativeRetryStrategy(), nil

	case "aggressive":
		return NewAggressiveRetryStrategy(), nil

	case "custom":
		if config.PerTryTimeout == "" {
			config.PerTryTimeout = "5s"
		}
		duration, err := parseDuration(config.PerTryTimeout)
		if err != nil {
			return nil, fmt.Errorf("invalid per_try_timeout: %w", err)
		}
		return NewCustomRetryStrategy(config.MaxRetries, config.RetryOn, duration), nil

	default:
		return nil, ErrInvalidStrategyType("retry", config.Type)
	}
}

// createRateLimitStrategy creates a rate limiting strategy from config
//
//nolint:unparam // TODO: real implementations will surface construction errors
func (f *StrategyFactory) createRateLimitStrategy(config *types.RateLimitStrategyConfig) (RateLimitStrategy, error) {
	if config == nil {
		config = &types.RateLimitStrategyConfig{Type: "none"}
	}

	switch config.Type {
	case "none", "":
		return &NoOpRateLimitStrategy{}, nil

	// TODO: Implement actual rate limiting strategies
	default:
		// For now, return no-op for unimplemented types
		return &NoOpRateLimitStrategy{}, nil
	}
}

// createObservabilityStrategy creates an observability strategy from config
//
//nolint:unparam // TODO: real implementations will surface construction errors
func (f *StrategyFactory) createObservabilityStrategy(config *types.ObservabilityStrategyConfig) (ObservabilityStrategy, error) {
	if config == nil {
		return &NoOpObservabilityStrategy{}, nil
	}

	// TODO: Implement actual observability strategies
	// For now, return no-op
	return &NoOpObservabilityStrategy{}, nil
}

// Helper functions

func parseDuration(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}
