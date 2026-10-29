// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// LiteLLMConnectionSpec defines the desired LiteLLM endpoint consumed by ACH.
type LiteLLMConnectionSpec struct {
	// Endpoint is the base URL of the LiteLLM instance.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Endpoint string `json:"endpoint"`

	// MasterKeySecretRef points to the Secret key that carries the LiteLLM
	// master key. The Secret must live in the same namespace as the CR.
	//
	// +kubebuilder:validation:Required
	MasterKeySecretRef SecretKeyRef `json:"masterKeySecretRef"`
}

// SecretKeyRef identifies a key in a same-namespace Secret.
type SecretKeyRef struct {
	// Name is the Kubernetes Secret name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key is the data key inside the Secret.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// LiteLLMConnectionStatus defines the observed LiteLLM connection state.
type LiteLLMConnectionStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=llmconn
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'default'",message="LiteLLMConnection name must be 'default'"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// LiteLLMConnection is the singleton connection definition used by the ACH
// operator. v1alpha1 admits only LiteLLMConnection/default per namespace.
type LiteLLMConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LiteLLMConnectionSpec   `json:"spec,omitempty"`
	Status LiteLLMConnectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LiteLLMConnectionList contains a list of LiteLLMConnection.
type LiteLLMConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LiteLLMConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LiteLLMConnection{}, &LiteLLMConnectionList{})
}
