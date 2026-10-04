// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/agentrender"
)

const (
	agentContainerName   = "agent"
	configVolumeName     = "ach-agent-config"
	configMountDir       = "/etc/ach-runtime"
	configFileName       = "config.json"
	configFilePath       = configMountDir + "/" + configFileName
	pvcVolumeName        = "ach-agent-state"
	configHashAnnotation = "ach.ackstorm.ai/config-hash"
	agentLabelKey        = "ach.ackstorm.ai/agent"
	defaultGraceSeconds  = int64(120)
	agentUID             = int64(10001)
	controlServicePort   = 8081
	bootstrapFileName    = "bootstrap.json"

	// controlProbePort is the fixed workspace-v1 control HTTP port (contract §11) the
	// container's health server binds, the probes target, and buildService's targetPort
	// uses. Render2 has no health block (the legacy agent-config-v1 health.host/port CRD
	// knob is compatibility-only now, read by nothing that builds real k8s objects) — a
	// fixed port here can never drift from what the rendered config's infrastructure.control
	// block and the control pod actually listen on.
	controlProbePort = int32(8080)
)

// controlCommand is the fixed workspace-v1 control launch command (contract §11): the
// control pod's harness role, never an unverified arbitrary image entrypoint and never the
// legacy distributed-placement "--role" args convention.
var controlCommand = []string{"python", "-m", "ach_runtime", "control", "--config", configFilePath}

// Contract §11 names live in agentrender (HarnessName, ExecutionServiceAccountName,
// ControlName): RenderInfrastructureV1 embeds them in the rendered wire config, so the k8s
// objects built here MUST use the same functions or the config lies about the topology.

// effectiveControlServiceAccountName is what the control pod runs as: the profile's stable
// controlServiceAccountName when set, else agentrender.HarnessName(uid).
func effectiveControlServiceAccountName(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) string {
	return agentrender.ControlServiceAccountName(string(a.UID), p)
}

var (
	defaultCPURequest    = resource.MustParse("100m")
	defaultMemoryRequest = resource.MustParse("128Mi")
	defaultCPULimit      = resource.MustParse("1")
	defaultMemoryLimit   = resource.MustParse("1Gi")
)

func agentResourceName(agentName string) string { return "achagent-" + agentName }

func derefEnv(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func agentLabels(a *achv1alpha1.ACHAgent) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "ach-agent",
		"app.kubernetes.io/managed-by": "ach-operator",
		agentLabelKey:                  a.Name,
	}
}

func agentSelectorLabels(agentName string) map[string]string {
	return map[string]string{agentLabelKey: agentName}
}

