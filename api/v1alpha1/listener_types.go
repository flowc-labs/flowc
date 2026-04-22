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

// ListenerSpec defines the desired state of Listener.
type ListenerSpec struct {
	// gatewayRef is the name of the parent Gateway resource.
	// +required
	GatewayRef string `json:"gatewayRef"`
	// port is the bind port; must be unique within the referenced gateway.
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port uint32 `json:"port"`
	// address is the bind address (default "0.0.0.0").
	// +optional
	// +kubebuilder:default="0.0.0.0"
	Address string `json:"address,omitempty"`
	// tls contains optional TLS configuration.
	// +optional
	TLS *TLSConfig `json:"tls,omitempty"`
	// hostnames are the hostnames for this listener (SNI matching + virtual host domains).
	// Each hostname may be an exact name or a wildcard (e.g., "*.example.com").
	// If empty, matches all hostnames.
	// +optional
	Hostnames []string `json:"hostnames,omitempty"`
	// http2 enables HTTP/2 on the listener.
	// +optional
	HTTP2 bool `json:"http2,omitempty"`
}

// ListenerStatus defines the observed state of Listener.
type ListenerStatus struct {
	// phase is the current lifecycle phase.
	// +optional
	Phase string `json:"phase,omitempty"`
	// conditions represent the current state of the Listener.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Gateway",type=string,JSONPath=`.spec.gatewayRef`
// +kubebuilder:printcolumn:name="Port",type=integer,JSONPath=`.spec.port`

// Listener is the Schema for the listeners API
type Listener struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Listener
	// +required
	Spec ListenerSpec `json:"spec"`

	// status defines the observed state of Listener
	// +optional
	Status ListenerStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ListenerList contains a list of Listener
type ListenerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Listener `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Listener{}, &ListenerList{})
}
