package ir

import (
	"context"
	"fmt"
)

// Parser is the interface that all API spec parsers must implement
// Different parsers convert different API specifications (OpenAPI, AsyncAPI, Protobuf, GraphQL)
// into the unified IR (Intermediate Representation) format
type Parser interface {
	// Parse converts raw API specification data into the IR format
	Parse(ctx context.Context, data []byte) (*API, error)

	// SupportedType returns the API type this parser supports
	SupportedType() APIType

	// SupportedFormats returns the specification formats this parser can handle
	// For example: ["openapi-3.0", "openapi-3.1", "swagger-2.0"]
	SupportedFormats() []string

	// Validate validates the raw specification before parsing
	Validate(ctx context.Context, data []byte) error
}

// ParserRegistry manages multiple parsers for different API types
type ParserRegistry struct {
	parsers map[APIType]Parser
}

// NewParserRegistry creates a new parser registry
func NewParserRegistry() *ParserRegistry {
	return &ParserRegistry{
		parsers: make(map[APIType]Parser),
	}
}

// Register registers a parser for a specific API type
func (r *ParserRegistry) Register(apiType APIType, parser Parser) error {
	if parser == nil {
		return fmt.Errorf("parser cannot be nil")
	}

	if _, exists := r.parsers[apiType]; exists {
		return fmt.Errorf("parser for API type %s already registered", apiType)
	}

	r.parsers[apiType] = parser
	return nil
}

// GetParser retrieves a parser for a specific API type
func (r *ParserRegistry) GetParser(apiType APIType) (Parser, error) {
	parser, exists := r.parsers[apiType]
	if !exists {
		return nil, fmt.Errorf("no parser registered for API type: %s", apiType)
	}
	return parser, nil
}

// Parse uses the appropriate parser based on API type
func (r *ParserRegistry) Parse(ctx context.Context, apiType APIType, data []byte) (*API, error) {
	parser, err := r.GetParser(apiType)
	if err != nil {
		return nil, err
	}

	return parser.Parse(ctx, data)
}

// SupportedTypes returns all supported API types
func (r *ParserRegistry) SupportedTypes() []APIType {
	types := make([]APIType, 0, len(r.parsers))
	for apiType := range r.parsers {
		types = append(types, apiType)
	}
	return types
}

// DefaultParserRegistry creates a registry with all built-in parsers
func DefaultParserRegistry() *ParserRegistry {
	registry := NewParserRegistry()

	// Register OpenAPI/REST parser
	_ = registry.Register(APITypeREST, NewOpenAPIParser())

	// Future parsers will be registered here:
	// registry.Register(APITypeGRPC, NewProtobufParser())
	// registry.Register(APITypeGraphQL, NewGraphQLParser())
	// registry.Register(APITypeWebSocket, NewAsyncAPIParser())
	// registry.Register(APITypeSSE, NewAsyncAPIParser())

	return registry
}

// ParseOptions provides options for parsing
type ParseOptions struct {
	// Strict mode: fail on warnings
	Strict bool

	// Validate spec before parsing
	Validate bool

	// Include examples in the IR
	IncludeExamples bool

	// Include extensions in the IR
	IncludeExtensions bool

	// Custom context data
	Context map[string]any
}

// DefaultParseOptions returns default parsing options
func DefaultParseOptions() *ParseOptions {
	return &ParseOptions{
		Strict:            false,
		Validate:          true,
		IncludeExamples:   true,
		IncludeExtensions: true,
		Context:           make(map[string]any),
	}
}