// computeConfigHash digests every pod-template input so any change rolls the pod. secretHash is
// a salted HMAC of secret .Data computed by the reconciler (never plaintext here).
func computeConfigHash(configJSON, envJSON, podTemplateJSON []byte, image, secretHash string) string {
	h := sha256.New()
	h.Write(configJSON)
	h.Write(envJSON)
	h.Write(podTemplateJSON)
	h.Write([]byte(image))
	h.Write([]byte(secretHash))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// buildAgentEnv is the SINGLE source of the agent container env. The ek is a secretKeyRef
// (never inline); profile env is merged with agent env and reserved ACH_* names are dropped.
// overrideEnv returns base with each entry in overrides replacing any existing entry of the
// same Name (global authority — the reconciler uses this to let the operator's runtime
// storage env win over anything buildAgentEnv already set at the same name), appending any
// override name not already present.
func overrideEnv(base, overrides []corev1.EnvVar) []corev1.EnvVar {
	idx := make(map[string]int, len(base))
	for i, e := range base {
		idx[e.Name] = i
	}
	out := append([]corev1.EnvVar(nil), base...)
	for _, o := range overrides {
		if i, ok := idx[o.Name]; ok {
			out[i] = o
			continue
		}
		out = append(out, o)
	}
	return out
}

func buildAgentEnv(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile, defaultBaseURL string) []corev1.EnvVar {
	resolvedAch := agentrender.ResolveAch(a.Spec.Ach, p.Spec.Achagent.Ach)
	env := []corev1.EnvVar{
		{Name: "ACH_BASE_URL", Value: agentrender.ResolveAchBaseURL(a.Spec.Ach, p.Spec.Achagent.Ach, defaultBaseURL)},
		{Name: "ACH_ENVIRONMENT", Value: derefEnv(resolvedAch.Environment)},
		{Name: "ACH_CONFIG_PATH", Value: configFilePath},
		// POD_NAMESPACE feeds the ach-memory project slug ({POD_NAMESPACE}-{agent.name}).
		// Without it the harness silently degrades to the bare agent name — the same bank
		// under a different key, with no error anywhere.
		{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}},
		{Name: agentrender.AchIdentityAliasEnv, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: a.Spec.Ach.Identity.SecretRef.Name},
			Key:                  a.Spec.Ach.Identity.SecretRef.Key,
		}}},
	}
	for _, e := range agentrender.ResolveEnv(a.Spec.Env, p.Spec.Env) {
		if strings.HasPrefix(e.Name, "ACH_") {
			continue // reserved — defense-in-depth behind the CEL marker
		}
		env = append(env, e)
	}
	// Channel auth and generated handoff/script aliases are injected as env vars, NOT
	// mounted files: the agent runs same-uid as the harness and can read mounted
	// secret files, but not the harness process env (PR_SET_DUMPABLE=0). Value via
	// secretKeyRef only — never an inline literal in the PodSpec.
	for _, ref := range agentrender.ChannelSecretEnv(*p, *a) {
		env = append(env, corev1.EnvVar{Name: ref.EnvName, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: ref.SecretName},
			Key:                  ref.Key,
		}}})
	}
	// ach-memory user-key secret (memory.achMemory.auth) — same env-not-file wiring.
	if ref := agentrender.MemorySecretEnv(*a); ref != nil {
		env = append(env, corev1.EnvVar{Name: ref.EnvName, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: ref.SecretName},
			Key:                  ref.Key,
		}}})
	}
	return env
}

// buildConfigMap carries config.json only (contract §11 scope reset — no broker/TLS
// material rides alongside it any more).
func buildConfigMap(a *achv1alpha1.ACHAgent, configJSON []byte) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		Data:       map[string]string{configFileName: string(configJSON)},
	}
}

// buildWorkspaceRole grants the Harness (control pod) exactly what it needs to create and
// manage per-Workspace execution StatefulSets/Services/Pods in its own namespace (contract
// §11 creator contract) — named ach-harness-<uid>, same as the control ServiceAccount it
// binds. No Secret/RBAC CRUD, TokenReview, pod create/exec, or cross-namespace grant: the
// Harness's own runtime client is responsible for restricting operations to its own
// UID/owner-labelled Workspace objects — this namespace Role is not per-agent isolation by
// itself.
func buildWorkspaceRole(a *achv1alpha1.ACHAgent) *rbacv1.Role {
	name := agentrender.HarnessName(string(a.UID))
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: a.Namespace, Labels: agentLabels(a)},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"apps"}, Resources: []string{"statefulsets"}, Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
			{APIGroups: []string{"apps"}, Resources: []string{"statefulsets/scale"}, Verbs: []string{"get", "update", "patch"}},
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list", "watch", "delete"}},
			{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
		},
	}
}

// buildWorkspaceRoleBinding binds buildWorkspaceRole to the control (Harness) ServiceAccount
// only: controlSA is the effective control SA (the profile's stable one, or the per-agent
// ach-harness-<uid>); the Role/RoleBinding names stay per-agent. The execution ServiceAccount is never bound to this or any
// other Role (contract §11: execution stays unbound).
func buildWorkspaceRoleBinding(a *achv1alpha1.ACHAgent, controlSA string) *rbacv1.RoleBinding {
	name := agentrender.HarnessName(string(a.UID))
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: a.Namespace, Labels: agentLabels(a)},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: controlSA, Namespace: a.Namespace}},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: name},
	}
}

// buildControlServiceAccount is the control (harness) pod's identity, named per contract
// §11 (ach-harness-<uid>) — RenderInfrastructureV1 embeds this exact name in
// infrastructure.control.serviceAccount. Not applied when the profile sets
// controlServiceAccountName (that SA is external; ach-harness-<uid> SAs orphaned by a
// profile switch are not deleted by the operator).
func buildControlServiceAccount(a *achv1alpha1.ACHAgent) *corev1.ServiceAccount {
	trueVal := true
	return &corev1.ServiceAccount{
		ObjectMeta:                   metav1.ObjectMeta{Name: agentrender.HarnessName(string(a.UID)), Namespace: a.Namespace, Labels: agentLabels(a)},
		AutomountServiceAccountToken: &trueVal,
	}
}

