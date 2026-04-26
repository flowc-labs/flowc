# IR (Intermediate Representation) Examples

This directory contains examples demonstrating FlowC's IR layer for multi-protocol API support.

## What is IR?

The IR (Intermediate Representation) is FlowC's unified abstraction for different API types. It allows FlowC to work with REST, gRPC, GraphQL, WebSocket, and SSE APIs in a consistent manner.

## Examples

### 1. REST API (OpenAPI) - WORKING ✅

The most common use case, fully implemented and tested.

**Bundle Structure:**
```
rest-api-bundle.zip
├── flowc.yaml
└── openapi.yaml
```

**flowc.yaml:**
```yaml
name: "user-api"
version: "1.0.0"
description: "User management REST API"
context: "api/users"
api_type: "rest"           # Specifies REST API
spec_file: "openapi.yaml"  # Points to OpenAPI spec

gateway:
  node_id: "gateway-node-1"
  listener: "http"
  virtual_host:
    name: "api"
    domains: ["*"]

upstream:
  host: "user-service.local"
  port: 8080
  scheme: "http"
  timeout: "30s"
```

**openapi.yaml:**
```yaml
openapi: 3.0.0
info:
  title: User API
  version: 1.0.0

paths:
  /users:
    get:
      operationId: listUsers
      summary: List all users
      responses:
        '200':
          description: List of users
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/User'
  
  /users/{id}:
    get:
      operationId: getUser
      summary: Get user by ID
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        '200':
          description: User details
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/User'

components:
  schemas:
    User:
      type: object
      required:
        - id
        - email
      properties:
        id:
          type: string
        email:
          type: string
          format: email
        name:
          type: string
```

**What happens:**
1. Bundle loader detects `api_type: "rest"`
2. Loads `openapi.yaml`
3. OpenAPIParser converts to IR:
   - `GET /users` → Endpoint{Type: EndpointTypeHTTP, Method: "GET", Path: "/users"}
   - `GET /users/{id}` → Endpoint{Type: EndpointTypeHTTP, Method: "GET", Path: "/users/{id}"}
   - User schema → DataModel{Name: "User", Properties: [...]}
4. Translator generates Envoy xDS resources
5. Envoy routes traffic to upstream

### 2. gRPC API (Protobuf) - FUTURE 🔜

Support for high-performance gRPC services.

**Bundle Structure:**
```
grpc-api-bundle.zip
├── flowc.yaml
└── user_service.proto
```

**flowc.yaml:**
```yaml
name: "user-grpc-api"
version: "1.0.0"
description: "User management gRPC API"
context: "grpc/users"
api_type: "grpc"               # Specifies gRPC API
spec_file: "user_service.proto" # Points to protobuf file

gateway:
  node_id: "gateway-node-1"
  listener: "http2"
  virtual_host:
    name: "grpc-api"
    domains: ["grpc.example.com"]

upstream:
  host: "user-service.local"
  port: 50051
  scheme: "http2"
  timeout: "30s"
```

**user_service.proto:**
```protobuf
syntax = "proto3";

package user.v1;

service UserService {
  // Unary RPC
  rpc GetUser (GetUserRequest) returns (UserResponse);
  
  // Server streaming RPC
  rpc ListUsers (ListUsersRequest) returns (stream UserResponse);
  
  // Client streaming RPC
  rpc CreateUsers (stream CreateUserRequest) returns (CreateUsersResponse);
  
  // Bidirectional streaming RPC
  rpc Chat (stream ChatMessage) returns (stream ChatMessage);
}

message GetUserRequest {
  string id = 1;
}

message UserResponse {
  string id = 1;
  string email = 2;
  string name = 3;
}

message ListUsersRequest {
  int32 page_size = 1;
  string page_token = 2;
}

message CreateUserRequest {
  string email = 1;
  string name = 2;
}

message CreateUsersResponse {
  int32 count = 1;
}

message ChatMessage {
  string user_id = 1;
  string content = 2;
  int64 timestamp = 3;
}
```

