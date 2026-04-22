package loader

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/pkg/types"
	"gopkg.in/yaml.v3"
)

// BundleLoader handles loading and parsing of API bundles
// Supports multiple API types through the IR (Intermediate Representation) layer
type BundleLoader struct {
	parserRegistry *ir.ParserRegistry
}

// NewBundleLoader creates a new bundle loader instance
func NewBundleLoader() *BundleLoader {
	return &BundleLoader{
		parserRegistry: ir.DefaultParserRegistry(),
	}
}

// DeploymentBundle contains the parsed results from a bundle
type DeploymentBundle struct {
	FlowCMetadata *types.FlowCMetadata // FlowC metadata from flowc.yaml
	IR            *ir.API              // Unified IR representation (transient, for translation only)
	Spec          []byte               // Raw specification file (OpenAPI, AsyncAPI, proto, GraphQL, etc.)
}

// LoadBundle loads a bundle from a zip file
// This method automatically detects the API type and uses the appropriate parser
func (l *BundleLoader) LoadBundle(zipData []byte) (*DeploymentBundle, error) {
	ctx := context.Background()

	// Create a reader from the zip data
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to read zip file: %w", err)
	}

	var flowcData []byte
	specFiles := make(map[string][]byte) // Store all potential spec files

	// Extract files from zip
	for _, file := range reader.File {
		fileName := filepath.Base(file.Name)

		switch fileName {
		case "flowc.yaml", "flowc.yml":
			flowcData, err = l.extractFile(file)
			if err != nil {
				return nil, fmt.Errorf("failed to extract flowc.yaml: %w", err)
			}
		case "openapi.yaml", "openapi.yml", "swagger.yaml", "swagger.yml":
			data, err := l.extractFile(file)
			if err != nil {
				return nil, fmt.Errorf("failed to extract %s: %w", fileName, err)
			}
			specFiles["openapi"] = data
		case "asyncapi.yaml", "asyncapi.yml":
			data, err := l.extractFile(file)
			if err != nil {
				return nil, fmt.Errorf("failed to extract %s: %w", fileName, err)
			}
			specFiles["asyncapi"] = data
		default:
			// Check for other spec file types
			if filepath.Ext(fileName) == ".proto" {
				data, err := l.extractFile(file)
				if err != nil {
					return nil, fmt.Errorf("failed to extract %s: %w", fileName, err)
				}
				specFiles["proto"] = data
			} else if filepath.Ext(fileName) == ".graphql" || filepath.Ext(fileName) == ".gql" {
				data, err := l.extractFile(file)
				if err != nil {
					return nil, fmt.Errorf("failed to extract %s: %w", fileName, err)
				}
				specFiles["graphql"] = data
			}
		}
	}

	// Validate required files
	if flowcData == nil {
		return nil, fmt.Errorf("flowc.yaml not found in zip file")
	}

	// Load FlowC metadata
	flowcMetadata, err := l.loadFlowCMetadata(flowcData)
	if err != nil {
		return nil, fmt.Errorf("failed to load flowc.yaml: %w", err)
	}

	// Determine API type and spec file
	apiType, specData, err := l.determineAPITypeAndSpec(flowcMetadata, specFiles)
	if err != nil {
		return nil, fmt.Errorf("failed to determine API type: %w", err)
	}

	// Parse the specification using the appropriate parser through IR
	irAPI, err := l.parseSpecification(ctx, apiType, specData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse specification: %w", err)
	}

	// Set the gateway basepath from FlowCMetadata.Context
	// This is a unified concept that works across all API types
	irAPI.Metadata.BasePath = l.normalizeBasePath(flowcMetadata.Context)

	return &DeploymentBundle{
		FlowCMetadata: flowcMetadata,
		IR:            irAPI,    // Always populated
		Spec:          specData, // Raw spec file (always populated)
	}, nil
}

// determineAPITypeAndSpec determines which API type and spec file to use
func (l *BundleLoader) determineAPITypeAndSpec(metadata *types.FlowCMetadata, specFiles map[string][]byte) (ir.APIType, []byte, error) {
	// If API type is explicitly specified in metadata
	if metadata.APIType != "" {
		apiType := ir.APIType(metadata.APIType)

		// If spec file is explicitly named
		if metadata.SpecFile != "" {
			// This would require extracting the specific file from the zip
			// For now, we'll map to the type
			data, err := l.getSpecDataForType(apiType, specFiles)
			return apiType, data, err
		}

		// Use default file for the API type
		data, err := l.getSpecDataForType(apiType, specFiles)
		return apiType, data, err
	}

	// Auto-detect based on available files (backward compatibility)
	if data, ok := specFiles["openapi"]; ok {
		return ir.APITypeREST, data, nil
	}
	if data, ok := specFiles["asyncapi"]; ok {
		// Default to WebSocket for AsyncAPI
		return ir.APITypeWebSocket, data, nil
	}
	if data, ok := specFiles["proto"]; ok {
		return ir.APITypeGRPC, data, nil
	}
	if data, ok := specFiles["graphql"]; ok {
		return ir.APITypeGraphQL, data, nil
	}

	return "", nil, fmt.Errorf("no supported API specification file found in bundle")
}

