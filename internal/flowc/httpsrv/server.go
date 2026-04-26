// Package httpsrv hosts the flowc HTTP server. It owns the mux, server
// lifecycle, and middleware, and mounts handlers from three sibling packages:
//
//   - admin/      operational endpoints (health, root)
//   - dataplane/  Envoy-facing artifacts (bootstrap, deploy instructions)
//   - providers/rest/  resource CRUD that writes to the Store
//
// The package is intentionally a thin transport layer; business logic lives in
// the mounted handler packages.
package httpsrv

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/flowc-labs/flowc/internal/flowc/httpsrv/admin"
	"github.com/flowc-labs/flowc/internal/flowc/httpsrv/dataplane"
	"github.com/flowc-labs/flowc/internal/flowc/providers/rest"
	"github.com/flowc-labs/flowc/internal/flowc/store"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// version reported by the /health and / endpoints.
const version = "3.0.0"

// Server is the flowc HTTP transport. It multiplexes admin, dataplane, and
// resource-CRUD endpoints onto a single listener.
type Server struct {
	mux          *http.ServeMux
	server       *http.Server
	store        store.Store
	logger       *logger.EnvoyLogger
	port         int
	xdsPort      int
	readTimeout  time.Duration
	writeTimeout time.Duration
	idleTimeout  time.Duration
	startTime    time.Time
}

// NewServer constructs the HTTP server. xdsPort is baked into Envoy bootstrap
// configs the dataplane handlers serve.
func NewServer(port, xdsPort int, readTimeout, writeTimeout, idleTimeout time.Duration, resourceStore store.Store, log *logger.EnvoyLogger) *Server {
	s := &Server{
		mux:          http.NewServeMux(),
		store:        resourceStore,
		logger:       log,
		port:         port,
		xdsPort:      xdsPort,
		readTimeout:  readTimeout,
		writeTimeout: writeTimeout,
		idleTimeout:  idleTimeout,
		startTime:    time.Now(),
	}

	s.setupRoutes()
	return s
}

// setupRoutes configures all HTTP routes using Go 1.22+ method-based routing.
func (s *Server) setupRoutes() {
	// Provider — resource CRUD that writes to the Store.
	rh := rest.NewResourceHandler(s.store, s.logger)
	uh := rest.NewUploadHandler(s.store, s.logger)

	// Dataplane — Envoy-facing artifacts (read-only against the Store).
	bh := dataplane.NewBootstrapHandler(s.store, "host.docker.internal", s.xdsPort, s.logger)
	dh := dataplane.NewDeployHandler(s.store, "host.docker.internal", s.xdsPort, s.port, s.logger)

	// Admin — health, root doc.
	hh := admin.NewHealthHandler(s.startTime, version)
	rooth := admin.NewRootHandler()

	// Admin
	s.mux.HandleFunc("GET /health", hh.Handle)
	s.mux.HandleFunc("GET /", rooth.Handle)

	// --- Flat K8s-style resource endpoints (provider/rest) ---

	// Gateways
	s.mux.HandleFunc("PUT /api/v1/gateways/{name}", rh.HandlePut("Gateway"))
	s.mux.HandleFunc("GET /api/v1/gateways/{name}", rh.HandleGet("Gateway"))
	s.mux.HandleFunc("GET /api/v1/gateways", rh.HandleList("Gateway"))
	s.mux.HandleFunc("DELETE /api/v1/gateways/{name}", rh.HandleDelete("Gateway"))

	// Listeners
	s.mux.HandleFunc("PUT /api/v1/listeners/{name}", rh.HandlePut("Listener"))
	s.mux.HandleFunc("GET /api/v1/listeners/{name}", rh.HandleGet("Listener"))
	s.mux.HandleFunc("GET /api/v1/listeners", rh.HandleList("Listener"))
	s.mux.HandleFunc("DELETE /api/v1/listeners/{name}", rh.HandleDelete("Listener"))

	// APIs
	s.mux.HandleFunc("PUT /api/v1/apis/{name}", rh.HandlePut("API"))
	s.mux.HandleFunc("GET /api/v1/apis/{name}", rh.HandleGet("API"))
	s.mux.HandleFunc("GET /api/v1/apis", rh.HandleList("API"))
	s.mux.HandleFunc("DELETE /api/v1/apis/{name}", rh.HandleDelete("API"))

	// Deployments
	s.mux.HandleFunc("PUT /api/v1/deployments/{name}", rh.HandlePut("Deployment"))
	s.mux.HandleFunc("GET /api/v1/deployments/{name}", rh.HandleGet("Deployment"))
	s.mux.HandleFunc("GET /api/v1/deployments", rh.HandleList("Deployment"))
	s.mux.HandleFunc("DELETE /api/v1/deployments/{name}", rh.HandleDelete("Deployment"))

	// GatewayPolicies
	s.mux.HandleFunc("PUT /api/v1/gatewaypolicies/{name}", rh.HandlePut("GatewayPolicy"))
	s.mux.HandleFunc("GET /api/v1/gatewaypolicies/{name}", rh.HandleGet("GatewayPolicy"))
	s.mux.HandleFunc("GET /api/v1/gatewaypolicies", rh.HandleList("GatewayPolicy"))
	s.mux.HandleFunc("DELETE /api/v1/gatewaypolicies/{name}", rh.HandleDelete("GatewayPolicy"))

	// APIPolicies
	s.mux.HandleFunc("PUT /api/v1/apipolicies/{name}", rh.HandlePut("APIPolicy"))
	s.mux.HandleFunc("GET /api/v1/apipolicies/{name}", rh.HandleGet("APIPolicy"))
	s.mux.HandleFunc("GET /api/v1/apipolicies", rh.HandleList("APIPolicy"))
	s.mux.HandleFunc("DELETE /api/v1/apipolicies/{name}", rh.HandleDelete("APIPolicy"))

	// BackendPolicies
	s.mux.HandleFunc("PUT /api/v1/backendpolicies/{name}", rh.HandlePut("BackendPolicy"))
	s.mux.HandleFunc("GET /api/v1/backendpolicies/{name}", rh.HandleGet("BackendPolicy"))
	s.mux.HandleFunc("GET /api/v1/backendpolicies", rh.HandleList("BackendPolicy"))
	s.mux.HandleFunc("DELETE /api/v1/backendpolicies/{name}", rh.HandleDelete("BackendPolicy"))

	// Bulk apply (provider/rest)
	s.mux.HandleFunc("POST /api/v1/apply", rh.HandleApply)

	// ZIP upload convenience (provider/rest)
	s.mux.HandleFunc("POST /api/v1/upload", uh.HandleUpload)

	// --- Dataplane endpoints (Envoy-facing) ---
	s.mux.HandleFunc("GET /api/v1/gateways/{name}/bootstrap", bh.HandleBootstrap)
	s.mux.HandleFunc("GET /api/v1/gateways/{name}/deploy", dh.HandleDeploy)
}

// corsMiddleware adds CORS headers to all responses.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-Managed-By, If-Match")
		w.Header().Set("Access-Control-Max-Age", "3600")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.corsMiddleware(s.mux),
		ReadTimeout:  s.readTimeout,
		WriteTimeout: s.writeTimeout,
		IdleTimeout:  s.idleTimeout,
	}

	s.logger.WithFields(map[string]any{
		"port": s.port,
	}).Info("Starting FlowC HTTP server")

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	return nil
}

// Stop gracefully stops the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Stopping FlowC HTTP server")

	if s.server != nil {
		return s.server.Shutdown(ctx)
	}

	return nil
}