// buildExecutionServiceAccount is the execution pod's identity (contract §11:
// ach-execution-<uid>). The Workspace CR that schedules execution pods against this SA is
// deferred to v0.1.1 — this scaffolding is applied ahead of that so the identity already
// exists and matches infrastructure.execution.serviceAccount in the rendered config.
func buildExecutionServiceAccount(a *achv1alpha1.ACHAgent) *corev1.ServiceAccount {
	falseVal := false
	return &corev1.ServiceAccount{
		ObjectMeta:                   metav1.ObjectMeta{Name: agentrender.ExecutionServiceAccountName(string(a.UID)), Namespace: a.Namespace, Labels: agentLabels(a)},
		AutomountServiceAccountToken: &falseVal,
	}
}

// buildControlService is the control pod's headless governing Service (contract §11:
// ach-control-<agent name>, port 8081) — infrastructure.execution.controlEndpoint/facadeEndpoint
// are built from this exact DNS name by RenderInfrastructureV1. ClusterIP: None since the
// control StatefulSet has a single replica and callers address the Service name directly.
func buildControlService(a *achv1alpha1.ACHAgent) *corev1.Service {
	name := agentrender.ControlName(a.Name)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: a.Namespace, Labels: agentLabels(a)},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  agentSelectorLabels(a.Name),
			Ports:     []corev1.ServicePort{{Name: "control", Port: controlServicePort, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(controlServicePort)}},
		},
	}
}

// executionBootstrap is the execution pod's bootstrap.json (contract §11 scope reset): just
// enough for an execution pod to find control, before it has its own full config. Field
// values are copied verbatim from the already-rendered infrastructure.execution block —
// never recomputed — so this can't drift from what Render2 embeds in the agent-facing
// config. No auth block: D2 owns authenticated application calls (signed mini-harness
// bearer + HMAC facade auth), out of this task's scope.
type executionBootstrap struct {
	BootstrapVersion string                   `json:"bootstrapVersion"`
	Agent            agentrender.WSAgentBlock `json:"agent"`
	ControlEndpoint  string                   `json:"controlEndpoint"`
	FacadeEndpoint   string                   `json:"facadeEndpoint"`
}

// buildExecutionBootstrapConfigMap renders the execution pod's public bootstrap ConfigMap
// (contract §11: ach-execution-<uid>, carrying bootstrap.json only — no broker/TLS material).
func buildExecutionBootstrapConfigMap(a *achv1alpha1.ACHAgent, infra agentrender.WSInfrastructureBlock) (*corev1.ConfigMap, error) {
	boot := executionBootstrap{
		BootstrapVersion: "workspace-v1",
		Agent:            agentrender.WSAgentBlock{Name: a.Name, Namespace: a.Namespace, UID: string(a.UID)},
		ControlEndpoint:  infra.Execution.ControlEndpoint,
		FacadeEndpoint:   infra.Execution.FacadeEndpoint,
	}
	data, err := json.Marshal(boot)
	if err != nil {
		return nil, fmt.Errorf("marshal bootstrap.json: %w", err)
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: agentrender.ExecutionServiceAccountName(string(a.UID)), Namespace: a.Namespace, Labels: agentLabels(a)},
		Data:       map[string]string{bootstrapFileName: string(data)},
	}, nil
}

func buildPVC(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) (*corev1.PersistentVolumeClaim, error) {
	qty, err := resource.ParseQuantity(p.Spec.Persistence.Size)
	if err != nil {
		return nil, err
	}
	var scName *string
	if p.Spec.Persistence.StorageClassName != "" {
		scName = &p.Spec.Persistence.StorageClassName
	}
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: qty}},
			StorageClassName: scName,
		},
	}, nil
}

// buildService fronts the pod on the fixed workspace-v1 control port 8080 (contract §11;
// unlike the retired agent-config-v1 health.port knob, p/a are now unused for port
// resolution — kept as parameters for call-site symmetry with the other builders).
func buildService(a *achv1alpha1.ACHAgent, _ *achv1alpha1.AgentProfile) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: agentSelectorLabels(a.Name),
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(int(controlProbePort))}},
		},
	}
}

