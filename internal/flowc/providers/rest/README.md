# FlowC REST API Server

This package provides the REST API server for FlowC, which accepts API deployment bundles (zip files containing API specifications and FlowC metadata) and translates them into Envoy xDS resources through the translator layer.

## Overview

The FlowC API server is a RESTful HTTP service that:
- Accepts API deployment bundles via multipart/form-data uploads
- Supports multiple API types (REST, gRPC, GraphQL, WebSocket, SSE) through the IR (Intermediate Representation) layer
- Validates and parses API specifications using pluggable parsers
- Generates Envoy xDS resources via the translator architecture
- Manages deployment lifecycle (create, read, update, delete)
- Provides health checks and deployment statistics

**Key Features:**
- Multi-API support (REST, gRPC, GraphQL, WebSocket, SSE)
- Clean separation of concerns (handlers → services → repositories)
- Pluggable repository pattern for different storage backends
- IR-based translation for consistent handling across API types
- Comprehensive error handling and logging
- Go 1.22+ HTTP mux with method-based routing

## Architecture

The server follows a layered architecture with clear separation of concerns:

```
┌─────────────────────────────────────────────────────────┐
│                    HTTP Layer                           │
│                  (server.go)                            │
│                                                         │
│  • Routes requests to handlers                         │
│  • Manages server lifecycle (start/stop)              │
│  • Configures timeouts and middleware                 │
└─────────────────────────────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│                  Handler Layer                          │
│                (handlers/)                              │
│                                                         │
│  • HTTP request/response handling                      │
│  • Input validation (file type, multipart form)       │
│  • JSON serialization/deserialization                  │
│  • Error response formatting                           │
└─────────────────────────────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│                  Service Layer                          │
│                (services/)                              │
│                                                         │
│  • Business logic                                      │
│  • Orchestrates bundle loading and translation        │
│  • Manages xDS cache updates                          │
│  • Deployment lifecycle management                     │
└─────────────────────────────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│                  Repository Layer                       │
│                (repository/)                            │
│                                                         │
│  • Data persistence abstraction                        │
│  • Deployment storage (CRUD operations)               │
│  • Node ID mapping (deployment ↔ Envoy node)         │
│  • Statistics aggregation                              │
└─────────────────────────────────────────────────────────┘
```

### Supporting Components

**Bundle Loader** (`loader/`)
- Extracts and validates zip bundles
- Auto-detects API type (OpenAPI, proto, GraphQL, AsyncAPI)
- Uses parser registry to convert specs to IR
- Provides type-safe helpers (`IsRESTAPI()`, `IsGRPCAPI()`, etc.)

**Models** (`models/`)
- Data transfer objects (DTOs) for API requests/responses
- `APIDeployment` - Persisted deployment metadata (no IR)
- `DeploymentStatus` - Deployment lifecycle states
- Request/response wrapper types

**Middleware** (`middleware/`)
- OpenAPI validation middleware (request/response)
- Extensible for auth, rate limiting, etc.

## Package Structure

```
internal/flowc/server/
├── server.go              # APIServer - main HTTP server
├── handlers/
│   ├── handlers.go        # Handlers struct and factory
│   ├── deployments.go     # Deployment endpoints
│   └── gateways.go        # Gateway endpoints (future)
├── services/
│   ├── services.go        # Services struct and factory
│   ├── deployments.go     # DeploymentService - business logic
│   └── gateways.go        # GatewayService (future)
├── loader/
│   └── loader.go          # BundleLoader - multi-API bundle parsing
├── models/
│   └── models.go          # DTOs (APIDeployment, requests, responses)
└── repository/
    ├── repository.go      # Repository interface
    ├── factory.go         # Repository factory
    ├── memory.go          # In-memory implementation
    └── README.md          # Repository documentation
```

## Key Components

### 1. APIServer (`server.go`)

The main HTTP server that manages the REST API lifecycle.

**Responsibilities:**
- Route configuration using Go 1.22+ method-based routing
- Server lifecycle management (start/stop)
- Dependency injection (services, handlers)
- HTTP server configuration (timeouts, ports)

