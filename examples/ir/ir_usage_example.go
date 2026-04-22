package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/flowc-labs/flowc/internal/flowc/ir"
)

func main() {
	fmt.Println("FlowC IR (Intermediate Representation) Example")
	fmt.Println("=" + repeat("=", 45))
	fmt.Println()

	// Example 1: Parse OpenAPI specification to IR
	example1_ParseOpenAPI()

	fmt.Println()

	// Example 2: Use Parser Registry
	example2_ParserRegistry()

	fmt.Println()

	// Example 3: Inspect IR Structure
	example3_InspectIR()
}

// Example 1: Parse OpenAPI specification to IR
func example1_ParseOpenAPI() {
	fmt.Println("Example 1: Parse OpenAPI to IR")
	fmt.Println("-" + repeat("-", 30))

	ctx := context.Background()

	// Read OpenAPI specification
	openapiData, err := os.ReadFile("rest-example/openapi.yaml")
	if err != nil {
		fmt.Printf("Error reading OpenAPI file: %v\n", err)
		return
	}

	// Create OpenAPI parser
	parser := ir.NewOpenAPIParser()

	// Parse to IR
	api, err := parser.Parse(ctx, openapiData)
	if err != nil {
		fmt.Printf("Error parsing OpenAPI: %v\n", err)
		return
	}

	// Display basic information
	fmt.Printf("API Title: %s\n", api.Metadata.Title)
	fmt.Printf("API Version: %s\n", api.Metadata.Version)
	fmt.Printf("API Type: %s\n", api.Metadata.Type)
	fmt.Printf("Description: %s\n", api.Metadata.Description)
	fmt.Printf("Number of Endpoints: %d\n", len(api.Endpoints))
	fmt.Printf("Number of Data Models: %d\n", len(api.DataModels))
	fmt.Printf("Number of Security Schemes: %d\n", len(api.Security))
}

// Example 2: Use Parser Registry
func example2_ParserRegistry() {
	fmt.Println("Example 2: Use Parser Registry")
	fmt.Println("-" + repeat("-", 30))

	ctx := context.Background()

	// Create registry with all supported parsers
	registry := ir.DefaultParserRegistry()

	// List supported API types
	fmt.Println("Supported API Types:")
	for _, apiType := range registry.SupportedTypes() {
		parser, _ := registry.GetParser(apiType)
		formats := parser.SupportedFormats()
		fmt.Printf("  - %s (formats: %v)\n", apiType, formats)
	}

	fmt.Println()

	// Parse using registry
	openapiData, err := os.ReadFile("rest-example/openapi.yaml")
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return
	}

	api, err := registry.Parse(ctx, ir.APITypeREST, openapiData)
	if err != nil {
		fmt.Printf("Error parsing: %v\n", err)
		return
	}

	fmt.Printf("Successfully parsed %s API: %s\n", api.Metadata.Type, api.Metadata.Title)
}

// Example 3: Inspect IR Structure
func example3_InspectIR() {
	fmt.Println("Example 3: Inspect IR Structure")
	fmt.Println("-" + repeat("-", 30))

	ctx := context.Background()

	openapiData, err := os.ReadFile("rest-example/openapi.yaml")
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return
	}

	parser := ir.NewOpenAPIParser()
	api, err := parser.Parse(ctx, openapiData)
	if err != nil {
		fmt.Printf("Error parsing: %v\n", err)
		return
	}

	// Inspect endpoints
	fmt.Println("Endpoints:")
	for i, endpoint := range api.Endpoints {
		fmt.Printf("\n[%d] %s %s\n", i+1, endpoint.Method, endpoint.Path.Pattern)
		fmt.Printf("    ID: %s\n", endpoint.ID)
		fmt.Printf("    Type: %s\n", endpoint.Type)
		fmt.Printf("    Protocol: %s\n", endpoint.Protocol)
		if endpoint.Name != "" {
			fmt.Printf("    Name: %s\n", endpoint.Name)
		}
		if endpoint.Description != "" {
			fmt.Printf("    Description: %s\n", limitString(endpoint.Description, 60))
		}
		if len(endpoint.Tags) > 0 {
			fmt.Printf("    Tags: %v\n", endpoint.Tags)
		}

		// Request details
		if endpoint.Request != nil {
			if endpoint.Request.ContentType != "" {
				fmt.Printf("    Request Content-Type: %s\n", endpoint.Request.ContentType)
			}
			if len(endpoint.Request.QueryParameters) > 0 {
				fmt.Printf("    Query Parameters: %d\n", len(endpoint.Request.QueryParameters))
				for _, param := range endpoint.Request.QueryParameters {
					fmt.Printf("      - %s (%s)%s\n",
						param.Name,
						param.Schema.BaseType,
						requiredFlag(param.Required))
				}
			}
			if len(endpoint.Path.Parameters) > 0 {
				fmt.Printf("    Path Parameters: %d\n", len(endpoint.Path.Parameters))
				for _, param := range endpoint.Path.Parameters {
					fmt.Printf("      - %s (%s)%s\n",
						param.Name,
						param.Schema.BaseType,
						requiredFlag(param.Required))
				}
			}
		}

		// Response details
		if len(endpoint.Responses) > 0 {
			fmt.Printf("    Responses:\n")
			for _, response := range endpoint.Responses {
				status := fmt.Sprintf("%d", response.StatusCode)
				if response.StatusCode == 0 {
					status = "default"
				}
				fmt.Printf("      - %s: %s\n",
					status,
					limitString(response.Description, 50))
			}
		}
	}

	// Inspect data models
	if len(api.DataModels) > 0 {
		fmt.Println("\nData Models:")
		for i, model := range api.DataModels {
			fmt.Printf("\n[%d] %s\n", i+1, model.Name)
			if model.Description != "" {
				fmt.Printf("    Description: %s\n", limitString(model.Description, 60))
			}
			if model.Type != nil {
				fmt.Printf("    Type: %s\n", model.Type.BaseType)
			}
			if len(model.Properties) > 0 {
				fmt.Printf("    Properties: %d\n", len(model.Properties))
				for _, prop := range model.Properties {
					fmt.Printf("      - %s: %s%s\n",
						prop.Name,
						prop.Type.BaseType,
						requiredFlag(prop.Required))
				}
			}
		}
	}

	// Inspect security schemes
	if len(api.Security) > 0 {
		fmt.Println("\nSecurity Schemes:")
		for i, scheme := range api.Security {
			fmt.Printf("[%d] %s (%s)\n", i+1, scheme.Name, scheme.Type)
			if scheme.Description != "" {
				fmt.Printf("    Description: %s\n", limitString(scheme.Description, 60))
			}
		}
	}

	// Inspect servers
	if len(api.Servers) > 0 {
		fmt.Println("\nServers:")
		for i, server := range api.Servers {
			fmt.Printf("[%d] %s\n", i+1, server.URL)
			if server.Description != "" {
				fmt.Printf("    Description: %s\n", server.Description)
			}
		}
	}
}

// Helper functions

func repeat(s string, count int) string {
	var result strings.Builder
	for range count {
		result.WriteString(s)
	}
	return result.String()
}

func limitString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func requiredFlag(required bool) string {
	if required {
		return " [required]"
	}
	return ""
}
