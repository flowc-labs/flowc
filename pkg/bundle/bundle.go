package bundle

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/flowc-labs/flowc/pkg/types"
	"gopkg.in/yaml.v3"
)

const (
	// Standard filenames in FlowC bundles
	FlowCFileName = "flowc.yaml"

	// MaxBundleSize is the maximum size of a bundle (100MB)
	MaxBundleSize = 100 * 1024 * 1024
)

// Supported API specification file patterns
var (
	// REST/OpenAPI specification files
	RESTSpecFiles = []string{"openapi.yaml", "openapi.yml", "swagger.yaml", "swagger.yml", "openapi.json", "swagger.json"}

	// gRPC Protocol Buffer files
	GRPCSpecExtensions = []string{".proto"}

	// GraphQL schema files
	GraphQLSpecExtensions = []string{".graphql", ".gql"}

	// AsyncAPI specification files (WebSocket, SSE)
	AsyncAPISpecFiles = []string{"asyncapi.yaml", "asyncapi.yml", "asyncapi.json"}
)

// Bundle represents a FlowC API bundle containing flowc.yaml and an API specification
type Bundle struct {
	FlowCMetadata *types.FlowCMetadata
	SpecData      []byte // Raw API specification data (OpenAPI, Proto, GraphQL, AsyncAPI)
	SpecFileName  string // Name of the specification file
	APIType       string // Detected or specified API type (rest, grpc, graphql, websocket, sse)
}

// SpecFileInfo contains information about a detected specification file
type SpecFileInfo struct {
	FileName string // Name of the spec file in the bundle
	APIType  string // Detected API type based on file pattern
	Data     []byte // Raw file content
}

// NewBundle creates a new bundle from metadata and specification data
func NewBundle(metadata *types.FlowCMetadata, specData []byte, specFileName, apiType string) *Bundle {
	return &Bundle{
		FlowCMetadata: metadata,
		SpecData:      specData,
		SpecFileName:  specFileName,
		APIType:       apiType,
	}
}

// CreateZip creates a ZIP file containing flowc.yaml and a specification file
func CreateZip(flowcYAML, specData []byte, specFileName string) ([]byte, error) {
	if len(flowcYAML) == 0 {
		return nil, fmt.Errorf("flowc.yaml content is empty")
	}
	if len(specData) == 0 {
		return nil, fmt.Errorf("specification file content is empty")
	}
	if specFileName == "" {
		return nil, fmt.Errorf("specification file name is empty")
	}

	// Create a buffer to write the ZIP to
	buf := new(bytes.Buffer)
	zipWriter := zip.NewWriter(buf)

	// Add flowc.yaml
	if err := addFileToZip(zipWriter, FlowCFileName, flowcYAML); err != nil {
		_ = zipWriter.Close()
		return nil, fmt.Errorf("failed to add flowc.yaml: %w", err)
	}

	// Add specification file
	if err := addFileToZip(zipWriter, specFileName, specData); err != nil {
		_ = zipWriter.Close()
		return nil, fmt.Errorf("failed to add %s: %w", specFileName, err)
	}

	// Close the ZIP writer
	if err := zipWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close zip writer: %w", err)
	}

	return buf.Bytes(), nil
}

// CreateZipFromBundle creates a ZIP file from a Bundle
func CreateZipFromBundle(bundle *Bundle) ([]byte, error) {
	// Marshal FlowC metadata to YAML
	flowcYAML, err := yaml.Marshal(bundle.FlowCMetadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal flowc metadata: %w", err)
	}

	return CreateZip(flowcYAML, bundle.SpecData, bundle.SpecFileName)
}

// DetectAPIType detects the API type based on the specification file name
func DetectAPIType(fileName string) string {
	baseName := filepath.Base(fileName)
	ext := filepath.Ext(fileName)
	lowerName := strings.ToLower(baseName)

	// Check REST/OpenAPI files
	for _, restFile := range RESTSpecFiles {
		if lowerName == strings.ToLower(restFile) {
			return "rest"
		}
	}

	// Check gRPC files
	if slices.Contains(GRPCSpecExtensions, ext) {
		return "grpc"
	}

	// Check GraphQL files
	if slices.Contains(GraphQLSpecExtensions, ext) {
		return "graphql"
	}

	// Check AsyncAPI files (WebSocket/SSE)
	for _, asyncFile := range AsyncAPISpecFiles {
		if lowerName == strings.ToLower(asyncFile) {
			return "asyncapi" // Will be further classified as websocket or sse
		}
	}

	return ""
}

// IsRESTSpecFile checks if a file is a REST/OpenAPI specification
func IsRESTSpecFile(fileName string) bool {
	lowerName := strings.ToLower(filepath.Base(fileName))
	for _, restFile := range RESTSpecFiles {
		if lowerName == strings.ToLower(restFile) {
			return true
		}
	}
	return false
}

// IsGRPCSpecFile checks if a file is a gRPC Protocol Buffer file
func IsGRPCSpecFile(fileName string) bool {
	ext := filepath.Ext(fileName)
	return slices.Contains(GRPCSpecExtensions, ext)
}

// IsGraphQLSpecFile checks if a file is a GraphQL schema file
func IsGraphQLSpecFile(fileName string) bool {
	ext := filepath.Ext(fileName)
	return slices.Contains(GraphQLSpecExtensions, ext)
}

// IsAsyncAPISpecFile checks if a file is an AsyncAPI specification
func IsAsyncAPISpecFile(fileName string) bool {
	lowerName := strings.ToLower(filepath.Base(fileName))
	for _, asyncFile := range AsyncAPISpecFiles {
		if lowerName == strings.ToLower(asyncFile) {
			return true
		}
	}
	return false
}

