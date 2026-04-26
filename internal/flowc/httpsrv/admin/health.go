// Package admin contains operational HTTP handlers (health, root doc).
// These do not interact with the Store.
package admin

import (
	"net/http"
	"time"

	"github.com/flowc-labs/flowc/internal/flowc/httpsrv/httputil"
)

// HealthHandler reports server health and uptime.
type HealthHandler struct {
	startTime time.Time
	version   string
}

// NewHealthHandler returns a HealthHandler that reports uptime relative to startTime.
func NewHealthHandler(startTime time.Time, version string) *HealthHandler {
	return &HealthHandler{startTime: startTime, version: version}
}

// Handle handles GET /health.
func (h *HealthHandler) Handle(w http.ResponseWriter, _ *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":    "healthy",
		"timestamp": time.Now(),
		"version":   h.version,
		"uptime":    time.Since(h.startTime).String(),
	})
}
