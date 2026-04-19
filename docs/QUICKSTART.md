# FlowC Quick Start

This guide walks you through building FlowC, connecting an Envoy proxy, deploying an API, and invoking it.

## Prerequisites

- Go 1.23+
- Docker
- curl

## 1. Build and Run FlowC

```bash
make build
./flowc
```

FlowC starts three components:
- REST API on `:8080`
- xDS gRPC server on `:18000`
- Reconciler (watches for resource changes)

Verify it's running:

```bash
curl http://localhost:8080/health
```

## 2. Create a Gateway

A Gateway represents an Envoy proxy instance. The `nodeId` must match the Envoy node ID.

```bash
curl -X PUT http://localhost:8080/api/v1/gateways/my-gateway \
  -H "Content-Type: application/json" \
  -d '{
    "spec": {
      "nodeId": "my-gateway"
    }
  }'
```

## 3. Create a Listener

A Listener defines a port binding on the gateway.

```bash
curl -X PUT http://localhost:8080/api/v1/listeners/http \
  -H "Content-Type: application/json" \
  -d '{
    "spec": {
      "gatewayRef": "my-gateway",
      "port": 9095,
      "hostnames": ["*"]
    }
  }'
```

## 4. Download the Envoy Bootstrap Config

FlowC generates Envoy bootstrap YAML configured to connect to its xDS server.

```bash
curl -o envoy-bootstrap.yaml \
  http://localhost:8080/api/v1/gateways/my-gateway/bootstrap
```

Inspect it:

```bash
cat envoy-bootstrap.yaml
```

The bootstrap configures:
- Admin interface on `:9901`
- ADS connection to FlowC xDS at `host.docker.internal:18000`
- Node ID matching the gateway (`my-gateway`)

## 5. Run Envoy with Docker

```bash
docker run --rm --name my-gateway \
  -p 9095:9095 \
  -p 9901:9901 \
  -v $(pwd)/envoy-bootstrap.yaml:/etc/envoy/envoy.yaml \
  --add-host=host.docker.internal:host-gateway \
  envoyproxy/envoy:v1.31-latest
```

Verify Envoy is connected to FlowC by checking the admin interface:

```bash
curl http://localhost:9901/clusters
```

You should see an `xds_cluster` entry.

## 6. Create an API

An API defines the upstream service, base path, and optionally an OpenAPI spec.

```bash
curl -X PUT http://localhost:8080/api/v1/apis/httpbin \
  -H "Content-Type: application/json" \
  -d '{
    "spec": {
      "version": "v1",
      "context": "/httpbin",
      "upstream": {
        "host": "httpbin.org",
        "port": 443,
        "scheme": "https"
      }
    }
  }'
```

## 7. Deploy the API to the Gateway

A Deployment binds an API to a specific Gateway and Listener.

```bash
curl -X PUT http://localhost:8080/api/v1/deployments/httpbin-deploy \
  -H "Content-Type: application/json" \
  -d '{
    "spec": {
      "apiRef": "httpbin",
      "gateway": {
        "name": "my-gateway",
        "listener": "http"
      }
    }
  }'
```

Wait a moment for the reconciler to generate xDS config, then check the deployment status:

```bash
curl -s http://localhost:8080/api/v1/deployments/httpbin-deploy | python3 -m json.tool
```

The `status.phase` should be `Deployed`.

## 8. Invoke the API

Send a request through Envoy to the upstream service:

```bash
curl http://localhost:9095/httpbin/get
```

You should see the httpbin response with request details.

Try other httpbin endpoints:

```bash
# POST
curl -X POST http://localhost:9095/httpbin/post \
  -H "Content-Type: application/json" \
  -d '{"hello": "world"}'

# Headers
curl http://localhost:9095/httpbin/headers
```

## 9. Verify via Envoy Admin

Check the Envoy config dump to see the xDS resources FlowC generated:

```bash
# All clusters
curl -s http://localhost:9901/clusters

# Full config dump
curl -s http://localhost:9901/config_dump | python3 -m json.tool | head -50
```

## Cleanup

```bash
# Stop Envoy
docker stop my-gateway

# Stop FlowC
# Ctrl+C in the terminal running ./flowc

# Remove bootstrap file
rm envoy-bootstrap.yaml
```

## What's Next

- Create multiple listeners with different ports and hostnames
- Deploy multiple APIs to the same gateway
- Use `POST /api/v1/apply` for bulk resource creation
- Attach policies via GatewayPolicy, APIPolicy, and BackendPolicy resources
- Upload ZIP bundles with OpenAPI specs via `POST /api/v1/upload`
