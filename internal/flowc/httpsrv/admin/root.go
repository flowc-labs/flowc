package admin

import (
	"net/http"

	"github.com/flowc-labs/flowc/internal/flowc/httpsrv/httputil"
)

// RootHandler serves the API documentation at /.
type RootHandler struct{}

// NewRootHandler returns a RootHandler.
func NewRootHandler() *RootHandler {
	return &RootHandler{}
}

// Handle handles GET /. Returns a JSON description of the available endpoints.
func (h *RootHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"service":     "FlowC Control Plane",
		"version":     "3.0.0",
		"description": "Declarative Envoy xDS control plane with reconciliation-based architecture",
		"api_style":   "Flat K8s-style: PUT to create/update, GET/DELETE, POST /apply for bulk",
		"endpoints": map[string]any{
			"health": "GET /health",
			"resources": map[string]string{
				"gateways":        "/api/v1/gateways/{name}",
				"listeners":       "/api/v1/listeners/{name}",
				"apis":            "/api/v1/apis/{name}",
				"deployments":     "/api/v1/deployments/{name}",
				"gatewaypolicies": "/api/v1/gatewaypolicies/{name}",
				"apipolicies":     "/api/v1/apipolicies/{name}",
				"backendpolicies": "/api/v1/backendpolicies/{name}",
			},
			"bulk_apply": "POST /api/v1/apply",
			"upload":     "POST /api/v1/upload",
		},
		"notes": []string{
			"All resources use PUT for idempotent create-or-update",
			"Hierarchy is expressed through spec reference fields (gatewayRef, listenerRef, etc.)",
			"Reconciler watches the store and generates xDS snapshots automatically",
			"Use If-Match header for optimistic concurrency control",
			"Use X-Managed-By header for ownership tracking",
		},
	})
}
