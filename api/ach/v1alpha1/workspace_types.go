// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkspaceAgentReference pins a Workspace to one ACHAgent object incarnation.
type WorkspaceAgentReference struct {
	// Name is resolved in the Workspace's namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// UID is the canonical lowercase UID of the ACHAgent.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
	UID string `json:"uid"`
}

// WorkspaceResourceSpec describes the identity and requested replica count of one
// per-agent execution Workspace. Execution configuration is resolved from the owning
// ACHAgent's current AgentProfile by the operator; this API deliberately carries no
// image, resource, profile, secret, storage, or routing-key material.
// +kubebuilder:validation:XValidation:rule="self.agentRef == oldSelf.agentRef",message="agentRef is immutable"
// +kubebuilder:validation:XValidation:rule="self.workspaceRef == oldSelf.workspaceRef",message="workspaceRef is immutable"
type WorkspaceResourceSpec struct {
	// AgentRef identifies the same-namespace ACHAgent by name and immutable UID.
	// +kubebuilder:validation:Required
	AgentRef WorkspaceAgentReference `json:"agentRef"`
	// WorkspaceRef is the full lowercase SHA-256 digest of this Workspace identity.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{64}$`
	WorkspaceRef string `json:"workspaceRef"`
	// Replicas is required and limited to sleeping (0) or active (1). It has no default,
	// so creating a Workspace cannot activate execution implicitly.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	Replicas int32 `json:"replicas"`
	// ExpectedStatefulSetUID is an optional precondition for operating on an existing
	// execution StatefulSet.
	// +optional
	ExpectedStatefulSetUID string `json:"expectedStatefulSetUID,omitempty"`
	// ExpectedPodUID is an optional pod replacement precondition used when scaling to zero.
	// +optional
	ExpectedPodUID string `json:"expectedPodUID,omitempty"`
}

// WorkspaceStatus records operator observations for one requested generation.
type WorkspaceStatus struct {
	// ObservedGeneration is the Workspace generation evaluated by the operator.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// StatefulSetUID is the UID of the execution StatefulSet observed for this Workspace.
	// +optional
	StatefulSetUID string `json:"statefulSetUID,omitempty"`
	// Conditions use the standard Kubernetes condition shape. The operator publishes
	// OwnerResolved, WorkloadApplied, WorkloadReady, and UpdatePending conditions.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// Workspace requests a sleeping or active execution workload for one ACHAgent workspace.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ws
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=".spec.replicas"
// +kubebuilder:printcolumn:name="Applied",type=string,JSONPath=".status.conditions[?(@.type=='WorkloadApplied')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type Workspace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   WorkspaceResourceSpec `json:"spec"`
	Status WorkspaceStatus       `json:"status,omitempty"`
}

// WorkspaceList contains a list of Workspace resources.
// +kubebuilder:object:root=true
type WorkspaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workspace `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workspace{}, &WorkspaceList{})
}
