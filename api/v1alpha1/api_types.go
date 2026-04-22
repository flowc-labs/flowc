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

// APISpec defines the desired state of API.
type APISpec struct {
	// version is the semver version of this API.
	// +required
	Version string `json:"version"`
	// displayName is a human-friendly display name.
	// +optional
	DisplayName string `json:"displayName,omitempty"`
	// description is a human-readable description.
	// +optional
	Description string `json:"description,omitempty"`
	// context is the base path where this API is exposed (e.g., "/petstore").
	// +required
	Context string `json:"context"`
	// apiType is the specification type: rest, grpc, graphql, websocket, sse.
	// Auto-detected from specContent if empty.
	// +optional
	// +kubebuilder:validation:Enum=rest;grpc;graphql;websocket;sse;""
	APIType string `json:"apiType,omitempty"`
	// specContent holds the full API specification as a string (OpenAPI YAML, proto, etc.).
	// +optional
	SpecContent string `json:"specContent,omitempty"`
	// upstream defines the backend service.
	// +required
	Upstream UpstreamConfig `json:"upstream"`
	// routing defines route matching behavior.
	// +optional
	Routing *RoutingConfig `json:"routing,omitempty"`
	// policyChain is an ordered list of policy instances.
	// +optional
	PolicyChain []PolicyInstance `json:"policyChain,omitempty"`
}

// APIStatus defines the observed state of API.
type APIStatus struct {
	// phase is the current lifecycle phase.
	// +optional
	Phase string `json:"phase,omitempty"`
	// conditions represent the current state of the API.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// parsedInfo contains metadata extracted from the parsed specification.
	// +optional
	ParsedInfo *ParsedInfo `json:"parsedInfo,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Context",type=string,JSONPath=`.spec.context`

// API is the Schema for the apis API
type API struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of API
	// +required
	Spec APISpec `json:"spec"`

	// status defines the observed state of API
	// +optional
	Status APIStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// APIList contains a list of API
type APIList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []API `json:"items"`
}

func init() {
	SchemeBuilder.Register(&API{}, &APIList{})
}
