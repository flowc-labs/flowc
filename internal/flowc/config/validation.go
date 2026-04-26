package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate server config
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("server config: %w", err)
	}

	// Validate XDS config
	if err := c.XDS.Validate(); err != nil {
		return fmt.Errorf("xds config: %w", err)
	}

	// Validate logging config
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("logging config: %w", err)
	}

	// Validate store config
	if err := c.Store.Validate(); err != nil {
		return fmt.Errorf("store config: %w", err)
	}

	return nil
}

// Validate validates store configuration.
func (s *StoreConfig) Validate() error {
	validBackends := []string{StoreBackendMemory, StoreBackendKubernetes}
	if !contains(validBackends, s.Backend) {
		return fmt.Errorf("invalid backend: %q (must be one of: %s)", s.Backend, strings.Join(validBackends, ", "))
	}
	return nil
}

// Validate validates server configuration
func (s *ServerConfig) Validate() error {
	if s.APIPort < 1 || s.APIPort > 65535 {
		return fmt.Errorf("invalid api_port: %d (must be between 1-65535)", s.APIPort)
	}

	if s.XDSPort < 1 || s.XDSPort > 65535 {
		return fmt.Errorf("invalid xds_port: %d (must be between 1-65535)", s.XDSPort)
	}

	if s.APIPort == s.XDSPort {
		return fmt.Errorf("api_port and xds_port cannot be the same: %d", s.APIPort)
	}

	// Validate timeouts
	if err := validateDuration(s.ReadTimeout, "read_timeout"); err != nil {
		return err
	}
	if err := validateDuration(s.WriteTimeout, "write_timeout"); err != nil {
		return err
	}
	if err := validateDuration(s.IdleTimeout, "idle_timeout"); err != nil {
		return err
	}
	if err := validateDuration(s.ShutdownTimeout, "shutdown_timeout"); err != nil {
		return err
	}

	return nil
}

// Validate validates XDS configuration
func (x *XDSConfig) Validate() error {
	if x.DefaultListenerPort < 1 || x.DefaultListenerPort > 65535 {
		return fmt.Errorf("invalid default_listener_port: %d (must be between 1-65535)", x.DefaultListenerPort)
	}

	if x.DefaultNodeID == "" {
		return fmt.Errorf("default_node_id cannot be empty")
	}

	// Validate gRPC config
	if err := x.GRPC.Validate(); err != nil {
		return fmt.Errorf("grpc: %w", err)
	}

	return nil
}

// Validate validates gRPC configuration
func (g *GRPCConfig) Validate() error {
	if err := validateDuration(g.KeepaliveTime, "keepalive_time"); err != nil {
		return err
	}
	if err := validateDuration(g.KeepaliveTimeout, "keepalive_timeout"); err != nil {
		return err
	}
	if err := validateDuration(g.KeepaliveMinTime, "keepalive_min_time"); err != nil {
		return err
	}

	return nil
}

// Validate validates logging configuration
func (l *LoggingConfig) Validate() error {
	// Validate log level
	validLevels := []string{"debug", "info", "warn", "error"}
	level := strings.ToLower(l.Level)
	if !contains(validLevels, level) {
		return fmt.Errorf("invalid log level: %s (must be one of: %s)", l.Level, strings.Join(validLevels, ", "))
	}

	// Validate log format
	validFormats := []string{"json", "text"}
	format := strings.ToLower(l.Format)
	if !contains(validFormats, format) {
		return fmt.Errorf("invalid log format: %s (must be one of: %s)", l.Format, strings.Join(validFormats, ", "))
	}

	// Validate output (must be stdout, stderr, or a valid file path)
	if l.Output == "" {
		return fmt.Errorf("log output cannot be empty")
	}

	return nil
}

// validateDuration validates a duration string
func validateDuration(duration string, fieldName string) error {
	if duration == "" {
		return fmt.Errorf("%s cannot be empty", fieldName)
	}

	_, err := time.ParseDuration(duration)
	if err != nil {
		return fmt.Errorf("invalid %s: %s (must be a valid duration like '30s', '5m', '1h')", fieldName, duration)
	}

	return nil
}

// contains checks if a slice contains a string
func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}
