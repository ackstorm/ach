// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RuntimeBlock is the execution-resource half of an Environment (Hub §6, §6.1).
// Names resolve against LiteLLM's runtime state (§17) and are projected by
// the ACH Operator into the LiteLLM access group <environment>.
//
// CRD-02: both runtime and context blocks are always present in the hydrate
// response, even when one is empty. The list-element fields default to []
// so a manifest omitting a sub-field still surfaces an empty slice.
type RuntimeBlock struct {
	// Models lists LiteLLM model names (model_name) included in this Environment.
	// Names are projected into LiteLLM API URLs (the access-group sync path);
	// the looser runtime deny-pattern admits provider-prefixed ("openai/gpt-4")
	// and tagged ("gpt-4o:latest") names while forbidding the URL-injection
	// metacharacters ? # % plus whitespace and control chars (S2 defense-in-depth).
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^?#%\s\x00-\x1f]+$`
	Models []string `json:"models,omitempty"`

	// MCPServers lists LiteLLM MCP server names (server_name).
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^?#%\s\x00-\x1f]+$`
	MCPServers []string `json:"mcpServers,omitempty"`

	// A2AAgents lists LiteLLM A2A agent names (agent_name).
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^?#%\s\x00-\x1f]+$`
	A2AAgents []string `json:"a2aAgents,omitempty"`
}

// ContextBlock is the content-resource half of an Environment (Hub §6, §6.1).
// Names reference ACH-owned content objects (Prompt, Plugin, Artifact CRDs
// or marketplace_plugins rows) and are served by the ACH Content Service.
type ContextBlock struct {
	// Prompts lists referenced Prompt names. Context names map to content
	// filenames served by the Content Service, so the stricter deny-pattern
	// also forbids "/" and "\" (path-traversal) in addition to ? # %
	// whitespace and control chars (S2 defense-in-depth).
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^/\\?#%\s\x00-\x1f]+$`
	Prompts []string `json:"prompts,omitempty"`

	// Plugins lists referenced Plugin (or marketplace plugin) names.
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^/\\?#%\s\x00-\x1f]+$`
	Plugins []string `json:"plugins,omitempty"`

	// Artifacts lists referenced Artifact names.
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^/\\?#%\s\x00-\x1f]+$`
	Artifacts []string `json:"artifacts,omitempty"`
}

// EnvironmentSpec defines the desired state of Environment (CRD-02, Hub §6).
type EnvironmentSpec struct {
	// Runtime is the execution-resource bundle projected into the LiteLLM
	// access group (§6.2). Always present per CRD-02.
	//
	// +kubebuilder:validation:Required
	Runtime RuntimeBlock `json:"runtime"`

	// Context is the content-resource bundle served by Content Service
	// (§10, §15.6). Always present per CRD-02.
	//
	// +kubebuilder:validation:Required
	Context ContextBlock `json:"context"`

	// AuthorizedTeams references LiteLLM Team aliases (§6.1). The Environment
	// is unusable when no entry resolves to an existing LiteLLM Team;
	// admission requires at least one entry per Hub §6 (informational —
	// reconcile-time existence is verified per §6.4).
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	AuthorizedTeams []string `json:"authorizedTeams"`
}

// EnvironmentStatus defines the observed state of Environment (Hub §6.4, §6.6).
type EnvironmentStatus struct {
	// ObservedGeneration is the metadata.generation of the CR the reconciler
	// most recently processed.
	//
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions carries Environment condition types per §6.6 closed set:
	// Available, ContentReady, ExecutionResourcesResolved, AccessGroupSynced.
	//
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// UnresolvedRuntime lists runtime references not currently registered in
	// LiteLLM. Surfaced for `kubectl describe environment` per §6.4. The
	// field contract belongs here from Phase 1; the reconciler in Phase 2
	// rewrites it on every reconcile.
	//
	// +optional
	UnresolvedRuntime *UnresolvedRuntime `json:"unresolvedRuntime,omitempty"`

	// LitellmAccessGroup is the synced LiteLLM access group name (§6.4).
	// Echoed for operator visibility; equals metadata.name when set.
	//
	// +optional
	LitellmAccessGroup string `json:"litellmAccessGroup,omitempty"`
}

// UnresolvedRuntime mirrors the three runtime reference lists (§6.4) and
// names the specific entries that did not resolve against LiteLLM.
type UnresolvedRuntime struct {
	// +optional
	// +kubebuilder:default={}
	Models []string `json:"models,omitempty"`

	// +optional
	// +kubebuilder:default={}
	MCPServers []string `json:"mcpServers,omitempty"`

	// +optional
	// +kubebuilder:default={}
	A2AAgents []string `json:"a2aAgents,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=env
// +kubebuilder:validation:XValidation:rule="has(self.spec.runtime) && has(self.spec.context)",message="Environment.spec must declare both runtime and context blocks (CRD-02)"
// +kubebuilder:validation:XValidation:rule="size(self.spec.authorizedTeams) >= 1",message="Environment.spec.authorizedTeams must contain at least one team (Hub §6)"
// +kubebuilder:printcolumn:name="AccessGroupSynced",type=string,JSONPath=".status.conditions[?(@.type=='AccessGroupSynced')].status"
// +kubebuilder:printcolumn:name="Available",type=string,JSONPath=".status.conditions[?(@.type=='Available')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Environment is the Schema for the environments API (Hub §6).
//
// An Environment is the ACH product boundary: a bundle of runtime
// (models, mcpServers, a2aAgents) and context (prompts, plugins,
// artifacts) capabilities exposed to authorized Teams. The ACH
// Operator reconciles spec.runtime into a LiteLLM access group of
// the same name (§6.2). The CEL XValidation rules above enforce
// CRD-02 at admission.
type Environment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EnvironmentSpec   `json:"spec,omitempty"`
	Status EnvironmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EnvironmentList contains a list of Environment.
type EnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Environment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Environment{}, &EnvironmentList{})
}