**Key Methods:**
- `NewAPIServer()` - Creates server with dependencies
- `Start()` - Starts the HTTP server
- `Stop(ctx)` - Gracefully shuts down the server
- `setupRoutes()` - Configures HTTP routes

**Available Endpoints:**
```
GET  /health                           # Health check
GET  /                                 # API documentation
POST /api/v1/deployments              # Deploy API
GET  /api/v1/deployments              # List deployments
GET  /api/v1/deployments/{id}         # Get deployment
PUT  /api/v1/deployments/{id}         # Update deployment
DELETE /api/v1/deployments/{id}       # Delete deployment
GET  /api/v1/deployments/stats        # Deployment statistics
POST /api/v1/validate                 # Validate bundle
```

**Configuration:**
```go
server := NewAPIServer(
    port,            // API port (default: 8080)
    readTimeout,     // Read timeout (e.g., 30s)
    writeTimeout,    // Write timeout (e.g., 30s)
    idleTimeout,     // Idle timeout (e.g., 60s)
    configManager,   // xDS cache manager
    logger,          // Envoy logger
)
```

### 2. Handlers (`handlers/`)

HTTP request handlers that process incoming requests and format responses.

**Handlers Struct:**
```go
type Handlers struct {
    services  *services.Services
    logger    *logger.EnvoyLogger
    startTime time.Time
}
```

**Key Methods:**
- `DeployAPI()` - Handles API deployment uploads
- `GetDeployment()` - Retrieves a specific deployment
- `ListDeployments()` - Lists all deployments
- `UpdateDeployment()` - Updates an existing deployment
- `DeleteDeployment()` - Removes a deployment
- `GetDeploymentStats()` - Returns deployment statistics
- `HealthCheck()` - Returns health status
- `ValidateZip()` - Validates bundle without deploying

**Request Handling Flow:**
1. Parse multipart form (for file uploads)
2. Validate file type (must be zip)
3. Extract file data
4. Delegate to service layer
5. Format JSON response

**Error Handling:**
- HTTP 400 for invalid requests (bad file type, missing fields)
- HTTP 404 for not found resources
- HTTP 500 for internal errors
- Structured JSON error responses

### 3. Services (`services/`)

Business logic layer that orchestrates the deployment workflow.

**DeploymentService:**
```go
type DeploymentService struct {
    configManager *cache.ConfigManager
    bundleLoader  *loader.BundleLoader
    logger        *logger.EnvoyLogger
    repo          repository.Repository
}
```

**Key Methods:**
- `DeployAPI(zipData, description)` - Deploys a new API
- `GetDeployment(id)` - Retrieves deployment by ID
- `ListDeployments()` - Lists all deployments
- `UpdateDeployment(id, zipData)` - Updates existing deployment
- `DeleteDeployment(id)` - Removes deployment and xDS resources
- `GetDeploymentStats()` - Returns deployment statistics

**Deployment Flow (DeployAPI):**

```
1. Validate zip file
   └─> bundle.ValidateZip(zipData)

2. Load and parse bundle
   └─> bundleLoader.LoadBundle(zipData)
       ├─> Extract flowc.yaml (metadata)
       ├─> Extract spec file (openapi.yaml, *.proto, etc.)
       ├─> Auto-detect API type
       ├─> Parse spec to IR using appropriate parser
       └─> Return DeploymentBundle (metadata + IR + raw spec)

3. Create deployment record
   └─> bundle.ToAPIDeployment(id)
       └─> Creates APIDeployment from metadata (IR not persisted)

4. Store deployment in repository
   └─> repo.Create(deployment)
   └─> repo.SetNodeID(deploymentID, nodeID)

5. Generate xDS resources
   └─> Create translator strategies
       └─> factory.CreateStrategySet(config, deployment)
   └─> Create composite translator
       └─> translator.NewCompositeTranslator(strategies, options)
   └─> Translate to xDS
       └─> translator.Translate(ctx, deployment, IR, nodeID)
           └─> Returns XDSResources (clusters, routes, listeners, endpoints)

6. Deploy to xDS cache
   └─> configManager.DeployAPI(nodeID, xdsResources)

7. Update deployment status
   └─> repo.Update(deployment) with status="deployed"

8. Return deployment
```

