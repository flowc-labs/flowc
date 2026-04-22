package ir

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// OpenAPIParser parses OpenAPI specifications into the IR format
type OpenAPIParser struct {
	options *ParseOptions
}

// NewOpenAPIParser creates a new OpenAPI parser
func NewOpenAPIParser() *OpenAPIParser {
	return &OpenAPIParser{
		options: DefaultParseOptions(),
	}
}

// WithOptions sets custom parsing options
func (p *OpenAPIParser) WithOptions(options *ParseOptions) *OpenAPIParser {
	p.options = options
	return p
}

// SupportedType returns the API type this parser supports
func (p *OpenAPIParser) SupportedType() APIType {
	return APITypeREST
}

// SupportedFormats returns the OpenAPI formats this parser can handle
func (p *OpenAPIParser) SupportedFormats() []string {
	return []string{"openapi-3.0", "openapi-3.1", "swagger-2.0"}
}

// Validate validates the OpenAPI specification
func (p *OpenAPIParser) Validate(ctx context.Context, data []byte) error {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	spec, err := loader.LoadFromData(data)
	if err != nil {
		return fmt.Errorf("failed to load OpenAPI spec: %w", err)
	}

	if err := spec.Validate(ctx); err != nil {
		return fmt.Errorf("OpenAPI spec validation failed: %w", err)
	}

	return nil
}

// Parse converts an OpenAPI specification to IR format
func (p *OpenAPIParser) Parse(ctx context.Context, data []byte) (*API, error) {
	// Load the OpenAPI specification
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	spec, err := loader.LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("failed to load OpenAPI spec: %w", err)
	}

	// Validate if requested
	if p.options.Validate {
		if err := spec.Validate(ctx); err != nil {
			if p.options.Strict {
				return nil, fmt.Errorf("OpenAPI spec validation failed: %w", err)
			}
			// Log warning but continue in non-strict mode
		}
	}

	// Convert to IR
	api := &API{
		Metadata:   p.parseMetadata(spec),
		Endpoints:  p.parseEndpoints(spec),
		DataModels: p.parseDataModels(spec),
		Security:   p.parseSecuritySchemes(spec),
		Servers:    p.parseServers(spec),
	}

	// Include extensions if requested
	if p.options.IncludeExtensions {
		api.Extensions = p.parseExtensions(spec)
	}

	return api, nil
}

// parseMetadata extracts metadata from OpenAPI spec
func (p *OpenAPIParser) parseMetadata(spec *openapi3.T) APIMetadata {
	metadata := APIMetadata{
		Type: APITypeREST,
	}

	if spec.Info != nil {
		metadata.Title = spec.Info.Title
		metadata.Description = spec.Info.Description
		metadata.Version = spec.Info.Version
		metadata.TermsOfService = spec.Info.TermsOfService

		if spec.Info.Contact != nil {
			metadata.Contact = &Contact{
				Name:  spec.Info.Contact.Name,
				URL:   spec.Info.Contact.URL,
				Email: spec.Info.Contact.Email,
			}
		}

		if spec.Info.License != nil {
			metadata.License = &License{
				Name: spec.Info.License.Name,
				URL:  spec.Info.License.URL,
			}
		}
	}

	// Extract tags
	if len(spec.Tags) > 0 {
		metadata.Tags = make([]string, len(spec.Tags))
		for i, tag := range spec.Tags {
			metadata.Tags[i] = tag.Name
		}
	}

	return metadata
}

// parseEndpoints extracts all endpoints/operations from the OpenAPI spec
func (p *OpenAPIParser) parseEndpoints(spec *openapi3.T) []Endpoint {
	endpoints := make([]Endpoint, 0)

	if spec.Paths == nil {
		return endpoints
	}

	for path, pathItem := range spec.Paths.Map() {
		if pathItem == nil {
			continue
		}

		// Process each HTTP method
		operations := map[string]*openapi3.Operation{
			"GET":     pathItem.Get,
			"POST":    pathItem.Post,
			"PUT":     pathItem.Put,
			"PATCH":   pathItem.Patch,
			"DELETE":  pathItem.Delete,
			"HEAD":    pathItem.Head,
			"OPTIONS": pathItem.Options,
			"TRACE":   pathItem.Trace,
		}

		for method, operation := range operations {
			if operation == nil {
				continue
			}

			endpoint := p.parseOperation(path, method, operation, pathItem.Parameters)
			endpoints = append(endpoints, endpoint)
		}
	}

	return endpoints
}

