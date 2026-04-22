package ir

import (
	"time"
)

// APIType represents the type of API specification
type APIType string

const (
	// API Types
	APITypeREST      APIType = "rest"      // REST/HTTP APIs (OpenAPI)
	APITypeWebSocket APIType = "websocket" // WebSocket APIs (AsyncAPI)
	APITypeSSE       APIType = "sse"       // Server-Sent Events (AsyncAPI)
	APITypeGRPC      APIType = "grpc"      // gRPC APIs (Protobuf)
	APITypeGraphQL   APIType = "graphql"   // GraphQL APIs (Schema)
)

// Protocol represents the transport protocol
type Protocol string

const (
	ProtocolHTTP      Protocol = "http"
	ProtocolHTTPS     Protocol = "https"
	ProtocolHTTP2     Protocol = "http2"
	ProtocolWebSocket Protocol = "websocket"
	ProtocolGRPC      Protocol = "grpc"
)

// API is the top-level intermediate representation for any API type
// This unified model allows FlowC to work with different API specifications
// in a consistent manner
type API struct {
	// Metadata about the API
	Metadata APIMetadata `json:"metadata" yaml:"metadata"`

	// Endpoints define all the operations/methods available in the API
	// For REST: HTTP paths with methods
	// For gRPC: Service methods
	// For GraphQL: Queries, mutations, subscriptions
	// For WebSocket/SSE: Event channels
	Endpoints []Endpoint `json:"endpoints" yaml:"endpoints"`

	// DataModels define the schemas/types used in the API
	// For REST: JSON schemas, request/response bodies
	// For gRPC: Protobuf messages
	// For GraphQL: GraphQL types
	DataModels []DataModel `json:"data_models,omitempty" yaml:"data_models,omitempty"`

	// Security schemes available for this API
	Security []SecurityScheme `json:"security,omitempty" yaml:"security,omitempty"`

	// Servers define where the API is hosted
	Servers []Server `json:"servers,omitempty" yaml:"servers,omitempty"`

	// Extensions for API-specific features that don't fit the common model
	Extensions map[string]any `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// APIMetadata contains metadata about the API
type APIMetadata struct {
	// Type of API (REST, gRPC, GraphQL, WebSocket, SSE)
	Type APIType `json:"type" yaml:"type"`

	// Name of the API
	Name string `json:"name" yaml:"name"`

	// Version of the API
	Version string `json:"version" yaml:"version"`

	// Description of the API
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Title of the API (for display purposes)
	Title string `json:"title,omitempty" yaml:"title,omitempty"`

	// BasePath is the gateway base path where this API is exposed
	// This is a unified concept that works across all API types:
	// - REST: Base path prefix for all HTTP routes (e.g., "/api/v1")
	// - gRPC: Base path for gRPC services (e.g., "/grpc/v1")
	// - GraphQL: Base path for GraphQL endpoint (e.g., "/graphql")
	// - WebSocket: Base path for WebSocket connections (e.g., "/ws")
	// - SSE: Base path for Server-Sent Events (e.g., "/events")
	BasePath string `json:"base_path,omitempty" yaml:"base_path,omitempty"`

	// Terms of service URL
	TermsOfService string `json:"terms_of_service,omitempty" yaml:"terms_of_service,omitempty"`

	// Contact information
	Contact *Contact `json:"contact,omitempty" yaml:"contact,omitempty"`

	// License information
	License *License `json:"license,omitempty" yaml:"license,omitempty"`

	// Tags for organization
	Tags []string `json:"tags,omitempty" yaml:"tags,omitempty"`

	// Labels for additional metadata
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// Contact represents contact information
type Contact struct {
	Name  string `json:"name,omitempty" yaml:"name,omitempty"`
	URL   string `json:"url,omitempty" yaml:"url,omitempty"`
	Email string `json:"email,omitempty" yaml:"email,omitempty"`
}

// License represents license information
type License struct {
	Name string `json:"name" yaml:"name"`
	URL  string `json:"url,omitempty" yaml:"url,omitempty"`
}

// Endpoint represents a single API operation/method
// This is a unified representation that works across different API types
type Endpoint struct {
	// Unique identifier for this endpoint
	ID string `json:"id" yaml:"id"`

	// Name/title of the endpoint
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	// Description of what this endpoint does
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Type of endpoint
	Type EndpointType `json:"type" yaml:"type"`

	// Protocol used (HTTP, HTTP2, WebSocket, gRPC)
	Protocol Protocol `json:"protocol" yaml:"protocol"`

	// Path/Route information
	Path PathInfo `json:"path" yaml:"path"`

	// Method/Operation (GET, POST, Subscribe, Query, etc.)
	Method string `json:"method" yaml:"method"`

	// Request specification
	Request *RequestSpec `json:"request,omitempty" yaml:"request,omitempty"`

	// Response specification(s)
	Responses []ResponseSpec `json:"responses,omitempty" yaml:"responses,omitempty"`

	// Security requirements for this endpoint
	Security []SecurityRequirement `json:"security,omitempty" yaml:"security,omitempty"`

	// Tags for organization
	Tags []string `json:"tags,omitempty" yaml:"tags,omitempty"`

	// Whether this endpoint is deprecated
	Deprecated bool `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`

	// Timeout configuration
	Timeout *time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	// Rate limit configuration specific to this endpoint
	RateLimit *RateLimit `json:"rate_limit,omitempty" yaml:"rate_limit,omitempty"`

	// Extensions for endpoint-specific features
	Extensions map[string]any `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// EndpointType represents the type of endpoint
type EndpointType string

const (
	// REST endpoint types
	EndpointTypeHTTP EndpointType = "http"

	// gRPC endpoint types
	EndpointTypeGRPCUnary         EndpointType = "grpc_unary"
	EndpointTypeGRPCServerStream  EndpointType = "grpc_server_stream"
	EndpointTypeGRPCClientStream  EndpointType = "grpc_client_stream"
	EndpointTypeGRPCBidirectional EndpointType = "grpc_bidirectional"

	// GraphQL endpoint types
	EndpointTypeGraphQLQuery        EndpointType = "graphql_query"
	EndpointTypeGraphQLMutation     EndpointType = "graphql_mutation"
	EndpointTypeGraphQLSubscription EndpointType = "graphql_subscription"

	// Event-driven endpoint types
	EndpointTypeWebSocket EndpointType = "websocket"
	EndpointTypeSSE       EndpointType = "sse"
	EndpointTypePubSub    EndpointType = "pubsub"
)

// PathInfo contains path/routing information
type PathInfo struct {
	// Full path pattern (e.g., "/api/users/{id}", "/grpc.Service/Method")
	Pattern string `json:"pattern" yaml:"pattern"`

	// Path parameters
	Parameters []Parameter `json:"parameters,omitempty" yaml:"parameters,omitempty"`

	// Base path (for grouping)
	BasePath string `json:"base_path,omitempty" yaml:"base_path,omitempty"`
}

// RequestSpec defines the request structure
type RequestSpec struct {
	// Content type (application/json, application/grpc, etc.)
	ContentType string `json:"content_type,omitempty" yaml:"content_type,omitempty"`

	// Body/payload specification
	Body *DataModel `json:"body,omitempty" yaml:"body,omitempty"`

	// Query parameters
	QueryParameters []Parameter `json:"query_parameters,omitempty" yaml:"query_parameters,omitempty"`

	// Header parameters
	HeaderParameters []Parameter `json:"header_parameters,omitempty" yaml:"header_parameters,omitempty"`

	// Cookie parameters
	CookieParameters []Parameter `json:"cookie_parameters,omitempty" yaml:"cookie_parameters,omitempty"`

	// For streaming endpoints: indicates if streaming is supported
	Streaming bool `json:"streaming,omitempty" yaml:"streaming,omitempty"`

	// Validation rules
	Validation *Validation `json:"validation,omitempty" yaml:"validation,omitempty"`
}

// ResponseSpec defines a response structure
type ResponseSpec struct {
	// Status code (for HTTP) or response type
	StatusCode int `json:"status_code,omitempty" yaml:"status_code,omitempty"`

	// Description of this response
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Content type
	ContentType string `json:"content_type,omitempty" yaml:"content_type,omitempty"`

	// Body/payload specification
	Body *DataModel `json:"body,omitempty" yaml:"body,omitempty"`

	// Headers in the response
	Headers []Parameter `json:"headers,omitempty" yaml:"headers,omitempty"`

	// For streaming endpoints
	Streaming bool `json:"streaming,omitempty" yaml:"streaming,omitempty"`

	// Whether this is an error response
	IsError bool `json:"is_error,omitempty" yaml:"is_error,omitempty"`
}

// Parameter represents a parameter (path, query, header, etc.)
type Parameter struct {
	// Name of the parameter
	Name string `json:"name" yaml:"name"`

	// Location of the parameter (path, query, header, cookie)
	In ParameterLocation `json:"in" yaml:"in"`

	// Description
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Whether the parameter is required
	Required bool `json:"required,omitempty" yaml:"required,omitempty"`

	// Schema/type information
	Schema *DataType `json:"schema,omitempty" yaml:"schema,omitempty"`

	// Default value
	Default any `json:"default,omitempty" yaml:"default,omitempty"`

	// Example value
	Example any `json:"example,omitempty" yaml:"example,omitempty"`

	// Deprecated flag
	Deprecated bool `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
}

// ParameterLocation defines where a parameter is located
type ParameterLocation string

const (
	ParameterLocationPath   ParameterLocation = "path"
	ParameterLocationQuery  ParameterLocation = "query"
	ParameterLocationHeader ParameterLocation = "header"
	ParameterLocationCookie ParameterLocation = "cookie"
)

// DataModel represents a data structure/schema
type DataModel struct {
	// Name of the model
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	// Description
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Type information
	Type *DataType `json:"type" yaml:"type"`

	// Properties for object types
	Properties []Property `json:"properties,omitempty" yaml:"properties,omitempty"`

	// For array types: item type
	Items *DataType `json:"items,omitempty" yaml:"items,omitempty"`

	// Required properties
	Required []string `json:"required,omitempty" yaml:"required,omitempty"`

	// Additional properties allowed
	AdditionalProperties bool `json:"additional_properties,omitempty" yaml:"additional_properties,omitempty"`

	// Example value
	Example any `json:"example,omitempty" yaml:"example,omitempty"`

	// Reference to another model (for composition)
	Ref string `json:"ref,omitempty" yaml:"ref,omitempty"`
}

// Property represents a property in a data model
type Property struct {
	// Name of the property
	Name string `json:"name" yaml:"name"`

	// Description
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Type information
	Type *DataType `json:"type" yaml:"type"`

	// Required flag
	Required bool `json:"required,omitempty" yaml:"required,omitempty"`

	// Default value
	Default any `json:"default,omitempty" yaml:"default,omitempty"`

	// Example value
	Example any `json:"example,omitempty" yaml:"example,omitempty"`

	// Validation rules
	Validation *Validation `json:"validation,omitempty" yaml:"validation,omitempty"`
}

// DataType represents type information
type DataType struct {
	// Base type (string, number, integer, boolean, array, object, any)
	BaseType string `json:"base_type" yaml:"base_type"`

	// Format (e.g., date-time, email, uuid, int32, int64)
	Format string `json:"format,omitempty" yaml:"format,omitempty"`

	// For object types: reference to a DataModel
	ModelRef string `json:"model_ref,omitempty" yaml:"model_ref,omitempty"`

	// For array types: item type
	Items *DataType `json:"items,omitempty" yaml:"items,omitempty"`

	// Enum values
	Enum []any `json:"enum,omitempty" yaml:"enum,omitempty"`

	// Nullable flag
	Nullable bool `json:"nullable,omitempty" yaml:"nullable,omitempty"`
}

// Validation defines validation rules
type Validation struct {
	// String validations
	MinLength *int   `json:"min_length,omitempty" yaml:"min_length,omitempty"`
	MaxLength *int   `json:"max_length,omitempty" yaml:"max_length,omitempty"`
	Pattern   string `json:"pattern,omitempty" yaml:"pattern,omitempty"`

	// Number validations
	Minimum          *float64 `json:"minimum,omitempty" yaml:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty" yaml:"maximum,omitempty"`
	ExclusiveMinimum bool     `json:"exclusive_minimum,omitempty" yaml:"exclusive_minimum,omitempty"`
	ExclusiveMaximum bool     `json:"exclusive_maximum,omitempty" yaml:"exclusive_maximum,omitempty"`
	MultipleOf       *float64 `json:"multiple_of,omitempty" yaml:"multiple_of,omitempty"`

	// Array validations
	MinItems    *int `json:"min_items,omitempty" yaml:"min_items,omitempty"`
	MaxItems    *int `json:"max_items,omitempty" yaml:"max_items,omitempty"`
	UniqueItems bool `json:"unique_items,omitempty" yaml:"unique_items,omitempty"`

	// Object validations
	MinProperties *int `json:"min_properties,omitempty" yaml:"min_properties,omitempty"`
	MaxProperties *int `json:"max_properties,omitempty" yaml:"max_properties,omitempty"`
}

// SecurityScheme defines a security mechanism
type SecurityScheme struct {
	// Type of security (apiKey, http, oauth2, openIdConnect, mutualTLS)
	Type string `json:"type" yaml:"type"`

	// Name/identifier
	Name string `json:"name" yaml:"name"`

	// Description
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// For apiKey: location (query, header, cookie)
	In string `json:"in,omitempty" yaml:"in,omitempty"`

	// For http: scheme (basic, bearer, etc.)
	Scheme string `json:"scheme,omitempty" yaml:"scheme,omitempty"`

	// For http bearer: format (JWT, etc.)
	BearerFormat string `json:"bearer_format,omitempty" yaml:"bearer_format,omitempty"`

	// For oauth2: flows
	Flows *OAuthFlows `json:"flows,omitempty" yaml:"flows,omitempty"`

	// For openIdConnect: discovery URL
	OpenIDConnectURL string `json:"openid_connect_url,omitempty" yaml:"openid_connect_url,omitempty"`
}

// OAuthFlows defines OAuth 2.0 flows
type OAuthFlows struct {
	Implicit          *OAuthFlow `json:"implicit,omitempty" yaml:"implicit,omitempty"`
	Password          *OAuthFlow `json:"password,omitempty" yaml:"password,omitempty"`
	ClientCredentials *OAuthFlow `json:"client_credentials,omitempty" yaml:"client_credentials,omitempty"`
	AuthorizationCode *OAuthFlow `json:"authorization_code,omitempty" yaml:"authorization_code,omitempty"`
}

// OAuthFlow defines an OAuth 2.0 flow
type OAuthFlow struct {
	AuthorizationURL string            `json:"authorization_url,omitempty" yaml:"authorization_url,omitempty"`
	TokenURL         string            `json:"token_url,omitempty" yaml:"token_url,omitempty"`
	RefreshURL       string            `json:"refresh_url,omitempty" yaml:"refresh_url,omitempty"`
	Scopes           map[string]string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}

// SecurityRequirement specifies security requirements for an endpoint
type SecurityRequirement struct {
	// Name of the security scheme
	Name string `json:"name" yaml:"name"`

	// Scopes required (for OAuth2)
	Scopes []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}

// Server defines a server/host configuration
type Server struct {
	// URL of the server
	URL string `json:"url" yaml:"url"`

	// Description
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Variables for URL templating
	Variables map[string]ServerVariable `json:"variables,omitempty" yaml:"variables,omitempty"`
}

// ServerVariable defines a variable in a server URL
type ServerVariable struct {
	Default     string   `json:"default" yaml:"default"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Enum        []string `json:"enum,omitempty" yaml:"enum,omitempty"`
}

// RateLimit defines rate limiting configuration
type RateLimit struct {
	// Requests per time window
	Requests int `json:"requests" yaml:"requests"`

	// Time window duration (e.g., "1m", "1h")
	Window string `json:"window" yaml:"window"`

	// Burst size
	Burst int `json:"burst,omitempty" yaml:"burst,omitempty"`
}