**Update Flow:**
Similar to deploy but:
- Verifies deployment exists
- Retrieves existing node ID
- Sets status to "updating"
- Generates new xDS resources
- Updates xDS cache
- Updates deployment record

**Delete Flow:**
1. Retrieve deployment and node ID from repository
2. Remove from xDS cache (`configManager.RemoveNode(nodeID)`)
3. Delete from repository (`repo.Delete(id)`)
4. Clean up node ID mapping (`repo.DeleteNodeID(id)`)

### 4. Bundle Loader (`loader/`)

Handles loading and parsing of API deployment bundles with multi-API support.

**BundleLoader:**
```go
type BundleLoader struct {
    parserRegistry *ir.ParserRegistry
}
```

**DeploymentBundle:**
```go
type DeploymentBundle struct {
    FlowCMetadata *types.FlowCMetadata  // From flowc.yaml
    IR            *ir.API               // Unified IR (transient)
    Spec          []byte                // Raw spec file
}
```

**API Type Detection:**
1. **Explicit** - From `api_type` field in `flowc.yaml`:
   ```yaml
   api_type: "rest"     # or "grpc", "graphql", "websocket", "sse"
   spec_file: "openapi.yaml"
   ```

2. **Auto-detect** - From file extensions (backward compatibility):
   - `openapi.yaml`, `swagger.yaml` → REST
   - `*.proto` → gRPC
   - `*.graphql`, `*.gql` → GraphQL
   - `asyncapi.yaml` → WebSocket (default) or SSE

**Supported Spec Files:**
- REST: `openapi.yaml`, `openapi.yml`, `swagger.yaml`, `swagger.yml`
- gRPC: `*.proto`
- GraphQL: `*.graphql`, `*.gql`
- WebSocket/SSE: `asyncapi.yaml`, `asyncapi.yml`

**Bundle Helper Methods:**
```go
bundle.ToAPIDeployment(id)    // Create APIDeployment
bundle.GetIR()                 // Access IR
bundle.GetAPIType()            // Get API type
bundle.IsRESTAPI()             // Check if REST
bundle.IsGRPCAPI()             // Check if gRPC
bundle.IsGraphQLAPI()          // Check if GraphQL
bundle.IsWebSocketAPI()        // Check if WebSocket
bundle.IsSSEAPI()              // Check if SSE
```

**Parsing Flow:**
1. Extract zip files
2. Load `flowc.yaml` and validate required fields
3. Determine API type (explicit or auto-detect)
4. Get appropriate spec file
5. Use parser registry to parse spec to IR
6. Set gateway basepath from `context` field
7. Return DeploymentBundle with metadata, IR, and raw spec

### 5. Models (`models/`)

Data transfer objects for API requests and responses.

**APIDeployment** (Persisted):
```go
type APIDeployment struct {
    ID        string              `json:"id"`         // UUID
    Name      string              `json:"name"`       // From metadata
    Version   string              `json:"version"`    // From metadata
    Context   string              `json:"context"`    // Base path
    Status    string              `json:"status"`     // Deployment status
    CreatedAt time.Time           `json:"created_at"`
    UpdatedAt time.Time           `json:"updated_at"`
    Metadata  types.FlowCMetadata `json:"metadata"`   // Full metadata
}
```

**Important:** IR is NOT persisted. It's transient and only used during translation.

**DeploymentStatus:**
```go
const (
    StatusPending   = "pending"
    StatusDeploying = "deploying"
    StatusDeployed  = "deployed"
    StatusFailed    = "failed"
    StatusUpdating  = "updating"
    StatusDeleting  = "deleting"
    StatusDeleted   = "deleted"
)
```

**Request/Response Types:**
- `DeploymentRequest` - Deployment creation request
- `DeploymentResponse` - Single deployment response
- `ListDeploymentsResponse` - Multiple deployments response
- `GetDeploymentResponse` - Single deployment retrieval
- `DeleteDeploymentResponse` - Deletion confirmation
- `HealthResponse` - Health check response

### 6. Repository (`repository/`)

Data persistence abstraction following the repository pattern.

**Repository Interface:**
```go
type Repository interface {
    DeploymentRepository      // CRUD operations
    NodeMappingRepository     // Deployment ↔ Node ID mapping
    StatsRepository           // Deployment statistics
    Close() error
    Ping(ctx) error
}
```