// dnsEgressRule allows name resolution. Always first in every rendered policy: without
// it, a default-deny egress policy breaks DNS and every outbound call fails with a
// resolution error that looks nothing like a policy denial — the #1 NetworkPolicy footgun.
//
// ponytail: port-53-to-anywhere rather than a kube-dns podSelector. CoreDNS labels and
// node-local DNS cache addresses vary per distro, and a wrong selector fails closed and
// silently, which is the worst failure mode for a security control. Known ceiling: this
// does not stop DNS-tunnel exfiltration. If a deployment needs DNS pinned to kube-dns,
// add a `dns:` knob to NetworkPolicySpec — do not widen this rule.
func dnsEgressRule() networkingv1.NetworkPolicyEgressRule {
	udp, tcp := corev1.ProtocolUDP, corev1.ProtocolTCP
	port := intstr.FromInt(53)
	return networkingv1.NetworkPolicyEgressRule{
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: &udp, Port: &port},
			{Protocol: &tcp, Port: &port},
		},
	}
}

// needsNetworkPolicy reports whether the operator renders the agent's egress policy.
// Presence of the block is the opt-in; absence keeps the pre-feature unrestricted egress.
func needsNetworkPolicy(p *achv1alpha1.AgentProfile) bool {
	return p.Spec.NetworkPolicy != nil
}

// buildNetworkPolicy renders the agent's egress allowlist: DNS plus the profile-declared
// rules, denying everything else. Egress-only — policyTypes omits Ingress so gateway→agent
// routing (expose.service) is unaffected.
//
// The operator does not derive the ACH peer: ach.baseUrl is a URL and upstream
// NetworkPolicy has no FQDN peer type. The profile author declares peers; the operator
// contributes the pod selector (its own labels), DNS, and lifecycle.
//
// Deliberately NOT part of computeConfigHash: the policy is not a pod-template input, so
// editing it must not roll the pod.
func buildNetworkPolicy(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) *networkingv1.NetworkPolicy {
	// Fresh slice: p comes from the informer cache and must never be appended into.
	egress := []networkingv1.NetworkPolicyEgressRule{dnsEgressRule()}
	egress = append(egress, p.Spec.NetworkPolicy.Egress...)
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: agentSelectorLabels(a.Name)},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      egress,
		},
	}
}

// needsService reports whether the operator creates the ClusterIP Service.
// Now an explicit opt-in (expose.service) rather than inferred from channel
// type: an inbound HTTP channel is only reachable when the author asks for a
// Service. Fully private by default.
func needsService(a *achv1alpha1.ACHAgent) bool {
	return a.Spec.Expose != nil && a.Spec.Expose.Service
}

// exposeGateway reports whether the agent opts into shared-gateway routing.
// CEL guarantees gateway ⇒ service, so an exposed agent always has a Service.
func exposeGateway(a *achv1alpha1.ACHAgent) bool {
	return a.Spec.Expose != nil && a.Spec.Expose.Gateway
}

