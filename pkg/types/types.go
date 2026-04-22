package types

import (
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// StrategyConfig contains all strategy configurations for xDS generation
// This can be specified at API level (flowc.yaml) for this specific deployment
type StrategyConfig struct {
	// Deployment strategy configuration (cluster-focused)
	Deployment *DeploymentStrategyConfig `yaml:"deployment,omitempty" json:"deployment,omitempty"`

	// Route matching strategy configuration
	RouteMatching *RouteMatchStrategyConfig `yaml:"route_matching,omitempty" json:"route_matching,omitempty"`

	// Load balancing strategy configuration
	LoadBalancing *LoadBalancingStrategyConfig `yaml:"load_balancing,omitempty" json:"load_balancing,omitempty"`

	// Retry strategy configuration
	Retry *RetryStrategyConfig `yaml:"retry,omitempty" json:"retry,omitempty"`

	// Rate limiting strategy configuration
	RateLimit *RateLimitStrategyConfig `yaml:"rate_limiting,omitempty" json:"rate_limiting,omitempty"`

	// Observability strategy configuration
	Observability *ObservabilityStrategyConfig `yaml:"observability,omitempty" json:"observability,omitempty"`
}

// BlueGreenConfig defines blue-green deployment configuration
type BlueGreenConfig struct {
	// ActiveVersion is the currently serving version
	ActiveVersion string

	// StandbyVersion is the version ready to switch to
	StandbyVersion string

	// AutoPromote indicates if automatic promotion is enabled
	AutoPromote bool
}

// CanaryConfig defines canary deployment configuration
type CanaryConfig struct {
	// BaselineVersion is the stable version
	BaselineVersion string

	// CanaryVersion is the new version being tested
	CanaryVersion string

	// CanaryWeight is the percentage of traffic to canary (0-100)
	CanaryWeight int

	// MatchCriteria for header-based routing
	MatchCriteria *MatchCriteria
}

// MatchCriteria defines advanced traffic matching
type MatchCriteria struct {
	// Headers to match for routing
	Headers map[string]string

	// QueryParams to match
	QueryParams map[string]string

	// SourceLabels to match (for service mesh)
	SourceLabels map[string]string
}

// DeploymentStrategyConfig configures the deployment strategy (cluster generation)
type DeploymentStrategyConfig struct {
	// Type: basic, canary, blue-green
	Type string `yaml:"type" json:"type"`

	// Canary configuration (if type is "canary")
	Canary *CanaryConfig `yaml:"canary,omitempty" json:"canary,omitempty"`

	// Blue-green configuration (if type is "blue-green")
	BlueGreen *BlueGreenConfig `yaml:"blue_green,omitempty" json:"blue_green,omitempty"`
}

// RouteMatchStrategyConfig configures how routes are matched
type RouteMatchStrategyConfig struct {
	// Type: prefix, exact, regex, header-versioned
	Type string `yaml:"type" json:"type"`

	// For header-versioned routing
	VersionHeader string `yaml:"version_header,omitempty" json:"version_header,omitempty"`

	// Case sensitivity for path matching
	CaseSensitive bool `yaml:"case_sensitive,omitempty" json:"case_sensitive,omitempty"`
}

// LoadBalancingStrategyConfig configures load balancing behavior
type LoadBalancingStrategyConfig struct {
	// Type: round-robin, least-request, random, consistent-hash, locality-aware
	Type string `yaml:"type" json:"type"`

	// For consistent-hash
	HashOn     string `yaml:"hash_on,omitempty" json:"hash_on,omitempty"`         // header, cookie, source-ip
	HeaderName string `yaml:"header_name,omitempty" json:"header_name,omitempty"` // if hash_on=header
	CookieName string `yaml:"cookie_name,omitempty" json:"cookie_name,omitempty"` // if hash_on=cookie

	// For least-request
	ChoiceCount uint32 `yaml:"choice_count,omitempty" json:"choice_count,omitempty"` // Number of hosts to consider

	// Health check settings
	HealthCheck *HealthCheckConfig `yaml:"health_check,omitempty" json:"health_check,omitempty"`
}

// HealthCheckConfig configures health checking
type HealthCheckConfig struct {
	Enabled        bool   `yaml:"enabled" json:"enabled"`
	Interval       string `yaml:"interval,omitempty" json:"interval,omitempty"`               // e.g., "10s"
	Timeout        string `yaml:"timeout,omitempty" json:"timeout,omitempty"`                 // e.g., "5s"
	HealthyCount   uint32 `yaml:"healthy_count,omitempty" json:"healthy_count,omitempty"`     // Consecutive successes
	UnhealthyCount uint32 `yaml:"unhealthy_count,omitempty" json:"unhealthy_count,omitempty"` // Consecutive failures
	Path           string `yaml:"path,omitempty" json:"path,omitempty"`                       // HTTP path for health check
	ExpectedStatus uint32 `yaml:"expected_status,omitempty" json:"expected_status,omitempty"` // Expected HTTP status
}

// RetryStrategyConfig configures retry behavior
type RetryStrategyConfig struct {
	// Type: none, conservative, aggressive, custom
	Type string `yaml:"type" json:"type"`

	// Retry settings (for custom type or to override presets)
	MaxRetries uint32 `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`
	// e.g., "5xx,reset,connect-failure"
	RetryOn string `yaml:"retry_on,omitempty" json:"retry_on,omitempty"`
	// e.g., "2s"
	PerTryTimeout string `yaml:"per_try_timeout,omitempty" json:"per_try_timeout,omitempty"`

	// Retry priority
	RetriableStatusCodes []uint32 `yaml:"retriable_status_codes,omitempty" json:"retriable_status_codes,omitempty"`

	// Retry budget — max % of requests that can be retried
	BudgetPercent float64 `yaml:"budget_percent,omitempty" json:"budget_percent,omitempty"`
}

// RateLimitStrategyConfig configures rate limiting
type RateLimitStrategyConfig struct {
	// Type: none, global, per-ip, per-user, custom
	Type string `yaml:"type" json:"type"`

	// Global rate limit
	RequestsPerMinute uint32 `yaml:"requests_per_minute,omitempty" json:"requests_per_minute,omitempty"`
	BurstSize         uint32 `yaml:"burst_size,omitempty" json:"burst_size,omitempty"`

	// Per-user rate limiting
	IdentifyBy string `yaml:"identify_by,omitempty" json:"identify_by,omitempty"` // jwt_claim, header, cookie
	ClaimName  string `yaml:"claim_name,omitempty" json:"claim_name,omitempty"`   // JWT claim name
	HeaderName string `yaml:"header_name,omitempty" json:"header_name,omitempty"` // Header name
	CookieName string `yaml:"cookie_name,omitempty" json:"cookie_name,omitempty"` // Cookie name

	// External rate limit service
	ExternalService string `yaml:"external_service,omitempty" json:"external_service,omitempty"` // gRPC endpoint
}

// ObservabilityStrategyConfig configures tracing, metrics, and logging
type ObservabilityStrategyConfig struct {
	// Tracing configuration
	Tracing *TracingConfig `yaml:"tracing,omitempty" json:"tracing,omitempty"`

	// Metrics configuration
	Metrics *MetricsConfig `yaml:"metrics,omitempty" json:"metrics,omitempty"`

	// Access logging configuration
	AccessLogs *AccessLogsConfig `yaml:"access_logs,omitempty" json:"access_logs,omitempty"`
}

// TracingConfig configures distributed tracing
type TracingConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// zipkin, jaeger, datadog, opentelemetry
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`
	// 0.0 to 1.0
	SamplingRate float64 `yaml:"sampling_rate,omitempty" json:"sampling_rate,omitempty"`
	// Collector endpoint
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
}

// MetricsConfig configures metrics collection
type MetricsConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"` // prometheus, statsd
	Path     string `yaml:"path,omitempty" json:"path,omitempty"`         // Metrics endpoint path (e.g., /metrics)
	Port     uint32 `yaml:"port,omitempty" json:"port,omitempty"`         // Metrics port
}