**What will happen:**
1. Bundle loader detects `api_type: "grpc"`
2. Loads `user_service.proto`
3. GRPCParser converts to IR:
   - `GetUser` RPC → Endpoint{Type: EndpointTypeGRPCUnary, Method: "GetUser"}
   - `ListUsers` RPC → Endpoint{Type: EndpointTypeGRPCServerStream, Method: "ListUsers"}
   - `CreateUsers` RPC → Endpoint{Type: EndpointTypeGRPCClientStream, Method: "CreateUsers"}
   - `Chat` RPC → Endpoint{Type: EndpointTypeGRPCBidirectional, Method: "Chat"}
   - Protobuf messages → DataModels
4. Translator generates xDS with gRPC support:
   - HTTP/2 listener
   - gRPC routing
   - Streaming support
5. Envoy proxies gRPC traffic

### 3. GraphQL API - FUTURE 🔜

Support for GraphQL query language APIs.

**Bundle Structure:**
```
graphql-api-bundle.zip
├── flowc.yaml
└── schema.graphql
```

**flowc.yaml:**
```yaml
name: "user-graphql-api"
version: "1.0.0"
description: "User management GraphQL API"
context: "graphql"
api_type: "graphql"         # Specifies GraphQL API
spec_file: "schema.graphql"  # Points to GraphQL schema

gateway:
  node_id: "gateway-node-1"
  listener: "http"
  virtual_host:
    name: "graphql-api"
    domains: ["api.example.com"]

upstream:
  host: "graphql-service.local"
  port: 4000
  scheme: "http"
  timeout: "60s"

# GraphQL-specific extensions
extensions:
  graphql:
    query_depth_limit: 10
    query_complexity_limit: 100
    enable_introspection: true
```

**schema.graphql:**
```graphql
type Query {
  """Get user by ID"""
  user(id: ID!): User
  
  """List all users"""
  users(limit: Int, offset: Int): [User!]!
  
  """Search users"""
  searchUsers(query: String!): [User!]!
}

type Mutation {
  """Create a new user"""
  createUser(input: CreateUserInput!): User!
  
  """Update user"""
  updateUser(id: ID!, input: UpdateUserInput!): User!
  
  """Delete user"""
  deleteUser(id: ID!): Boolean!
}

type Subscription {
  """Subscribe to user updates"""
  userUpdated(id: ID!): User!
  
  """Subscribe to new users"""
  userCreated: User!
}

type User {
  id: ID!
  email: String!
  name: String
  createdAt: DateTime!
  posts: [Post!]!
}

type Post {
  id: ID!
  title: String!
  content: String
  author: User!
}

input CreateUserInput {
  email: String!
  name: String
}

input UpdateUserInput {
  email: String
  name: String
}

scalar DateTime
```

**What will happen:**
1. Bundle loader detects `api_type: "graphql"`
2. Loads `schema.graphql`
3. GraphQLParser converts to IR:
   - Query fields → Endpoint{Type: EndpointTypeGraphQLQuery, Path: "/graphql"}
   - Mutation fields → Endpoint{Type: EndpointTypeGraphQLMutation, Path: "/graphql"}
   - Subscription fields → Endpoint{Type: EndpointTypeGraphQLSubscription, Path: "/graphql"}
   - GraphQL types → DataModels
4. Translator generates xDS:
   - Single `/graphql` endpoint
   - POST method support
   - WebSocket for subscriptions
5. Envoy routes to GraphQL server

### 4. WebSocket API (AsyncAPI) - FUTURE 🔜

Support for real-time WebSocket connections.

**Bundle Structure:**
```
websocket-api-bundle.zip
├── flowc.yaml
└── asyncapi.yaml
```

**flowc.yaml:**
```yaml
name: "chat-websocket-api"
version: "1.0.0"
description: "Real-time chat WebSocket API"
context: "ws/chat"
api_type: "websocket"      # Specifies WebSocket API
spec_file: "asyncapi.yaml"  # Points to AsyncAPI spec

gateway:
  node_id: "gateway-node-1"
  listener: "http"
  virtual_host:
    name: "ws-api"
    domains: ["ws.example.com"]

upstream:
  host: "chat-service.local"
  port: 8080
  scheme: "http"
  timeout: "300s"  # Long timeout for WebSocket

# WebSocket-specific extensions
extensions:
  websocket:
    max_frame_size: 65536
    idle_timeout: "600s"
```