**DeploymentRepository:**
- `Create(ctx, deployment)` - Store new deployment
- `Get(ctx, id)` - Retrieve by ID
- `Update(ctx, deployment)` - Modify existing
- `Delete(ctx, id)` - Remove deployment
- `List(ctx)` - Get all deployments
- `ListByStatus(ctx, status)` - Filter by status
- `Count(ctx)` - Total count
- `Exists(ctx, id)` - Check existence

**NodeMappingRepository:**
- `SetNodeID(ctx, deploymentID, nodeID)` - Map deployment to Envoy node
- `GetNodeID(ctx, deploymentID)` - Get node ID for deployment
- `DeleteNodeID(ctx, deploymentID)` - Remove mapping
- `GetDeploymentsByNodeID(ctx, nodeID)` - Get all deployments for a node

**StatsRepository:**
- `GetStats(ctx)` - Returns `DeploymentStats` with counts by status

**Standard Errors:**
```go
ErrNotFound          // Resource not found
ErrAlreadyExists     // Duplicate resource
ErrInvalidInput      // Invalid data
ErrConnectionFailed  // Storage connection issue
ErrTransactionFailed // Transaction error
```

**Current Implementation:**
- In-memory repository (default)
- Thread-safe with sync.RWMutex
- Suitable for development and testing

**Future Implementations:**
The repository pattern supports future backends:
- PostgreSQL
- MySQL
- Redis
- MongoDB

See `repository/README.md` for details on implementing custom repositories.

## Multi-API Support

The server supports multiple API types through the IR (Intermediate Representation) layer:

### Supported API Types

| API Type    | Spec File         | IR Endpoint Types                    | Status      |
|-------------|-------------------|--------------------------------------|-------------|
| REST        | `openapi.yaml`    | `EndpointTypeHTTP`                  | ✅ Complete |
| gRPC        | `*.proto`         | `EndpointTypeGRPCUnary`, etc.       | 🚧 Stub     |
| GraphQL     | `*.graphql`       | `EndpointTypeGraphQLQuery`, etc.    | 🚧 Stub     |
| WebSocket   | `asyncapi.yaml`   | `EndpointTypeWebSocket`             | 🚧 Stub     |
| SSE         | `asyncapi.yaml`   | `EndpointTypeSSE`                   | 🚧 Stub     |

### IR Flow

1. **Bundle Upload** → Extract spec file based on extension or `api_type` field
2. **Parser Selection** → Get parser from registry based on API type
3. **Parse to IR** → Convert spec to unified `ir.API` representation
4. **Translation** → Translator uses IR to generate xDS resources
5. **xDS Deployment** → Resources pushed to Envoy via cache manager

### API Type Configuration

**Explicit (Recommended):**
```yaml
# flowc.yaml
name: "my-api"
version: "1.0.0"
api_type: "rest"
spec_file: "openapi.yaml"
context: "/api/v1"
upstream:
  host: "backend.example.com"
  port: 8080
```

**Auto-detect (Backward Compatible):**
```
api-bundle.zip
├── flowc.yaml         # api_type field optional
└── openapi.yaml       # Detected as REST
```

### IR Benefits

1. **Unified Processing** - Same translation logic across API types
2. **Extensibility** - Add new API types by implementing parsers
3. **Consistency** - Common concepts (endpoints, methods, auth) across types
4. **Protocol-Specific Features** - Extensions and metadata for unique features

## Error Handling

### Error Categories

**Client Errors (4xx):**
- `400 Bad Request` - Invalid input (bad zip, missing files, invalid spec)
- `404 Not Found` - Deployment not found

**Server Errors (5xx):**
- `500 Internal Server Error` - Unexpected errors (translation failure, xDS update failure)

### Error Response Format

```json
{
  "success": false,
  "error": "descriptive error message"
}
```

### Service-Level Error Handling

Services wrap errors with context:
```go
return nil, fmt.Errorf("failed to parse zip file: %w", err)
```

Repository errors are checked and converted:
```go
if errors.Is(err, repository.ErrNotFound) {
    return nil, fmt.Errorf("deployment not found: %s", deploymentID)
}
```

