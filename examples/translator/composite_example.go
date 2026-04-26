package main

import (
	"context"
	"fmt"
	"time"

	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/models"
	"github.com/flowc-labs/flowc/internal/flowc/xds/translator"
	"github.com/flowc-labs/flowc/pkg/logger"
	"github.com/flowc-labs/flowc/pkg/types"
)

// This example demonstrates the complete composite translator architecture
// showing how different strategies are composed together
func main() {
	// Create logger
	log := logger.NewDefaultEnvoyLogger()

	fmt.Println("=== FlowC Composite Translator Example ===")
	fmt.Println()

	// =========================================================================
	// SCENARIO 1: Payment API with Custom Strategy Configuration
	// =========================================================================
	fmt.Println("--- Scenario 1: Payment API (Canary + Custom Strategies) ---")

	paymentMetadata := types.FlowCMetadata{
		Name:    "payment-api",
		Version: "v2.0.0",
		Context: "/api/payments",
		Upstream: types.UpstreamConfig{
			Host:   "payment-service.internal",
			Port:   8080,
			Scheme: "https",
		},
	}

	// Create IR representation
	paymentIR := &ir.API{
		Metadata: ir.APIMetadata{
			Type:     ir.APITypeREST,
			Name:     "payment-api",
			Version:  "v2.0.0",
			BasePath: "/api/payments",
		},
		Endpoints: []ir.Endpoint{
			{
				ID:       "process-payment",
				Method:   "POST",
				Path:     ir.PathInfo{Pattern: "/process"},
				Protocol: ir.ProtocolHTTP,
			},
			{
				ID:       "process-refund",
				Method:   "POST",
				Path:     ir.PathInfo{Pattern: "/refund"},
				Protocol: ir.ProtocolHTTP,
			},
		},
	}

	// Create deployment
	paymentDeployment := &models.APIDeployment{
		ID:        "deploy-payment-001",
		Name:      paymentMetadata.Name,
		Version:   paymentMetadata.Version,
		Context:   paymentMetadata.Context,
		Status:    string(models.StatusDeployed),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  paymentMetadata,
	}

	// Configure strategies for payment API
	paymentConfig := &types.StrategyConfig{
		// Canary deployment - gradual rollout
		Deployment: &types.DeploymentStrategyConfig{
			Type: "canary",
			Canary: &types.CanaryConfig{
				BaselineVersion: "v1.0.0",
				CanaryVersion:   "v2.0.0",
				CanaryWeight:    10, // Start with 10% traffic
			},
		},
		// Exact path matching for security
		RouteMatching: &types.RouteMatchStrategyConfig{
			Type:          "exact",
			CaseSensitive: true,
		},
		// Session affinity for payment flows
		LoadBalancing: &types.LoadBalancingStrategyConfig{
			Type:       "consistent-hash",
			HashOn:     "header",
			HeaderName: "x-session-id",
		},
		// NO retry for payments (avoid double-charging!)
		Retry: &types.RetryStrategyConfig{
			Type: "none",
		},
	}

	nodeID := "envoy-gateway-1"

	// Create translator using config resolver and factory
	paymentTranslator, err := createCompositeTranslator(paymentConfig, paymentDeployment, log)
	if err != nil {
		fmt.Printf("❌ Failed to create translator: %v\n", err)
		return
	}

	// Translate
	paymentResources, err := paymentTranslator.Translate(context.Background(), paymentDeployment, paymentIR, nodeID)
	if err != nil {
		fmt.Printf("❌ Translation failed: %v\n", err)
		return
	}

	printTranslationResults("Payment API", paymentTranslator, paymentResources)
	fmt.Println()

	// =========================================================================
	// SCENARIO 2: User API with Default Strategies
	// =========================================================================
	fmt.Println("--- Scenario 2: User API (Basic + Aggressive Retry) ---")

	userMetadata := types.FlowCMetadata{
		Name:    "user-api",
		Version: "v1.0.0",
		Context: "/api/users",
		Upstream: types.UpstreamConfig{
			Host:   "user-service.internal",
			Port:   8080,
			Scheme: "http",
		},
	}

	userIR := &ir.API{
		Metadata: ir.APIMetadata{
			Type:     ir.APITypeREST,
			Name:     "user-api",
			Version:  "v1.0.0",
			BasePath: "/api/users",
		},
		Endpoints: []ir.Endpoint{
			{
				ID:       "list-users",
				Method:   "GET",
				Path:     ir.PathInfo{Pattern: "/list"},
				Protocol: ir.ProtocolHTTP,
			},
			{
				ID:       "get-user",
				Method:   "GET",
				Path:     ir.PathInfo{Pattern: "/{id}"},
				Protocol: ir.ProtocolHTTP,
			},
			{
				ID:       "update-user",
				Method:   "PUT",
				Path:     ir.PathInfo{Pattern: "/{id}"},
				Protocol: ir.ProtocolHTTP,
			},
			{
				ID:       "delete-user",
				Method:   "DELETE",
				Path:     ir.PathInfo{Pattern: "/{id}"},
				Protocol: ir.ProtocolHTTP,
			},
		},
	}

	userDeployment := &models.APIDeployment{
		ID:        "deploy-user-001",
		Name:      userMetadata.Name,
		Version:   userMetadata.Version,
		Context:   userMetadata.Context,
		Status:    string(models.StatusDeployed),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  userMetadata,
	}

	// Simpler config for user API
	userConfig := &types.StrategyConfig{
		// Basic deployment
		Deployment: &types.DeploymentStrategyConfig{
			Type: "basic",
		},
		// Prefix matching (default)
		RouteMatching: &types.RouteMatchStrategyConfig{
			Type: "prefix",
		},
		// Round-robin LB
		LoadBalancing: &types.LoadBalancingStrategyConfig{
			Type: "round-robin",
		},
		// Aggressive retry OK for read-heavy API
		Retry: &types.RetryStrategyConfig{
			Type: "aggressive",
		},
	}

	userTranslator, err := createCompositeTranslator(userConfig, userDeployment, log)
	if err != nil {
		fmt.Printf("❌ Failed to create translator: %v\n", err)
		return
	}

	userResources, err := userTranslator.Translate(context.Background(), userDeployment, userIR, nodeID)
	if err != nil {
		fmt.Printf("❌ Translation failed: %v\n", err)
		return
	}

	printTranslationResults("User API", userTranslator, userResources)
	fmt.Println()

	// =========================================================================
	// SCENARIO 3: Order API with Blue-Green Deployment
	// =========================================================================
	fmt.Println("--- Scenario 3: Order API (Blue-Green + Conservative Retry) ---")

	orderMetadata := types.FlowCMetadata{
		Name:    "order-api",
		Version: "v2.0.0",
		Context: "/api/orders",
		Upstream: types.UpstreamConfig{
			Host:   "order-service.internal",
			Port:   8080,
			Scheme: "http",
		},
	}

	orderIR := &ir.API{
		Metadata: ir.APIMetadata{
			Type:     ir.APITypeREST,
			Name:     "order-api",
			Version:  "v2.0.0",
			BasePath: "/api/orders",
		},
		Endpoints: []ir.Endpoint{
			{
				ID:       "create-order",
				Method:   "POST",
				Path:     ir.PathInfo{Pattern: "/create"},
				Protocol: ir.ProtocolHTTP,
			},
			{
				ID:       "get-order",
				Method:   "GET",
				Path:     ir.PathInfo{Pattern: "/{id}"},
				Protocol: ir.ProtocolHTTP,
			},
		},
	}

	orderDeployment := &models.APIDeployment{
		ID:        "deploy-order-001",
		Name:      orderMetadata.Name,
		Version:   orderMetadata.Version,
		Context:   orderMetadata.Context,
		Status:    string(models.StatusDeployed),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  orderMetadata,
	}

	orderConfig := &types.StrategyConfig{
		// Blue-green deployment
		Deployment: &types.DeploymentStrategyConfig{
			Type: "blue-green",
			BlueGreen: &types.BlueGreenConfig{
				ActiveVersion:  "v1.0.0",
				StandbyVersion: "v2.0.0",
				AutoPromote:    false,
			},
		},
		RouteMatching: &types.RouteMatchStrategyConfig{
			Type: "prefix",
		},
		LoadBalancing: &types.LoadBalancingStrategyConfig{
			Type:        "least-request",
			ChoiceCount: 2,
		},
		Retry: &types.RetryStrategyConfig{
			Type: "conservative",
		},
	}

	orderTranslator, err := createCompositeTranslator(orderConfig, orderDeployment, log)
	if err != nil {
		fmt.Printf("❌ Failed to create translator: %v\n", err)
		return
	}

	orderResources, err := orderTranslator.Translate(context.Background(), orderDeployment, orderIR, nodeID)
	if err != nil {
		fmt.Printf("❌ Translation failed: %v\n", err)
		return
	}

	printTranslationResults("Order API", orderTranslator, orderResources)
	fmt.Println()

	// =========================================================================
	// SUMMARY
	// =========================================================================
	fmt.Println("=== Summary ===")
	fmt.Println("✅ Successfully demonstrated composite translator architecture")
	fmt.Println("✅ Each API uses different strategy combinations:")
	fmt.Println("   • Payment: Canary + Exact Match + Consistent Hash + No Retry")
	fmt.Println("   • User:    Basic + Prefix Match + Round Robin + Aggressive Retry")
	fmt.Println("   • Order:   Blue-Green + Prefix Match + Least Request + Conservative Retry")
	fmt.Println("\n✅ All strategies are independently configurable and composable!")
}

