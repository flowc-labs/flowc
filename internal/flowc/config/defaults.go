package config

import "github.com/flowc-labs/flowc/pkg/types"

// Default returns the default configuration
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			APIPort:          8080,
			XDSPort:          18000,
			ReadTimeout:      "30s",
			WriteTimeout:     "30s",
			IdleTimeout:      "60s",
			GracefulShutdown: true,
			ShutdownTimeout:  "10s",
		},
		XDS: XDSConfig{
			DefaultListenerPort: 10000,
			DefaultNodeID:       "test-envoy-node",
			SnapshotCache: SnapshotCacheConfig{
				ADS: true,
			},
			GRPC: GRPCConfig{
				KeepaliveTime:                "30s",
				KeepaliveTimeout:             "5s",
				KeepaliveMinTime:             "5s",
				KeepalivePermitWithoutStream: true,
			},
		},
		DefaultStrategy: &types.StrategyConfig{
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
				AccessLogs: &types.AccessLogsConfig{
					Enabled: false,
					Format:  "json",
				},
			},
		},
		Logging: LoggingConfig{
			Level:            "info",
			Format:           "json",
			Output:           "stdout",
			Structured:       true,
			EnableCaller:     false,
			EnableStacktrace: false,
		},
		Features: FeaturesConfig{
			ExternalTranslators: true,
			OpenAPIValidation:   true,
			Metrics:             false,
			Tracing:             false,
			RateLimiting:        false,
		},
		Store: StoreConfig{
			Backend: StoreBackendMemory,
			Kubernetes: KubernetesStoreConfig{
				Namespace: "default",
			},
		},
		Controller: ControllerConfig{
			Enabled:   false,
			Namespace: "",
			LeaderElection: LeaderElectionConfig{
				Enabled:   false,
				LeaseName: "flowc-controller",
			},
			XDS: ControllerXDSConfig{
				Address: "flowc:18000",
			},
			Envoy: EnvoyConfig{
				Image:           "envoyproxy/envoy:v1.31-latest",
				ImagePullPolicy: "IfNotPresent",
				AdminPort:       9901,
			},
			MetricsAddr: "0",
			ProbeAddr:   ":8081",
		},
	}
}
