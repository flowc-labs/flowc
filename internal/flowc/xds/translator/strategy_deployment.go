package translator

import (
	"context"
	"fmt"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	"github.com/flowc-labs/flowc/internal/flowc/models"
	"github.com/flowc-labs/flowc/internal/flowc/xds/resources/cluster"
	"github.com/flowc-labs/flowc/pkg/logger"
	"github.com/flowc-labs/flowc/pkg/types"
)

// =============================================================================
// DEPLOYMENT STRATEGIES (Cluster Generation)
// These are the original translators refactored as deployment strategies
// =============================================================================

const defaultScheme = "http"

// BasicDeploymentStrategy implements basic 1:1 deployment
type BasicDeploymentStrategy struct {
	options *TranslatorOptions
	logger  *logger.EnvoyLogger
}

func NewBasicDeploymentStrategy(options *TranslatorOptions, log *logger.EnvoyLogger) *BasicDeploymentStrategy {
	if options == nil {
		options = DefaultTranslatorOptions()
	}
	return &BasicDeploymentStrategy{
		options: options,
		logger:  log,
	}
}

func (s *BasicDeploymentStrategy) Name() string {
	return "basic"
}

func (s *BasicDeploymentStrategy) Validate(deployment *models.APIDeployment) error {
	if deployment == nil {
		return fmt.Errorf("deployment is nil")
	}
	if deployment.Name == "" {
		return fmt.Errorf("deployment name is required")
	}
	if deployment.Version == "" {
		return fmt.Errorf("deployment version is required")
	}
	if deployment.Metadata.Upstream.Host == "" {
		return fmt.Errorf("upstream host is required")
	}
	if deployment.Metadata.Upstream.Port == 0 {
		return fmt.Errorf("upstream port is required")
	}
	return nil
}

func (s *BasicDeploymentStrategy) GenerateClusters(ctx context.Context, deployment *models.APIDeployment) ([]*clusterv3.Cluster, error) {
	if err := s.Validate(deployment); err != nil {
		return nil, err
	}

	upstream := deployment.Metadata.Upstream
	scheme := upstream.Scheme
	if scheme == "" {
		scheme = defaultScheme
	}

	clusterName := s.generateClusterName(deployment.Name, deployment.Version)

	return []*clusterv3.Cluster{
		cluster.CreateClusterWithScheme(clusterName, upstream.Host, upstream.Port, scheme),
	}, nil
}

func (s *BasicDeploymentStrategy) GetClusterNames(deployment *models.APIDeployment) []string {
	return []string{
		s.generateClusterName(deployment.Name, deployment.Version),
	}
}

func (s *BasicDeploymentStrategy) generateClusterName(name, version string) string {
	return fmt.Sprintf("%s-%s-cluster", name, version)
}

// =============================================================================

// CanaryDeploymentStrategy implements canary deployment
type CanaryDeploymentStrategy struct {
	canaryConfig *types.CanaryConfig
	options      *TranslatorOptions
	logger       *logger.EnvoyLogger
}

func NewCanaryDeploymentStrategy(canaryConfig *types.CanaryConfig, options *TranslatorOptions, log *logger.EnvoyLogger) *CanaryDeploymentStrategy {
	if options == nil {
		options = DefaultTranslatorOptions()
	}
	return &CanaryDeploymentStrategy{
		canaryConfig: canaryConfig,
		options:      options,
		logger:       log,
	}
}

func (s *CanaryDeploymentStrategy) Name() string {
	return "canary"
}

func (s *CanaryDeploymentStrategy) Validate(deployment *models.APIDeployment) error {
	// Basic validation
	if deployment == nil {
		return fmt.Errorf("deployment is nil")
	}

	// Canary-specific validation
	if s.canaryConfig == nil {
		return fmt.Errorf("canary configuration is required")
	}
	if s.canaryConfig.BaselineVersion == "" {
		return fmt.Errorf("baseline version is required")
	}
	if s.canaryConfig.CanaryVersion == "" {
		return fmt.Errorf("canary version is required")
	}
	if s.canaryConfig.CanaryWeight < 0 || s.canaryConfig.CanaryWeight > 100 {
		return fmt.Errorf("canary weight must be between 0 and 100")
	}

	return nil
}

