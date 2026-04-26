package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/flowc-labs/flowc/pkg/types"
	"gopkg.in/yaml.v3"
)

// Config represents the complete flowc control plane configuration
type Config struct {
	// Server configuration
	Server ServerConfig `yaml:"server" json:"server"`

	// XDS configuration
	XDS XDSConfig `yaml:"xds" json:"xds"`

	// Default strategy configuration
	DefaultStrategy *types.StrategyConfig `yaml:"default_strategy" json:"default_strategy"`

	// Logging configuration
	Logging LoggingConfig `yaml:"logging" json:"logging"`

	// Feature flags
	Features FeaturesConfig `yaml:"features" json:"features"`

	// Store backend selection and per-backend settings
	Store StoreConfig `yaml:"store" json:"store"`

	// Controller configuration (K8s CRD controller)
	Controller ControllerConfig `yaml:"controller" json:"controller"`
}

// StoreConfig selects the source-of-truth backend and carries per-backend
// settings. The rest of the binary is backend-agnostic.
type StoreConfig struct {
	// Backend is one of: "memory", "kubernetes". Defaults to "memory".
	Backend string `yaml:"backend" json:"backend"`

	// Kubernetes contains settings applied when Backend == "kubernetes".
	Kubernetes KubernetesStoreConfig `yaml:"kubernetes" json:"kubernetes"`
}

// KubernetesStoreConfig configures the K8s-backed store.
type KubernetesStoreConfig struct {
	// Namespace is the namespace the store reads and writes in. Defaults to "default".
	Namespace string `yaml:"namespace" json:"namespace"`

	// Kubeconfig is an optional explicit kubeconfig path. When empty, the
	// standard in-cluster + KUBECONFIG discovery is used.
	Kubeconfig string `yaml:"kubeconfig" json:"kubeconfig"`
}

// ControllerConfig gates the in-process K8s CRD controller and supplies the
// knobs its reconcilers need. Only meaningful when store.backend=="kubernetes".
type ControllerConfig struct {
	// Enabled turns the K8s controllers on. Defaults to false.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Namespace is where provisioned Envoy resources (Deployment, Service,
	// ConfigMap) are created. Defaults to store.kubernetes.namespace.
	Namespace string `yaml:"namespace" json:"namespace"`

	// LeaderElection controls whether reconcilers are gated by a lease.
	// Mode 3 (single replica) leaves this off; Mode 4 (HA) turns it on.
	LeaderElection LeaderElectionConfig `yaml:"leader_election" json:"leader_election"`

	// XDS contains settings the controller bakes into the Envoy bootstrap
	// so provisioned proxies know where to fetch config.
	XDS ControllerXDSConfig `yaml:"xds" json:"xds"`

	// Envoy describes the Envoy proxy image and runtime settings used when
	// provisioning gateways.
	Envoy EnvoyConfig `yaml:"envoy" json:"envoy"`

	// MetricsAddr is the :port the controller-runtime metrics endpoint binds
	// to. Empty disables it.
	MetricsAddr string `yaml:"metrics_addr" json:"metrics_addr"`

	// ProbeAddr is the :port the controller-runtime health probe endpoint
	// binds to. Empty disables it.
	ProbeAddr string `yaml:"probe_addr" json:"probe_addr"`
}

// LeaderElectionConfig configures the controller-runtime lease.
type LeaderElectionConfig struct {
	// Enabled turns leader election on. Default false (Mode 3).
	Enabled bool `yaml:"enabled" json:"enabled"`
	// LeaseName is the name of the coordination Lease.
	LeaseName string `yaml:"lease_name" json:"lease_name"`
	// Namespace holds the Lease. Defaults to the controller namespace.
	Namespace string `yaml:"namespace" json:"namespace"`
}

// ControllerXDSConfig supplies xDS target information for provisioned Envoy
// proxies. Address is a host:port reachable from the data plane pods.
type ControllerXDSConfig struct {
	// Address Envoy dials for xDS (e.g. "flowc.flowc-system.svc.cluster.local:18000").
	Address string `yaml:"address" json:"address"`
	// UseTLS switches the Envoy xDS cluster to TLS. Defaults to false (plaintext).
	UseTLS bool `yaml:"use_tls" json:"use_tls"`
}

