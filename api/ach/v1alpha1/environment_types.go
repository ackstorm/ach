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
	// Names are projected into LiteLLM API request bodies (not ACH URL routing),
	// so the looser deny-pattern admits provider-prefixed ("openai/gpt-4") and
	// tagged ("gpt-4o:latest") names while forbidding URL-injection metacharacters
	// ? # % plus whitespace, control chars (U+0000-U+001F), and DEL (U+007F)
	// (S2 defense-in-depth). Only Models uses the loose pattern; see MCPServers
	// and A2AAgents below for why those must use the strict (no-slash) pattern.
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^?#%\s\x00-\x1f\x7f]+$`
	Models []string `json:"models,omitempty"`

	// MCPServers lists LiteLLM MCP server names (server_name).
	// Names are used as chi route parameters at the forwarder (/mcp/{name});
	// a slash-containing name would be admitted but always 403 (chi matches
	// raw "%2F"-encoded segment against the decoded DB value — never matches).
	// The strict deny-pattern therefore also forbids "/" and "\" in addition
	// to ? # % whitespace, control chars (U+0000-U+001F), and DEL (U+007F)
	// (S2 defense-in-depth).
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^/\\?#%\s\x00-\x1f\x7f]+$`
	MCPServers []string `json:"mcpServers,omitempty"`

	// A2AAgents lists LiteLLM A2A agent names (agent_name).
	// Names are used as chi route parameters at the forwarder (/a2a/{name});
	// same routing constraint as MCPServers — slash-containing names always 403.
	// The strict deny-pattern forbids "/" and "\" in addition to ? # %
	// whitespace, control chars (U+0000-U+001F), and DEL (U+007F)
	// (S2 defense-in-depth).
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^/\\?#%\s\x00-\x1f\x7f]+$`
	A2AAgents []string `json:"a2aAgents,omitempty"`
}

// ContextBlock is the content-resource half of an Environment (Hub §6, §6.1).
// Names reference ACH-owned content objects (Prompt, Plugin, Artifact CRDs
// or marketplace_plugins rows) and are served by the ACH Content Service.
type ContextBlock struct {
	// Prompts lists referenced Prompt names. Context names map to content
	// filenames served by the Content Service, so the strict deny-pattern
	// forbids "/" and "\" (path-traversal) in addition to ? # % whitespace,
	// control chars (U+0000-U+001F), and DEL (U+007F) (S2 defense-in-depth).
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^/\\?#%\s\x00-\x1f\x7f]+$`
	Prompts []string `json:"prompts,omitempty"`

	// Plugins lists referenced plugin names. A bare name ("code-review")
	// resolves to an internal Plugin CRD; a scoped name
	// ("code-review@anthropics-official") resolves to that
	// PluginMarketplace's plugin by exact (marketplace, name). Same strict
	// deny-pattern as Prompts (no "/" "\" ? # % whitespace, control chars
	// or DEL); "@" is permitted as the marketplace separator.
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^/\\?#%\s\x00-\x1f\x7f]+$`
	Plugins []string `json:"plugins,omitempty"`

	// Artifacts lists referenced Artifact names.
	// Same strict deny-pattern as Prompts (no "/" "\" ? # % whitespace
	// control chars or DEL).
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^/\\?#%\s\x00-\x1f\x7f]+$`
	Artifacts []string `json:"artifacts,omitempty"`

	// Skills lists referenced Skill names. A bare name ("pdf-processing")
	// resolves to a Skill CR in the operator namespace; a scoped
	// "name@marketplace" ("branding@ackstorm") resolves a skill discovered
	// inside a SkillMarketplace (the final "@" separates name from marketplace,
	// mirroring context.plugins). Content-gated like Plugins. Same strict
	// deny-pattern as Plugins (no "/" "\" ? # % whitespace, control chars or
	// DEL).
	// +optional
	// +kubebuilder:default={}
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[^/\\?#%\s\x00-\x1f\x7f]+$`
	Skills []string `json:"skills,omitempty"`
}

// EnvironmentSpec defines the desired state of Environment (CRD-02, Hub §6).
type EnvironmentSpec struct {
	// Runtime is the execution-resource bundle projected into the LiteLLM
	// access group (§6.2). Optional in the manifest; defaults to an empty
	// block whose list fields backfill to [] so CRD-02 ("always present in
	// the hydrate response") holds even when the author omits it.
	//
	// +optional
	// +kubebuilder:default={}
	Runtime RuntimeBlock `json:"runtime,omitempty"`

	// Context is the content-resource bundle served by Content Service
	// (§10, §15.6). Optional in the manifest; defaults to an empty block
	// whose list fields backfill to [] so CRD-02 ("always present in the
	// hydrate response") holds even when the author omits it.
	//
	// +optional
	// +kubebuilder:default={}
	Context ContextBlock `json:"context,omitempty"`

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

	// UnresolvedContextPlugins lists context.plugins entries whose content
	// has not yet been synced (last_successful_refresh IS NULL in the
	// plugins or marketplace_plugins projection row). A non-empty list
	// blocks ExecutionResourcesResolved from becoming True — prevents the
	// Available=True false-green that would otherwise let hydrate issue a
	// 404 at runtime.
	//
	// Bare entries (no @) resolve against the plugins (CRD) projection
	// table; scoped entries (name@marketplace) resolve against
	// marketplace_plugins. Both arms require last_successful_refresh to
	// be non-null before the plugin is considered content-present.
	//
	// +optional
	// +kubebuilder:default={}
	UnresolvedContextPlugins []string `json:"unresolvedContextPlugins,omitempty"`

	// UnresolvedContextSkills lists context.skills entries whose content has
	// not yet been synced (last_successful_refresh IS NULL in the skills
	// projection row). A non-empty list blocks ExecutionResourcesResolved
	// from becoming True — same content-gating as UnresolvedContextPlugins.
	//
	// +optional
	// +kubebuilder:default={}
	UnresolvedContextSkills []string `json:"unresolvedContextSkills,omitempty"`

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
