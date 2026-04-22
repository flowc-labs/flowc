package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/flowc-labs/flowc/internal/flowc/resource/store"
	"github.com/flowc-labs/flowc/internal/flowc/server/loader"
	"github.com/flowc-labs/flowc/pkg/bundle"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// UploadHandler handles ZIP bundle uploads and converts them to API + Deployment resources.
type UploadHandler struct {
	store        store.Store
	bundleLoader *loader.BundleLoader
	logger       *logger.EnvoyLogger
}

// NewUploadHandler creates a new upload handler.
func NewUploadHandler(s store.Store, log *logger.EnvoyLogger) *UploadHandler {
	return &UploadHandler{
		store:        s,
		bundleLoader: loader.NewBundleLoader(),
		logger:       log,
	}
}

// HandleUpload handles POST /api/v1/upload
// Accepts a multipart ZIP file, creates an API resource and optionally a Deployment resource.
func (h *UploadHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer func() { _ = file.Close() }()

	zipData, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read file")
		return
	}

	// Validate ZIP
	if err := bundle.ValidateZip(zipData); err != nil {
		writeError(w, http.StatusBadRequest, "invalid zip: "+err.Error())
		return
	}

	// Load bundle
	deploymentBundle, err := h.bundleLoader.LoadBundle(zipData)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse bundle: "+err.Error())
		return
	}

	meta := deploymentBundle.FlowCMetadata

	// Create API resource spec
	apiSpec := map[string]any{
		"version":     meta.Version,
		"description": meta.Description,
		"context":     meta.Context,
		"apiType":     meta.APIType,
		"specContent": string(deploymentBundle.Spec),
		"upstream": map[string]any{
			"host":    meta.Upstream.Host,
			"port":    meta.Upstream.Port,
			"scheme":  meta.Upstream.Scheme,
			"timeout": meta.Upstream.Timeout,
		},
	}

	apiName := meta.Name
	apiSpecJSON, _ := json.Marshal(apiSpec)
	apiStored := &store.StoredResource{
		Meta: store.StoreMeta{
			Kind: "API",
			Name: apiName,
		},
		SpecJSON: apiSpecJSON,
	}

	managedBy := r.Header.Get("X-Managed-By")
	if managedBy == "" {
		managedBy = "upload"
	}

	apiOut, err := h.store.Put(r.Context(), apiStored, store.PutOptions{ManagedBy: managedBy})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store API: "+err.Error())
		return
	}

	result := []ApplyResultItem{
		{
			Kind:   "API",
			Name:   apiOut.Meta.Name,
			Action: actionFromRevision(apiOut.Meta.Revision),
		},
	}

	// If gateway config is present, create a Deployment resource too
	if meta.Gateway.GatewayID != "" || meta.Gateway.NodeID != "" {
		depName := fmt.Sprintf("%s-deploy", apiName)
		depSpec := map[string]any{
			"apiRef": apiName,
			"gateway": map[string]any{
				"name":     coalesce(meta.Gateway.GatewayID, meta.Gateway.NodeID),
				"listener": fmt.Sprintf("port-%d", meta.Gateway.Port),
			},
		}
		if meta.Strategy != nil {
			depSpec["strategy"] = meta.Strategy
		}

		depSpecJSON, _ := json.Marshal(depSpec)
		depStored := &store.StoredResource{
			Meta: store.StoreMeta{
				Kind: "Deployment",
				Name: depName,
			},
			SpecJSON: depSpecJSON,
		}

		depOut, err := h.store.Put(r.Context(), depStored, store.PutOptions{ManagedBy: managedBy})
		if err != nil {
			// API was created but deployment failed
			result = append(result, ApplyResultItem{
				Kind:   "Deployment",
				Name:   depName,
				Action: "failed",
				Error:  err.Error(),
			})
		} else {
			result = append(result, ApplyResultItem{
				Kind:   "Deployment",
				Name:   depOut.Meta.Name,
				Action: actionFromRevision(depOut.Meta.Revision),
			})
		}
	}

	writeJSON(w, http.StatusOK, ApplyResult{Results: result})
}

func actionFromRevision(rev int64) string {
	if rev == 1 {
		return "created"
	}
	return "updated"
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