// EnvoyConfig describes the Envoy image and admin port used for provisioned
// gateway data planes.
type EnvoyConfig struct {
	// Image is the Envoy container image reference.
	Image string `yaml:"image" json:"image"`
	// ImagePullPolicy overrides the default Always/IfNotPresent behavior.
	ImagePullPolicy string `yaml:"image_pull_policy" json:"image_pull_policy"`
	// AdminPort is the Envoy admin endpoint port (default 9901).
	AdminPort int32 `yaml:"admin_port" json:"admin_port"`
}

// Store backend constants.
const (
	StoreBackendMemory     = "memory"
	StoreBackendKubernetes = "kubernetes"
)

// ServerConfig contains API server configuration
type ServerConfig struct {
	// API server port
	APIPort int `yaml:"api_port" json:"api_port"`

	// XDS server port
	XDSPort int `yaml:"xds_port" json:"xds_port"`

	// Read timeout for HTTP requests
	ReadTimeout string `yaml:"read_timeout" json:"read_timeout"`

	// Write timeout for HTTP responses
	WriteTimeout string `yaml:"write_timeout" json:"write_timeout"`

	// Idle timeout for HTTP connections
	IdleTimeout string `yaml:"idle_timeout" json:"idle_timeout"`

	// Enable graceful shutdown
	GracefulShutdown bool `yaml:"graceful_shutdown" json:"graceful_shutdown"`

	// Graceful shutdown timeout
	ShutdownTimeout string `yaml:"shutdown_timeout" json:"shutdown_timeout"`
}

// XDSConfig contains XDS server configuration
type XDSConfig struct {
	// Default listener port for Envoy proxies
	DefaultListenerPort int `yaml:"default_listener_port" json:"default_listener_port"`

	// Default node ID for testing
	DefaultNodeID string `yaml:"default_node_id" json:"default_node_id"`

	// Snapshot cache configuration
	SnapshotCache SnapshotCacheConfig `yaml:"snapshot_cache" json:"snapshot_cache"`

	// gRPC server configuration
	GRPC GRPCConfig `yaml:"grpc" json:"grpc"`
}

// SnapshotCacheConfig contains snapshot cache settings
type SnapshotCacheConfig struct {
	// Enable Aggregated Discovery Service
	ADS bool `yaml:"ads" json:"ads"`
}