// parseOperation converts an OpenAPI operation to an IR endpoint
func (p *OpenAPIParser) parseOperation(path, method string, operation *openapi3.Operation, pathParams openapi3.Parameters) Endpoint {
	// Generate endpoint ID
	endpointID := operation.OperationID
	if endpointID == "" {
		endpointID = fmt.Sprintf("%s_%s", strings.ToLower(method), sanitizePath(path))
	}

	endpoint := Endpoint{
		ID:          endpointID,
		Name:        operation.Summary,
		Description: operation.Description,
		Type:        EndpointTypeHTTP,
		Protocol:    ProtocolHTTP,
		Method:      method,
		Path: PathInfo{
			Pattern: path,
		},
		Tags:       operation.Tags,
		Deprecated: operation.Deprecated,
	}

	// Parse parameters
	allParams := append(pathParams, operation.Parameters...)
	endpoint.Path.Parameters = p.parsePathParameters(allParams)

	if endpoint.Request == nil {
		endpoint.Request = &RequestSpec{}
	}
	endpoint.Request.QueryParameters = p.parseQueryParameters(allParams)
	endpoint.Request.HeaderParameters = p.parseHeaderParameters(allParams)
	endpoint.Request.CookieParameters = p.parseCookieParameters(allParams)

	// Parse request body
	if operation.RequestBody != nil {
		endpoint.Request.Body = p.parseRequestBody(operation.RequestBody)
		if operation.RequestBody.Value != nil && operation.RequestBody.Value.Content != nil {
			for contentType := range operation.RequestBody.Value.Content {
				endpoint.Request.ContentType = contentType
				break // Use first content type
			}
		}
	}

	// Parse responses
	if operation.Responses != nil {
		endpoint.Responses = p.parseResponses(operation.Responses)
	}

	// Parse security requirements
	if operation.Security != nil {
		endpoint.Security = p.parseSecurityRequirements(*operation.Security)
	}

	// Parse extensions
	if p.options.IncludeExtensions && len(operation.Extensions) > 0 {
		endpoint.Extensions = make(map[string]any)
		maps.Copy(endpoint.Extensions, operation.Extensions)
	}

	return endpoint
}

// parsePathParameters extracts path parameters
func (p *OpenAPIParser) parsePathParameters(params openapi3.Parameters) []Parameter {
	parameters := make([]Parameter, 0)

	for _, paramRef := range params {
		if paramRef == nil || paramRef.Value == nil {
			continue
		}

		param := paramRef.Value
		if param.In == "path" {
			parameters = append(parameters, p.convertParameter(param))
		}
	}

	return parameters
}

// parseQueryParameters extracts query parameters
func (p *OpenAPIParser) parseQueryParameters(params openapi3.Parameters) []Parameter {
	parameters := make([]Parameter, 0)

	for _, paramRef := range params {
		if paramRef == nil || paramRef.Value == nil {
			continue
		}

		param := paramRef.Value
		if param.In == "query" {
			parameters = append(parameters, p.convertParameter(param))
		}
	}

	return parameters
}

// parseHeaderParameters extracts header parameters
func (p *OpenAPIParser) parseHeaderParameters(params openapi3.Parameters) []Parameter {
	parameters := make([]Parameter, 0)

	for _, paramRef := range params {
		if paramRef == nil || paramRef.Value == nil {
			continue
		}

		param := paramRef.Value
		if param.In == "header" {
			parameters = append(parameters, p.convertParameter(param))
		}
	}

	return parameters
}

// parseCookieParameters extracts cookie parameters
func (p *OpenAPIParser) parseCookieParameters(params openapi3.Parameters) []Parameter {
	parameters := make([]Parameter, 0)

	for _, paramRef := range params {
		if paramRef == nil || paramRef.Value == nil {
			continue
		}

		param := paramRef.Value
		if param.In == "cookie" {
			parameters = append(parameters, p.convertParameter(param))
		}
	}

	return parameters
}

