// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackendTargetRef identifies the route a BackendIdentityPolicy applies to
// (Hub §9.3).
type BackendTargetRef struct {
	// Kind is the routed backend type. CEL-enforced enum.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=MCPServer;A2AAgent
	Kind string `json:"kind"`

	// Name is the bare route segment the Forwarder sees as <name> in
	// /mcp/<name> or /a2a/<name>. MUST satisfy DNS-1123 subdomain rules
	// (≤253 chars, `[a-z0-9]([-a-z0-9.]*[a-z0-9])?`). Pattern enforced
	// per CRD-08 / Hub §9.3.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`
}

// BackendIdentityPolicySpec defines the desired state of BackendIdentityPolicy
// (Hub §9.3, CRD-08).
//
// The CR is the OPT-IN switch for attaching the ACH-signed JWT to forwarded
// /mcp/<name> or /a2a/<name> requests. Without a matching CR, the Forwarder
// strips the client Authorization header and writes none of its own.
//
// CRD-08: forwardIdentityJWT is REQUIRED — no Go zero-value default,
// no kubebuilder default. The explicit false form is a documentation aid;
// admission rejects a CR omitting the field via the resource-root CEL rule
// on the type below.
type BackendIdentityPolicySpec struct {
	// Target identifies the backend route this policy controls.
	//
	// +kubebuilder:validation:Required
	Target BackendTargetRef `json:"target"`

	// ForwardIdentityJWT, when true, instructs the Forwarder to sign and
	// attach the §9.1 ACH JWT to forwarded requests for this target. When
	// false, the Forwarder forwards without an Authorization header (an
	// explicit no-JWT declaration; operationally indistinguishable from no
	// CR at all).
	//
	// REQUIRED per CRD-08. There is no default. Admission rejects a CR
	// that omits the field.
	//
	// +kubebuilder:validation:Required
	ForwardIdentityJWT bool `json:"forwardIdentityJWT"`
}

// BackendIdentityPolicyStatus defines the observed state of
// BackendIdentityPolicy.
//
// Deliberate scope: the reconciler does NOT perform DuplicateTarget
// shadow resolution. Multiple CRs targeting the same (kind, name) are
// allowed; the Forwarder's runtime read path resolves the duplicate
// deterministically by sorting matching CRs by metadata.name ASC and
// using the LAST entry's forwardIdentityJWT value. Operators wanting
// different precedence rename their CRs. This is intentional —
// operator stays dumb, user owns the CR set, no Synced=DuplicateTarget
// condition is written.
type BackendIdentityPolicyStatus struct {
	// ObservedGeneration is the metadata.generation of the CR the reconciler
	// most recently processed.
	//
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions is reserved for future per-CR status surfaces. It is
	// NOT populated today — duplicate targets are resolved by the
	// Forwarder at read time (alphabetically-LAST CR wins) without any
	// operator-side status churn.
	//
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=bip
// +kubebuilder:validation:XValidation:rule="has(self.spec.forwardIdentityJWT)",message="BackendIdentityPolicy.spec.forwardIdentityJWT is REQUIRED with no default (CRD-08)"
// +kubebuilder:printcolumn:name="Target-Kind",type=string,JSONPath=".spec.target.kind"
// +kubebuilder:printcolumn:name="Target-Name",type=string,JSONPath=".spec.target.name"
// +kubebuilder:printcolumn:name="ForwardJWT",type=boolean,JSONPath=".spec.forwardIdentityJWT"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// BackendIdentityPolicy is the Schema for the backendidentitypolicies API
// (Hub §9.3, CRD-08).
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
