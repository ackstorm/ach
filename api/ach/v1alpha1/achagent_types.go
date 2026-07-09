// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IdentitySpec carries the ACH ek_ (config: injected as ACH_TOKEN env via secretKeyRef).
type IdentitySpec struct {
	// SecretRef points at a Secret holding the ek_ (create it yourself, e.g. `ach-cli keys create`).
	// +kubebuilder:validation:Required
	SecretRef SecretKeyRef `json:"secretRef"`
}

// ExcludeSpec is the governance gate ABOVE the model (config: capability.filter.exclude).
type ExcludeSpec struct {
	// +optional
	Tools []string `json:"tools,omitempty"`
	// +optional
	McpServers []string `json:"mcpServers,omitempty"`
	// +optional
	Skills []string `json:"skills,omitempty"`
}

// FilterSpec wraps the exclude gate.
type FilterSpec struct {
	// +optional
	Exclude *ExcludeSpec `json:"exclude,omitempty"`
}

// CapabilitySpec is the per-agent capability block (config: capability{type:ach,ach.environment,filter}).
type CapabilitySpec struct {
	// Environment is the ACH Hub Environment name, for documentation/intent only.
	// The ek already scopes the environment server-side, and the harness reads the
	// hydrated environment (manifest.environment) — NOT this field. Optional: omit
	// to let the ek decide; when set it is rendered but never authoritative.
	// +optional
	Environment string `json:"environment,omitempty"`
	// +optional
	Filter *FilterSpec `json:"filter,omitempty"`
}

// PromptSystemSpec is a discriminated persona source (config: prompt.system).
// The ach form MAY carry an optional achFile (rendered as system.file — the schema's SystemAch
// allows `file` as an optional subpath within the named prompt).
// +kubebuilder:validation:XValidation:rule="(self.type=='text' && has(self.text)) || (self.type=='file' && has(self.file)) || (self.type=='ach' && has(self.ach))",message="prompt.system: the block matching type is required"
type PromptSystemSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=text;file;ach
	Type string `json:"type"`
	// +optional
	Text string `json:"text,omitempty"`
	// +optional
	File string `json:"file,omitempty"`
	// +optional
	Ach string `json:"ach,omitempty"`
	// AchFile is an optional subpath within an `ach` prompt (rendered as prompt.system.file).
	// +optional
	AchFile string `json:"achFile,omitempty"`
}

// AgentPromptSpec configures the system prompt (config: prompt).
type AgentPromptSpec struct {
	// +kubebuilder:validation:Required
	System PromptSystemSpec `json:"system"`
	// +optional
	// +kubebuilder:validation:Enum=replace;append
	// +kubebuilder:default=append
	Compose string `json:"compose,omitempty"`
}

// MentalModelSpec is one Hindsight mental model the harness provisions at boot
// (config: memory.hindsight.mentalModels[]). Was a bare id string pre-facade.
type MentalModelSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// SourceQuery is the question the harness runs to build/refresh the model.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SourceQuery string `json:"sourceQuery"`
	// AutoRefresh triggers a refresh after consolidation (harness default false).
	// +optional
	AutoRefresh bool `json:"autoRefresh,omitempty"`
	// MaxTokens caps the rendered summary (harness default 2048). Omit to use it.
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxTokens *int64 `json:"maxTokens,omitempty"`
}

// HindsightSpec is the hindsight memory backend (config: memory.hindsight).
type HindsightSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Endpoint string `json:"endpoint"`
	// Bank is the static, harness-owned memory bank id. NEVER template it from
	// inbound payload (untrusted → cross-tenant memory); per-repo partitioning is
	// via tags, harness-side.
	// +optional
	Bank string `json:"bank,omitempty"`
	// Auth is the admin secret for the harness→Hindsight path (Bearer, NOT the ek_).
	// Same env-only secretKeyRef mechanism as webhook/a2a: the operator injects the
	// value into the pod from this Secret and renders only the env NAME. Omit for an
	// internal/no-auth Hindsight URL.
	// +optional
	Auth *SecretKeyRef `json:"auth,omitempty"`
	// Mission is passed to create_bank at provisioning (free text).
	// +optional
	Mission string `json:"mission,omitempty"`
	// +optional
	MentalModels []MentalModelSpec `json:"mentalModels,omitempty"`
}

// CodememSpec is the codemem memory backend (config: memory.codemem). All fields optional.
type CodememSpec struct {
	// +optional
	DBPath string `json:"dbPath,omitempty"`
	// +optional
	Project string `json:"project,omitempty"`
}