// getSpecDataForType retrieves the spec data for a given API type
func (l *BundleLoader) getSpecDataForType(apiType ir.APIType, specFiles map[string][]byte) ([]byte, error) {
	switch apiType {
	case ir.APITypeREST:
		if data, ok := specFiles["openapi"]; ok {
			return data, nil
		}
		return nil, fmt.Errorf("openapi spec not found for api_type: rest")

	case ir.APITypeGRPC:
		if data, ok := specFiles["proto"]; ok {
			return data, nil
		}
		return nil, fmt.Errorf("protobuf spec not found for api_type: grpc")

	case ir.APITypeGraphQL:
		if data, ok := specFiles["graphql"]; ok {
			return data, nil
		}
		return nil, fmt.Errorf("graphql schema not found for api_type: graphql")

	case ir.APITypeWebSocket, ir.APITypeSSE:
		if data, ok := specFiles["asyncapi"]; ok {
			return data, nil
		}
		return nil, fmt.Errorf("asyncapi spec not found for api_type: %s", apiType)

	default:
		return nil, fmt.Errorf("unsupported api_type: %s", apiType)
	}
}

// parseSpecification parses the API specification using the IR layer
func (l *BundleLoader) parseSpecification(ctx context.Context, apiType ir.APIType, specData []byte) (*ir.API, error) {
	parser, err := l.parserRegistry.GetParser(apiType)
	if err != nil {
		return nil, fmt.Errorf("no parser available for API type %s: %w", apiType, err)
	}

	irAPI, err := parser.Parse(ctx, specData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s specification: %w", apiType, err)
	}

	return irAPI, nil
}

// extractFile extracts a single file from the zip archive
func (l *BundleLoader) extractFile(file *zip.File) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// loadFlowCMetadata loads the FlowC metadata from YAML
func (l *BundleLoader) loadFlowCMetadata(data []byte) (*types.FlowCMetadata, error) {
	var metadata types.FlowCMetadata
	if err := yaml.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal flowc.yaml: %w", err)
	}

	// Validate required fields
	if metadata.Name == "" {
		return nil, fmt.Errorf("name is required in flowc.yaml")
	}
	if metadata.Version == "" {
		return nil, fmt.Errorf("version is required in flowc.yaml")
	}
	if metadata.Context == "" {
		return nil, fmt.Errorf("context is required in flowc.yaml")
	}
	if metadata.Upstream.Host == "" {
		return nil, fmt.Errorf("upstream.host is required in flowc.yaml")
	}
	if metadata.Upstream.Port == 0 {
		return nil, fmt.Errorf("upstream.port is required in flowc.yaml")
	}

	// Gateway configuration is optional in flowc.yaml
	// It's required only for deployments, not for API catalog operations
	// Validation happens in DeploymentService.DeployAPI() before deployment

	// Set defaults
	if metadata.APIType == "" {
		metadata.APIType = "rest" // Default to REST for backward compatibility
	}
	if metadata.Upstream.Scheme == "" {
		metadata.Upstream.Scheme = "http"
	}
	if metadata.Upstream.Timeout == "" {
		metadata.Upstream.Timeout = "30s"
	}

	return &metadata, nil
}

// normalizeBasePath normalizes a base path to ensure it starts with a slash
// and doesn't end with a slash (unless it's the root path)
func (l *BundleLoader) normalizeBasePath(path string) string {
	if path == "" {
		return "/"
	}

	// Remove trailing slash (unless it's the root)
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}

	// Ensure leading slash
	if path[0] != '/' {
		path = "/" + path
	}

	return path
}

// GetIR returns the IR representation from a bundle
// This is a convenience method for accessing the unified IR
func (b *DeploymentBundle) GetIR() *ir.API {
	return b.IR
}

// GetAPIType returns the API type from the bundle
func (b *DeploymentBundle) GetAPIType() ir.APIType {
	if b.IR != nil {
		return b.IR.Metadata.Type
	}
	// Fallback to metadata
	if b.FlowCMetadata != nil && b.FlowCMetadata.APIType != "" {
		return ir.APIType(b.FlowCMetadata.APIType)
	}
	return ir.APITypeREST // Default
}

// IsRESTAPI checks if this is a REST/HTTP API
func (b *DeploymentBundle) IsRESTAPI() bool {
	return b.GetAPIType() == ir.APITypeREST
}

// IsGRPCAPI checks if this is a gRPC API
func (b *DeploymentBundle) IsGRPCAPI() bool {
	return b.GetAPIType() == ir.APITypeGRPC
}

// IsGraphQLAPI checks if this is a GraphQL API
func (b *DeploymentBundle) IsGraphQLAPI() bool {
	return b.GetAPIType() == ir.APITypeGraphQL
}

// IsWebSocketAPI checks if this is a WebSocket API
func (b *DeploymentBundle) IsWebSocketAPI() bool {
	return b.GetAPIType() == ir.APITypeWebSocket
}

// IsSSEAPI checks if this is a Server-Sent Events API
func (b *DeploymentBundle) IsSSEAPI() bool {
	return b.GetAPIType() == ir.APITypeSSE
}