### Logging

All errors are logged with context:
```go
logger.WithFields(map[string]interface{}{
    "deploymentID": id,
    "error": err.Error(),
}).Error("Failed to deploy API")
```

## Usage Examples

### Starting the Server

```go
package main

import (
    "context"
    "time"

    "github.com/flowc-labs/flowc/internal/flowc/providers/rest"
    "github.com/flowc-labs/flowc/internal/flowc/xds/cache"
    "github.com/flowc-labs/flowc/pkg/logger"
)

func main() {
    // Create logger
    log := logger.NewEnvoyLogger("flowc-api", "info")

    // Create xDS cache manager
    configManager := cache.NewConfigManager(log)

    // Create API server
    apiServer := server.NewAPIServer(
        8080,                    // port
        30*time.Second,          // read timeout
        30*time.Second,          // write timeout
        60*time.Second,          // idle timeout
        configManager,
        log,
    )

    // Start server
    if err := apiServer.Start(); err != nil {
        log.WithError(err).Fatal("Failed to start API server")
    }

    // Graceful shutdown
    // ... wait for signal ...
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    apiServer.Stop(ctx)
}
```

### Deploying an API via cURL

```bash
# Create a deployment bundle
cd examples/api-deployment
zip api-deployment.zip flowc.yaml openapi.yaml

# Deploy
curl -X POST http://localhost:8080/api/v1/deployments \
  -F "file=@api-deployment.zip" \
  -F "description=My API deployment"

# Response:
# {
#   "success": true,
#   "message": "API deployed successfully",
#   "deployment": {
#     "id": "550e8400-e29b-41d4-a716-446655440000",
#     "name": "petstore-api",
#     "version": "1.0.0",
#     "context": "/petstore",
#     "status": "deployed",
#     "created_at": "2024-01-15T10:30:00Z",
#     "updated_at": "2024-01-15T10:30:00Z"
#   }
# }
```

### Listing Deployments

```bash
curl http://localhost:8080/api/v1/deployments

# Response:
# {
#   "success": true,
#   "deployments": [
#     {
#       "id": "550e8400-e29b-41d4-a716-446655440000",
#       "name": "petstore-api",
#       "version": "1.0.0",
#       "context": "/petstore",
#       "status": "deployed",
#       ...
#     }
#   ],
#   "total": 1
# }
```

### Getting a Specific Deployment

```bash
curl http://localhost:8080/api/v1/deployments/550e8400-e29b-41d4-a716-446655440000

# Response:
# {
#   "success": true,
#   "deployment": {
#     "id": "550e8400-e29b-41d4-a716-446655440000",
#     "name": "petstore-api",
#     ...
#   }
# }
```

### Updating a Deployment

```bash
# Create updated bundle
zip api-deployment-v2.zip flowc.yaml openapi.yaml

# Update
curl -X PUT http://localhost:8080/api/v1/deployments/550e8400-e29b-41d4-a716-446655440000 \
  -F "file=@api-deployment-v2.zip"
```

### Deleting a Deployment

```bash
curl -X DELETE http://localhost:8080/api/v1/deployments/550e8400-e29b-41d4-a716-446655440000

# Response:
# {
#   "success": true,
#   "message": "Deployment deleted successfully"
# }
```

### Validating a Bundle

```bash
curl -X POST http://localhost:8080/api/v1/validate \
  -F "file=@api-deployment.zip"

# Response:
# {
#   "success": true,
#   "message": "Zip file is valid",
#   "files": ["flowc.yaml", "openapi.yaml"]
# }
```

### Health Check

```bash
curl http://localhost:8080/health

# Response:
# {
#   "status": "healthy",
#   "timestamp": "2024-01-15T10:30:00Z",
#   "version": "1.0.0",
#   "uptime": "2h30m15s"
# }
```

## Testing

### Unit Testing

Test individual components in isolation:

