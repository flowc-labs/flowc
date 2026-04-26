package rest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/flowc-labs/flowc/internal/flowc/httpsrv/httputil"
	"github.com/flowc-labs/flowc/internal/flowc/store"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// ResourceHandler is the unified HTTP handler for all declarative resource operations.
type ResourceHandler struct {
	store  store.Store
	logger *logger.EnvoyLogger
}

// NewResourceHandler creates a new resource handler.
func NewResourceHandler(s store.Store, log *logger.EnvoyLogger) *ResourceHandler {
	return &ResourceHandler{store: s, logger: log}
}

// ApplyRequest is the bulk-apply request body.
type ApplyRequest struct {
	Resources []json.RawMessage `json:"resources"`
}

// ApplyResultItem describes the outcome of applying one resource.
type ApplyResultItem struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"` // "created", "updated", "unchanged", "failed"
	Error  string `json:"error,omitempty"`
}

// ApplyResult is the response for a bulk-apply request.
type ApplyResult struct {
	Results []ApplyResultItem `json:"results"`
}

// HandlePut handles PUT /api/v1/{kind-plural}/{name}
// Creates or updates a resource. Returns 201 for create, 200 for update.
func (h *ResourceHandler) HandlePut(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "failed to read request body")
			return
		}

		// Parse the spec from the body
		var envelope struct {
			Spec   json.RawMessage `json:"spec"`
			Status json.RawMessage `json:"status,omitempty"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if envelope.Spec == nil {
			// Allow full resource body without wrapper
			envelope.Spec = body
		}

		// Validate the typed resource
		if err := validateResource(name, envelope.Spec); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Build stored resource
		meta := store.StoreMeta{
			Kind:   kind,
			Name:   name,
			Labels: extractLabels(body),
		}

		// Extract conflict policy from body
		var metaOverrides struct {
			Metadata struct {
				ConflictPolicy string `json:"conflictPolicy"`
			} `json:"metadata"`
		}
		_ = json.Unmarshal(body, &metaOverrides)
		if metaOverrides.Metadata.ConflictPolicy != "" {
			meta.ConflictPolicy = metaOverrides.Metadata.ConflictPolicy
		}

		stored := &store.StoredResource{
			Meta:       meta,
			SpecJSON:   envelope.Spec,
			StatusJSON: envelope.Status,
		}

		opts := store.PutOptions{
			ManagedBy: r.Header.Get("X-Managed-By"),
		}

		// If-Match header for optimistic concurrency
		if ifMatch := r.Header.Get("If-Match"); ifMatch != "" {
			rev, err := strconv.ParseInt(ifMatch, 10, 64)
			if err == nil {
				opts.ExpectedRevision = rev
			}
		}

		out, err := h.store.Put(r.Context(), stored, opts)
		if err != nil {
			handleStoreError(w, err)
			return
		}

		status := http.StatusOK
		if out.Meta.Revision == 1 {
			status = http.StatusCreated
		}

		writeResourceResponse(w, status, kind, out)
	}
}

// HandleGet handles GET /api/v1/{kind-plural}/{name}
func (h *ResourceHandler) HandleGet(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		key := store.ResourceKey{Kind: kind, Name: name}
		res, err := h.store.Get(r.Context(), key)
		if err != nil {
			handleStoreError(w, err)
			return
		}

		writeResourceResponse(w, http.StatusOK, kind, res)
	}
}

// HandleList handles GET /api/v1/{kind-plural}
// Supports query params: labels (metadata labels), gatewayRef, listenerRef (spec fields).
func (h *ResourceHandler) HandleList(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := store.ListFilter{
			Kind:   kind,
			Labels: parseLabelsQuery(r),
		}

		items, err := h.store.List(r.Context(), filter)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Apply spec-field filters (gatewayRef, listenerRef, etc.).
		// These are post-filters applied after the store list since the store
		// only supports kind+label filtering.
		specFilters := parseSpecFilters(r)
		if len(specFilters) > 0 {
			items = filterBySpec(items, specFilters)
		}

		crdItems := make([]map[string]any, 0, len(items))
		for _, item := range items {
			crdItems = append(crdItems, map[string]any{
				"apiVersion": "flowc.io/v1alpha1",
				"kind":       kind,
				"metadata":   store.StoreMetaToObjectMeta(item.Meta),
				"spec":       item.SpecJSON,
				"status":     item.StatusJSON,
			})
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"apiVersion": "flowc.io/v1alpha1",
			"kind":       kind + "List",
			"items":      crdItems,
		})
	}
}

// HandleDelete handles DELETE /api/v1/{kind-plural}/{name}
func (h *ResourceHandler) HandleDelete(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		key := store.ResourceKey{Kind: kind, Name: name}

		opts := store.DeleteOptions{}
		if ifMatch := r.Header.Get("If-Match"); ifMatch != "" {
			rev, err := strconv.ParseInt(ifMatch, 10, 64)
			if err == nil {
				opts.ExpectedRevision = rev
			}
		}

		if err := h.store.Delete(r.Context(), key, opts); err != nil {
			handleStoreError(w, err)
			return
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"message": fmt.Sprintf("%s %q deleted", kind, name),
		})
	}
}

// HandleApply handles POST /api/v1/apply -- bulk create-or-update.
func (h *ResourceHandler) HandleApply(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req ApplyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	managedBy := r.Header.Get("X-Managed-By")
	var results []ApplyResultItem

	for _, raw := range req.Resources {
		var envelope struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name           string            `json:"name"`
				Labels         map[string]string `json:"labels,omitempty"`
				ConflictPolicy string            `json:"conflictPolicy,omitempty"`
			} `json:"metadata"`
			Spec   json.RawMessage `json:"spec"`
			Status json.RawMessage `json:"status,omitempty"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			results = append(results, ApplyResultItem{
				Action: "failed",
				Error:  "invalid resource: " + err.Error(),
			})
			continue
		}

		meta := store.StoreMeta{
			Kind:           envelope.Kind,
			Name:           envelope.Metadata.Name,
			Labels:         envelope.Metadata.Labels,
			ConflictPolicy: envelope.Metadata.ConflictPolicy,
		}

		stored := &store.StoredResource{
			Meta:       meta,
			SpecJSON:   envelope.Spec,
			StatusJSON: envelope.Status,
		}

		out, err := h.store.Put(r.Context(), stored, store.PutOptions{ManagedBy: managedBy})
		if err != nil {
			results = append(results, ApplyResultItem{
				Kind:   envelope.Kind,
				Name:   envelope.Metadata.Name,
				Action: "failed",
				Error:  err.Error(),
			})
			continue
		}

		action := "updated"
		if out.Meta.Revision == 1 {
			action = "created"
		}
		results = append(results, ApplyResultItem{
			Kind:   envelope.Kind,
			Name:   out.Meta.Name,
			Action: action,
		})
	}

	httputil.WriteJSON(w, http.StatusOK, ApplyResult{Results: results})
}

