package dataplane

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/flowc-labs/flowc/internal/flowc/httpsrv/httputil"
	"github.com/flowc-labs/flowc/internal/flowc/store"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// BootstrapHandler generates Envoy bootstrap configurations for gateways.
type BootstrapHandler struct {
	store            store.Store
	logger           *logger.EnvoyLogger
	controlPlaneHost string
	controlPlanePort int
}

// NewBootstrapHandler creates a new bootstrap handler.
func NewBootstrapHandler(s store.Store, controlPlaneHost string, controlPlanePort int, log *logger.EnvoyLogger) *BootstrapHandler {
	return &BootstrapHandler{
		store:            s,
		logger:           log,
		controlPlaneHost: controlPlaneHost,
		controlPlanePort: controlPlanePort,
	}
}

// HandleBootstrap generates an Envoy bootstrap YAML for a gateway.
// GET /api/v1/gateways/{name}/bootstrap
func (h *BootstrapHandler) HandleBootstrap(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	stored, err := h.store.Get(r.Context(), store.ResourceKey{Kind: "Gateway", Name: name})
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "gateway not found")
		} else {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// Unmarshal spec to get nodeId
	var spec struct {
		NodeID string `json:"nodeId"`
	}
	if err := json.Unmarshal(stored.SpecJSON, &spec); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to parse gateway spec: "+err.Error())
		return
	}

	nodeID := spec.NodeID
	if nodeID == "" {
		nodeID = name
	}

	bootstrapYAML := generateBasicBootstrapYAML(nodeID, h.controlPlaneHost, h.controlPlanePort)

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", "attachment; filename=envoy-bootstrap.yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(bootstrapYAML))
}

// generateBasicBootstrapYAML generates a basic Envoy bootstrap YAML.
func generateBasicBootstrapYAML(nodeID, controlPlaneHost string, controlPlanePort int) string {
	return fmt.Sprintf(`admin:
  address:
    socket_address:
      address: 0.0.0.0
      port_value: 9901

node:
  cluster: flowc
  id: %s

dynamic_resources:
  ads_config:
    api_type: GRPC
    transport_api_version: V3
    grpc_services:
    - envoy_grpc:
        cluster_name: xds_cluster
  lds_config:
    resource_api_version: V3
    ads: {}
  cds_config:
    resource_api_version: V3
    ads: {}

static_resources:
  clusters:
  - name: xds_cluster
    connect_timeout: 1s
    type: STRICT_DNS
    load_assignment:
      cluster_name: xds_cluster
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address:
                address: %s
                port_value: %d
    typed_extension_protocol_options:
      envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http2_protocol_options: {}
`, nodeID, controlPlaneHost, controlPlanePort)
}