**asyncapi.yaml:**
```yaml
asyncapi: 2.6.0
info:
  title: Chat WebSocket API
  version: 1.0.0
  description: Real-time chat messaging

servers:
  production:
    url: ws://chat-service.local:8080
    protocol: ws

channels:
  /chat/{roomId}:
    description: Chat room messages
    parameters:
      roomId:
        description: Chat room identifier
        schema:
          type: string
    
    subscribe:
      operationId: receiveMessage
      description: Receive messages from the chat room
      message:
        $ref: '#/components/messages/ChatMessage'
    
    publish:
      operationId: sendMessage
      description: Send a message to the chat room
      message:
        $ref: '#/components/messages/ChatMessage'
  
  /presence/{userId}:
    description: User presence updates
    parameters:
      userId:
        description: User identifier
        schema:
          type: string
    
    subscribe:
      operationId: receivePresence
      description: Receive user presence updates
      message:
        $ref: '#/components/messages/PresenceUpdate'

components:
  messages:
    ChatMessage:
      payload:
        type: object
        required:
          - userId
          - content
          - timestamp
        properties:
          userId:
            type: string
          content:
            type: string
          timestamp:
            type: integer
            format: int64
          roomId:
            type: string
    
    PresenceUpdate:
      payload:
        type: object
        required:
          - userId
          - status
        properties:
          userId:
            type: string
          status:
            type: string
            enum: [online, offline, away]
          lastSeen:
            type: integer
            format: int64
```

**What will happen:**
1. Bundle loader detects `api_type: "websocket"`
2. Loads `asyncapi.yaml`
3. AsyncAPIParser converts to IR:
   - `/chat/{roomId}` subscribe → Endpoint{Type: EndpointTypeWebSocket, Method: "SUBSCRIBE"}
   - `/chat/{roomId}` publish → Endpoint{Type: EndpointTypeWebSocket, Method: "PUBLISH"}
   - Message payloads → DataModels
4. Translator generates xDS:
   - HTTP/1.1 listener
   - WebSocket upgrade support
   - Route to upstream
5. Envoy handles WebSocket upgrade and proxying

### 5. Server-Sent Events (SSE) API - FUTURE 🔜

Support for server push notifications via SSE.

**Bundle Structure:**
```
sse-api-bundle.zip
├── flowc.yaml
└── asyncapi.yaml
```

**flowc.yaml:**
```yaml
name: "notifications-sse-api"
version: "1.0.0"
description: "Server-sent events for notifications"
context: "events"
api_type: "sse"            # Specifies SSE API
spec_file: "asyncapi.yaml"  # Points to AsyncAPI spec

gateway:
  node_id: "gateway-node-1"
  listener: "http"
  virtual_host:
    name: "events-api"
    domains: ["events.example.com"]

upstream:
  host: "notification-service.local"
  port: 8080
  scheme: "http"
  timeout: "300s"  # Long timeout for SSE
```

**asyncapi.yaml:**
```yaml
asyncapi: 2.6.0
info:
  title: Notifications SSE API
  version: 1.0.0
  description: Server-sent events for real-time notifications

servers:
  production:
    url: http://notification-service.local:8080
    protocol: sse

channels:
  /notifications/{userId}:
    description: User-specific notifications
    parameters:
      userId:
        description: User identifier
        schema:
          type: string
    
    subscribe:
      operationId: receiveNotifications
      description: Receive real-time notifications
      message:
        $ref: '#/components/messages/Notification'
    
    bindings:
      http:
        type: sse

components:
  messages:
    Notification:
      payload:
        type: object
        required:
          - id
          - type
          - timestamp
        properties:
          id:
            type: string
          type:
            type: string
            enum: [message, alert, system]
          title:
            type: string
          body:
            type: string
          timestamp:
            type: integer
            format: int64
```

## Using the IR in Code

