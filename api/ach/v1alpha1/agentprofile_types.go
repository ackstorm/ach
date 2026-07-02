// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
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
type AchEndpointSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	BaseURL string `json:"baseUrl"`
}

// ModelSpec selects the ACH-served model (config: model{name,type,params}).
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

// PodSecuritySpec sets the agent pod's run-as identity. RunAsNonRoot is always
// enforced by the operator regardless of these fields. FSGroup is what lets a
// non-root harness read the 0440-mode channel-secret files (webhook/a2a): it
// owns the mounted secrets + state PVC and joins the process's supplementary
// groups. Set it (matching the image's runtime gid) whenever the agent has a
// webhook/a2a channel or persistence.
type PodSecuritySpec struct {
	// RunAsUser is the container uid. Must be non-root.
	// +optional
	// +kubebuilder:validation:Minimum=1
	RunAsUser *int64 `json:"runAsUser,omitempty"`
	// RunAsGroup is the container primary gid.
	// +optional
	// +kubebuilder:validation:Minimum=1
	RunAsGroup *int64 `json:"runAsGroup,omitempty"`
	// FSGroup owns mounted volumes and joins the process supplementary groups so
	// the non-root harness can read 0440 channel-secret files and write the PVC.
	// +optional
	// +kubebuilder:validation:Minimum=1
	FSGroup *int64 `json:"fsGroup,omitempty"`
}

// AgentProfileSpec is the reusable infra + defaults half.
type AgentProfileSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`
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
	// Security sets the pod run-as identity (runAsUser/runAsGroup/fsGroup).
	// RunAsNonRoot is always enforced regardless.
	// +optional
	Security *PodSecuritySpec `json:"security,omitempty"`
	// +kubebuilder:validation:Required
	Ach AchEndpointSpec `json:"ach"`
	// +optional
	Model *ModelSpec `json:"model,omitempty"`
	// +optional
	Engine *EngineSpec `json:"engine,omitempty"`
	// +optional
	Limits *LimitsSpec `json:"limits,omitempty"`
	// +optional
	Health *HealthSpec `json:"health,omitempty"`
	// +optional
	Persistence *PersistenceSpec `json:"persistence,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`
}

// AgentProfileStatus is minimal — profiles are read by ACHAgent; they have no side effects.
type AgentProfileStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=aprofile
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=".spec.image"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 50",message="AgentProfile name must be <= 50 chars (operator derives <=63-char child names)"

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