// convertParameter converts an OpenAPI parameter to IR format
func (p *OpenAPIParser) convertParameter(param *openapi3.Parameter) Parameter {
	parameter := Parameter{
		Name:        param.Name,
		In:          ParameterLocation(param.In),
		Description: param.Description,
		Required:    param.Required,
		Deprecated:  param.Deprecated,
	}

	if param.Schema != nil && param.Schema.Value != nil {
		parameter.Schema = p.convertSchemaToDataType(param.Schema.Value)
		parameter.Default = param.Schema.Value.Default

		if p.options.IncludeExamples && param.Example != nil {
			parameter.Example = param.Example
		}
	}

	return parameter
}

// parseRequestBody converts an OpenAPI request body to IR format
func (p *OpenAPIParser) parseRequestBody(requestBody *openapi3.RequestBodyRef) *DataModel {
	if requestBody == nil || requestBody.Value == nil || requestBody.Value.Content == nil {
		return nil
	}

	// Get first content type (usually application/json)
	for _, mediaType := range requestBody.Value.Content {
		if mediaType.Schema != nil && mediaType.Schema.Value != nil {
			return p.convertSchemaToDataModel(mediaType.Schema.Value, "")
		}
	}

	return nil
}

// parseResponses converts OpenAPI responses to IR format
func (p *OpenAPIParser) parseResponses(responses *openapi3.Responses) []ResponseSpec {
	responseSpecs := make([]ResponseSpec, 0)

	if responses == nil {
		return responseSpecs
	}

	for statusCode, responseRef := range responses.Map() {
		if responseRef == nil || responseRef.Value == nil {
			continue
		}

		response := responseRef.Value

		// Parse status code
		var code int
		if statusCode == "default" {
			code = 0
		} else {
			_, _ = fmt.Sscanf(statusCode, "%d", &code)
		}

		responseSpec := ResponseSpec{
			StatusCode:  code,
			Description: *response.Description,
			IsError:     code >= 400,
		}

		// Parse response body
		if response.Content != nil {
			for contentType, mediaType := range response.Content {
				responseSpec.ContentType = contentType
				if mediaType.Schema != nil && mediaType.Schema.Value != nil {
					responseSpec.Body = p.convertSchemaToDataModel(mediaType.Schema.Value, "")
				}
				break // Use first content type
			}
		}

		// Parse response headers
		if response.Headers != nil {
			responseSpec.Headers = make([]Parameter, 0, len(response.Headers))
			for headerName, headerRef := range response.Headers {
				if headerRef == nil || headerRef.Value == nil {
					continue
				}

				header := headerRef.Value
				param := Parameter{
					Name:        headerName,
					In:          ParameterLocationHeader,
					Description: header.Description,
					Required:    header.Required,
				}

				if header.Schema != nil && header.Schema.Value != nil {
					param.Schema = p.convertSchemaToDataType(header.Schema.Value)
				}

				responseSpec.Headers = append(responseSpec.Headers, param)
			}
		}

		responseSpecs = append(responseSpecs, responseSpec)
	}

	return responseSpecs
}

// parseDataModels extracts all schema definitions
func (p *OpenAPIParser) parseDataModels(spec *openapi3.T) []DataModel {
	models := make([]DataModel, 0)

	if spec.Components == nil || spec.Components.Schemas == nil {
		return models
	}

	for name, schemaRef := range spec.Components.Schemas {
		if schemaRef == nil || schemaRef.Value == nil {
			continue
		}

		model := p.convertSchemaToDataModel(schemaRef.Value, name)
		models = append(models, *model)
	}

	return models
}

// convertSchemaToDataModel converts an OpenAPI schema to a DataModel
func (p *OpenAPIParser) convertSchemaToDataModel(schema *openapi3.Schema, name string) *DataModel {
	if schema == nil {
		return nil
	}

	model := &DataModel{
		Name:        name,
		Description: schema.Description,
		Type:        p.convertSchemaToDataType(schema),
		Required:    schema.Required,
	}

	// Handle object properties
	if schema.Type.Is("object") && schema.Properties != nil {
		model.Properties = make([]Property, 0, len(schema.Properties))
		for propName, propSchemaRef := range schema.Properties {
			if propSchemaRef == nil || propSchemaRef.Value == nil {
				continue
			}

			propSchema := propSchemaRef.Value
			property := Property{
				Name:        propName,
				Description: propSchema.Description,
				Type:        p.convertSchemaToDataType(propSchema),
				Required:    contains(schema.Required, propName),
				Default:     propSchema.Default,
			}

			if p.options.IncludeExamples && propSchema.Example != nil {
				property.Example = propSchema.Example
			}

			// Add validation rules
			property.Validation = p.extractValidation(propSchema)

			model.Properties = append(model.Properties, property)
		}
	}

	// Handle array items
	if schema.Type.Is("array") && schema.Items != nil && schema.Items.Value != nil {
		model.Items = p.convertSchemaToDataType(schema.Items.Value)
	}

	model.AdditionalProperties = schema.AdditionalProperties.Has != nil && *schema.AdditionalProperties.Has

	if p.options.IncludeExamples && schema.Example != nil {
		model.Example = schema.Example
	}

	return model
}

