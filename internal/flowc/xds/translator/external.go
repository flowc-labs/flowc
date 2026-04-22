package translator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/server/models"
	"github.com/flowc-labs/flowc/pkg/logger"
	"google.golang.org/protobuf/encoding/protojson"
)

// ExternalTranslator delegates xDS generation to an external HTTP/gRPC service
// This allows for custom, user-defined translation logic outside of FlowC
type ExternalTranslator struct {
	// endpoint is the URL of the external translation service
	endpoint string

	// httpClient for making requests
	httpClient *http.Client

	// options for the translator
	options *TranslatorOptions

	// logger
	logger *logger.EnvoyLogger

	// timeout for requests
	timeout time.Duration
}

// ExternalTranslatorConfig configures the external translator
type ExternalTranslatorConfig struct {
	// Endpoint URL of the external translation service
	Endpoint string

	// Timeout for requests (default: 30s)
	Timeout time.Duration

	// Headers to include in requests
	Headers map[string]string

	// TLS configuration (for HTTPS)
	TLSConfig any // TODO: Add proper TLS config

	// Authentication token (if required)
	AuthToken string
}

// ExternalTranslationRequest is sent to the external service
type ExternalTranslationRequest struct {
	// DeploymentID for tracking
	DeploymentID string `json:"deployment_id"`

	// Metadata from FlowC
	Metadata any `json:"metadata"`

	// IR representation of the API
	IR any `json:"ir"`

	// NodeID for xDS targeting
	NodeID string `json:"node_id"`

	// Options passed to the translator
	Options *TranslatorOptions `json:"options,omitempty"`
}

// ExternalTranslationResponse is received from the external service
type ExternalTranslationResponse struct {
	// Success indicates if translation was successful
	Success bool `json:"success"`

	// Error message if translation failed
	Error string `json:"error,omitempty"`

	// Resources contains the generated xDS resources as JSON
	Resources *ExternalXDSResources `json:"resources,omitempty"`
}

// ExternalXDSResources represents xDS resources in JSON format
// The external service returns Envoy protos serialized as JSON
type ExternalXDSResources struct {
	Clusters  []json.RawMessage `json:"clusters,omitempty"`
	Endpoints []json.RawMessage `json:"endpoints,omitempty"`
	Listeners []json.RawMessage `json:"listeners,omitempty"`
	Routes    []json.RawMessage `json:"routes,omitempty"`
}

// NewExternalTranslator creates a new external translator
func NewExternalTranslator(config *ExternalTranslatorConfig, options *TranslatorOptions, log *logger.EnvoyLogger) (*ExternalTranslator, error) {
	if config == nil {
		return nil, fmt.Errorf("external translator config is required")
	}
	if config.Endpoint == "" {
		return nil, fmt.Errorf("external translator endpoint is required")
	}
	if options == nil {
		options = DefaultTranslatorOptions()
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &ExternalTranslator{
		endpoint: config.Endpoint,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		options: options,
		logger:  log,
		timeout: timeout,
	}, nil
}

// Name returns the translator name
func (t *ExternalTranslator) Name() string {
	return "external"
}

// Validate checks if the deployment is valid
func (t *ExternalTranslator) Validate(deployment *models.APIDeployment, irAPI *ir.API) error {
	if deployment == nil {
		return fmt.Errorf("deployment is nil")
	}
	return nil
}

// Translate sends the deployment to the external service and receives xDS resources
func (t *ExternalTranslator) Translate(ctx context.Context, deployment *models.APIDeployment, irAPI *ir.API, nodeID string) (*XDSResources, error) {
	if err := t.Validate(deployment, irAPI); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	if t.logger != nil {
		t.logger.WithFields(map[string]any{
			"translator": t.Name(),
			"endpoint":   t.endpoint,
			"deployment": deployment.ID,
		}).Info("Delegating xDS translation to external service")
	}

	// Prepare request
	req := &ExternalTranslationRequest{
		DeploymentID: deployment.ID,
		Metadata:     deployment.Metadata,
		IR:           irAPI,
		NodeID:       nodeID,
		Options:      t.options,
	}

	// Call external service
	response, err := t.callExternalService(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("external service call failed: %w", err)
	}

	if !response.Success {
		return nil, fmt.Errorf("external translation failed: %s", response.Error)
	}

	// Parse the response into xDS resources
	resources, err := t.parseExternalResources(response.Resources)
	if err != nil {
		return nil, fmt.Errorf("failed to parse external resources: %w", err)
	}

	if t.logger != nil {
		t.logger.WithFields(map[string]any{
			"clusters":  len(resources.Clusters),
			"routes":    len(resources.Routes),
			"listeners": len(resources.Listeners),
			"endpoints": len(resources.Endpoints),
		}).Info("Successfully received xDS resources from external service")
	}

	return resources, nil
}

// callExternalService makes an HTTP request to the external translation service
func (t *ExternalTranslator) callExternalService(ctx context.Context, req *ExternalTranslationRequest) (*ExternalTranslationResponse, error) {
	// Marshal request to JSON
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", t.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Make request
	httpResp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	// Check status code
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("external service returned status %d: %s", httpResp.StatusCode, string(body))
	}

	// Parse response
	var response ExternalTranslationResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &response, nil
}

// parseExternalResources converts JSON resources to proto resources
func (t *ExternalTranslator) parseExternalResources(external *ExternalXDSResources) (*XDSResources, error) {
	if external == nil {
		return nil, fmt.Errorf("external resources are nil")
	}

	resources := &XDSResources{
		Clusters:  make([]*clusterv3.Cluster, 0, len(external.Clusters)),
		Endpoints: make([]*endpointv3.ClusterLoadAssignment, 0, len(external.Endpoints)),
		Listeners: make([]*listenerv3.Listener, 0, len(external.Listeners)),
		Routes:    make([]*routev3.RouteConfiguration, 0, len(external.Routes)),
	}

	// Parse clusters
	for _, clusterJSON := range external.Clusters {
		cluster := &clusterv3.Cluster{}
		if err := protojson.Unmarshal(clusterJSON, cluster); err != nil {
			return nil, fmt.Errorf("failed to parse cluster: %w", err)
		}
		resources.Clusters = append(resources.Clusters, cluster)
	}

	// Parse endpoints
	for _, endpointJSON := range external.Endpoints {
		endpoint := &endpointv3.ClusterLoadAssignment{}
		if err := protojson.Unmarshal(endpointJSON, endpoint); err != nil {
			return nil, fmt.Errorf("failed to parse endpoint: %w", err)
		}
		resources.Endpoints = append(resources.Endpoints, endpoint)
	}

	// Parse listeners
	for _, listenerJSON := range external.Listeners {
		listener := &listenerv3.Listener{}
		if err := protojson.Unmarshal(listenerJSON, listener); err != nil {
			return nil, fmt.Errorf("failed to parse listener: %w", err)
		}
		resources.Listeners = append(resources.Listeners, listener)
	}

	// Parse routes
	for _, routeJSON := range external.Routes {
		route := &routev3.RouteConfiguration{}
		if err := protojson.Unmarshal(routeJSON, route); err != nil {
			return nil, fmt.Errorf("failed to parse route: %w", err)
		}
		resources.Routes = append(resources.Routes, route)
	}

	return resources, nil
}
