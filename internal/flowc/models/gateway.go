package models

import (
	"time"

	"github.com/flowc-labs/flowc/pkg/types"
)

// Gateway represents a registered gateway (Envoy proxy) in the control plane.
// Gateways are the top-level entity representing a physical Envoy proxy instance.
// Gateways contain Listeners, which contain GatewayVirtualHosts, which contain API deployments.
type Gateway struct {
	// ID is the unique identifier for the gateway (UUID, auto-generated)
	ID string `json:"id"`

	// NodeID is the Envoy node ID that this gateway uses to connect to the control plane.
	// This must be unique across all gateways.
	NodeID string `json:"node_id"`

	// Name is a human-friendly name for the gateway
	Name string `json:"name"`

	// Description is an optional description of the gateway
	Description string `json:"description,omitempty"`

	// Status represents the connection status of the gateway
	Status GatewayStatus `json:"status"`

	// Defaults contains default strategy configurations for APIs deployed to this gateway.
	// These defaults are used when an API deployment doesn't specify its own strategies.
	// Strategy precedence: API config (flowc.yaml) > Gateway defaults > Built-in defaults
	Defaults *types.StrategyConfig `json:"defaults,omitempty"`

	// Labels are key-value pairs for organizing and filtering gateways
	Labels map[string]string `json:"labels,omitempty"`

	// CreatedAt is the timestamp when the gateway was created
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the timestamp when the gateway was last updated
	UpdatedAt time.Time `json:"updated_at"`
}

// GatewayStatus represents the connection status of a gateway
type GatewayStatus string

const (
	// GatewayStatusConnected indicates the gateway is connected to the control plane
	GatewayStatusConnected GatewayStatus = "connected"

	// GatewayStatusDisconnected indicates the gateway is not connected
	GatewayStatusDisconnected GatewayStatus = "disconnected"

	// GatewayStatusUnknown indicates the connection status is unknown
	GatewayStatusUnknown GatewayStatus = "unknown"
)

// Listener represents a port binding within a gateway.
// Each listener binds to a specific port and can host multiple GatewayVirtualHosts.
type Listener struct {
	// ID is the unique identifier for the listener (UUID, auto-generated)
	ID string `json:"id"`

	// GatewayID is the ID of the parent gateway
	GatewayID string `json:"gateway_id"`

	// Port is the port this listener binds to (unique within gateway)
	Port uint32 `json:"port"`

	// Address is the bind address (default: 0.0.0.0)
	Address string `json:"address,omitempty"`

	// TLS contains TLS configuration for the listener
	TLS *TLSConfig `json:"tls,omitempty"`

	// HTTP2 enables HTTP/2 support on the listener
	HTTP2 bool `json:"http2,omitempty"`

	// AccessLog is the path for access logs (stdout, stderr, or file path)
	AccessLog string `json:"access_log,omitempty"`

	// CreatedAt is the timestamp when the listener was created
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the timestamp when the listener was last updated
	UpdatedAt time.Time `json:"updated_at"`
}

// TLSConfig represents TLS settings for a listener
type TLSConfig struct {
	// CertPath is the path to the TLS certificate
	CertPath string `json:"cert_path"`

	// KeyPath is the path to the TLS private key
	KeyPath string `json:"key_path"`

	// CAPath is the path to the CA certificate for client verification
	CAPath string `json:"ca_path,omitempty"`

	// RequireClientCert enables mutual TLS (mTLS)
	RequireClientCert bool `json:"require_client_cert,omitempty"`

	// MinVersion is the minimum TLS version (TLSv1.2, TLSv1.3)
	MinVersion string `json:"min_version,omitempty"`

	// CipherSuites is a list of allowed cipher suites
	CipherSuites []string `json:"cipher_suites,omitempty"`
}