```go
func TestDeploymentService_DeployAPI(t *testing.T) {
    // Create mocks
    mockRepo := repository.NewMemoryRepository()
    mockConfigManager := cache.NewConfigManager(logger)
    mockLogger := logger.NewEnvoyLogger("test", "info")

    // Create service
    service := NewDeploymentServiceWithRepository(
        mockConfigManager,
        mockLogger,
        mockRepo,
    )

    // Test deployment
    zipData := loadTestZip(t, "testdata/valid-api.zip")
    deployment, err := service.DeployAPI(zipData, "test deployment")

    assert.NoError(t, err)
    assert.NotNil(t, deployment)
    assert.Equal(t, "deployed", deployment.Status)
}
```

### Integration Testing

Test complete API flows:

```go
func TestAPIServer_DeploymentFlow(t *testing.T) {
    // Start test server
    server := setupTestServer(t)
    defer server.Stop(context.Background())

    // Deploy API
    zipData := loadTestZip(t, "testdata/petstore.zip")
    resp := postDeployment(t, server, zipData)
    
    assert.Equal(t, http.StatusCreated, resp.StatusCode)
    
    // Verify deployment exists
    deployment := getDeployment(t, server, resp.DeploymentID)
    assert.Equal(t, "deployed", deployment.Status)
}
```

### Test Data

Example test bundle structure:
```
testdata/
├── valid-rest-api.zip
│   ├── flowc.yaml
│   └── openapi.yaml
├── valid-grpc-api.zip
│   ├── flowc.yaml
│   └── service.proto
├── invalid-missing-metadata.zip
│   └── openapi.yaml
└── invalid-bad-spec.zip
    ├── flowc.yaml
    └── invalid.yaml
```

## Configuration

The server is configured via the control plane config file (`flowc-config.yaml`):

```yaml
# API Server Configuration
api:
  port: 8080
  read_timeout: "30s"
  write_timeout: "30s"
  idle_timeout: "60s"
  max_upload_size: "32MB"

# xDS Server Configuration
xds:
  port: 18000
  # ...

# Logging
logging:
  level: "info"
  format: "json"

# Repository (optional - defaults to in-memory)
repository:
  type: "memory"  # or "postgres", "mysql", "redis", "mongodb"
  # connection_string: "postgres://..."
  # max_connections: 10
```

### Environment Variables

Override config with environment variables:
```bash
FLOWC_API_PORT=8080
FLOWC_API_READ_TIMEOUT=30s
FLOWC_LOG_LEVEL=debug
```

See `internal/flowc/config/README.md` for full configuration documentation.

## Dependencies

### Internal Dependencies
- `internal/flowc/config` - Configuration management
- `internal/flowc/ir` - Intermediate Representation layer
- `internal/flowc/xds/cache` - xDS cache manager
- `internal/flowc/xds/translator` - xDS translation
- `pkg/bundle` - Bundle validation
- `pkg/logger` - Envoy-compatible logging
- `pkg/openapi` - OpenAPI utilities
- `pkg/types` - Shared types (FlowCMetadata, etc.)

### External Dependencies
- `github.com/google/uuid` - UUID generation
- `gopkg.in/yaml.v3` - YAML parsing
- `github.com/getkin/kin-openapi` - OpenAPI validation (middleware)

## Best Practices

### 1. Always Use Repository Interface

Never depend on concrete repository implementations in services:

```go
// ✅ Good
func NewDeploymentService(configManager *cache.ConfigManager, logger *logger.EnvoyLogger) *DeploymentService {
    return NewDeploymentServiceWithRepository(
        configManager,
        logger,
        repository.NewDefaultRepository(),  // Factory method
    )
}

// ❌ Bad
func NewDeploymentService() *DeploymentService {
    return &DeploymentService{
        repo: &MemoryRepository{},  // Concrete type
    }
}
```

### 2. Handle Repository Errors Consistently

Always check for standard errors:

```go
deployment, err := s.repo.Get(ctx, id)
if err != nil {
    if errors.Is(err, repository.ErrNotFound) {
        return nil, fmt.Errorf("deployment not found: %s", id)
    }
    return nil, fmt.Errorf("failed to get deployment: %w", err)
}
```

### 3. Use Context for Cancellation

Always pass context to repository methods:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

deployment, err := repo.Get(ctx, id)
```

### 4. Log with Context

Include relevant fields in logs:

```go
logger.WithFields(map[string]interface{}{
    "deploymentID": deployment.ID,
    "apiName":      deployment.Name,
    "apiVersion":   deployment.Version,
}).Info("API deployment completed successfully")
```

### 5. Use IR for Multi-API Support

Always work with IR in translators:

```go
// ✅ Good - Uses IR
xdsResources, err := translator.Translate(ctx, deployment, bundle.IR, nodeID)

