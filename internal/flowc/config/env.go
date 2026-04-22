package config

import (
	"os"
	"strconv"
)

// applyEnvOverrides applies environment variable overrides to the configuration
func applyEnvOverrides(config *Config) {
	applyServerEnvOverrides(&config.Server)
	applyXDSEnvOverrides(&config.XDS)
	applyLoggingEnvOverrides(&config.Logging)
	applyFeatureEnvOverrides(&config.Features)
}

func applyServerEnvOverrides(server *ServerConfig) {
	if val := os.Getenv("FLOWC_API_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil && port > 0 && port < 65536 {
			server.APIPort = port
		}
	}

	if val := os.Getenv("FLOWC_XDS_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil && port > 0 && port < 65536 {
			server.XDSPort = port
		}
	}

	if val := os.Getenv("FLOWC_READ_TIMEOUT"); val != "" {
		server.ReadTimeout = val
	}

	if val := os.Getenv("FLOWC_WRITE_TIMEOUT"); val != "" {
		server.WriteTimeout = val
	}

	if val := os.Getenv("FLOWC_IDLE_TIMEOUT"); val != "" {
		server.IdleTimeout = val
	}

	if val := os.Getenv("FLOWC_SHUTDOWN_TIMEOUT"); val != "" {
		server.ShutdownTimeout = val
	}

	if val := os.Getenv("FLOWC_GRACEFUL_SHUTDOWN"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			server.GracefulShutdown = enabled
		}
	}
}

func applyXDSEnvOverrides(xds *XDSConfig) {
	if val := os.Getenv("FLOWC_DEFAULT_LISTENER_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil && port > 0 && port < 65536 {
			xds.DefaultListenerPort = port
		}
	}

	if val := os.Getenv("FLOWC_DEFAULT_NODE_ID"); val != "" {
		xds.DefaultNodeID = val
	}

	if val := os.Getenv("FLOWC_XDS_ADS"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			xds.SnapshotCache.ADS = enabled
		}
	}

	if val := os.Getenv("FLOWC_GRPC_KEEPALIVE_TIME"); val != "" {
		xds.GRPC.KeepaliveTime = val
	}

	if val := os.Getenv("FLOWC_GRPC_KEEPALIVE_TIMEOUT"); val != "" {
		xds.GRPC.KeepaliveTimeout = val
	}

	if val := os.Getenv("FLOWC_GRPC_KEEPALIVE_MIN_TIME"); val != "" {
		xds.GRPC.KeepaliveMinTime = val
	}

	if val := os.Getenv("FLOWC_GRPC_KEEPALIVE_PERMIT_WITHOUT_STREAM"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			xds.GRPC.KeepalivePermitWithoutStream = enabled
		}
	}
}

func applyLoggingEnvOverrides(logging *LoggingConfig) {
	if val := os.Getenv("FLOWC_LOG_LEVEL"); val != "" {
		logging.Level = val
	}

	if val := os.Getenv("FLOWC_LOG_FORMAT"); val != "" {
		logging.Format = val
	}

	if val := os.Getenv("FLOWC_LOG_OUTPUT"); val != "" {
		logging.Output = val
	}

	if val := os.Getenv("FLOWC_LOG_STRUCTURED"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			logging.Structured = enabled
		}
	}

	if val := os.Getenv("FLOWC_LOG_ENABLE_CALLER"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			logging.EnableCaller = enabled
		}
	}

	if val := os.Getenv("FLOWC_LOG_ENABLE_STACKTRACE"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			logging.EnableStacktrace = enabled
		}
	}
}

func applyFeatureEnvOverrides(features *FeaturesConfig) {
	if val := os.Getenv("FLOWC_FEATURE_EXTERNAL_TRANSLATORS"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			features.ExternalTranslators = enabled
		}
	}

	if val := os.Getenv("FLOWC_FEATURE_OPENAPI_VALIDATION"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			features.OpenAPIValidation = enabled
		}
	}

	if val := os.Getenv("FLOWC_FEATURE_METRICS"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			features.Metrics = enabled
		}
	}

	if val := os.Getenv("FLOWC_FEATURE_TRACING"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			features.Tracing = enabled
		}
	}

	if val := os.Getenv("FLOWC_FEATURE_RATE_LIMITING"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			features.RateLimiting = enabled
		}
	}
}
