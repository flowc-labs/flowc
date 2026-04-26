package translator

import (
	"time"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/flowc-labs/flowc/internal/flowc/models"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// =============================================================================
// RETRY STRATEGIES
// =============================================================================

// ConservativeRetryStrategy implements conservative retry policy
// Suitable for most APIs - retry only on clear failures
type ConservativeRetryStrategy struct {
	maxRetries    uint32
	retryOn       string
	perTryTimeout time.Duration
}

func NewConservativeRetryStrategy() *ConservativeRetryStrategy {
	return &ConservativeRetryStrategy{
		maxRetries:    1,
		retryOn:       "5xx,reset",
		perTryTimeout: 5 * time.Second,
	}
}

func (s *ConservativeRetryStrategy) ConfigureRetry(route *routev3.Route, deployment *models.APIDeployment) error {
	routeAction, ok := route.Action.(*routev3.Route_Route)
	if !ok {
		return nil // Not a route action
	}

	routeAction.Route.RetryPolicy = &routev3.RetryPolicy{
		RetryOn:       s.retryOn,
		NumRetries:    wrapperspb.UInt32(s.maxRetries),
		PerTryTimeout: durationpb.New(s.perTryTimeout),
	}

	return nil
}

func (s *ConservativeRetryStrategy) Name() string {
	return "conservative"
}

// AggressiveRetryStrategy implements aggressive retry policy
// Suitable for idempotent read operations
type AggressiveRetryStrategy struct {
	maxRetries    uint32
	retryOn       string
	perTryTimeout time.Duration
}

func NewAggressiveRetryStrategy() *AggressiveRetryStrategy {
	return &AggressiveRetryStrategy{
		maxRetries:    3,
		retryOn:       "5xx,reset,connect-failure,refused-stream",
		perTryTimeout: 2 * time.Second,
	}
}

func (s *AggressiveRetryStrategy) ConfigureRetry(route *routev3.Route, deployment *models.APIDeployment) error {
	routeAction, ok := route.Action.(*routev3.Route_Route)
	if !ok {
		return nil
	}

	routeAction.Route.RetryPolicy = &routev3.RetryPolicy{
		RetryOn: s.retryOn,
		NumRetries: &wrapperspb.UInt32Value{
			Value: s.maxRetries,
		},
		PerTryTimeout: durationpb.New(s.perTryTimeout),
		RetryHostPredicate: []*routev3.RetryPolicy_RetryHostPredicate{
			{
				Name: "envoy.retry_host_predicates.previous_hosts",
			},
		},
	}

	return nil
}

func (s *AggressiveRetryStrategy) Name() string {
	return "aggressive"
}

// CustomRetryStrategy allows full customization of retry policy
type CustomRetryStrategy struct {
	maxRetries           uint32
	retryOn              string
	perTryTimeout        time.Duration
	retriableStatusCodes []uint32
	budgetPercent        float64
}

func NewCustomRetryStrategy(maxRetries uint32, retryOn string, perTryTimeout time.Duration) *CustomRetryStrategy {
	return &CustomRetryStrategy{
		maxRetries:    maxRetries,
		retryOn:       retryOn,
		perTryTimeout: perTryTimeout,
		budgetPercent: 20.0, // Default: allow 20% of requests to be retries
	}
}

func (s *CustomRetryStrategy) WithRetriableStatusCodes(codes []uint32) *CustomRetryStrategy {
	s.retriableStatusCodes = codes
	return s
}

func (s *CustomRetryStrategy) WithBudgetPercent(percent float64) *CustomRetryStrategy {
	s.budgetPercent = percent
	return s
}

func (s *CustomRetryStrategy) ConfigureRetry(route *routev3.Route, deployment *models.APIDeployment) error {
	routeAction, ok := route.Action.(*routev3.Route_Route)
	if !ok {
		return nil
	}

	retryPolicy := &routev3.RetryPolicy{
		RetryOn: s.retryOn,
		NumRetries: &wrapperspb.UInt32Value{
			Value: s.maxRetries,
		},
		PerTryTimeout: durationpb.New(s.perTryTimeout),
	}

	// Add retriable status codes if specified
	if len(s.retriableStatusCodes) > 0 {
		retryPolicy.RetriableStatusCodes = s.retriableStatusCodes
	}

	// Add retry budget
	if s.budgetPercent > 0 {
		retryPolicy.RetryBackOff = &routev3.RetryPolicy_RetryBackOff{
			BaseInterval: durationpb.New(25 * time.Millisecond),
			MaxInterval:  durationpb.New(250 * time.Millisecond),
		}
	}

	routeAction.Route.RetryPolicy = retryPolicy

	return nil
}

func (s *CustomRetryStrategy) Name() string {
	return "custom"
}