// MemorySpec is a discriminated memory backend (config: memory). Omit for no memory (fail-open).
// Asymmetry is intentional and mirrors the schema: HindsightMemory REQUIRES the hindsight block
// (endpoint has no default); CodememMemory requires only `type` — {"type":"codemem"} is valid
// (dbPath/project are derived/defaulted by the harness).
// +kubebuilder:validation:XValidation:rule="(self.type=='hindsight' && has(self.hindsight)) || (self.type=='codemem')",message="memory.hindsight is required when type=hindsight"
type MemorySpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=hindsight;codemem
	Type string `json:"type"`
	// +optional
	Hindsight *HindsightSpec `json:"hindsight,omitempty"`
	// +optional
	Codemem *CodememSpec `json:"codemem,omitempty"`
}

// WebhookAuthSpec configures webhook auth (config: channels[].webhook.auth; secretRef → secretPath).
// +kubebuilder:validation:XValidation:rule="self.type=='none' || has(self.secretRef)",message="webhook.auth.secretRef is required unless type=none"
// +kubebuilder:validation:XValidation:rule="self.type!='header_token' || (has(self.header) && size(self.header)>0)",message="webhook.auth.header is required when type=header_token"
type WebhookAuthSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=gitlab_token;hmac;header_token;none
	Type string `json:"type"`
	// +optional
	Header string `json:"header,omitempty"`
	// +optional
	SecretRef *SecretKeyRef `json:"secretRef,omitempty"`
}

// WebhookSpec configures a webhook channel (config: channels[].webhook + channels[].source).
type WebhookSpec struct {
	// +kubebuilder:validation:Required
	Auth WebhookAuthSpec `json:"auth"`
	// +optional
	// +kubebuilder:validation:items:Enum=merge_request;issue;note
	GitlabEvents []string `json:"gitlabEvents,omitempty"`
	// BotUsername is the GitLab username the agent posts AS (the egress PAT's
	// user — a distinct fact from the agent name). When set, the harness drops
	// inbound events authored by this user plus gitlab-generated system notes
	// pre-enqueue (loop-guard). Omit → guard off. gitlab source only; ignored
	// for github/generic. Rendered verbatim to channels[].webhook.botUsername.
	// +optional
	BotUsername *string `json:"botUsername,omitempty"`
	// TriggerUsers is an actor allowlist: only these GitLab usernames may
	// trigger the agent (every routed kind: mr/issue/note). Omit → any author
	// triggers. gitlab source only; ignored for github/generic. Rendered
	// verbatim to channels[].webhook.triggerUsers.
	// +optional
	TriggerUsers []string `json:"triggerUsers,omitempty"`
}

// CronSpec configures a cron channel (config: channels[].cron).
type CronSpec struct {
	// +kubebuilder:validation:Required
	Schedule string `json:"schedule"`
	// +optional
	// +kubebuilder:default=UTC
	Timezone string `json:"timezone,omitempty"`
}

// QueueSpec configures a redis queue channel (config: channels[].queue; type/ackMode are constants).
type QueueSpec struct {
	// +kubebuilder:validation:Required
	Key string `json:"key"`
}

// A2AAuthSpec configures a2a inbound auth (config: channels[].a2a.auth; secretRef → secretPath).
type A2AAuthSpec struct {
	// +optional
	// +kubebuilder:default=x-a2a-custom-api-key
	Header string `json:"header,omitempty"`
	// +kubebuilder:validation:Required
	SecretRef SecretKeyRef `json:"secretRef"`
}

// A2ASpec configures an a2a channel (config: channels[].a2a; mode async-only in v1).
type A2ASpec struct {
	// +kubebuilder:validation:Required
	Auth A2AAuthSpec `json:"auth"`
}

// SessionSpec selects which opencode conversation a channel turn reuses and
// bounds its growth (config: channels[].session). type is the discriminator;
// key is the {{ }} template, valid ONLY when type==custom. Omitting the whole
// block lets the harness apply its own default (type: none). This changes only
// which session a turn reuses — the router lane key (event.session_key) is
// unaffected.
// +kubebuilder:validation:XValidation:rule="self.type != 'custom' ? !has(self.key) : (has(self.key) && size(self.key) > 0)",message="session.key is required (non-empty) iff type is custom, forbidden otherwise"
type SessionSpec struct {
	// none: fresh session per event, deleted post-turn. auto: reuse the
	// channel-derived session_key. custom: reuse the session named by key.
	// +optional
	// +kubebuilder:default=none
	// +kubebuilder:validation:Enum=auto;none;custom
	Type string `json:"type,omitempty"`
	// Key is the {{ }} session template (payload.* / internal.*). REQUIRED iff
	// type==custom, FORBIDDEN otherwise. An empty render falls back to none + WARN.
	// +optional
	Key *string `json:"key,omitempty"`
	// MaxTokens caps growth: once the previous turn's input_tokens exceed it,
	// apply overflow (auto/custom only; ignored for none).
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxTokens *int64 `json:"maxTokens,omitempty"`
	// Overflow: compact summarizes the session in place; rotate starts a fresh
	// session and deletes the old one.
	// +optional
	// +kubebuilder:default=compact
	// +kubebuilder:validation:Enum=compact;rotate
	Overflow string `json:"overflow,omitempty"`
}