// GatewayVirtualHost represents a virtual host within a listener.
// Virtual hosts use hostname-based SNI for filter chain matching, allowing
// multiple isolated virtual hosts to share the same listener port.
type GatewayVirtualHost struct {
	// ID is the unique identifier for the virtual host (UUID, auto-generated)
	ID string `json:"id"`

	// ListenerID is the ID of the parent listener
	ListenerID string `json:"listener_id"`

	// Name is the virtual host name (e.g., "production", "staging")
	// Must be unique within a listener
	Name string `json:"name"`

	// Hostname is the SNI hostname for filter chain matching
	// Must be unique within a listener
	Hostname string `json:"hostname"`

	// Description is an optional description of the virtual host
	Description string `json:"description,omitempty"`

	// HTTPFilters contains HTTP filters applied to this virtual host's filter chain
	HTTPFilters []types.HTTPFilter `json:"http_filters,omitempty"`

	// Labels are key-value pairs for organizing and filtering virtual hosts
	Labels map[string]string `json:"labels,omitempty"`

	// CreatedAt is the timestamp when the virtual host was created
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the timestamp when the virtual host was last updated
	UpdatedAt time.Time `json:"updated_at"`
}

// ListenerConfig represents the configuration for creating a listener during gateway creation.
// This is used to create listeners as part of the gateway creation request.
type ListenerConfig struct {
	// Port is required and must be unique within the gateway
	Port uint32 `json:"port"`

	// Address is optional (default: 0.0.0.0)
	Address string `json:"address,omitempty"`

	// TLS is optional TLS configuration
	TLS *TLSConfig `json:"tls,omitempty"`

	// HTTP2 enables HTTP/2 support
	HTTP2 bool `json:"http2,omitempty"`

	// AccessLog is optional access log path
	AccessLog string `json:"access_log,omitempty"`

	// VirtualHosts contains the virtual host configurations for this listener.
	// If empty, a default virtual host with hostname "*" will be created.
	VirtualHosts []VirtualHostConfig `json:"virtualHosts,omitempty"`
}

// VirtualHostConfig represents the configuration for creating a virtual host.
// This is used to create virtual hosts as part of listener or gateway creation requests.
type VirtualHostConfig struct {
	// Name is required and must be unique within the listener
	Name string `json:"name"`

	// Hostname is required for SNI matching and must be unique within the listener
	Hostname string `json:"hostname"`

	// Description is optional
	Description string `json:"description,omitempty"`

	// HTTPFilters are optional virtual-host-specific filters
	HTTPFilters []types.HTTPFilter `json:"http_filters,omitempty"`

	// Labels are optional
	Labels map[string]string `json:"labels,omitempty"`
}

// CreateGatewayRequest represents the request to create a new gateway
type CreateGatewayRequest struct {
	// NodeID is required and must be unique
	NodeID string `json:"node_id"`

	// Name is required
	Name string `json:"name"`

	// Description is optional
	Description string `json:"description,omitempty"`

	// Defaults are optional strategy defaults
	Defaults *types.StrategyConfig `json:"defaults,omitempty"`

	// Labels are optional
	Labels map[string]string `json:"labels,omitempty"`

	// Listeners contains optional listener configurations to create with the gateway.
	// If empty or nil, a default listener will be created on the default port with
	// a "production" environment.
	Listeners []ListenerConfig `json:"listeners,omitempty"`
}

// UpdateGatewayRequest represents the request to update an existing gateway.
// All fields are optional; only provided fields will be updated.
type UpdateGatewayRequest struct {
	// Name updates the gateway name
	Name *string `json:"name,omitempty"`

	// Description updates the gateway description
	Description *string `json:"description,omitempty"`

	// Defaults updates the strategy defaults
	Defaults *types.StrategyConfig `json:"defaults,omitempty"`

	// Labels updates the labels
	Labels map[string]string `json:"labels,omitempty"`
}

// CreateListenerRequest represents the request to create a new listener
type CreateListenerRequest struct {
	// Port is required and must be unique within the gateway
	Port uint32 `json:"port"`

	// Address is optional (default: 0.0.0.0)
	Address string `json:"address,omitempty"`

	// TLS is optional TLS configuration
	TLS *TLSConfig `json:"tls,omitempty"`

	// HTTP2 enables HTTP/2 support
	HTTP2 bool `json:"http2,omitempty"`

	// AccessLog is optional access log path
	AccessLog string `json:"access_log,omitempty"`

	// VirtualHosts contains required virtual host configurations for this listener.
	// At least one virtual host must be provided.
	VirtualHosts []VirtualHostConfig `json:"virtualHosts"`
}