// IsSpecFile checks if a file is any supported API specification file
func IsSpecFile(fileName string) bool {
	return IsRESTSpecFile(fileName) ||
		IsGRPCSpecFile(fileName) ||
		IsGraphQLSpecFile(fileName) ||
		IsAsyncAPISpecFile(fileName)
}

// ValidateZip checks if a ZIP file contains the required files
func ValidateZip(zipData []byte) error {
	if len(zipData) == 0 {
		return fmt.Errorf("zip data is empty")
	}

	if len(zipData) > MaxBundleSize {
		return fmt.Errorf("bundle size exceeds maximum allowed size of %d bytes", MaxBundleSize)
	}

	// Check ZIP signature
	if len(zipData) < 4 || !bytes.HasPrefix(zipData, []byte("PK\x03\x04")) {
		return fmt.Errorf("invalid ZIP file: missing ZIP signature")
	}

	// Create a reader from the ZIP data
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("failed to read zip file: %w", err)
	}

	// Check for required files
	hasFlowC := false
	hasSpec := false

	for _, file := range reader.File {
		fileName := filepath.Base(file.Name)

		// Check for flowc.yaml
		if fileName == FlowCFileName || fileName == "flowc.yml" {
			hasFlowC = true
			continue
		}

		// Check for any supported API specification file
		if IsSpecFile(fileName) {
			hasSpec = true
		}
	}

	if !hasFlowC {
		return fmt.Errorf("bundle missing required file: %s", FlowCFileName)
	}
	if !hasSpec {
		return fmt.Errorf(
			"bundle missing API specification file (supported: openapi.yaml, *.proto, *.graphql, asyncapi.yaml)",
		)
	}

	// Note: If multiple spec files are found, the bundle loader will handle selection
	// based on flowc.yaml configuration or precedence rules

	return nil
}

// GetSpecFileInfo extracts information about the API specification file in a bundle
// It returns the spec file name, detected API type, and file data
func GetSpecFileInfo(zipData []byte, preferredSpecFile string) (*SpecFileInfo, error) {
	// Validate first
	if err := ValidateZip(zipData); err != nil {
		return nil, err
	}

	// Create a reader from the ZIP data
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to read zip file: %w", err)
	}

	var candidates []*SpecFileInfo

	// Find all spec files
	for _, file := range reader.File {
		fileName := filepath.Base(file.Name)

		if IsSpecFile(fileName) {
			data, err := extractFile(file)
			if err != nil {
				return nil, fmt.Errorf("failed to extract %s: %w", fileName, err)
			}

			apiType := DetectAPIType(fileName)
			candidates = append(candidates, &SpecFileInfo{
				FileName: fileName,
				APIType:  apiType,
				Data:     data,
			})
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no API specification file found in bundle")
	}

	// If a preferred spec file is specified, find it
	if preferredSpecFile != "" {
		for _, candidate := range candidates {
			if candidate.FileName == preferredSpecFile {
				return candidate, nil
			}
		}
		return nil, fmt.Errorf("specified spec file %s not found in bundle", preferredSpecFile)
	}

	// Otherwise, use precedence rules:
	// 1. REST/OpenAPI files (most common)
	// 2. gRPC Proto files
	// 3. GraphQL schema files
	// 4. AsyncAPI files
	for _, candidate := range candidates {
		if candidate.APIType == "rest" {
			return candidate, nil
		}
	}
	for _, candidate := range candidates {
		if candidate.APIType == "grpc" {
			return candidate, nil
		}
	}
	for _, candidate := range candidates {
		if candidate.APIType == "graphql" {
			return candidate, nil
		}
	}
	for _, candidate := range candidates {
		if candidate.APIType == "asyncapi" {
			return candidate, nil
		}
	}

	// Return the first one found
	return candidates[0], nil
}

// ExtractFiles extracts flowc.yaml and the API specification file from a ZIP bundle
// It returns the flowc.yaml content and information about the detected spec file
// The preferredSpecFile parameter can be used to specify which spec file to extract if multiple are present
func ExtractFiles(zipData []byte, preferredSpecFile string) (flowcYAML []byte, specInfo *SpecFileInfo, err error) {
	// Validate first
	if err := ValidateZip(zipData); err != nil {
		return nil, nil, err
	}

	// Create a reader from the ZIP data
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read zip file: %w", err)
	}

	// Extract flowc.yaml
	for _, file := range reader.File {
		fileName := filepath.Base(file.Name)

		if fileName == FlowCFileName || fileName == "flowc.yml" {
			flowcYAML, err = extractFile(file)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to extract %s: %w", fileName, err)
			}
			break
		}
	}

	if flowcYAML == nil {
		return nil, nil, fmt.Errorf("flowc.yaml not found in bundle")
	}

	// Get spec file info
	specInfo, err = GetSpecFileInfo(zipData, preferredSpecFile)
	if err != nil {
		return nil, nil, err
	}

	return flowcYAML, specInfo, nil
}

// ListFiles returns a list of all files in the ZIP bundle
func ListFiles(zipData []byte) ([]string, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to read zip file: %w", err)
	}

	files := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		files = append(files, file.Name)
	}

	return files, nil
}

// addFileToZip adds a file to a ZIP archive
func addFileToZip(zipWriter *zip.Writer, filename string, data []byte) error {
	fileWriter, err := zipWriter.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file in zip: %w", err)
	}

	_, err = fileWriter.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	return nil
}

// extractFile extracts a single file from the ZIP archive
func extractFile(file *zip.File) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return data, nil
}
