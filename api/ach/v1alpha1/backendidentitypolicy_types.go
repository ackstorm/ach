// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// BackendIdentityPolicySpec defines the desired state of BackendIdentityPolicy.
type BackendIdentityPolicySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of BackendIdentityPolicy. Edit backendidentitypolicy_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// BackendIdentityPolicyStatus defines the observed state of BackendIdentityPolicy.
type BackendIdentityPolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// BackendIdentityPolicy is the Schema for the backendidentitypolicies API.
type BackendIdentityPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackendIdentityPolicySpec   `json:"spec,omitempty"`
	Status BackendIdentityPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BackendIdentityPolicyList contains a list of BackendIdentityPolicy.
type BackendIdentityPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackendIdentityPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BackendIdentityPolicy{}, &BackendIdentityPolicyList{})
}