// UpdateListenerRequest represents the request to update an existing listener.
// All fields are optional; only provided fields will be updated.
type UpdateListenerRequest struct {
	// Address updates the bind address
	Address *string `json:"address,omitempty"`

	// TLS updates the TLS configuration
	TLS *TLSConfig `json:"tls,omitempty"`

	// HTTP2 updates HTTP/2 support
	HTTP2 *bool `json:"http2,omitempty"`

	// AccessLog updates the access log path
	AccessLog *string `json:"access_log,omitempty"`
}

// CreateVirtualHostRequest represents the request to create a new virtual host
type CreateVirtualHostRequest struct {
	// Name is required and must be unique within the listener
	Name string `json:"name"`

	// Hostname is required for SNI matching and must be unique within the listener
	Hostname string `json:"hostname"`

	// Description is optional
	Description string `json:"description,omitempty"`

	// HTTPFilters are optional virtual-host-specific filters
	HTTPFilters []types.HTTPFilter `json:"http_filters,omitempty"`

	// Labels are optional
	Labels map[string]string `json:"labels,omitempty"`
}

// UpdateVirtualHostRequest represents the request to update an existing virtual host.
// All fields are optional; only provided fields will be updated.
type UpdateVirtualHostRequest struct {
	// Hostname updates the SNI hostname
	Hostname *string `json:"hostname,omitempty"`

	// Description updates the virtual host description
	Description *string `json:"description,omitempty"`

	// HTTPFilters updates the HTTP filters
	HTTPFilters []types.HTTPFilter `json:"http_filters,omitempty"`

	// Labels updates the labels
	Labels map[string]string `json:"labels,omitempty"`
}

// GatewayResponse represents the response for a single gateway operation
type GatewayResponse struct {
	Success bool     `json:"success"`
	Gateway *Gateway `json:"gateway,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// ListGatewaysResponse represents the response for listing gateways
type ListGatewaysResponse struct {
	Success  bool       `json:"success"`
	Gateways []*Gateway `json:"gateways"`
	Total    int        `json:"total"`
}

// GatewayAPIsResponse represents the response for listing APIs deployed to a gateway
type GatewayAPIsResponse struct {
	Success     bool             `json:"success"`
	GatewayID   string           `json:"gateway_id"`
	Deployments []*APIDeployment `json:"deployments"`
	Total       int              `json:"total"`
}

// DeleteGatewayResponse represents the response for deleting a gateway
type DeleteGatewayResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// ListenerResponse represents the response for a single listener operation
type ListenerResponse struct {
	Success  bool      `json:"success"`
	Listener *Listener `json:"listener,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// ListListenersResponse represents the response for listing listeners
type ListListenersResponse struct {
	Success   bool        `json:"success"`
	Listeners []*Listener `json:"listeners"`
	Total     int         `json:"total"`
}

// DeleteListenerResponse represents the response for deleting a listener
type DeleteListenerResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// VirtualHostResponse represents the response for a single virtual host operation
type VirtualHostResponse struct {
	Success     bool                `json:"success"`
	VirtualHost *GatewayVirtualHost `json:"virtualHost,omitempty"`
	Error       string              `json:"error,omitempty"`
}

// ListVirtualHostsResponse represents the response for listing virtual hosts
type ListVirtualHostsResponse struct {
	Success      bool                  `json:"success"`
	VirtualHosts []*GatewayVirtualHost `json:"virtualHosts"`
	Total        int                   `json:"total"`
}

// VirtualHostAPIsResponse represents the response for listing APIs deployed to a virtual host
type VirtualHostAPIsResponse struct {
	Success       bool             `json:"success"`
	VirtualHostID string           `json:"virtualHost_id"`
	Deployments   []*APIDeployment `json:"deployments"`
	Total         int              `json:"total"`
}

// DeleteVirtualHostResponse represents the response for deleting a virtual host
type DeleteVirtualHostResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}