// AccessLogsConfig configures access logging
type AccessLogsConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Format  string `yaml:"format,omitempty" json:"format,omitempty"` // json, text
	Path    string `yaml:"path,omitempty" json:"path,omitempty"`     // Log file path or stdout/stderr
}

// VirtualHostConfig represents virtual host settings
type VirtualHostConfig struct {
	// Name of the virtual host (auto-generated if not provided)
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// Domains this virtual host should match
	Domains []string `yaml:"domains,omitempty" json:"domains,omitempty"`

	// Use existing virtual host by name (for grouping APIs)
	UseExisting string `yaml:"use_existing,omitempty" json:"use_existing,omitempty"`
}

// GatewayConfig represents gateway targeting configuration in flowc.yaml.
// APIs are deployed to a specific virtual host within a listener within a gateway.
type GatewayConfig struct {
	// GatewayID is the UUID of the target gateway (preferred method)
	GatewayID string `yaml:"gateway_id,omitempty" json:"gateway_id,omitempty"`

	// NodeID is the Envoy node ID of the target gateway (alternative to GatewayID for backward compatibility)
	NodeID string `yaml:"node_id,omitempty" json:"node_id,omitempty"`

	// Port is the listener port within the gateway (required)
	Port uint32 `yaml:"port" json:"port"`

	// VirtualHostRef is the name of the target virtual host within the listener (optional)
	VirtualHostRef string `yaml:"virtualHostRef,omitempty" json:"virtualHostRef,omitempty"`

	// VirtualHost configuration for route matching
	VirtualHost VirtualHostConfig `yaml:"virtual_host,omitempty" json:"virtual_host,omitempty"`
}