func (s *CanaryDeploymentStrategy) GenerateClusters(ctx context.Context, deployment *models.APIDeployment) ([]*clusterv3.Cluster, error) {
	if err := s.Validate(deployment); err != nil {
		return nil, err
	}

	upstream := deployment.Metadata.Upstream
	scheme := upstream.Scheme
	if scheme == "" {
		scheme = defaultScheme
	}

	// Generate clusters for both baseline and canary
	baselineCluster := cluster.CreateClusterWithScheme(
		s.generateClusterName(deployment.Name, s.canaryConfig.BaselineVersion),
		upstream.Host,
		upstream.Port,
		scheme,
	)

	canaryCluster := cluster.CreateClusterWithScheme(
		s.generateClusterName(deployment.Name, s.canaryConfig.CanaryVersion),
		upstream.Host,
		upstream.Port,
		scheme,
	)

	return []*clusterv3.Cluster{baselineCluster, canaryCluster}, nil
}

func (s *CanaryDeploymentStrategy) GetClusterNames(deployment *models.APIDeployment) []string {
	return []string{
		s.generateClusterName(deployment.Name, s.canaryConfig.BaselineVersion),
		s.generateClusterName(deployment.Name, s.canaryConfig.CanaryVersion),
	}
}

func (s *CanaryDeploymentStrategy) generateClusterName(name, version string) string {
	return fmt.Sprintf("%s-%s-cluster", name, version)
}

// =============================================================================

// BlueGreenDeploymentStrategy implements blue-green deployment
type BlueGreenDeploymentStrategy struct {
	blueGreenConfig *types.BlueGreenConfig
	options         *TranslatorOptions
	logger          *logger.EnvoyLogger
}

func NewBlueGreenDeploymentStrategy(blueGreenConfig *types.BlueGreenConfig, options *TranslatorOptions, log *logger.EnvoyLogger) *BlueGreenDeploymentStrategy {
	if options == nil {
		options = DefaultTranslatorOptions()
	}
	return &BlueGreenDeploymentStrategy{
		blueGreenConfig: blueGreenConfig,
		options:         options,
		logger:          log,
	}
}

func (s *BlueGreenDeploymentStrategy) Name() string {
	return "blue-green"
}

func (s *BlueGreenDeploymentStrategy) Validate(deployment *models.APIDeployment) error {
	if deployment == nil {
		return fmt.Errorf("deployment is nil")
	}

	if s.blueGreenConfig == nil {
		return fmt.Errorf("blue-green configuration is required")
	}
	if s.blueGreenConfig.ActiveVersion == "" {
		return fmt.Errorf("active version is required")
	}
	if s.blueGreenConfig.StandbyVersion == "" {
		return fmt.Errorf("standby version is required")
	}

	return nil
}

func (s *BlueGreenDeploymentStrategy) GenerateClusters(ctx context.Context, deployment *models.APIDeployment) ([]*clusterv3.Cluster, error) {
	if err := s.Validate(deployment); err != nil {
		return nil, err
	}

	upstream := deployment.Metadata.Upstream
	scheme := upstream.Scheme
	if scheme == "" {
		scheme = defaultScheme
	}

	// Generate clusters for both active and standby
	activeCluster := cluster.CreateClusterWithScheme(
		s.generateClusterName(deployment.Name, s.blueGreenConfig.ActiveVersion, "active"),
		upstream.Host,
		upstream.Port,
		scheme,
	)

	standbyCluster := cluster.CreateClusterWithScheme(
		s.generateClusterName(deployment.Name, s.blueGreenConfig.StandbyVersion, "standby"),
		upstream.Host,
		upstream.Port,
		scheme,
	)

	return []*clusterv3.Cluster{activeCluster, standbyCluster}, nil
}

func (s *BlueGreenDeploymentStrategy) GetClusterNames(deployment *models.APIDeployment) []string {
	// Return active cluster first (primary)
	return []string{
		s.generateClusterName(deployment.Name, s.blueGreenConfig.ActiveVersion, "active"),
		s.generateClusterName(deployment.Name, s.blueGreenConfig.StandbyVersion, "standby"),
	}
}

func (s *BlueGreenDeploymentStrategy) generateClusterName(name, version, environment string) string {
	return fmt.Sprintf("%s-%s-%s-cluster", name, version, environment)
}
