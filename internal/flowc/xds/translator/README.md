# xDS Translator Architecture

This package implements a **composite strategy-based architecture** for translating FlowC API deployments into Envoy xDS resources. It uses the Strategy Pattern with composition to enable flexible, configuration-driven xDS generation that supports multiple deployment patterns, routing strategies, load balancing policies, and more.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Core Interfaces](#core-interfaces)
- [Strategy Interfaces](#strategy-interfaces)
- [Built-in Strategies](#built-in-strategies)
- [Configuration System](#configuration-system)
- [Usage Examples](#usage-examples)
- [Extension Guide](#extension-guide)

## Overview

The translator architecture separates xDS generation into **composable strategies**, where each strategy handles a specific concern:

1. **Deployment Strategy** - Cluster generation (basic, canary, blue-green)
2. **Route Match Strategy** - Path matching logic (prefix, exact, regex, header-versioned)
3. **Load Balancing Strategy** - LB policies (round-robin, least-request, consistent-hash, locality-aware)
4. **Retry Strategy** - Retry policies (none, conservative, aggressive, custom)
5. **Rate Limit Strategy** - Rate limiting configuration
6. **Observability Strategy** - Tracing, metrics, and logging

Key benefits:
- **Separation of Concerns** - Each strategy handles ONE responsibility
- **Composition Over Inheritance** - Mix and match strategies independently
- **Configuration-Driven** - Change behavior without code changes
- **Extensible** - Add new strategies without touching existing code
- **Testable** - Test strategies in isolation

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                  API Deployment + IR                        │
│         (Metadata + OpenAPI/gRPC/GraphQL spec)              │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│                  ConfigResolver                             │
│  (Resolves strategy config with 3-level precedence)        │
│  Built-in Defaults → Gateway Config → API Config           │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│                  StrategyFactory                            │
│         (Creates strategy instances from config)            │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│                  StrategySet                                │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │ Deployment  │  │ RouteMatch  │  │LoadBalancing│        │
│  │  Strategy   │  │  Strategy   │  │  Strategy   │        │
│  └─────────────┘  └─────────────┘  └─────────────┘        │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │   Retry     │  │ RateLimit   │  │Observability│        │
│  │  Strategy   │  │  Strategy   │  │  Strategy   │        │
│  └─────────────┘  └─────────────┘  └─────────────┘        │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│              CompositeTranslator                            │
│       (Orchestrates strategies in translation phases)       │
│                                                             │
│  Phase 1: Generate Clusters (DeploymentStrategy)           │
│  Phase 2: Apply Load Balancing (LoadBalancingStrategy)     │
│  Phase 3: Generate Routes (RouteMatchStrategy)             │
│  Phase 4: Apply Retry Policies (RetryStrategy)             │
│  Phase 5: Generate Listeners                               │
│  Phase 6: Apply Rate Limiting (RateLimitStrategy)          │
│  Phase 7: Apply Observability (ObservabilityStrategy)      │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
           ┌──────────────────────┐
           │   xDS Resources       │
           │ (Clusters, Routes,    │
           │  Listeners, Endpoints)│
           └──────────────────────┘
```

## Core Interfaces

### Translator Interface

All translators implement this interface:

```go
type Translator interface {
    // Translate converts a deployment into xDS resources
    // deployment: The persisted APIDeployment with metadata
    // ir: The transient IR representation (not persisted)
    // nodeID: Target Envoy node ID
    Translate(ctx context.Context, deployment *models.APIDeployment, ir *ir.API, nodeID string) (*XDSResources, error)
    
    // Name returns the name/type of this translator
    Name() string
    
    // Validate checks if the deployment is valid for this translator
    Validate(deployment *models.APIDeployment, ir *ir.API) error
}
```

**Key Points:**
- Takes `*models.APIDeployment` (persisted deployment metadata) and `*ir.API` (transient IR)
- IR supports multiple API types (REST, gRPC, GraphQL, WebSocket, SSE)
- Returns `*XDSResources` containing all Envoy xDS resource types

### XDSResources

Complete set of xDS resources:

```go
type XDSResources struct {
    Clusters  []*clusterv3.Cluster
    Endpoints []*endpointv3.ClusterLoadAssignment
    Listeners []*listenerv3.Listener
    Routes    []*routev3.RouteConfiguration
}
```

## Strategy Interfaces

### 1. DeploymentStrategy

Handles cluster generation based on deployment patterns.

```go
type DeploymentStrategy interface {
    // GenerateClusters creates clusters for the deployment
    GenerateClusters(ctx context.Context, deployment *models.APIDeployment) ([]*clusterv3.Cluster, error)
    
    // GetClusterNames returns the names of clusters that will be created
    GetClusterNames(deployment *models.APIDeployment) []string
    
    // Name returns the strategy name
    Name() string
    
    // Validate checks if the deployment is valid for this strategy
    Validate(deployment *models.APIDeployment) error
}
```

**Purpose:** Determines **how many clusters** and **which versions** to deploy.

**Examples:** Basic (1 cluster), Canary (2 clusters with weighted traffic), Blue-Green (2 clusters, active + standby)

### 2. RouteMatchStrategy

Handles how routes are matched against incoming requests.

```go
type RouteMatchStrategy interface {
    // CreateMatcher creates a route matcher for the given path and method
    CreateMatcher(path, method string, endpoint *ir.Endpoint) *routev3.RouteMatch
    
    // Name returns the strategy name
    Name() string
}
```

**Purpose:** Determines **how paths are matched** in Envoy route configuration.

**Examples:** Prefix (`/users` matches `/users/123`), Exact (`/users` only), Regex, Header-versioned

### 3. LoadBalancingStrategy

Configures load balancing policies for clusters.

```go
type LoadBalancingStrategy interface {
    // ConfigureCluster applies load balancing settings to a cluster
    ConfigureCluster(cluster *clusterv3.Cluster, deployment *models.APIDeployment) error
    
    // Name returns the strategy name
    Name() string
}
```

**Purpose:** Determines **how traffic is distributed** across backend instances.

**Examples:** Round-robin, Least-request, Consistent-hash (session affinity), Locality-aware

### 4. RetryStrategy

Configures retry policies for failed requests.

```go
type RetryStrategy interface {
    // ConfigureRetry applies retry policy to a route
    ConfigureRetry(route *routev3.Route, deployment *models.APIDeployment) error
    
    // Name returns the strategy name
    Name() string
}
```

**Purpose:** Determines **retry behavior** for failed requests.

**Examples:** None (no retry), Conservative (1 retry), Aggressive (3 retries), Custom

### 5. RateLimitStrategy

Configures rate limiting policies.

```go
type RateLimitStrategy interface {
    // ConfigureRateLimit applies rate limiting to listeners/routes
    ConfigureRateLimit(listener *listenerv3.Listener, deployment *models.APIDeployment) error
    
    // Name returns the strategy name
    Name() string
}
```

**Purpose:** Controls **request rate limiting**.

**Status:** Currently uses no-op implementation (future enhancement)

### 6. ObservabilityStrategy

Configures tracing, metrics, and logging.

```go
type ObservabilityStrategy interface {
    // ConfigureObservability applies observability settings to listener/cluster
    ConfigureObservability(listener *listenerv3.Listener, clusters []*clusterv3.Cluster, deployment *models.APIDeployment) error
    
    // Name returns the strategy name
    Name() string
}
```

**Purpose:** Configures **tracing, metrics, and access logs**.

**Status:** Currently uses no-op implementation (future enhancement)

## Built-in Strategies

### Deployment Strategies

#### BasicDeploymentStrategy

**Type:** `basic`

**Purpose:** Standard 1:1 deployment with a single cluster.

**Use Case:** Most APIs that don't need advanced deployment patterns.

**Configuration:**
```yaml
strategies:
  deployment:
    type: basic
```

**Behavior:**
- Creates 1 cluster: `{name}-{version}-cluster`
- All traffic goes to this cluster

---

#### CanaryDeploymentStrategy

**Type:** `canary`

**Purpose:** Weighted traffic splitting between baseline and canary versions.

**Use Case:** Gradual rollout of new API versions with controlled traffic shifting.

**Configuration:**
```yaml
strategies:
  deployment:
    type: canary
    canary:
      baseline_version: v1.0.0
      canary_version: v2.0.0
      canary_weight: 20  # 20% to canary, 80% to baseline
```

**Behavior:**
- Creates 2 clusters: baseline and canary
- Routes weighted traffic based on `canary_weight`
- Supports header-based routing for targeted testing

---

#### BlueGreenDeploymentStrategy

**Type:** `blue-green`

**Purpose:** Zero-downtime deployment with instant switchover capability.

**Use Case:** Risk-free deployments with easy rollback.

**Configuration:**
```yaml
strategies:
  deployment:
    type: blue-green
    blue_green:
      active_version: v1.0.0
      standby_version: v2.0.0
```

**Behavior:**
- Creates 2 clusters: active and standby
- All traffic goes to active cluster
- Switch traffic by changing `active_version` config

---

### Route Match Strategies

#### PrefixRouteMatchStrategy

**Type:** `prefix` (default)

**Configuration:**
```yaml
strategies:
  route_matching:
    type: prefix
    case_sensitive: true
```

**Behavior:** Matches path prefixes (e.g., `/users` matches `/users/123`)

---

#### ExactRouteMatchStrategy

**Type:** `exact`

**Configuration:**
```yaml
strategies:
  route_matching:
    type: exact
    case_sensitive: true
```

**Behavior:** Matches exact paths only (e.g., `/users` does not match `/users/123`)

---

#### RegexRouteMatchStrategy

**Type:** `regex`

**Configuration:**
```yaml
strategies:
  route_matching:
    type: regex
    case_sensitive: true
```

**Behavior:** Uses regex patterns for path matching

---

#### HeaderVersionedRouteMatchStrategy

**Type:** `header-versioned`

**Configuration:**
```yaml
strategies:
  route_matching:
    type: header-versioned
    version_header: x-api-version
```

**Behavior:** Routes based on API version header (e.g., `x-api-version: v2`)

---

### Load Balancing Strategies

#### RoundRobinLoadBalancingStrategy

**Type:** `round-robin` (default)

**Configuration:**
```yaml
strategies:
  load_balancing:
    type: round-robin
```

**Behavior:** Distributes requests evenly across all healthy backends

---

#### LeastRequestLoadBalancingStrategy

**Type:** `least-request`

**Configuration:**
```yaml
strategies:
  load_balancing:
    type: least-request
    choice_count: 2
```

**Behavior:** Routes to the backend with the fewest active requests

---

#### ConsistentHashLoadBalancingStrategy

**Type:** `consistent-hash`

**Configuration:**
```yaml
strategies:
  load_balancing:
    type: consistent-hash
    hash_on: header
    header_name: x-session-id
```

**Behavior:** Provides session affinity by hashing on header/cookie/source-ip

---

#### LocalityAwareLoadBalancingStrategy

**Type:** `locality-aware`

**Configuration:**
```yaml
strategies:
  load_balancing:
    type: locality-aware
```

**Behavior:** Prefers backends in the same locality/zone

---

### Retry Strategies

#### ConservativeRetryStrategy

**Type:** `conservative` (default)

**Configuration:**
```yaml
strategies:
  retry:
    type: conservative
```

**Behavior:**
- Max retries: 1
- Retry on: 5xx, reset, connect-failure
- Per-try timeout: 5s
- Safe for most APIs

---

#### AggressiveRetryStrategy

**Type:** `aggressive`

**Configuration:**
```yaml
strategies:
  retry:
    type: aggressive
```

**Behavior:**
- Max retries: 3
- Retry on: 5xx, reset, connect-failure, refused-stream
- Per-try timeout: 3s
- For idempotent read-only APIs

---

#### CustomRetryStrategy

**Type:** `custom`

**Configuration:**
```yaml
strategies:
  retry:
    type: custom
    max_retries: 2
    retry_on: "5xx,reset"
    per_try_timeout: "2s"
```

**Behavior:** Fully customizable retry policy

---

#### NoOpRetryStrategy

**Type:** `none`

**Configuration:**
```yaml
strategies:
  retry:
    type: none
```

**Behavior:** No retry policy (important for non-idempotent operations like payments)

---

## Configuration System

FlowC uses a **3-level configuration hierarchy** with precedence:

```
Built-in Defaults (code)
    ↓  (overridden by)
Gateway Config (gateway-wide defaults)
    ↓  (overridden by)
API Config (flowc.yaml in deployment bundle) ← HIGHEST PRECEDENCE
```

### Level 1: Built-in Defaults (Code)

Defined in `config.go`:

```go
func DefaultStrategyConfig() *types.StrategyConfig {
    return &types.StrategyConfig{
        Deployment: &types.DeploymentStrategyConfig{
            Type: "basic",
        },
        RouteMatching: &types.RouteMatchStrategyConfig{
            Type:          "prefix",
            CaseSensitive: true,
        },
        LoadBalancing: &types.LoadBalancingStrategyConfig{
            Type:        "round-robin",
            ChoiceCount: 2,
        },
        Retry: &types.RetryStrategyConfig{
            Type:          "conservative",
            MaxRetries:    1,
            RetryOn:       "5xx,reset",
            PerTryTimeout: "5s",
        },
        RateLimit: &types.RateLimitStrategyConfig{
            Type: "none",
        },
        Observability: &types.ObservabilityStrategyConfig{
            Tracing: &types.TracingConfig{
                Enabled:      false,
                SamplingRate: 0.01,
            },
            Metrics: &types.MetricsConfig{
                Enabled: false,
            },
        },
    }
}
```

### Level 2: Gateway Config (Optional)

Gateway-wide defaults for all deployments:

```yaml
# gateway-config.yaml (example)
gateway:
  xds_defaults:
    route_matching:
      type: prefix
      case_sensitive: true
    load_balancing:
      type: round-robin
    retry:
      type: conservative
    rate_limiting:
      type: global
      requests_per_minute: 100000
```

### Level 3: API Config (Per Deployment)

Specified in `flowc.yaml` inside the deployment bundle:

```yaml
# flowc.yaml
name: payment-api
version: v2.0.0
context: /api/v1/payments

upstream:
  host: payment-backend.internal
  port: 8080
  scheme: http

strategies:
  deployment:
    type: canary
    canary:
      baseline_version: v1.0.0
      canary_version: v2.0.0
      canary_weight: 10
  
  route_matching:
    type: exact  # Override gateway default
    case_sensitive: true
  
  load_balancing:
    type: consistent-hash  # Session affinity
    hash_on: header
    header_name: x-session-id
  
  retry:
    type: none  # NO retry for payment operations!
```

### ConfigResolver

The `ConfigResolver` merges configurations with proper precedence:

```go
resolver := translator.NewConfigResolver(gatewayDefaults, logger)
resolvedConfig := resolver.Resolve(apiConfig) // Applies precedence rules
```

### StrategyFactory

The `StrategyFactory` creates strategy instances from resolved configuration:

```go
factory := translator.NewStrategyFactory(options, logger)
strategySet, err := factory.CreateStrategySet(resolvedConfig, deployment)
```

## Usage Examples

### Example 1: Basic Deployment

```go
package main

import (
    "context"
    "github.com/flowc-labs/flowc/internal/flowc/xds/translator"
    "github.com/flowc-labs/flowc/pkg/types"
)

func main() {
    // 1. Get deployment and IR (from bundle loader)
    deployment := getAPIDeployment() // *models.APIDeployment
    irAPI := getIR()                  // *ir.API
    
    // 2. Resolve configuration (API config overrides defaults)
    resolver := translator.NewConfigResolver(nil, logger)
    config := resolver.Resolve(deployment.Metadata.Strategies)
    
    // 3. Create strategy set
    factory := translator.NewStrategyFactory(nil, logger)
    strategies, err := factory.CreateStrategySet(config, deployment)
    if err != nil {
        panic(err)
    }
    
    // 4. Create composite translator
    compositeTranslator, err := translator.NewCompositeTranslator(strategies, nil, logger)
    if err != nil {
        panic(err)
    }
    
    // 5. Translate to xDS resources
    resources, err := compositeTranslator.Translate(context.Background(), deployment, irAPI, "envoy-node-1")
    if err != nil {
        panic(err)
    }
    
    // 6. Deploy to xDS cache
    cache.SetSnapshot(nodeID, resources)
}
```

### Example 2: Canary Deployment for Payment API

```yaml
# flowc.yaml
name: payment-api
version: v2.0.0
context: /api/v1/payments

upstream:
  host: payment-svc.default.svc.cluster.local
  port: 8080

strategies:
  deployment:
    type: canary
    canary:
      baseline_version: v1.0.0
      canary_version: v2.0.0
      canary_weight: 10  # Start with 10% traffic to v2
  
  route_matching:
    type: exact  # Exact path matching for security
    case_sensitive: true
  
  load_balancing:
    type: consistent-hash
    hash_on: header
    header_name: x-session-id  # Session affinity for payment flows
  
  retry:
    type: none  # CRITICAL: No retry for payment operations!
```

### Example 3: Blue-Green Deployment for Order API

```yaml
# flowc.yaml
name: order-api
version: v2.0.0
context: /api/v1/orders

upstream:
  host: order-svc.default.svc.cluster.local
  port: 8080

strategies:
  deployment:
    type: blue-green
    blue_green:
      active_version: v1.0.0
      standby_version: v2.0.0  # v2 ready but not receiving traffic
  
  load_balancing:
    type: least-request
    choice_count: 2
  
  retry:
    type: conservative
    max_retries: 1
```

**To switch traffic to v2.0.0:**
```yaml
blue_green:
  active_version: v2.0.0    # ← Changed
  standby_version: v1.0.0   # ← Now standby
```

### Example 4: Read-Only User API with Aggressive Retry

```yaml
# flowc.yaml
name: user-api
version: v1.0.0
context: /api/v1/users

upstream:
  host: user-svc.default.svc.cluster.local
  port: 8080

strategies:
  deployment:
    type: basic  # Simple deployment
  
  route_matching:
    type: prefix  # Standard prefix matching
    case_sensitive: true
  
  load_balancing:
    type: round-robin
  
  retry:
    type: aggressive  # Safe for read-only operations
```

### Example 5: Strategy Combination Matrix

| API Type | Deployment | Route Match | Load Balancing | Retry | Rationale |
|----------|------------|-------------|----------------|-------|-----------|
| **Payment API** | Canary | Exact | Consistent Hash | None | Non-idempotent, session affinity needed |
| **User API (Read)** | Basic | Prefix | Round Robin | Aggressive | Idempotent reads, simple deployment |
| **Order API** | Blue-Green | Prefix | Least Request | Conservative | Instant rollback, balanced load |
| **Analytics API** | Basic | Regex | Locality-Aware | Conservative | Complex patterns, locality important |
| **Auth API** | Blue-Green | Exact | Consistent Hash | None | Security-critical, session affinity |

## Extension Guide

### Creating a Custom Strategy

#### Step 1: Implement the Strategy Interface

```go
package translator

import (
    clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
    "github.com/flowc-labs/flowc/internal/flowc/models"
)

// CustomLoadBalancingStrategy implements a custom LB policy
type CustomLoadBalancingStrategy struct {
    config *CustomLBConfig
}

func NewCustomLoadBalancingStrategy(config *CustomLBConfig) *CustomLoadBalancingStrategy {
    return &CustomLoadBalancingStrategy{config: config}
}

func (s *CustomLoadBalancingStrategy) Name() string {
    return "custom-lb"
}

func (s *CustomLoadBalancingStrategy) ConfigureCluster(cluster *clusterv3.Cluster, deployment *models.APIDeployment) error {
    // Apply your custom load balancing configuration
    // Modify cluster.LoadAssignment, cluster.LbPolicy, etc.
    return nil
}
```

#### Step 2: Add Configuration Type

```go
// In pkg/types/types.go

type CustomLBConfig struct {
    Algorithm string `yaml:"algorithm" json:"algorithm"`
    Weight    int    `yaml:"weight" json:"weight"`
}
```

#### Step 3: Register with Factory

```go
// In resolver.go, modify createLoadBalancingStrategy()

func (f *StrategyFactory) createLoadBalancingStrategy(config *types.LoadBalancingStrategyConfig) (LoadBalancingStrategy, error) {
    switch config.Type {
    // ... existing cases ...
    
    case "custom-lb":
        if config.Custom == nil {
            return nil, ErrStrategyConfigMissing("custom-lb")
        }
        return NewCustomLoadBalancingStrategy(config.Custom), nil
    
    default:
        return nil, ErrInvalidStrategyType("load_balancing", config.Type)
    }
}
```

#### Step 4: Use in Configuration

```yaml
# flowc.yaml
strategies:
  load_balancing:
    type: custom-lb
    custom:
      algorithm: weighted-random
      weight: 10
```

### Testing Strategies

Strategies can be tested in isolation:

```go
func TestCustomLoadBalancingStrategy(t *testing.T) {
    strategy := NewCustomLoadBalancingStrategy(&CustomLBConfig{
        Algorithm: "weighted-random",
        Weight: 10,
    })
    
    cluster := &clusterv3.Cluster{Name: "test-cluster"}
    deployment := &models.APIDeployment{/* ... */}
    
    err := strategy.ConfigureCluster(cluster, deployment)
    assert.NoError(t, err)
    
    // Verify cluster configuration
    assert.Equal(t, expectedLBPolicy, cluster.LbPolicy)
}
```

## Design Patterns

This architecture uses several design patterns:

1. **Strategy Pattern** - Different algorithms for the same task (core pattern)
2. **Composite Pattern** - Compose multiple strategies together
3. **Factory Pattern** - Create strategies from configuration
4. **Template Method Pattern** - CompositeTranslator orchestration phases
5. **Chain of Responsibility** - Configuration precedence resolution

## Performance Characteristics

- **Strategy Creation**: O(1) - Factory lookup
- **Translation**: O(n) where n = number of API endpoints
- **Memory**: Minimal - strategies are lightweight and stateless
- **Thread Safety**: Yes - strategies are stateless and safe for concurrent use

## Future Enhancements

### Short Term
- [ ] Implement actual rate limiting strategies (per-user, per-IP, token bucket)
- [ ] Implement observability strategies (distributed tracing, metrics collection)
- [ ] Circuit breaker strategy
- [ ] Timeout strategy

### Medium Term
- [ ] Multi-cluster deployment strategy
- [ ] Shadow traffic strategy (mirror production traffic to test environment)
- [ ] A/B testing strategy
- [ ] Geo-based routing strategy
- [ ] External translator support (delegate to external HTTP/gRPC service)

### Long Term
- [ ] ML-based adaptive strategies
- [ ] Policy-based translation (OPA integration)
- [ ] Service mesh integration (Istio, Linkerd)
- [ ] Advanced traffic shaping (weighted routing, traffic mirroring)

## Related Documentation

- `pkg/types/types.go` - Configuration type definitions
- `internal/flowc/ir/` - Intermediate Representation (IR) for multi-API support
- `examples/translator/composite_example.go` - Complete usage example

## Summary

The xDS translator architecture provides:

✅ **Flexible** - Any combination of strategies  
✅ **Maintainable** - Each strategy is independent  
✅ **Testable** - Test strategies in isolation  
✅ **Extensible** - Add new strategies easily  
✅ **Configuration-Driven** - No code changes needed  
✅ **Production-Ready** - Supports real-world deployment patterns  

This enables FlowC to support any deployment pattern while keeping the code clean, maintainable, and extensible!
