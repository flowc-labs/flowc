package translator

import (
	"context"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/models"
)

// XDSResources represents the complete set of xDS resources
type XDSResources struct {
	Clusters  []*clusterv3.Cluster
	Endpoints []*endpointv3.ClusterLoadAssignment
	Listeners []*listenerv3.Listener
	Routes    []*routev3.RouteConfiguration
}

// Translator is the interface that all xDS translators must implement
// It converts an APIDeployment + IR into Envoy xDS resources
type Translator interface {
	// Translate converts a deployment into xDS resources
	// deployment: The persisted APIDeployment with metadata
	// irAPI: The transient IR representation (not persisted)
	// nodeID: Target Envoy node ID
	Translate(ctx context.Context, deployment *models.APIDeployment, irAPI *ir.API, nodeID string) (*XDSResources, error)

	// Name returns the name/type of this translator
	Name() string

	// Validate checks if the deployment is valid for this translator
	Validate(deployment *models.APIDeployment, irAPI *ir.API) error
}

// TranslatorOptions provides configuration options for translators
type TranslatorOptions struct {
	// DefaultListenerPort is the default port for listeners
	DefaultListenerPort uint32

	// EnableHTTPS enables HTTPS/TLS configuration
	EnableHTTPS bool

	// EnableTracing enables distributed tracing
	EnableTracing bool

	// EnableMetrics enables metrics collection
	EnableMetrics bool

	// Additional custom options
	CustomOptions map[string]any
}

// DefaultTranslatorOptions returns default translator options
func DefaultTranslatorOptions() *TranslatorOptions {
	return &TranslatorOptions{
		DefaultListenerPort: 9095,
		EnableHTTPS:         false,
		EnableTracing:       false,
		EnableMetrics:       false,
		CustomOptions:       make(map[string]any),
	}
}
