/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeploymentSpec defines the desired state of Deployment.
type DeploymentSpec struct {
	// apiRef is the name of the API resource to deploy.
	// +required
	APIRef string `json:"apiRef"`
	// gateway specifies the target gateway and optionally the listener and virtual host.
	// +required
	Gateway DeploymentGatewayRef `json:"gateway"`
	// strategy overrides API/gateway defaults for this deployment.
	// +optional
	Strategy *StrategyConfig `json:"strategy,omitempty"`
}

// DeploymentGatewayRef identifies the target gateway and listener for a deployment.
type DeploymentGatewayRef struct {
	// name is the name of the target Gateway (required).
	// +required
	Name string `json:"name"`
	// listener is the name of the target Listener (optional; auto-resolved if gateway has exactly one).
	// +optional
	Listener string `json:"listener,omitempty"`
}

// DeploymentStatus defines the observed state of Deployment.
type DeploymentStatus struct {
	// phase is the current lifecycle phase: Pending, Deploying, Deployed, Failed.
	// +optional
	Phase string `json:"phase,omitempty"`
	// conditions represent the current state of the Deployment.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// xdsSnapshotVersion is the current xDS snapshot version for this deployment.
	// +optional
	XDSSnapshotVersion string `json:"xdsSnapshotVersion,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="API",type=string,JSONPath=`.spec.apiRef`
// +kubebuilder:printcolumn:name="Gateway",type=string,JSONPath=`.spec.gateway.name`

// Deployment is the Schema for the deployments API
type Deployment struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Deployment
	// +required
	Spec DeploymentSpec `json:"spec"`

	// status defines the observed state of Deployment
	// +optional
	Status DeploymentStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DeploymentList contains a list of Deployment
type DeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Deployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Deployment{}, &DeploymentList{})
}
