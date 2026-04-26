package dataplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/flowc-labs/flowc/internal/flowc/httpsrv/httputil"
	"github.com/flowc-labs/flowc/internal/flowc/store"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// DeployHandler generates deployment instructions for gateways.
type DeployHandler struct {
	store            store.Store
	logger           *logger.EnvoyLogger
	controlPlaneHost string
	controlPlanePort int
	apiPort          int
}

// NewDeployHandler creates a new deploy instructions handler.
func NewDeployHandler(s store.Store, controlPlaneHost string, controlPlanePort, apiPort int, log *logger.EnvoyLogger) *DeployHandler {
	return &DeployHandler{
		store:            s,
		logger:           log,
		controlPlaneHost: controlPlaneHost,
		controlPlanePort: controlPlanePort,
		apiPort:          apiPort,
	}
}

// HandleDeploy generates deployment instructions for a gateway.
// GET /api/v1/gateways/{name}/deploy
func (h *DeployHandler) HandleDeploy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	gwStored, err := h.store.Get(r.Context(), store.ResourceKey{Kind: "Gateway", Name: name})
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "gateway not found")
		} else {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// Unmarshal gateway spec
	var gwSpec struct {
		NodeID string `json:"nodeId"`
	}
	if err := json.Unmarshal(gwStored.SpecJSON, &gwSpec); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to parse gateway spec: "+err.Error())
		return
	}

	// Load listeners for this gateway
	allListeners, err := h.store.List(r.Context(), store.ListFilter{Kind: "Listener"})
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var listenerPorts []uint32
	for _, l := range allListeners {
		var lSpec struct {
			GatewayRef string `json:"gatewayRef"`
			Port       uint32 `json:"port"`
		}
		if err := json.Unmarshal(l.SpecJSON, &lSpec); err == nil && lSpec.GatewayRef == name {
			listenerPorts = append(listenerPorts, lSpec.Port)
		}
	}

	instructions := h.buildInstructions(name, gwSpec.NodeID, listenerPorts)
	httputil.WriteJSON(w, http.StatusOK, instructions)
}

// DeployInstructions is the response body for the deploy endpoint.
type DeployInstructions struct {
	Gateway    GatewayInfo        `json:"gateway"`
	Docker     DockerInstructions `json:"docker"`
	Kubernetes K8sInstructions    `json:"kubernetes"`
}

// GatewayInfo summarizes the gateway for deploy instructions.
type GatewayInfo struct {
	Name       string `json:"name"`
	NodeID     string `json:"nodeId"`
	EnvoyImage string `json:"envoyImage"`
}

// DockerInstructions contains Docker deployment details.
type DockerInstructions struct {
	BootstrapURL   string `json:"bootstrapUrl"`
	RunCommand     string `json:"runCommand"`
	ComposeSnippet string `json:"composeSnippet"`
}

// K8sInstructions contains Kubernetes deployment details.
type K8sInstructions struct {
	Manifest     string `json:"manifest"`
	ApplyCommand string `json:"applyCommand"`
}

func (h *DeployHandler) buildInstructions(gwName, nodeID string, listenerPorts []uint32) *DeployInstructions {
	envoyImage := "envoyproxy/envoy:v1.31-latest"
	adminPort := 9901

	bootstrapURL := fmt.Sprintf("http://%s:%d/api/v1/gateways/%s/bootstrap",
		h.controlPlaneHost, h.apiPort, gwName)

	// Build Docker port mappings from listeners.
	portMappings := make([]string, 0, 1+len(listenerPorts))
	portMappings = append(portMappings, fmt.Sprintf("-p %d:%d", adminPort, adminPort))
	for _, port := range listenerPorts {
		portMappings = append(portMappings, fmt.Sprintf("-p %d:%d", port, port))
	}

	dockerRun := fmt.Sprintf(
		"docker run --rm --name %s \\\n  %s \\\n  -v $(pwd)/envoy-bootstrap.yaml:/etc/envoy/envoy.yaml \\\n  %s",
		gwName,
		strings.Join(portMappings, " \\\n  "),
		envoyImage,
	)

	var composeSnippet strings.Builder
	fmt.Fprintf(&composeSnippet, `  %s:
    image: %s
    volumes:
      - ./envoy-bootstrap.yaml:/etc/envoy/envoy.yaml
    ports:`, gwName, envoyImage)
	fmt.Fprintf(&composeSnippet, "\n      - \"%d:%d\"", adminPort, adminPort)
	for _, port := range listenerPorts {
		fmt.Fprintf(&composeSnippet, "\n      - \"%d:%d\"", port, port)
	}
	composeSnippet.WriteString("\n    network_mode: host")

	// K8s manifest
	var k8sManifest strings.Builder
	fmt.Fprintf(&k8sManifest, `apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  labels:
    app: %s
    flowc.io/gateway: "%s"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: envoy
        image: %s
        ports:
        - containerPort: %d
          name: admin`,
		gwName, gwName, gwName,
		gwName, gwName, envoyImage, adminPort)

	for _, port := range listenerPorts {
		fmt.Fprintf(&k8sManifest, "\n        - containerPort: %d\n          name: listener-%d", port, port)
	}

	fmt.Fprintf(&k8sManifest, `
        volumeMounts:
        - name: bootstrap
          mountPath: /etc/envoy/envoy.yaml
          subPath: envoy.yaml
      volumes:
      - name: bootstrap
        configMap:
          name: %s-bootstrap`, gwName)

	return &DeployInstructions{
		Gateway: GatewayInfo{
			Name:       gwName,
			NodeID:     nodeID,
			EnvoyImage: envoyImage,
		},
		Docker: DockerInstructions{
			BootstrapURL:   bootstrapURL,
			RunCommand:     dockerRun,
			ComposeSnippet: composeSnippet.String(),
		},
		Kubernetes: K8sInstructions{
			Manifest:     k8sManifest.String(),
			ApplyCommand: fmt.Sprintf("kubectl apply -f %s-deployment.yaml", gwName),
		},
	}
}