### Parsing an API Specification

```go
package main

import (
    "context"
    "fmt"
    "os"
    
    "github.com/flowc-labs/flowc/internal/flowc/ir"
)

func main() {
    ctx := context.Background()
    
    // Read OpenAPI specification
    openapiData, err := os.ReadFile("openapi.yaml")
    if err != nil {
        panic(err)
    }
    
    // Parse to IR
    parser := ir.NewOpenAPIParser()
    api, err := parser.Parse(ctx, openapiData)
    if err != nil {
        panic(err)
    }
    
    // Inspect the IR
    fmt.Printf("API: %s v%s\n", api.Metadata.Title, api.Metadata.Version)
    fmt.Printf("Type: %s\n", api.Metadata.Type)
    fmt.Printf("Endpoints: %d\n", len(api.Endpoints))
    
    // Iterate through endpoints
    for _, endpoint := range api.Endpoints {
        fmt.Printf("\n%s %s\n", endpoint.Method, endpoint.Path.Pattern)
        fmt.Printf("  Type: %s\n", endpoint.Type)
        fmt.Printf("  Description: %s\n", endpoint.Description)
        
        if endpoint.Request != nil {
            fmt.Printf("  Request: %s\n", endpoint.Request.ContentType)
        }
        
        for _, response := range endpoint.Responses {
            fmt.Printf("  Response %d: %s\n", 
                response.StatusCode, 
                response.Description)
        }
    }
}
```

### Using Parser Registry

```go
package main

import (
    "context"
    "fmt"
    "os"
    
    "github.com/flowc-labs/flowc/internal/flowc/ir"
)

func main() {
    ctx := context.Background()
    
    // Create registry with all parsers
    registry := ir.DefaultParserRegistry()
    
    // Parse different API types
    apiType := ir.APITypeREST  // or APITypeGRPC, APITypeGraphQL, etc.
    specData, _ := os.ReadFile("spec.yaml")
    
    api, err := registry.Parse(ctx, apiType, specData)
    if err != nil {
        panic(err)
    }
    
    fmt.Printf("Parsed %s API: %s\n", api.Metadata.Type, api.Metadata.Title)
}
```

### Loading a Bundle

```go
package main

import (
    "fmt"
    "os"
    
    "github.com/flowc-labs/flowc/internal/flowc/providers/rest/loader"
)

func main() {
    // Read bundle ZIP
    zipData, err := os.ReadFile("api-bundle.zip")
    if err != nil {
        panic(err)
    }
    
    // Load bundle
    bundleLoader := loader.NewBundleLoader()
    bundle, err := bundleLoader.LoadBundle(zipData)
    if err != nil {
        panic(err)
    }
    
    // Access the IR
    api := bundle.GetIR()
    fmt.Printf("Loaded %s API: %s\n", api.Metadata.Type, api.Metadata.Title)
    
    // Check API type
    if bundle.IsRESTAPI() {
        fmt.Println("This is a REST API")
    } else if bundle.IsGRPCAPI() {
        fmt.Println("This is a gRPC API")
    }
}
```

## Next Steps

1. **For REST APIs**: The current implementation is production-ready
2. **For gRPC APIs**: Implement the GRPCParser to parse .proto files
3. **For GraphQL APIs**: Implement the GraphQLParser to parse GraphQL SDL
4. **For WebSocket/SSE**: Implement the AsyncAPIParser to parse AsyncAPI specs

## Contributing

To add support for a new API type:

1. Define the API type in `internal/flowc/ir/types.go`
2. Create a parser in `internal/flowc/ir/{type}_parser.go`
3. Implement the `Parser` interface
4. Register it in `DefaultParserRegistry()`
5. Add examples in this directory
6. Update documentation

## References

- [IR Architecture Documentation](../../docs/IR_ARCHITECTURE.md)
- [IR Package README](../../internal/flowc/ir/README.md)
- [OpenAPI Specification](https://swagger.io/specification/)
- [Protocol Buffers](https://protobuf.dev/)
- [GraphQL](https://graphql.org/)
- [AsyncAPI](https://www.asyncapi.com/)