// --- Helpers ---

func validateResource(name string, specJSON json.RawMessage) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	var raw map[string]any
	return json.Unmarshal(specJSON, &raw)
}

func extractLabels(body []byte) map[string]string {
	var wrapper struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal(body, &wrapper)
	return wrapper.Metadata.Labels
}

func parseLabelsQuery(r *http.Request) map[string]string {
	labelStr := r.URL.Query().Get("labels")
	if labelStr == "" {
		return nil
	}
	labels := make(map[string]string)
	for pair := range strings.SplitSeq(labelStr, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			labels[parts[0]] = parts[1]
		}
	}
	return labels
}

// parseSpecFilters extracts spec-field query params (gatewayRef, listenerRef, etc.).
func parseSpecFilters(r *http.Request) map[string]string {
	filters := make(map[string]string)
	for _, key := range []string{"gatewayRef", "listenerRef", "apiRef"} {
		if v := r.URL.Query().Get(key); v != "" {
			filters[key] = v
		}
	}
	return filters
}

// filterBySpec post-filters stored resources by spec JSON fields.
func filterBySpec(items []*store.StoredResource, filters map[string]string) []*store.StoredResource {
	var result []*store.StoredResource
	for _, item := range items {
		if matchesSpecFilters(item.SpecJSON, filters) {
			result = append(result, item)
		}
	}
	return result
}

// specFilterAliases maps query param names to nested JSON paths for resources
// that use nested structures (e.g., Deployment.spec.gateway.name).
var specFilterAliases = map[string]string{
	"gatewayRef":  "gateway.name",
	"listenerRef": "gateway.listener",
}

// matchesSpecFilters checks if a resource's spec JSON contains all the
// specified field values. Tries the flat key first (e.g., spec.gatewayRef),
// then falls back to a nested alias (e.g., spec.gateway.name) for resources
// like Deployments that use nested structures.
func matchesSpecFilters(specJSON json.RawMessage, filters map[string]string) bool {
	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return false
	}
	for key, expected := range filters {
		actual := resolveNestedField(spec, key)
		if actual == nil {
			if alias, ok := specFilterAliases[key]; ok {
				actual = resolveNestedField(spec, alias)
			}
		}
		if actual == nil {
			return false
		}
		if fmt.Sprintf("%v", actual) != expected {
			return false
		}
	}
	return true
}

// resolveNestedField resolves a dot-notation key (e.g., "gateway.name") against a map.
func resolveNestedField(m map[string]any, key string) any {
	parts := strings.Split(key, ".")
	var current any = m
	for _, part := range parts {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = obj[part]
		if !ok {
			return nil
		}
	}
	return current
}

func writeResourceResponse(w http.ResponseWriter, status int, kind string, res *store.StoredResource) {
	httputil.WriteJSON(w, status, map[string]any{
		"apiVersion": "flowc.io/v1alpha1",
		"kind":       kind,
		"metadata":   store.StoreMetaToObjectMeta(res.Meta),
		"spec":       res.SpecJSON,
		"status":     res.StatusJSON,
	})
}

func handleStoreError(w http.ResponseWriter, err error) {
	switch {
	case isNotFound(err):
		httputil.WriteError(w, http.StatusNotFound, err.Error())
	case isRevisionConflict(err):
		httputil.WriteError(w, http.StatusConflict, err.Error())
	case isOwnershipConflict(err):
		httputil.WriteError(w, http.StatusConflict, err.Error())
	default:
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
	}
}

func isNotFound(err error) bool {
	return err == store.ErrNotFound
}

func isRevisionConflict(err error) bool {
	_, ok := err.(*store.RevisionConflictError)
	return ok || err == store.ErrRevisionConflict
}

func isOwnershipConflict(err error) bool {
	_, ok := err.(*store.OwnershipConflictError)
	return ok || err == store.ErrOwnershipConflict
}