// UpstreamConfig represents upstream service configuration
type UpstreamConfig struct {
	// Host of the upstream service
	Host string `yaml:"host" json:"host"`

	// Port of the upstream service
	Port uint32 `yaml:"port" json:"port"`

	// Scheme of the upstream service
	Scheme string `yaml:"scheme,omitempty" json:"scheme,omitempty"`

	// Timeout of the upstream service
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// HTTPFilter represents an HTTP filter to apply to the gateway
type HTTPFilter struct {
	// Name of the HTTP filter
	Name string `yaml:"name" json:"name"`

	// Configuration of the HTTP filter
	Config map[string]any `yaml:"config" json:"config"`
}

// APIDeployment represents a complete API deployment
type APIDeploymentInfo struct {
	ID        string    `yaml:"id" json:"id"`
	Name      string    `yaml:"name" json:"name"`
	Version   string    `yaml:"version" json:"version"`
	Context   string    `yaml:"context" json:"context"`
	Status    string    `yaml:"status" json:"status"`
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at" json:"updated_at"`
}

// APIRoute represents a route extracted from OpenAPI paths
type APIRoute struct {
	Path        string              `yaml:"path" json:"path"`
	Method      string              `yaml:"method" json:"method"`
	Operation   *openapi3.Operation `yaml:"operation,omitempty" json:"operation,omitempty"`
	OperationID string              `yaml:"operation_id,omitempty" json:"operation_id,omitempty"`
	Summary     string              `yaml:"summary,omitempty" json:"summary,omitempty"`
	Tags        []string            `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// FlowCMetadata represents the metadata from flowc.yaml
type FlowCMetadata struct {
	// Name of the API
	Name string `yaml:"name" json:"name"`

	// Version of the API
	Version string `yaml:"version" json:"version"`

	// Description of the API
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Context is the gateway base path where this API is exposed
	// This is a unified concept that works across all API types:
	// - REST: Base path prefix for all HTTP routes (e.g., "/api/v1" or "api/v1")
	// - gRPC: Base path for gRPC services (e.g., "/grpc/v1" or "grpc/v1")
	// - GraphQL: Base path for GraphQL endpoint (e.g., "/graphql" or "graphql")
	// - WebSocket: Base path for WebSocket connections (e.g., "/ws" or "ws")
	// - SSE: Base path for Server-Sent Events (e.g., "/events" or "events")
	// The path can be specified with or without a leading slash; it will be normalized.
	Context string `yaml:"context" json:"context"`

	// API type (rest, grpc, graphql, websocket, sse)
	// Determines which parser to use for the specification file
	APIType string `yaml:"api_type,omitempty" json:"api_type,omitempty"`

	// Specification file name in the bundle (e.g., "openapi.yaml", "service.proto")
	// If not specified, defaults are used based on api_type
	SpecFile string `yaml:"spec_file,omitempty" json:"spec_file,omitempty"`

	// Gateway configuration
	Gateway GatewayConfig `yaml:"gateway" json:"gateway"`

	// Upstream configuration
	Upstream UpstreamConfig `yaml:"upstream" json:"upstream"`

	// Strategy configuration for this specific deployment
	// This defines how this API should be deployed, routed, load balanced, etc.
	Strategy *StrategyConfig `yaml:"strategy,omitempty" json:"strategy,omitempty"`

	// Labels for the API
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}
