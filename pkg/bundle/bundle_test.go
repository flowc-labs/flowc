package bundle

import (
	"testing"

	"github.com/flowc-labs/flowc/pkg/types"
	"gopkg.in/yaml.v3"
)

const (
	apiTypeREST     = "rest"
	apiTypeGRPC     = "grpc"
	specOpenAPIYAML = "openapi.yaml"
)

func TestCreateZip(t *testing.T) {
	flowcYAML := []byte(`name: test-api
version: v1.0.0
context: test
gateway:
  mediation: {}
upstream:
  host: localhost
  port: 8080
`)

	openapiYAML := []byte(`openapi: 3.0.0
info:
  title: Test API
  version: 1.0.0
paths:
  /test:
    get:
      summary: Test endpoint
`)

	zipData, err := CreateZip(flowcYAML, openapiYAML, specOpenAPIYAML)
	if err != nil {
		t.Fatalf("CreateZip failed: %v", err)
	}

	if len(zipData) == 0 {
		t.Fatal("CreateZip returned empty data")
	}

	// Verify ZIP signature
	if len(zipData) < 4 || string(zipData[:4]) != "PK\x03\x04" {
		t.Fatal("CreateZip did not create valid ZIP file")
	}
}

func TestValidateZip(t *testing.T) {
	tests := []struct {
		name    string
		zipData []byte
		wantErr bool
	}{
		{
			name:    "empty data",
			zipData: []byte{},
			wantErr: true,
		},
		{
			name:    "invalid zip signature",
			zipData: []byte("not a zip file"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateZip(tt.zipData)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateZip() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestExtractFiles(t *testing.T) {
	// Create a valid ZIP
	flowcYAML := []byte(`name: test-api
version: v1.0.0
context: test
gateway:
  mediation: {}
upstream:
  host: localhost
  port: 8080
`)

	openapiYAML := []byte(`openapi: 3.0.0
info:
  title: Test API
  version: 1.0.0
paths:
  /test:
    get:
      summary: Test endpoint
`)

	zipData, err := CreateZip(flowcYAML, openapiYAML, specOpenAPIYAML)
	if err != nil {
		t.Fatalf("CreateZip failed: %v", err)
	}

	// Extract files
	extractedFlowC, specInfo, err := ExtractFiles(zipData, "")
	if err != nil {
		t.Fatalf("ExtractFiles failed: %v", err)
	}

	if string(extractedFlowC) != string(flowcYAML) {
		t.Errorf("Extracted flowc.yaml does not match original")
	}

	if string(specInfo.Data) != string(openapiYAML) {
		t.Errorf("Extracted openapi.yaml does not match original")
	}

	if specInfo.APIType != apiTypeREST {
		t.Errorf("Expected API type 'rest', got %s", specInfo.APIType)
	}
}

func TestCreateZipFromBundle(t *testing.T) {
	metadata := &types.FlowCMetadata{
		Name:    "test-api",
		Version: "v1.0.0",
		Context: "test",
		Gateway: types.GatewayConfig{},
		Upstream: types.UpstreamConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	openapiData := []byte(`openapi: 3.0.0
info:
  title: Test API
  version: 1.0.0
paths:
  /test:
    get:
      summary: Test endpoint
`)

	bundle := NewBundle(metadata, openapiData, specOpenAPIYAML, apiTypeREST)
	zipData, err := CreateZipFromBundle(bundle)
	if err != nil {
		t.Fatalf("CreateZipFromBundle failed: %v", err)
	}

	// Validate the created ZIP
	if err := ValidateZip(zipData); err != nil {
		t.Fatalf("Created ZIP is invalid: %v", err)
	}

	// Extract and verify
	flowcYAML, specInfo, err := ExtractFiles(zipData, "")
	if err != nil {
		t.Fatalf("ExtractFiles failed: %v", err)
	}

	// Unmarshal and compare metadata
	var extractedMetadata types.FlowCMetadata
	if err := yaml.Unmarshal(flowcYAML, &extractedMetadata); err != nil {
		t.Fatalf("Failed to unmarshal extracted flowc.yaml: %v", err)
	}

	if extractedMetadata.Name != metadata.Name {
		t.Errorf("Name mismatch: got %s, want %s", extractedMetadata.Name, metadata.Name)
	}

	if string(specInfo.Data) != string(openapiData) {
		t.Errorf("OpenAPI data mismatch")
	}
}

func TestListFiles(t *testing.T) {
	flowcYAML := []byte(`name: test-api
version: v1.0.0
context: test
`)

	openapiYAML := []byte(`openapi: 3.0.0
info:
  title: Test API
  version: 1.0.0
`)

	zipData, err := CreateZip(flowcYAML, openapiYAML, specOpenAPIYAML)
	if err != nil {
		t.Fatalf("CreateZip failed: %v", err)
	}

	files, err := ListFiles(zipData)
	if err != nil {
		t.Fatalf("ListFiles failed: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("Expected 2 files, got %d", len(files))
	}

	expectedFiles := map[string]bool{
		FlowCFileName:   false,
		specOpenAPIYAML: false,
	}

	for _, file := range files {
		if _, exists := expectedFiles[file]; exists {
			expectedFiles[file] = true
		}
	}

	for file, found := range expectedFiles {
		if !found {
			t.Errorf("Expected file %s not found in bundle", file)
		}
	}
}

// Multi-API Support Tests

func TestDetectAPIType(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		want     string
	}{
		// REST/OpenAPI files
		{specOpenAPIYAML, specOpenAPIYAML, apiTypeREST},
		{"openapi.yml", "openapi.yml", apiTypeREST},
		{"swagger.yaml", "swagger.yaml", apiTypeREST},
		{"swagger.yml", "swagger.yml", apiTypeREST},
		{"openapi.json", "openapi.json", apiTypeREST},
		{"swagger.json", "swagger.json", apiTypeREST},

		// gRPC files
		{"service.proto", "service.proto", apiTypeGRPC},
		{"user_service.proto", "user_service.proto", apiTypeGRPC},
		{"api.proto", "api.proto", apiTypeGRPC},

		// GraphQL files
		{"schema.graphql", "schema.graphql", "graphql"},
		{"schema.gql", "schema.gql", "graphql"},
		{"api.graphql", "api.graphql", "graphql"},

		// AsyncAPI files
		{"asyncapi.yaml", "asyncapi.yaml", "asyncapi"},
		{"asyncapi.yml", "asyncapi.yml", "asyncapi"},
		{"asyncapi.json", "asyncapi.json", "asyncapi"},

		// Unknown files
		{"readme.md", "readme.md", ""},
		{"config.yaml", "config.yaml", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectAPIType(tt.fileName)
			if got != tt.want {
				t.Errorf("DetectAPIType(%s) = %s, want %s", tt.fileName, got, tt.want)
			}
		})
	}
}

func TestIsSpecFile(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		want     bool
	}{
		{specOpenAPIYAML, specOpenAPIYAML, true},
		{"service.proto", "service.proto", true},
		{"schema.graphql", "schema.graphql", true},
		{"asyncapi.yaml", "asyncapi.yaml", true},
		{"readme.md", "readme.md", false},
		{"flowc.yaml", "flowc.yaml", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSpecFile(tt.fileName)
			if got != tt.want {
				t.Errorf("IsSpecFile(%s) = %v, want %v", tt.fileName, got, tt.want)
			}
		})
	}
}

func TestCreateZip_gRPC(t *testing.T) {
	flowcYAML := []byte(`name: grpc-api
version: v1.0.0
context: grpc/v1
api_type: grpc
gateway:
  node_id: gateway-1
  listener: http2
upstream:
  host: grpc-service
  port: 50051
`)

	protoData := []byte(`syntax = "proto3";

package myapi.v1;

service MyService {
  rpc SayHello (HelloRequest) returns (HelloResponse);
}

message HelloRequest {
  string name = 1;
}

message HelloResponse {
  string message = 1;
}
`)

	zipData, err := CreateZip(flowcYAML, protoData, "service.proto")
	if err != nil {
		t.Fatalf("CreateZip failed: %v", err)
	}

	if len(zipData) == 0 {
		t.Fatal("CreateZip returned empty data")
	}

	// Validate the ZIP
	if err := ValidateZip(zipData); err != nil {
		t.Fatalf("ValidateZip failed: %v", err)
	}
}

func TestCreateZip_GraphQL(t *testing.T) {
	flowcYAML := []byte(`name: graphql-api
version: v1.0.0
context: graphql
api_type: graphql
gateway:
  node_id: gateway-1
  listener: http
upstream:
  host: graphql-service
  port: 4000
`)

	graphqlSchema := []byte(`type Query {
  hello(name: String!): String!
  users: [User!]!
}

type User {
  id: ID!
  name: String!
  email: String!
}

type Mutation {
  createUser(name: String!, email: String!): User!
}
`)

	zipData, err := CreateZip(flowcYAML, graphqlSchema, "schema.graphql")
	if err != nil {
		t.Fatalf("CreateZip failed: %v", err)
	}

	if len(zipData) == 0 {
		t.Fatal("CreateZip returned empty data")
	}

	// Validate the ZIP
	if err := ValidateZip(zipData); err != nil {
		t.Fatalf("ValidateZip failed: %v", err)
	}
}

func TestCreateZip_AsyncAPI(t *testing.T) {
	flowcYAML := []byte(`name: websocket-api
version: v1.0.0
context: ws
api_type: websocket
gateway:
  node_id: gateway-1
  listener: http
upstream:
  host: ws-service
  port: 8080
`)

	asyncapiSpec := []byte(`asyncapi: 2.6.0
info:
  title: WebSocket API
  version: 1.0.0

channels:
  /messages:
    subscribe:
      message:
        payload:
          type: object
          properties:
            text:
              type: string
`)

	zipData, err := CreateZip(flowcYAML, asyncapiSpec, "asyncapi.yaml")
	if err != nil {
		t.Fatalf("CreateZip failed: %v", err)
	}

	if len(zipData) == 0 {
		t.Fatal("CreateZip returned empty data")
	}

	// Validate the ZIP
	if err := ValidateZip(zipData); err != nil {
		t.Fatalf("ValidateZip failed: %v", err)
	}
}

func TestExtractFiles_REST(t *testing.T) {
	flowcYAML := []byte(`name: rest-api
version: v1.0.0
context: api/v1
api_type: rest
`)

	openapiYAML := []byte(`openapi: 3.0.0
info:
  title: REST API
  version: 1.0.0
paths:
  /users:
    get:
      summary: List users
`)

	zipData, err := CreateZip(flowcYAML, openapiYAML, specOpenAPIYAML)
	if err != nil {
		t.Fatalf("CreateZip failed: %v", err)
	}

	// Extract files
	extractedFlowC, specInfo, err := ExtractFiles(zipData, "")
	if err != nil {
		t.Fatalf("ExtractFiles failed: %v", err)
	}

	if string(extractedFlowC) != string(flowcYAML) {
		t.Errorf("Extracted flowc.yaml does not match original")
	}

	if specInfo.FileName != specOpenAPIYAML {
		t.Errorf("Expected spec file name 'openapi.yaml', got %s", specInfo.FileName)
	}

	if specInfo.APIType != apiTypeREST {
		t.Errorf("Expected API type 'rest', got %s", specInfo.APIType)
	}

	if string(specInfo.Data) != string(openapiYAML) {
		t.Errorf("Extracted spec data does not match original")
	}
}

func TestExtractFiles_gRPC(t *testing.T) {
	flowcYAML := []byte(`name: grpc-api
version: v1.0.0
context: grpc/v1
api_type: grpc
`)

	protoData := []byte(`syntax = "proto3";
package myapi.v1;
service MyService {
  rpc SayHello (HelloRequest) returns (HelloResponse);
}
`)

	zipData, err := CreateZip(flowcYAML, protoData, "service.proto")
	if err != nil {
		t.Fatalf("CreateZip failed: %v", err)
	}

	// Extract files
	extractedFlowC, specInfo, err := ExtractFiles(zipData, "")
	if err != nil {
		t.Fatalf("ExtractFiles failed: %v", err)
	}

	if string(extractedFlowC) != string(flowcYAML) {
		t.Errorf("Extracted flowc.yaml does not match original")
	}

	if specInfo.FileName != "service.proto" {
		t.Errorf("Expected spec file name 'service.proto', got %s", specInfo.FileName)
	}

	if specInfo.APIType != apiTypeGRPC {
		t.Errorf("Expected API type 'grpc', got %s", specInfo.APIType)
	}

	if string(specInfo.Data) != string(protoData) {
		t.Errorf("Extracted spec data does not match original")
	}
}

func TestGetSpecFileInfo(t *testing.T) {
	flowcYAML := []byte(`name: test-api
version: v1.0.0
context: test
`)

	openapiYAML := []byte(`openapi: 3.0.0
info:
  title: Test API
  version: 1.0.0
`)

	zipData, err := CreateZip(flowcYAML, openapiYAML, specOpenAPIYAML)
	if err != nil {
		t.Fatalf("CreateZip failed: %v", err)
	}

	// Get spec file info without preference
	specInfo, err := GetSpecFileInfo(zipData, "")
	if err != nil {
		t.Fatalf("GetSpecFileInfo failed: %v", err)
	}

	if specInfo.FileName != specOpenAPIYAML {
		t.Errorf("Expected spec file name 'openapi.yaml', got %s", specInfo.FileName)
	}

	if specInfo.APIType != apiTypeREST {
		t.Errorf("Expected API type 'rest', got %s", specInfo.APIType)
	}

	// Get spec file info with preference
	specInfo2, err := GetSpecFileInfo(zipData, specOpenAPIYAML)
	if err != nil {
		t.Fatalf("GetSpecFileInfo with preference failed: %v", err)
	}

	if specInfo2.FileName != specOpenAPIYAML {
		t.Errorf("Expected spec file name 'openapi.yaml', got %s", specInfo2.FileName)
	}
}

func TestNewBundle(t *testing.T) {
	metadata := &types.FlowCMetadata{
		Name:    "grpc-api",
		Version: "v1.0.0",
		Context: "grpc/v1",
		APIType: apiTypeGRPC,
		Gateway: types.GatewayConfig{
			NodeID:         "gateway-1",
			Port:           8080,
			VirtualHostRef: "prod",
		},
		Upstream: types.UpstreamConfig{
			Host: "grpc-service",
			Port: 50051,
		},
	}

	protoData := []byte(`syntax = "proto3";
package myapi.v1;
`)

	bundle := NewBundle(metadata, protoData, "service.proto", apiTypeGRPC)

	if bundle.APIType != apiTypeGRPC {
		t.Errorf("Expected API type 'grpc', got %s", bundle.APIType)
	}

	if bundle.SpecFileName != "service.proto" {
		t.Errorf("Expected spec file name 'service.proto', got %s", bundle.SpecFileName)
	}

	if string(bundle.SpecData) != string(protoData) {
		t.Errorf("Spec data does not match")
	}
}

func TestValidateZip_MultipleAPITypes(t *testing.T) {
	tests := []struct {
		name     string
		flowc    []byte
		specData []byte
		specFile string
		wantErr  bool
	}{
		{
			name: "valid REST bundle",
			flowc: []byte(`name: rest-api
version: v1.0.0
`),
			specData: []byte(`openapi: 3.0.0`),
			specFile: specOpenAPIYAML,
			wantErr:  false,
		},
		{
			name: "valid gRPC bundle",
			flowc: []byte(`name: grpc-api
version: v1.0.0
`),
			specData: []byte(`syntax = "proto3";`),
			specFile: "service.proto",
			wantErr:  false,
		},
		{
			name: "valid GraphQL bundle",
			flowc: []byte(`name: graphql-api
version: v1.0.0
`),
			specData: []byte(`type Query { hello: String }`),
			specFile: "schema.graphql",
			wantErr:  false,
		},
		{
			name: "valid AsyncAPI bundle",
			flowc: []byte(`name: ws-api
version: v1.0.0
`),
			specData: []byte(`asyncapi: 2.6.0`),
			specFile: "asyncapi.yaml",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zipData, err := CreateZip(tt.flowc, tt.specData, tt.specFile)
			if err != nil {
				t.Fatalf("CreateZip failed: %v", err)
			}

			err = ValidateZip(zipData)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateZip() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