// ChannelSpec is one inbound channel (config: channels[]).
// +kubebuilder:validation:XValidation:rule="(self.type=='webhook' && has(self.webhook)) || (self.type=='cron' && has(self.cron)) || (self.type=='queue' && has(self.queue)) || (self.type=='a2a' && has(self.a2a))",message="channels: the block matching type is required"
// +kubebuilder:validation:XValidation:rule="self.type=='webhook' || !has(self.source)",message="channels.source is only valid for webhook channels"
type ChannelSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=webhook;cron;queue;a2a
	Type string `json:"type"`
	// +optional
	// +kubebuilder:validation:Enum=gitlab;github;generic
	Source string `json:"source,omitempty"`
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Concurrency *int64 `json:"concurrency,omitempty"`
	// +optional
	Session *SessionSpec `json:"session,omitempty"`
	// +optional
	Prompt string `json:"prompt,omitempty"`
	// +optional
	Webhook *WebhookSpec `json:"webhook,omitempty"`
	// +optional
	Cron *CronSpec `json:"cron,omitempty"`
	// +optional
	Queue *QueueSpec `json:"queue,omitempty"`
	// +optional
	A2A *A2ASpec `json:"a2a,omitempty"`
}

// ExposeSpec controls how an agent is reachable. Both axes default false —
// an agent is fully private (harness Pod only, no Service, no public route)
// unless it explicitly opts in. gateway requires service (the gateway proxies
// to the Service; there is nothing to route to without it).
// +kubebuilder:validation:XValidation:rule="!self.gateway || self.service",message="expose.gateway requires expose.service"
type ExposeSpec struct {
	// Service creates the ClusterIP Service (achagent-<name>) so in-cluster
	// peers (a2a) or your own ingress can reach the harness. Required for any
	// inbound HTTP channel (webhook/a2a) to be reachable at all.
	// +optional
	Service bool `json:"service,omitempty"`
	// Gateway publishes the agent on the shared ACH gateway
	// (/agents/{ns}/{service} route + status.gatewayURL). Requires service.
	// +optional
	Gateway bool `json:"gateway,omitempty"`
}

// McpServerSpec is one harness-managed MCP server (rendered into config
// mcpServers[<name>]). Discriminated by type: repoCheckout is HARNESS-HOSTED (the
// harness runs a checkout_repo facade, injecting the agent's ek_); local/remote are
// PASSTHROUGH (opencode launches a stdio subprocess / connects to a remote endpoint
// directly, NOT via the ACH proxy). The operator renders the list into the config's
// mcpServers map keyed by name. Distinct from the Environment's ACH-fronted MCP set
// (hydrated as runtime.mcpServers) — different namespace, no collision.
// +kubebuilder:validation:XValidation:rule="(self.type=='repoCheckout' && has(self.repoCheckout)) || (self.type=='local' && has(self.local)) || (self.type=='remote' && has(self.remote))",message="mcpServers: the block matching type is required"
type McpServerSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=repoCheckout;local;remote
	Type string `json:"type"`
	// +optional
	RepoCheckout *RepoCheckoutSpec `json:"repoCheckout,omitempty"`
	// +optional
	Local *LocalMcpSpec `json:"local,omitempty"`
	// +optional
	Remote *RemoteMcpSpec `json:"remote,omitempty"`
}

// RepoCheckoutSpec configures the harness-hosted checkout_repo tool. The harness reads
// gitlab://{project}/archive/{ref} from the hydrated MCP server named by
// sourceMcpServerId (with the agent's ek_, harness-side) and extracts it into a
// per-checkout dir under tmpBase, TTL-swept. A sourceMcpServerId that names no MCP
// server the agent's Environment exposes makes the tool fail-soft at runtime (no
// crash); ACH does not cross-validate it at admission (see the 2026-07-07 addendum).
type RepoCheckoutSpec struct {
	// SourceMcpServerID is the hydrated runtime.mcpServers[].id whose endpoint serves
	// the gitlab archive resource.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SourceMcpServerID string `json:"sourceMcpServerId"`
	// TmpBase is the parent dir for per-checkout tmp dirs (harness default /tmp/gitlab).
	// +optional
	TmpBase string `json:"tmpBase,omitempty"`
	// TTLSeconds bounds how long a stale checkout lingers before the next call sweeps
	// it (harness default 3600).
	// +optional
	// +kubebuilder:validation:Minimum=0
	TTLSeconds *int64 `json:"ttlSeconds,omitempty"`
}