// buildStatefulSet builds the single-replica, single-container control StatefulSet (contract
// §11: "control one-container StatefulSet"). env is built once by the caller (buildAgentEnv)
// so what's hashed equals what's deployed. Inbound channel-auth secrets ride in env
// (secretKeyRef), never as mounted files.
//
// Per-Workspace execution StatefulSets themselves are NOT built here: the Harness running in
// THIS control pod creates them directly at runtime against buildExecutionServiceAccount/
// buildExecutionBootstrapConfigMap — that consumption is real in v0.1.0. Only the declarative
// Workspace CR (a user-facing k8s resource for managing workspaces) is deferred to v0.1.1;
// the execution identity/bootstrap resources the Harness needs are not deferred.
// ServiceName is the control pod's own headless Service (buildControlService) — required by
// StatefulSet and also the exact DNS name RenderInfrastructureV1 embeds as controlEndpoint.
func buildStatefulSet(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile, configHash string, env []corev1.EnvVar) (*appsv1.StatefulSet, error) {
	one := int32(1)
	falseVal, trueVal := false, true

	volumes := buildReservedInfraVolumes(a)
	mounts := buildReservedInfraMounts()

	if p.Spec.Persistence != nil && p.Spec.Persistence.Enabled {
		volumes = append(volumes, corev1.Volume{Name: pvcVolumeName, VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: agentResourceName(a.Name)}}})
		mounts = append(mounts, corev1.VolumeMount{Name: pvcVolumeName, MountPath: p.Spec.Persistence.MountPath})
	}

	grace := defaultGraceSeconds
	if p.Spec.TerminationGracePeriodSeconds != nil {
		grace = *p.Spec.TerminationGracePeriodSeconds
	}

	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: defaultCPURequest.DeepCopy(), corev1.ResourceMemory: defaultMemoryRequest.DeepCopy()},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: defaultCPULimit.DeepCopy(), corev1.ResourceMemory: defaultMemoryLimit.DeepCopy()},
	}
	if p.Spec.Resources != nil {
		resources = *p.Spec.Resources.DeepCopy()
	}

	probe := func(path string) corev1.ProbeHandler {
		return corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt(int(controlProbePort))}}
	}
	// startupProbe budget tracks engine.startupTimeoutSeconds; liveness only arms after startup
	// succeeds, so a long hydration is not killed.
	startupFail := int32(30) // 30 * 5s = 150s
	if eng := agentrender.ResolveEngine(a.Spec.Engine, p.Spec.Achagent.Engine); eng != nil && eng.StartupTimeoutSeconds != nil && *eng.StartupTimeoutSeconds > 0 {
		startupFail = int32(*eng.StartupTimeoutSeconds/5) + 1
	}

	podLabels := agentLabels(a)
	maps.Copy(podLabels, agentSelectorLabels(a.Name))

	// Pin uid/gid/fsGroup 10001: the image runs as that uid and a fresh cloud PVC (EBS,
	// root-owned 0755) is unwritable without fsGroup — found by ach-agent on a persistent
	// standalone pod (2026-09-15; kind's local-path provisioner hands out 0777 dirs and
	// never showed it).
	uid := agentUID
	podSC := &corev1.PodSecurityContext{
		RunAsNonRoot: &trueVal, RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid,
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
	containers := []corev1.Container{{
		Name:    agentContainerName,
		Image:   agentrender.ResolveImage(a.Spec.Image, p.Spec.Achagent.Image),
		Command: controlCommand,
		// The health port doubles as the harness /metrics port (same server as
		// /healthz + /readyz). Declared named so the PodMonitor
		// (monitoring.coreos.com/v1) can reference it by name for scraping.
		Ports:          []corev1.ContainerPort{{Name: "health", ContainerPort: controlProbePort, Protocol: corev1.ProtocolTCP}},
		Env:            env,
		VolumeMounts:   mounts,
		Resources:      resources,
		StartupProbe:   &corev1.Probe{ProbeHandler: probe("/readyz"), PeriodSeconds: 5, FailureThreshold: startupFail},
		ReadinessProbe: &corev1.Probe{ProbeHandler: probe("/readyz"), PeriodSeconds: 10, FailureThreshold: 3},
		LivenessProbe:  &corev1.Probe{ProbeHandler: probe("/healthz"), PeriodSeconds: 20, FailureThreshold: 3},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}}

	sts := &appsv1.StatefulSet{
		// Name is the contract §11 control name (ach-control-<agent name>, agentrender.ControlName), matching ServiceName
		// below verbatim — not the legacy achagent-<name> scheme other children still use.
		ObjectMeta: metav1.ObjectMeta{Name: agentrender.ControlName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &one,
			ServiceName: agentrender.ControlName(a.Name),
			Selector:    &metav1.LabelSelector{MatchLabels: agentSelectorLabels(a.Name)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: map[string]string{configHashAnnotation: configHash},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            effectiveControlServiceAccountName(a, p),
					AutomountServiceAccountToken:  &trueVal,
					TerminationGracePeriodSeconds: &grace,
					ImagePullSecrets:              p.Spec.ImagePullSecrets,
					NodeSelector:                  p.Spec.NodeSelector,
					Tolerations:                   p.Spec.Tolerations,
					SecurityContext:               podSC,
					Volumes:                       volumes,
					Containers:                    containers,
				},
			},
		},
	}

	if p.Spec.PodTemplate != nil && len(p.Spec.PodTemplate.Raw) > 0 {
		tmpl, err := applyPodTemplateOverlay(sts.Spec.Template, p.Spec.PodTemplate.Raw, a, configHash)
		if err != nil {
			return nil, err
		}
		sts.Spec.Template = tmpl
	}
	return sts, nil
}

