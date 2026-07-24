// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LocalObjectRef references a resource by name in the CR's namespace.
type LocalObjectRef struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// AchEndpointSpec is the ACH platform coordinate (config: capability.ach.baseUrl + ACH_BASE_URL env).
// BaseURL is optional: it resolves as ACHAgent.spec.ach.baseUrl ?? AgentProfile.spec.achagent.ach.baseUrl ??
// operator ACH_BASE_URL env (agentrender.ResolveAchBaseURL). An empty result blocks the agent.
type AchEndpointSpec struct {
	// +optional
	BaseURL string `json:"baseUrl,omitempty"`
}

// ModelSpec selects the ACH-served model (config: model{name,type,params,thinking}).
type ModelSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=openai;gemini;anthropic
	Type string `json:"type"`
	// Params is an open, unvalidated dict splatted to the model client.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Params *apiextensionsv1.JSON `json:"params,omitempty"`
	// Thinking is the normalized model-level reasoning intent (config: model.thinking).
	// Free-form (no Enum) — ach-agent's Pydantic ThinkingBlock is the single enforcer
	// (D-2 precedent): effort one of minimal|low|medium|high|xhigh, requires enabled=true.
	// +optional
	Thinking *ThinkingSpec `json:"thinking,omitempty"`
}

// AgentDefaults is the shared set of profile defaults that an agent may override.
// AgentProfile.spec.achagent names these defaults, and ACHAgentSpec embeds this
// type inline. Resolution is a per-field deep merge: a field set on the agent
// wins, while an omitted field inherits from the profile. Slices, maps, and
// nested blocks such as engine.pi are atomic and are not recursively merged.
// The resolvers are the source of truth for this behavior. Image is required on
// the profile, but optional on the agent and inherited when omitted.
type AgentDefaults struct {
	// +optional
	Image string `json:"image,omitempty"`
	// +optional
	Ach *AchEndpointSpec `json:"ach,omitempty"`
	// +optional
	Model *ModelSpec `json:"model,omitempty"`
	// +optional
	Engine *EngineSpec `json:"engine,omitempty"`
	// +optional
	Limits *LimitsSpec `json:"limits,omitempty"`
	// +optional
	Health *HealthSpec `json:"health,omitempty"`
}

// ThinkingSpec is the normalized reasoning intent each engine translates for itself
// (pi: models.json reasoning + --thinking; opencode: per-call providerOptions).
type ThinkingSpec struct {
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// +optional
	Effort string `json:"effort,omitempty"`
}

// EngineSpec is the harness-local engine block (config: engine.*). Unset fields are omitted
// (the harness defaults them).
type EngineSpec struct {
	// +optional
	Home string `json:"home,omitempty"`
	// +optional
	WorkDir string `json:"workDir,omitempty"`
	// +optional
	ForwardEnv []string `json:"forwardEnv,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	IdleTTLSeconds *int64 `json:"idleTtlSeconds,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	StartupTimeoutSeconds *int64 `json:"startupTimeoutSeconds,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxToolCalls *int64 `json:"maxToolCalls,omitempty"`
	// Type selects the engine. Free string ("opencode"|"pi"); the harness validates and
	// hard-fails on an unknown value. Omitted → harness default (opencode).
	// +optional
	Type string `json:"type,omitempty"`
	// Pi configures the Pi engine; consulted only when Type == "pi".
	// +optional
	Pi *PiEngineSpec `json:"pi,omitempty"`
}

// PiEngineSpec is the harness-local Pi engine block (config: engine.pi.*) — executable
// knobs ONLY (model identity and thinking intent live in ModelSpec). All fields are
// optional; empty binaryPath/mcpAdapterPath fall back to the image defaults (pi on PATH;
// the vendored adapter at /opt/pi-mcp-adapter/node_modules/pi-mcp-adapter). The
// v0.8.1-only model and thinking-level fields were removed for ach-agent v0.9.0.
type PiEngineSpec struct {
	// +optional
	BinaryPath string `json:"binaryPath,omitempty"`
	// +optional
	McpAdapterPath string `json:"mcpAdapterPath,omitempty"`
}

// LimitsSpec bounds invocations (config: limits.*). Unset → harness default.
type LimitsSpec struct {
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxConcurrentInvocations *int64 `json:"maxConcurrentInvocations,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxInvocationSeconds *int64 `json:"maxInvocationSeconds,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxQueuedTotal *int64 `json:"maxQueuedTotal,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	IdempotencyWindowSeconds *int64 `json:"idempotencyWindowSeconds,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxSteps *int64 `json:"maxSteps,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	TerminalOutputRetries *int64 `json:"terminalOutputRetries,omitempty"`
}

// HealthSpec is the harness HTTP surface (config: health{host,port}). Also drives the
// Service targetPort and the container probes. Harness default port is 8080.
type HealthSpec struct {
	// +optional
	Host string `json:"host,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`
}

