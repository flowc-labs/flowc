// Package httputil provides shared HTTP helpers for the flowc HTTP server
// and its mounted handler packages (admin, dataplane, providers/rest).
package httputil

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse is the standard JSON error envelope.
type ErrorResponse struct {
	Error   string            `json:"error"`
	Code    int               `json:"code"`
	Details map[string]string `json:"details,omitempty"`
}

// WriteJSON serializes v as JSON and writes it with the given status code.
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes a JSON ErrorResponse with the given status code.
func WriteError(w http.ResponseWriter, code int, msg string) {
	WriteJSON(w, code, ErrorResponse{Error: msg, Code: code})
}