// applyPodTemplateOverlay strategic-merges the profile's raw podTemplate over the operator-built
// pod template. Pass-through by design (ponytail: no field guardrails — the profile author already
// controls spec.achagent.image (agent-overridable via ACHAgent.spec.image); a broken overlay is PodTemplateInvalid or a failing rollout, both the
// author's problem). Only operator bookkeeping is re-pinned post-merge: the selector label
// (Deployment selector is immutable) and the config-hash annotation (rolls + WorkloadReady
// staleness detection).
func applyPodTemplateOverlay(base corev1.PodTemplateSpec, overlay []byte, a *achv1alpha1.ACHAgent, configHash string) (corev1.PodTemplateSpec, error) {
	baseJSON, err := json.Marshal(base)
	if err != nil {
		return base, fmt.Errorf("marshal pod template: %w", err)
	}
	mergedJSON, err := strategicpatch.StrategicMergePatch(baseJSON, overlay, corev1.PodTemplateSpec{})
	if err != nil {
		return base, fmt.Errorf("podTemplate overlay: %w", err)
	}
	var merged corev1.PodTemplateSpec
	if err := json.Unmarshal(mergedJSON, &merged); err != nil {
		return base, fmt.Errorf("podTemplate overlay result: %w", err)
	}
	if merged.Labels == nil {
		merged.Labels = map[string]string{}
	}
	for k, v := range agentSelectorLabels(a.Name) {
		merged.Labels[k] = v
	}
	if merged.Annotations == nil {
		merged.Annotations = map[string]string{}
	}
	merged.Annotations[configHashAnnotation] = configHash

	// Fixed infrastructure re-pin (October 1 TLS ruling: "fixed infra mounts cannot be
	// replaced by profile overlays"). Volumes.name and VolumeMount.mountPath are both
	// strategic-merge keys, so an overlay can delete ($patch:delete), rename-collide, or
	// redirect-the-source of any of these by targeting the same key — the merge above
	// cannot tell an attacker/mistake from a legitimate field add. Re-assert them
	// unconditionally after every merge, same idiom as the label/annotation re-pin above.
	merged.Spec.Volumes = replaceReservedVolumes(merged.Spec.Volumes, buildReservedInfraVolumes(a))

	// Reject a removed/replaced control container outright (contract §11: "control
	// one-container StatefulSet") rather than silently re-pinning mounts onto nothing, or
	// onto a wrong second container an overlay introduced — a mount re-pin alone cannot
	// close a $patch:delete-the-container-by-name-then-add-a-differently-named-one overlay,
	// since there would be no container named agentContainerName left to re-pin onto.
	if len(merged.Spec.Containers) != 1 {
		return base, fmt.Errorf("podTemplate overlay: must result in exactly one container (contract §11: control one-container StatefulSet), got %d", len(merged.Spec.Containers))
	}
	if merged.Spec.Containers[0].Name != agentContainerName {
		return base, fmt.Errorf("podTemplate overlay: must not remove or rename the %q container", agentContainerName)
	}
	merged.Spec.Containers[0].Command = controlCommand
	merged.Spec.Containers[0].VolumeMounts = replaceReservedMounts(merged.Spec.Containers[0].VolumeMounts, buildReservedInfraMounts())

	merged.Spec.ServiceAccountName = base.Spec.ServiceAccountName // already the effective control SA
	automount := true
	merged.Spec.AutomountServiceAccountToken = &automount

	return merged, nil
}

// buildReservedInfraVolumes/buildReservedInfraMounts are the single source of truth for the
// fixed config plumbing (contract §11 scope reset — broker-token/leaf-TLS plumbing is gone,
// not deferred) — called both by buildStatefulSet (normal build) and applyPodTemplateOverlay
// (re-pin after a merge), so there is exactly one place that knows their shape.
func buildReservedInfraVolumes(a *achv1alpha1.ACHAgent) []corev1.Volume {
	return []corev1.Volume{
		{
			Name:         configVolumeName,
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: agentResourceName(a.Name)}}},
		},
	}
}