// convertSchemaToDataType converts an OpenAPI schema to a DataType
func (p *OpenAPIParser) convertSchemaToDataType(schema *openapi3.Schema) *DataType {
	if schema == nil {
		return nil
	}

	dataType := &DataType{
		BaseType: p.getBaseType(schema),
		Format:   schema.Format,
		Nullable: schema.Nullable,
	}

	// Handle arrays
	if schema.Type.Is("array") && schema.Items != nil && schema.Items.Value != nil {
		dataType.Items = p.convertSchemaToDataType(schema.Items.Value)
	}

	// Handle enums
	if len(schema.Enum) > 0 {
		dataType.Enum = schema.Enum
	}

	return dataType
}

// getBaseType determines the base type from an OpenAPI schema
func (p *OpenAPIParser) getBaseType(schema *openapi3.Schema) string {
	if schema.Type == nil || len(*schema.Type) == 0 {
		// Check for common patterns
		if len(schema.Properties) > 0 {
			return "object"
		}
		if schema.Items != nil {
			return "array"
		}
		return "any"
	}

	// OpenAPI 3.1 supports multiple types, we'll use the first one
	types := *schema.Type
	if len(types) > 0 {
		return types[0]
	}

	return "any"
}

// extractValidation extracts validation rules from schema
func (p *OpenAPIParser) extractValidation(schema *openapi3.Schema) *Validation {
	validation := &Validation{}
	hasValidation := false

	// String validations
	if schema.MinLength > 0 {
		minLen := int(schema.MinLength)
		validation.MinLength = &minLen
		hasValidation = true
	}
	if schema.MaxLength != nil {
		maxLen := int(*schema.MaxLength)
		validation.MaxLength = &maxLen
		hasValidation = true
	}
	if schema.Pattern != "" {
		validation.Pattern = schema.Pattern
		hasValidation = true
	}

	// Number validations
	if schema.Min != nil {
		validation.Minimum = schema.Min
		hasValidation = true
	}
	if schema.Max != nil {
		validation.Maximum = schema.Max
		hasValidation = true
	}
	if schema.ExclusiveMin {
		validation.ExclusiveMinimum = true
		hasValidation = true
	}
	if schema.ExclusiveMax {
		validation.ExclusiveMaximum = true
		hasValidation = true
	}
	if schema.MultipleOf != nil {
		validation.MultipleOf = schema.MultipleOf
		hasValidation = true
	}

	// Array validations
	if schema.MinItems > 0 {
		minItems := int(schema.MinItems)
		validation.MinItems = &minItems
		hasValidation = true
	}
	if schema.MaxItems != nil {
		maxItems := int(*schema.MaxItems)
		validation.MaxItems = &maxItems
		hasValidation = true
	}
	if schema.UniqueItems {
		validation.UniqueItems = true
		hasValidation = true
	}

	// Object validations
	if schema.MinProps > 0 {
		minProps := int(schema.MinProps)
		validation.MinProperties = &minProps
		hasValidation = true
	}
	if schema.MaxProps != nil {
		maxProps := int(*schema.MaxProps)
		validation.MaxProperties = &maxProps
		hasValidation = true
	}

	if !hasValidation {
		return nil
	}

	return validation
}

