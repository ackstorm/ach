// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KeyGrantRequesters names who may exchange for the grant's identity. It is a
// block so other requester kinds can sit beside ServiceAccounts.
type KeyGrantRequesters struct {
	// ServiceAccounts are exact "<namespace>/<name>" ServiceAccounts, both
	// parts DNS-1123. No patterns.
	//
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:MaxLength=317
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?/[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	// +listType=set
	ServiceAccounts []string `json:"serviceAccounts"`
}

// KeyGrantSpec allows POST /platform/keys/exchange for one identity in every
// combination of its Environments and Requesters — and nothing that would
// only match by combining lists from different grants.
type KeyGrantSpec struct {
	// Identity is exactly one ACH identity: an email, lower-case. No patterns.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=254
	// +kubebuilder:validation:Pattern=`^[a-z0-9._%+-]+@[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)+$`
	Identity string `json:"identity"`

	// Environments are exact Environment names in this namespace.
	//
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	// +listType=set
	Environments []string `json:"environments"`

	// Requesters name who may exchange.
	//
	// +kubebuilder:validation:Required
	Requesters KeyGrantRequesters `json:"requesters"`

	// Budget caps EACH exchange key this grant covers, through that key's own
	// "key:<id>" tag. When several grants cover one key the lowest maxBudget
	// applies. Removing it leaves the last ceiling in place.
	//
	// +optional
	Budget *BudgetBlock `json:"budget,omitempty"`
}

// KeyGrantStatus is the observed state of a KeyGrant.
type KeyGrantStatus struct {
	// ObservedGeneration is the metadata.generation the reconciler last processed.
	//
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions carry Ready: False with reason IdentityUnknown (the identity
	// has never logged in to ACH) or EnvironmentMissing.
	//
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=kg
// +kubebuilder:printcolumn:name="Identity",type=string,JSONPath=".spec.identity"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// KeyGrant lets the named ServiceAccounts exchange their pod token for an ACH
// credential of Identity in the named Environments. A grant naming a shared
// ServiceAccount is usable by every pod running that ServiceAccount. Writing
// one is the power to act as Identity: the chart's keygrant-editor ClusterRole is bound to
// nobody and is never aggregated into admin or edit.
type KeyGrant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KeyGrantSpec   `json:"spec,omitempty"`
	Status KeyGrantStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KeyGrantList contains a list of KeyGrant.
type KeyGrantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KeyGrant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KeyGrant{}, &KeyGrantList{})
}