// createCompositeTranslator creates a composite translator from configuration
func createCompositeTranslator(
	config *types.StrategyConfig,
	deployment *models.APIDeployment,
	log *logger.EnvoyLogger,
) (*translator.CompositeTranslator, error) {
	// Resolve configuration (apply gateway defaults if needed)
	// For this example, we're using API-specific config directly
	resolver := translator.NewConfigResolver(nil, nil, log)
	resolvedConfig := resolver.Resolve(config)

	// Create strategy factory
	factory := translator.NewStrategyFactory(translator.DefaultTranslatorOptions(), log)

	// Create strategy set from resolved config
	strategies, err := factory.CreateStrategySet(resolvedConfig, deployment)
	if err != nil {
		return nil, fmt.Errorf("failed to create strategy set: %w", err)
	}

	// Create composite translator
	return translator.NewCompositeTranslator(strategies, translator.DefaultTranslatorOptions(), log)
}

// printTranslationResults prints the results in a nice format
func printTranslationResults(apiName string, t *translator.CompositeTranslator, resources *translator.XDSResources) {
	fmt.Printf("✅ %s Translation Complete\n", apiName)
	fmt.Printf("   Translator: %s\n", t.Name())
	fmt.Printf("   Resources Generated:\n")
	fmt.Printf("     • Clusters:  %d\n", len(resources.Clusters))
	for _, cluster := range resources.Clusters {
		fmt.Printf("       - %s\n", cluster.Name)
	}
	fmt.Printf("     • Routes:    %d\n", len(resources.Routes))
	for _, route := range resources.Routes {
		fmt.Printf("       - %s (%d virtual hosts)\n", route.Name, len(route.VirtualHosts))
	}
	fmt.Printf("     • Listeners: %d\n", len(resources.Listeners))
	fmt.Printf("     • Endpoints: %d\n", len(resources.Endpoints))
}
