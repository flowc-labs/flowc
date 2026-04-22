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

// GatewaySpec defines the desired state of Gateway.
type GatewaySpec struct {
	// nodeId is the Envoy node ID for xDS; must be unique across gateways.
	// +required
	NodeID string `json:"nodeId"`
	// defaults are optional strategy defaults for APIs deployed to this gateway.
	// +optional
	Defaults *StrategyConfig `json:"defaults,omitempty"`
}

// GatewayStatus defines the observed state of Gateway.
type GatewayStatus struct {
	// phase is the current lifecycle phase: Pending, Ready, Error.
	// +optional
	Phase string `json:"phase,omitempty"`
	// conditions represent the current state of the Gateway.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Node ID",type=string,JSONPath=`.spec.nodeId`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`

// Gateway is the Schema for the gateways API
type Gateway struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Gateway
	// +required
	Spec GatewaySpec `json:"spec"`

	// status defines the observed state of Gateway
	// +optional
	Status GatewayStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GatewayList contains a list of Gateway
type GatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Gateway `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Gateway{}, &GatewayList{})
}
