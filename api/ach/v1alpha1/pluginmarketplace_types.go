// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PluginMarketplaceSpec defines the desired state of PluginMarketplace.
type PluginMarketplaceSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of PluginMarketplace. Edit pluginmarketplace_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// PluginMarketplaceStatus defines the observed state of PluginMarketplace.
type PluginMarketplaceStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PluginMarketplace is the Schema for the pluginmarketplaces API.
type PluginMarketplace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PluginMarketplaceSpec   `json:"spec,omitempty"`
	Status PluginMarketplaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PluginMarketplaceList contains a list of PluginMarketplace.
type PluginMarketplaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PluginMarketplace `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PluginMarketplace{}, &PluginMarketplaceList{})
}