// buildReservedInfraMounts mounts the config ConfigMap as a single read-only DIRECTORY at
// configMountDir (no SubPath) — a SubPath mount never live-updates on a ConfigMap change,
// unlike a directory mount.
func buildReservedInfraMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{Name: configVolumeName, MountPath: configMountDir, ReadOnly: true},
	}
}

// replaceReservedVolumes drops any volume sharing a reserved Name (whether the overlay
// deleted our entry, or redefined it under the same merge key with a different source) and
// appends the authoritative ones — closing both the "removal" and "redirect" cases with the
// same operation, since both leave the slice without OUR entry for that name.
func replaceReservedVolumes(got, reserved []corev1.Volume) []corev1.Volume {
	reservedNames := make(map[string]bool, len(reserved))
	for _, v := range reserved {
		reservedNames[v.Name] = true
	}
	out := make([]corev1.Volume, 0, len(got)+len(reserved))
	for _, v := range got {
		if !reservedNames[v.Name] {
			out = append(out, v)
		}
	}
	return append(out, reserved...)
}

// replaceReservedMounts drops any mount sharing a reserved Name OR a reserved MountPath (the
// latter closes a same-path-different-name collision a bare name check would miss) and
// appends the authoritative ones.
func replaceReservedMounts(got, reserved []corev1.VolumeMount) []corev1.VolumeMount {
	reservedNames := make(map[string]bool, len(reserved))
	reservedPaths := make(map[string]bool, len(reserved))
	for _, m := range reserved {
		reservedNames[m.Name] = true
		reservedPaths[m.MountPath] = true
	}
	out := make([]corev1.VolumeMount, 0, len(got)+len(reserved))
	for _, m := range got {
		if !reservedNames[m.Name] && !reservedPaths[m.MountPath] {
			out = append(out, m)
		}
	}
	return append(out, reserved...)
}

// copySpec copies desired's mutable fields onto the fetched existing inside CreateOrUpdate
// (never the whole object — that would clobber resourceVersion / immutable fields).
func copySpec(existing, desired client.Object) {
	switch e := existing.(type) {
	case *corev1.ConfigMap:
		d := desired.(*corev1.ConfigMap)
		e.Labels, e.Data = d.Labels, d.Data
	case *corev1.ServiceAccount:
		d := desired.(*corev1.ServiceAccount)
		e.Labels, e.AutomountServiceAccountToken = d.Labels, d.AutomountServiceAccountToken
	case *appsv1.StatefulSet:
		d := desired.(*appsv1.StatefulSet)
		e.Labels = d.Labels
		if e.Spec.Selector == nil { // immutable — set only on create
			e.Spec.Selector = d.Spec.Selector
		}
		e.Spec.ServiceName = d.Spec.ServiceName // immutable post-create; same value every pass, harmless to resend
		e.Spec.Replicas = d.Spec.Replicas
		e.Spec.Template = d.Spec.Template
	case *corev1.Service:
		d := desired.(*corev1.Service)
		e.Labels = d.Labels
		e.Spec.Type = d.Spec.Type
		e.Spec.Selector = d.Spec.Selector
		if len(e.Spec.Ports) != len(d.Spec.Ports) {
			e.Spec.Ports = d.Spec.Ports
		} else {
			for i := range e.Spec.Ports {
				e.Spec.Ports[i].Name = d.Spec.Ports[i].Name
				e.Spec.Ports[i].Port = d.Spec.Ports[i].Port
				e.Spec.Ports[i].Protocol = d.Spec.Ports[i].Protocol
				e.Spec.Ports[i].TargetPort = d.Spec.Ports[i].TargetPort
			}
		}
	case *networkingv1.NetworkPolicy:
		d := desired.(*networkingv1.NetworkPolicy)
		e.Labels = d.Labels
		e.Spec = d.Spec
	case *rbacv1.Role:
		d := desired.(*rbacv1.Role)
		e.Labels, e.Rules = d.Labels, d.Rules
	case *rbacv1.RoleBinding:
		d := desired.(*rbacv1.RoleBinding)
		e.Labels, e.Subjects, e.RoleRef = d.Labels, d.Subjects, d.RoleRef
	}
}