// parseSecuritySchemes extracts security schemes
func (p *OpenAPIParser) parseSecuritySchemes(spec *openapi3.T) []SecurityScheme {
	schemes := make([]SecurityScheme, 0)

	if spec.Components == nil || spec.Components.SecuritySchemes == nil {
		return schemes
	}

	for name, schemeRef := range spec.Components.SecuritySchemes {
		if schemeRef == nil || schemeRef.Value == nil {
			continue
		}

		scheme := schemeRef.Value
		securityScheme := SecurityScheme{
			Type:        scheme.Type,
			Name:        name,
			Description: scheme.Description,
			In:          scheme.In,
			Scheme:      scheme.Scheme,
		}

		if scheme.Type == "http" && scheme.Scheme == "bearer" {
			securityScheme.BearerFormat = scheme.BearerFormat
		}

		if scheme.Type == "oauth2" && scheme.Flows != nil {
			securityScheme.Flows = p.parseOAuthFlows(scheme.Flows)
		}

		if scheme.Type == "openIdConnect" {
			securityScheme.OpenIDConnectURL = scheme.OpenIdConnectUrl
		}

		schemes = append(schemes, securityScheme)
	}

	return schemes
}

// parseOAuthFlows converts OpenAPI OAuth flows to IR format
func (p *OpenAPIParser) parseOAuthFlows(flows *openapi3.OAuthFlows) *OAuthFlows {
	if flows == nil {
		return nil
	}

	oauthFlows := &OAuthFlows{}

	if flows.Implicit != nil {
		oauthFlows.Implicit = &OAuthFlow{
			AuthorizationURL: flows.Implicit.AuthorizationURL,
			RefreshURL:       flows.Implicit.RefreshURL,
			Scopes:           flows.Implicit.Scopes,
		}
	}

	if flows.Password != nil {
		oauthFlows.Password = &OAuthFlow{
			TokenURL:   flows.Password.TokenURL,
			RefreshURL: flows.Password.RefreshURL,
			Scopes:     flows.Password.Scopes,
		}
	}

	if flows.ClientCredentials != nil {
		oauthFlows.ClientCredentials = &OAuthFlow{
			TokenURL:   flows.ClientCredentials.TokenURL,
			RefreshURL: flows.ClientCredentials.RefreshURL,
			Scopes:     flows.ClientCredentials.Scopes,
		}
	}

	if flows.AuthorizationCode != nil {
		oauthFlows.AuthorizationCode = &OAuthFlow{
			AuthorizationURL: flows.AuthorizationCode.AuthorizationURL,
			TokenURL:         flows.AuthorizationCode.TokenURL,
			RefreshURL:       flows.AuthorizationCode.RefreshURL,
			Scopes:           flows.AuthorizationCode.Scopes,
		}
	}

	return oauthFlows
}

// parseSecurityRequirements converts OpenAPI security requirements
func (p *OpenAPIParser) parseSecurityRequirements(security openapi3.SecurityRequirements) []SecurityRequirement {
	requirements := make([]SecurityRequirement, 0)

	for _, req := range security {
		for name, scopes := range req {
			requirements = append(requirements, SecurityRequirement{
				Name:   name,
				Scopes: scopes,
			})
		}
	}

	return requirements
}

// parseServers extracts server information
func (p *OpenAPIParser) parseServers(spec *openapi3.T) []Server {
	servers := make([]Server, 0, len(spec.Servers))

	for _, server := range spec.Servers {
		if server == nil {
			continue
		}

		srv := Server{
			URL:         server.URL,
			Description: server.Description,
		}

		if len(server.Variables) > 0 {
			srv.Variables = make(map[string]ServerVariable)
			for name, variable := range server.Variables {
				if variable == nil {
					continue
				}

				srv.Variables[name] = ServerVariable{
					Default:     variable.Default,
					Description: variable.Description,
					Enum:        variable.Enum,
				}
			}
		}

		servers = append(servers, srv)
	}

	return servers
}

// parseExtensions extracts custom extensions
func (p *OpenAPIParser) parseExtensions(spec *openapi3.T) map[string]any {
	extensions := make(map[string]any)

	if spec.Extensions != nil {
		maps.Copy(extensions, spec.Extensions)
	}

	return extensions
}

// Helper functions

func sanitizePath(path string) string {
	// Replace special characters with underscores
	result := strings.ReplaceAll(path, "/", "_")
	result = strings.ReplaceAll(result, "{", "")
	result = strings.ReplaceAll(result, "}", "")
	result = strings.Trim(result, "_")
	return result
}

func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}
