package models

import (
	"time"

	"github.com/flowc-labs/flowc/pkg/types"
)

// APIDeployment represents a complete API deployment
// This is the persisted model - IR is NOT stored here (it's transient for translation only)
type APIDeployment struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Version   string              `json:"version"`
	Context   string              `json:"context"`
	Status    string              `json:"status"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
	Metadata  types.FlowCMetadata `json:"metadata"`
}

// DeploymentStatus represents the status of an API deployment
type DeploymentStatus string

const (
	StatusPending   DeploymentStatus = "pending"
	StatusDeploying DeploymentStatus = "deploying"
	StatusDeployed  DeploymentStatus = "deployed"
	StatusFailed    DeploymentStatus = "failed"
	StatusUpdating  DeploymentStatus = "updating"
	StatusDeleting  DeploymentStatus = "deleting"
	StatusDeleted   DeploymentStatus = "deleted"
)

// DeploymentRequest represents the request payload for API deployment
type DeploymentRequest struct {
	Description string `json:"description,omitempty"`
}

// DeploymentResponse represents the response for API deployment
type DeploymentResponse struct {
	Success    bool           `json:"success"`
	Message    string         `json:"message"`
	Deployment *APIDeployment `json:"deployment,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// ListDeploymentsResponse represents the response for listing deployments
type ListDeploymentsResponse struct {
	Success     bool             `json:"success"`
	Deployments []*APIDeployment `json:"deployments"`
	Total       int              `json:"total"`
}

// GetDeploymentResponse represents the response for getting a specific deployment
type GetDeploymentResponse struct {
	Success    bool           `json:"success"`
	Deployment *APIDeployment `json:"deployment,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// DeleteDeploymentResponse represents the response for deleting a deployment
type DeleteDeploymentResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Version   string    `json:"version"`
	Uptime    string    `json:"uptime"`
}