// ❌ Bad - Direct OpenAPI (limits to REST only)
xdsResources, err := translator.TranslateOpenAPI(deployment.OpenAPISpec)
```

### 6. Validate Early

Validate inputs at the handler level:

```go
if !isZip {
    h.writeErrorResponse(w, http.StatusBadRequest, "File must be a zip archive")
    return
}
```

### 7. Keep IR Transient

Never persist IR in the database:

```go
// ✅ Good - IR not in APIDeployment
type APIDeployment struct {
    ID       string
    Metadata types.FlowCMetadata  // Persisted
}

// ❌ Bad - Don't do this
type APIDeployment struct {
    ID       string
    Metadata types.FlowCMetadata
    IR       *ir.API  // Don't persist IR!
}
```

## Troubleshooting

### Issue: Deployment fails with "zip validation failed"

**Cause:** Invalid zip file or missing required files

**Solution:**
1. Verify zip contains `flowc.yaml`
2. Verify zip contains spec file (e.g., `openapi.yaml`)
3. Use validation endpoint to check:
   ```bash
   curl -X POST http://localhost:8080/api/v1/validate -F "file=@bundle.zip"
   ```

### Issue: Deployment fails with "failed to parse specification"

**Cause:** Invalid API spec or unsupported API type

**Solution:**
1. Validate your API spec (e.g., validate OpenAPI with Swagger Editor)
2. Check API type is supported (REST fully supported, others are stubs)
3. Verify spec file matches `api_type` in `flowc.yaml`
4. Check server logs for detailed parser errors

### Issue: "deployment not found" after creation

**Cause:** In-memory repository cleared or server restarted

**Solution:**
- In-memory repository is ephemeral
- For persistence, implement database-backed repository
- See `repository/README.md` for custom implementations

### Issue: xDS resources not updating in Envoy

**Cause:** Node ID mismatch or cache manager issue

**Solution:**
1. Verify Envoy node ID matches deployment node ID
2. Check xDS server logs for connection issues
3. Verify cache manager is receiving updates:
   ```bash
   curl http://localhost:8080/api/v1/deployments/stats
   ```

### Issue: "Failed to create strategy set"

**Cause:** Invalid strategy configuration in `flowc.yaml`

**Solution:**
1. Verify strategy names are valid
2. Check strategy-specific configuration
3. See `internal/flowc/xds/translator/README.md` for strategy documentation

## Future Enhancements

### Short Term
- [ ] Complete gRPC, GraphQL, WebSocket, SSE parsers
- [ ] Authentication/authorization middleware
- [ ] Rate limiting support
- [ ] Deployment versioning and rollback

### Medium Term
- [ ] Database-backed repositories (PostgreSQL, MySQL)
- [ ] Multi-node xDS support (multiple Envoy instances)
- [ ] Deployment health checks and status tracking
- [ ] WebSocket for real-time deployment updates

### Long Term
- [ ] Distributed deployment storage (Redis, MongoDB)
- [ ] Multi-tenant support
- [ ] Deployment templates and pipelines
- [ ] Observability integrations (Prometheus, Grafana)

## Related Documentation

- [Intermediate Representation (IR)](../ir/README.md) - Multi-API IR layer
- [Translator Architecture](../xds/translator/README.md) - xDS translation
- [Repository Pattern](repository/README.md) - Data persistence
- [Configuration Guide](../config/README.md) - Control plane configuration
- [Bundle Package](../../../pkg/bundle/README.md) - Bundle utilities

## Contributing

When adding new features to the server:

1. **Follow the layered architecture** - Keep concerns separated
2. **Use repository interface** - Don't depend on concrete implementations
3. **Support multi-API** - Work with IR, not OpenAPI directly
4. **Add tests** - Unit tests for services, integration tests for endpoints
5. **Update documentation** - Keep this README in sync with changes
6. **Log appropriately** - Include context in log messages

## License

Copyright © 2024 FlowC Labs. All rights reserved.