// PersistenceSpec configures PVC-backed durable state (config: persistence{enabled,mountPath}).
// +kubebuilder:validation:XValidation:rule="!self.enabled || (has(self.size) && size(self.size) > 0)",message="persistence.size is required when persistence.enabled=true"
type PersistenceSpec struct {
	// +kubebuilder:validation:Required
	Enabled bool `json:"enabled"`
	// +optional
	Size string `json:"size,omitempty"`
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
	// +optional
	// +kubebuilder:default="/var/lib/ach-agent"
	MountPath string `json:"mountPath,omitempty"`
	// RetainPolicy controls PVC lifecycle on ACHAgent deletion. Retain → the PVC is created
	// WITHOUT a controller owner-ref, so it survives agent deletion (operator-managed cleanup).
	// +optional
	// +kubebuilder:validation:Enum=Retain;Delete
	RetainPolicy *string `json:"retainPolicy,omitempty"`
}

// NetworkPolicySpec renders a default-deny EGRESS NetworkPolicy selecting the agent pod.
// Presence is the opt-in: an omitted block means no policy at all (the agent keeps
// unrestricted egress — the pre-feature behaviour). An empty block (`networkPolicy: {}`)
// is deny-all-except-DNS.
//
// Egress-only by design: policyTypes never includes Ingress, so expose.service /
// gateway→agent routing is untouched.
//
// Rules are DECLARED here, not derived from ach.baseUrl: upstream NetworkPolicy has no
// FQDN peer type and ACH_BASE_URL is a URL, so the operator cannot translate the ACH
// endpoint into a peer portably. Declare the forwarder/gateway peer yourself — an
// in-cluster podSelector+namespaceSelector, or an ipBlock CIDR for an external endpoint.
// The operator contributes what only it knows: the pod selector (operator-owned labels),
// the DNS rule, and lifecycle (created/pruned/GC'd with the agent).
type NetworkPolicySpec struct {
	// Egress rules appended after the operator's DNS rule. Raw networking.k8s.io/v1
	// egress rules, pass-through (same contract as podTemplate: the profile author
	// already controls spec.achagent.image, so no field guardrails here). Empty → DNS only,
	// i.e. every other outbound connection is denied.
	// +optional
	Egress []networkingv1.NetworkPolicyEgressRule `json:"egress,omitempty"`
}

// AgentProfileSpec is the reusable infra + defaults half. Agent-scoped defaults
// (image/ach/model/engine/limits/health) live under the named achagent block and
// deep-merge with an ACHAgent's inline AgentDefaults (agent field wins);
// everything else here is profile-only infrastructure an agent cannot override.
type AgentProfileSpec struct {
	// Achagent holds the agent-overridable defaults. image is required here
	// (object-level CEL); the other fields are optional defaults.
	// +kubebuilder:validation:Required
	Achagent AgentDefaults `json:"achagent"`
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
	// ExtraEnv are additional pod-level env vars (e.g. HTTPS_PROXY). Reserved ACH_* names are
	// forbidden — the operator owns them (the ek arrives via identity.secretRef as ACH_TOKEN).
	// +optional
	// +kubebuilder:validation:XValidation:rule="self.all(e, !e.name.startsWith('ACH_'))",message="extraEnv must not set reserved ACH_* vars"
	ExtraEnv []corev1.EnvVar `json:"extraEnv,omitempty"`
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// +optional
	Persistence *PersistenceSpec `json:"persistence,omitempty"`
	// NetworkPolicy renders a default-deny egress NetworkPolicy for the agent pod.
	// Omitted → no policy (unrestricted egress). See NetworkPolicySpec.
	// +optional
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`
	// PodTemplate is a raw strategic-merge-patch overlay applied over the operator-rendered pod
	// template (containers/env/volumes merge by name, scalars user-wins). Pass-through by design
	// (ponytail: no field guardrails — the profile author already controls spec.achagent.image, i.e.
	// everything that runs in the pod). A malformed overlay surfaces as WorkloadApplied=False
	// (PodTemplateInvalid); a merged-but-broken pod surfaces as a failing rollout. Note the
	// extraEnv ACH_* CEL guard does NOT inspect this overlay. After the merge the operator
	// re-pins the selector label and the config-hash annotation.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	PodTemplate *apiextensionsv1.JSON `json:"podTemplate,omitempty"`
}

// AgentProfileStatus is minimal — profiles are read by ACHAgent; they have no side effects.
type AgentProfileStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=aprofile
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=".spec.achagent.image"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 50",message="AgentProfile name must be <= 50 chars (operator derives <=63-char child names)"
// +kubebuilder:validation:XValidation:rule="has(self.spec.achagent) && has(self.spec.achagent.image) && size(self.spec.achagent.image) > 0",message="spec.achagent.image is required (nonempty)"

// AgentProfile is the reusable infra + defaults for a class of agents.
type AgentProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentProfileSpec   `json:"spec,omitempty"`
	Status AgentProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentProfileList contains a list of AgentProfile.
type AgentProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentProfile{}, &AgentProfileList{})
}