// GRPCConfig contains gRPC server settings
type GRPCConfig struct {
	// Keepalive time
	KeepaliveTime string `yaml:"keepalive_time" json:"keepalive_time"`

	// Keepalive timeout
	KeepaliveTimeout string `yaml:"keepalive_timeout" json:"keepalive_timeout"`

	// Minimum time between keepalive pings
	KeepaliveMinTime string `yaml:"keepalive_min_time" json:"keepalive_min_time"`

	// Allow keepalive pings without active streams
	KeepalivePermitWithoutStream bool `yaml:"keepalive_permit_without_stream" json:"keepalive_permit_without_stream"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	// Log level: debug, info, warn, error
	Level string `yaml:"level" json:"level"`

	// Log format: json, text
	Format string `yaml:"format" json:"format"`

	// Output: stdout, stderr, or file path
	Output string `yaml:"output" json:"output"`

	// Enable structured logging
	Structured bool `yaml:"structured" json:"structured"`

	// Enable caller information in logs
	EnableCaller bool `yaml:"enable_caller" json:"enable_caller"`

	// Enable stack traces for errors
	EnableStacktrace bool `yaml:"enable_stacktrace" json:"enable_stacktrace"`
}

// FeaturesConfig contains feature flags
type FeaturesConfig struct {
	// Enable external translator support
	ExternalTranslators bool `yaml:"external_translators" json:"external_translators"`

	// Enable OpenAPI validation
	OpenAPIValidation bool `yaml:"openapi_validation" json:"openapi_validation"`

	// Enable metrics collection
	Metrics bool `yaml:"metrics" json:"metrics"`

	// Enable distributed tracing
	Tracing bool `yaml:"tracing" json:"tracing"`

	// Enable rate limiting
	RateLimiting bool `yaml:"rate_limiting" json:"rate_limiting"`
}

// Load loads configuration from a YAML file
func Load(configPath string) (*Config, error) {
	// If config path is empty, try default locations
	if configPath == "" {
		configPath = findConfigFile()
	}
	// If still no config file, return defaults
	if configPath == "" {
		return Default(), nil
	}

	// Read the config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	// Parse YAML
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	// Merge with defaults
	config = *mergeWithDefaults(&config)

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Apply environment variable overrides
	applyEnvOverrides(&config)

	return &config, nil
}

// LoadFromData loads configuration from byte data
func LoadFromData(data []byte) (*Config, error) {
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config data: %w", err)
	}

	// Merge with defaults
	config = *mergeWithDefaults(&config)

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Apply environment variable overrides
	applyEnvOverrides(&config)

	return &config, nil
}

// findConfigFile looks for config file in common locations
func findConfigFile() string {
	// List of paths to check (in order)
	paths := []string{
		"flowc-config.yaml",
		"flowc-config.yml",
		"config/flowc-config.yaml",
		"config/flowc-config.yml",
		"/etc/flowc/config.yaml",
		"/etc/flowc/config.yml",
	}

	// Check if FLOWC_CONFIG environment variable is set
	if envPath := os.Getenv("FLOWC_CONFIG"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}
	}

	// Check each path
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return ""
}

// mergeWithDefaults merges the provided config with default values
func mergeWithDefaults(config *Config) *Config {
	defaults := Default()

	// Merge server config
	if config.Server.APIPort == 0 {
		config.Server.APIPort = defaults.Server.APIPort
	}
	if config.Server.XDSPort == 0 {
		config.Server.XDSPort = defaults.Server.XDSPort
	}
	if config.Server.ReadTimeout == "" {
		config.Server.ReadTimeout = defaults.Server.ReadTimeout
	}
	if config.Server.WriteTimeout == "" {
		config.Server.WriteTimeout = defaults.Server.WriteTimeout
	}
	if config.Server.IdleTimeout == "" {
		config.Server.IdleTimeout = defaults.Server.IdleTimeout
	}
	if config.Server.ShutdownTimeout == "" {
		config.Server.ShutdownTimeout = defaults.Server.ShutdownTimeout
	}
	// GracefulShutdown defaults to true
	if !config.Server.GracefulShutdown {
		config.Server.GracefulShutdown = defaults.Server.GracefulShutdown
	}

	// Merge XDS config
	if config.XDS.DefaultListenerPort == 0 {
		config.XDS.DefaultListenerPort = defaults.XDS.DefaultListenerPort
	}
	if config.XDS.DefaultNodeID == "" {
		config.XDS.DefaultNodeID = defaults.XDS.DefaultNodeID
	}
	if config.XDS.GRPC.KeepaliveTime == "" {
		config.XDS.GRPC.KeepaliveTime = defaults.XDS.GRPC.KeepaliveTime
	}
	if config.XDS.GRPC.KeepaliveTimeout == "" {
		config.XDS.GRPC.KeepaliveTimeout = defaults.XDS.GRPC.KeepaliveTimeout
	}
	if config.XDS.GRPC.KeepaliveMinTime == "" {
		config.XDS.GRPC.KeepaliveMinTime = defaults.XDS.GRPC.KeepaliveMinTime
	}
	// Snapshot cache ADS defaults to true
	if !config.XDS.SnapshotCache.ADS {
		config.XDS.SnapshotCache.ADS = defaults.XDS.SnapshotCache.ADS
	}
	// GRPC keepalive permit without stream defaults to true
	if !config.XDS.GRPC.KeepalivePermitWithoutStream {
		config.XDS.GRPC.KeepalivePermitWithoutStream = defaults.XDS.GRPC.KeepalivePermitWithoutStream
	}

	// Merge logging config
	if config.Logging.Level == "" {
		config.Logging.Level = defaults.Logging.Level
	}
	if config.Logging.Format == "" {
		config.Logging.Format = defaults.Logging.Format
	}
	if config.Logging.Output == "" {
		config.Logging.Output = defaults.Logging.Output
	}

	// Features defaults - keep as is (false by default is fine)
	// Users explicitly enable features they want

	// Store defaults
	if config.Store.Backend == "" {
		config.Store.Backend = defaults.Store.Backend
	}
	if config.Store.Kubernetes.Namespace == "" {
		config.Store.Kubernetes.Namespace = defaults.Store.Kubernetes.Namespace
	}

	// Controller defaults
	if config.Controller.Namespace == "" {
		config.Controller.Namespace = config.Store.Kubernetes.Namespace
	}
	if config.Controller.LeaderElection.LeaseName == "" {
		config.Controller.LeaderElection.LeaseName = defaults.Controller.LeaderElection.LeaseName
	}
	if config.Controller.LeaderElection.Namespace == "" {
		config.Controller.LeaderElection.Namespace = config.Controller.Namespace
	}
	if config.Controller.XDS.Address == "" {
		config.Controller.XDS.Address = defaults.Controller.XDS.Address
	}
	if config.Controller.Envoy.Image == "" {
		config.Controller.Envoy.Image = defaults.Controller.Envoy.Image
	}
	if config.Controller.Envoy.ImagePullPolicy == "" {
		config.Controller.Envoy.ImagePullPolicy = defaults.Controller.Envoy.ImagePullPolicy
	}
	if config.Controller.Envoy.AdminPort == 0 {
		config.Controller.Envoy.AdminPort = defaults.Controller.Envoy.AdminPort
	}
	if config.Controller.MetricsAddr == "" {
		config.Controller.MetricsAddr = defaults.Controller.MetricsAddr
	}
	if config.Controller.ProbeAddr == "" {
		config.Controller.ProbeAddr = defaults.Controller.ProbeAddr
	}

	return config
}

// GetServerReadTimeout returns parsed read timeout
func (c *Config) GetServerReadTimeout() time.Duration {
	duration, err := time.ParseDuration(c.Server.ReadTimeout)
	if err != nil {
		return 30 * time.Second // fallback
	}
	return duration
}

// GetServerWriteTimeout returns parsed write timeout
func (c *Config) GetServerWriteTimeout() time.Duration {
	duration, err := time.ParseDuration(c.Server.WriteTimeout)
	if err != nil {
		return 30 * time.Second // fallback
	}
	return duration
}

// GetServerIdleTimeout returns parsed idle timeout
func (c *Config) GetServerIdleTimeout() time.Duration {
	duration, err := time.ParseDuration(c.Server.IdleTimeout)
	if err != nil {
		return 60 * time.Second // fallback
	}
	return duration
}

// GetShutdownTimeout returns parsed shutdown timeout
func (c *Config) GetShutdownTimeout() time.Duration {
	duration, err := time.ParseDuration(c.Server.ShutdownTimeout)
	if err != nil {
		return 10 * time.Second // fallback
	}
	return duration
}

// GetKeepaliveTime returns parsed keepalive time
func (c *Config) GetKeepaliveTime() time.Duration {
	duration, err := time.ParseDuration(c.XDS.GRPC.KeepaliveTime)
	if err != nil {
		return 30 * time.Second // fallback
	}
	return duration
}

// GetKeepaliveTimeout returns parsed keepalive timeout
func (c *Config) GetKeepaliveTimeout() time.Duration {
	duration, err := time.ParseDuration(c.XDS.GRPC.KeepaliveTimeout)
	if err != nil {
		return 5 * time.Second // fallback
	}
	return duration
}

// GetKeepaliveMinTime returns parsed keepalive minimum time
func (c *Config) GetKeepaliveMinTime() time.Duration {
	duration, err := time.ParseDuration(c.XDS.GRPC.KeepaliveMinTime)
	if err != nil {
		return 5 * time.Second // fallback
	}
	return duration
}

// SaveToFile saves the configuration to a YAML file
func (c *Config) SaveToFile(path string) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Marshal to YAML
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", path, err)
	}

	return nil
}