// LocalMcpSpec is a passthrough stdio MCP server opencode launches as a subprocess.
// env lists extra var NAMES to forward to the subprocess (ACH_*/ek_ are stripped
// defensively); wire their values into the pod via profile.spec.extraEnv (secretKeyRef).
type LocalMcpSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Command string `json:"command"`
	// +optional
	Args []string `json:"args,omitempty"`
	// +optional
	Env []string `json:"env,omitempty"`
}

// RemoteMcpSpec is a passthrough remote MCP endpoint opencode connects to directly.
// headers values are ${env:NAME} refs (NAMES, never secret values); wire the env into
// the pod via profile.spec.extraEnv (secretKeyRef). SECURITY: opencode receives the
// resolved header, so a co-resident same-uid agent CAN read it — front the server via
// ACH hydrate instead if that is unacceptable.
type RemoteMcpSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`
	// +optional
	Headers map[string]string `json:"headers,omitempty"`
}

// ACHAgentSpec defines the desired state of an agent instance.
type ACHAgentSpec struct {
	// +kubebuilder:validation:Required
	ProfileRef LocalObjectRef `json:"profileRef"`
	// +kubebuilder:validation:Required
	Identity IdentitySpec `json:"identity"`
	// +kubebuilder:validation:Required
	Capability CapabilitySpec `json:"capability"`
	// Model overrides the profile's default model.
	// +optional
	Model *ModelSpec `json:"model,omitempty"`
	// Limits overrides the profile's default limits.
	// +optional
	Limits *LimitsSpec `json:"limits,omitempty"`
	// Ach overrides the profile's ACH endpoint (e.g. point this agent at an external ACH).
	// Empty inherits AgentProfile.spec.ach ?? operator ACH_BASE_URL.
	// +optional
	Ach *AchEndpointSpec `json:"ach,omitempty"`
	// Health overrides the profile's health block (host/port). Drives the config health block,
	// the Service targetPort, and the container probes together — always resolved as one unit.
	// +optional
	Health *HealthSpec `json:"health,omitempty"`
	// +optional
	Prompt *AgentPromptSpec `json:"prompt,omitempty"`
	// +optional
	Memory *MemorySpec `json:"memory,omitempty"`
	// Expose controls reachability (Service + gateway route). Omit for a fully
	// private agent (no Service, no public URL).
	// +optional
	Expose *ExposeSpec `json:"expose,omitempty"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Channels []ChannelSpec `json:"channels"`
	// MCPServers are harness-managed MCP servers (repoCheckout / local / remote)
	// rendered into the config's mcpServers map. Presence = enabled; omit for none.
	// +optional
	// +listType=map
	// +listMapKey=name
	MCPServers []McpServerSpec `json:"mcpServers,omitempty"`
}

// ACHAgentStatus is the observed state.
type ACHAgentStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// GatewayURL is the inbound base URL for this agent on the shared gateway,
	// e.g. https://ach.example.com/agents/ach-system/achagent-gh. The last
	// segment is the agent's Service name; the gateway forwards anything
	// after it verbatim to that Service (append the harness route you need,
	// e.g. /channels/{name}/events for a webhook channel, or the a2a path).
	// Set only when the agent opts into gateway exposure (expose.gateway).
	// The host segment is only populated when the operator has
	// ACH_PUBLIC_BASE_URL (or, as a fallback, ACH_BASE_URL) configured;
	// otherwise this is the path-only form for the caller to prefix with
	// their own ingress host.
	// +optional
	GatewayURL string `json:"gatewayURL,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=agent
// +kubebuilder:printcolumn:name="Profile",type=string,JSONPath=".spec.profileRef.name"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Gateway",type=string,JSONPath=".status.gatewayURL",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 50",message="ACHAgent name must be <= 50 chars (operator derives <=63-char child names)"

// ACHAgent is a running agent instance.
type ACHAgent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ACHAgentSpec   `json:"spec,omitempty"`
	Status ACHAgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ACHAgentList contains a list of ACHAgent.
type ACHAgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ACHAgent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ACHAgent{}, &ACHAgentList{})
}
