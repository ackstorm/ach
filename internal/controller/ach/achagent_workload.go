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
	configMountDir       = "/etc/ach-agent"
	configFileName       = "config.json"
	configFilePath       = configMountDir + "/" + configFileName
	pvcVolumeName        = "ach-agent-state"
	configHashAnnotation = "ach.ackstorm.ai/config-hash"
	agentLabelKey        = "ach.ackstorm.ai/agent"
	defaultGraceSeconds  = int64(120)

	// Distributed placement (contract 2026-09-14): three role containers in ONE pod.
	roleChannels  = "channels"
	roleHarness   = "harness"
	roleEngine    = "engine"
	channelsPort  = int32(8080) // public ingress + channels health port; the Service targets it
	harnessPort   = int32(8090) // harness /healthz + /readyz (distributed only)
	enginePort    = int32(8081) // engine /healthz + /readyz (distributed only)
	agentUID      = int64(10001)
	ipcDir        = "/run/ach-agent"
	ephemeralBase = "/tmp/ach-agent" // data base when persistence is off
)

// rolePorts is the distributed HTTP probe/ingress port per role (contract 2026-09-14 rev 2):
// every container serves /healthz + /readyz on its port, bound to 0.0.0.0 by ach-agent.
// Unix sockets stay internal transports — the operator never probes them. Standalone keeps
// the configured health port instead (resolveHealthPort).
var rolePorts = map[string]int32{roleChannels: channelsPort, roleHarness: harnessPort, roleEngine: enginePort}

var (
	defaultCPURequest    = resource.MustParse("100m")
	defaultMemoryRequest = resource.MustParse("128Mi")
	defaultCPULimit      = resource.MustParse("1")
	defaultMemoryLimit   = resource.MustParse("1Gi")
)

func agentResourceName(agentName string) string { return "achagent-" + agentName }

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

// resolvePlacement is the pod topology via the shared agentrender.ResolvePlacement
// (agent overrides profile, empty ⇒ standalone) so buildService, buildDeployment and the
// config hash can never disagree.
func resolvePlacement(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) string {
	return agentrender.ResolvePlacement(a.Spec.Placement, p.Spec.Achagent.Placement)
}

// resolveHealthPort returns the probe/Service targetPort via the shared
// agentrender.ResolveHealth (agent overrides profile), so it can never drift from
// the rendered config health block.
func resolveHealthPort(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) int32 {
	_, port := agentrender.ResolveHealth(a.Spec.Health, p.Spec.Achagent.Health)
	return port
}

// computeConfigHash digests every pod-template input so any change rolls the pod. secretHash is
// a salted HMAC of secret .Data computed by the reconciler (never plaintext here). placement is
// a pod-template input too, so flipping it rolls the pod and WorkloadReady tracks the new
// generation.
func computeConfigHash(configJSON, envJSON, podTemplateJSON []byte, image, secretHash, placement string) string {
	h := sha256.New()
	h.Write(configJSON)
	h.Write(envJSON)
	h.Write(podTemplateJSON)
	h.Write([]byte(image))
	h.Write([]byte(secretHash))
	h.Write([]byte(placement))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// buildAgentEnv is the SINGLE source of the agent container env. The ek is a secretKeyRef
// (never inline); profile env is merged with agent env and reserved ACH_* names are dropped.
func buildAgentEnv(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile, defaultBaseURL string) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "ACH_BASE_URL", Value: agentrender.ResolveAchBaseURL(a.Spec.Ach, p.Spec.Achagent.Ach, defaultBaseURL)},
		{Name: "ACH_ENVIRONMENT", Value: a.Spec.Capability.Environment},
		{Name: "ACH_CONFIG_PATH", Value: configFilePath},
		// POD_NAMESPACE feeds the ach-memory project slug ({POD_NAMESPACE}-{agent.name}).
		// Without it the harness silently degrades to the bare agent name — the same bank
		// under a different key, with no error anywhere.
		{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}},
		{Name: "ACH_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: a.Spec.Identity.SecretRef.Name},
			Key:                  a.Spec.Identity.SecretRef.Key,
		}}},
	}
	for _, e := range agentrender.ResolveEnv(a.Spec.Env, p.Spec.Env) {
		if strings.HasPrefix(e.Name, "ACH_") {
			continue // reserved — defense-in-depth behind the CEL marker
		}
		env = append(env, e)
	}
	// Channel auth and generated prepare aliases are injected as env vars, NOT
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

// engineEnv is the engine container's env in distributed placement: the operator env
// filtered to the names the resolved engine.forwardEnv selects (agent replaces profile,
// per ResolveEngine). Values and secretKeyRefs are the existing ones — nothing is inlined,
// nothing is copied wholesale, no envFrom. ACH_* can never be selected (the operator owns
// that namespace; renderEngine strips it from config.json for the same reason).
func engineEnv(env []corev1.EnvVar, a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) []corev1.EnvVar {
	eng := agentrender.ResolveEngine(a.Spec.Engine, p.Spec.Achagent.Engine)
	if eng == nil {
		return nil
	}
	allow := map[string]struct{}{}
	for _, n := range eng.ForwardEnv {
		if !strings.HasPrefix(n, "ACH_") {
			allow[n] = struct{}{}
		}
	}
	var out []corev1.EnvVar
	for _, e := range env {
		if _, ok := allow[e.Name]; ok {
			out = append(out, e)
		}
	}
	return out
}

func buildConfigMap(a *achv1alpha1.ACHAgent, configJSON []byte) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		Data:       map[string]string{configFileName: string(configJSON)},
	}
}

func buildServiceAccount(a *achv1alpha1.ACHAgent) *corev1.ServiceAccount {
	falseVal := false
	return &corev1.ServiceAccount{
		ObjectMeta:                   metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		AutomountServiceAccountToken: &falseVal,
	}
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

// buildService fronts the pod on port 8080. Standalone targets the harness health port;
// distributed targets the channels container (contract §5: "distributed public ingress
// is Channels port 8080").
func buildService(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) *corev1.Service {
	target := resolveHealthPort(a, p)
	if resolvePlacement(a, p) == achv1alpha1.PlacementDistributed {
		target = channelsPort
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: agentSelectorLabels(a.Name),
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(int(target))}},
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

func emptyDir(name string) corev1.Volume {
	return corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}
}

// distributedVolumes is the volume set for the three-role pod: config (ConfigMap), ONE
// data volume (the PVC when persistent, an emptyDir otherwise — mounted by subPath, never
// whole), three IPC emptyDirs (directories, not socket files) and a private /tmp per role.
func distributedVolumes(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) []corev1.Volume {
	data := emptyDir(pvcVolumeName)
	if p.Spec.Persistence != nil && p.Spec.Persistence.Enabled {
		data.VolumeSource = corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: agentResourceName(a.Name)}}
	}
	return []corev1.Volume{
		{Name: configVolumeName, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: agentResourceName(a.Name)}}}},
		data,
		emptyDir("ach-agent-ipc-transfer"), emptyDir("ach-agent-ipc-channels"), emptyDir("ach-agent-ipc-engine"),
		emptyDir("tmp-" + roleChannels), emptyDir("tmp-" + roleHarness), emptyDir("tmp-" + roleEngine),
	}
}

// distributedContainers renders the contract's container matrix. env is the operator env
// (buildAgentEnv) — channels and harness get it verbatim, engine gets engineEnv(env).
// ach-agent owns every path under base and /run/ach-agent; the operator only mounts the
// directories the contract names (no tool-specific mounts, nothing derived from engine
// config).
func distributedContainers(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile, env []corev1.EnvVar, resources corev1.ResourceRequirements) []corev1.Container {
	base := ephemeralBase
	if p.Spec.Persistence != nil && p.Spec.Persistence.Enabled {
		base = p.Spec.Persistence.MountPath
	}
	data := func(sub string) corev1.VolumeMount {
		return corev1.VolumeMount{Name: pvcVolumeName, MountPath: base + "/" + sub, SubPath: sub}
	}
	ipc := func(name string, ro bool) corev1.VolumeMount {
		return corev1.VolumeMount{Name: "ach-agent-ipc-" + name, MountPath: ipcDir + "/" + name, ReadOnly: ro}
	}
	// /tmp is listed FIRST so the ephemeral base (/tmp/ach-agent/*) nests under the
	// private /tmp rather than being shadowed by it.
	tmp := func(role string) corev1.VolumeMount {
		return corev1.VolumeMount{Name: "tmp-" + role, MountPath: "/tmp"}
	}
	configMount := corev1.VolumeMount{Name: configVolumeName, MountPath: configFilePath, SubPath: configFileName, ReadOnly: true}

	roles := []struct {
		name   string
		env    []corev1.EnvVar
		ports  []corev1.ContainerPort
		mounts []corev1.VolumeMount
	}{
		{roleChannels, env,
			[]corev1.ContainerPort{{Name: "http", ContainerPort: rolePorts[roleChannels], Protocol: corev1.ProtocolTCP}},
			[]corev1.VolumeMount{tmp(roleChannels), ipc("channels", true)}},
		{roleHarness, env,
			// Named so the PodMonitor (monitoring.coreos.com/v1) can scrape /metrics by port name.
			[]corev1.ContainerPort{{Name: "health", ContainerPort: rolePorts[roleHarness], Protocol: corev1.ProtocolTCP}},
			[]corev1.VolumeMount{tmp(roleHarness), configMount, data("state"), data("workspace"), ipc("transfer", false), ipc("channels", false), ipc("engine", true)}},
		{roleEngine, engineEnv(env, a, p),
			[]corev1.ContainerPort{{Name: "engine", ContainerPort: rolePorts[roleEngine], Protocol: corev1.ProtocolTCP}},
			[]corev1.VolumeMount{tmp(roleEngine), data("home"), data("workspace"), ipc("transfer", false), ipc("engine", false)}},
	}

	falseVal := false
	image := agentrender.ResolveImage(a.Spec.Image, p.Spec.Achagent.Image)
	out := make([]corev1.Container, 0, len(roles))
	for _, r := range roles {
		probe := func(path string) corev1.ProbeHandler {
			return corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt(int(rolePorts[r.name]))}}
		}
		out = append(out, corev1.Container{
			Name:         r.name,
			Image:        image,
			Args:         []string{"--role", r.name}, // command never set: image entrypoint (tini) preserved
			Ports:        r.ports,
			Env:          r.env,
			VolumeMounts: r.mounts,
			Resources:    *resources.DeepCopy(),
			// Contract §4 timings. Initialization (incl. hydration) completes before /readyz
			// succeeds; liveness arms only after startup succeeds (kubelet semantics), so an
			// init failure keeps the container not-Ready and restarts it. ach-agent owns the
			// readiness logic — the operator only renders the schedule.
			StartupProbe:   &corev1.Probe{ProbeHandler: probe("/readyz"), InitialDelaySeconds: 15, PeriodSeconds: 5, TimeoutSeconds: 3, FailureThreshold: 6},
			ReadinessProbe: &corev1.Probe{ProbeHandler: probe("/readyz"), PeriodSeconds: 10, TimeoutSeconds: 3, FailureThreshold: 3},
			LivenessProbe:  &corev1.Probe{ProbeHandler: probe("/healthz"), PeriodSeconds: 20, TimeoutSeconds: 3, FailureThreshold: 3},
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: &falseVal,
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
		})
	}
	return out
}

// buildDeployment builds the single-replica agent Deployment for either placement
// (standalone: one `agent` container; distributed: channels/harness/engine). env is built
// once by the caller (buildAgentEnv) so what's hashed equals what's deployed. Inbound
// channel-auth secrets ride in env (secretKeyRef), never as mounted files.
func buildDeployment(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile, configHash string, env []corev1.EnvVar) (*appsv1.Deployment, error) {
	one := int32(1)
	falseVal, trueVal := false, true

	volumes := []corev1.Volume{{
		Name:         configVolumeName,
		VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: agentResourceName(a.Name)}}},
	}}
	mounts := []corev1.VolumeMount{{Name: configVolumeName, MountPath: configFilePath, SubPath: configFileName, ReadOnly: true}}

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

	port := resolveHealthPort(a, p)
	probe := func(path string) corev1.ProbeHandler {
		return corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt(int(port))}}
	}
	// startupProbe budget tracks engine.startupTimeoutSeconds; liveness only arms after startup
	// succeeds, so a long hydration is not killed.
	startupFail := int32(30) // 30 * 5s = 150s
	if eng := agentrender.ResolveEngine(a.Spec.Engine, p.Spec.Achagent.Engine); eng != nil && eng.StartupTimeoutSeconds != nil && *eng.StartupTimeoutSeconds > 0 {
		startupFail = int32(*eng.StartupTimeoutSeconds/5) + 1
	}

	podLabels := agentLabels(a)
	maps.Copy(podLabels, agentSelectorLabels(a.Name))

	// Both placements pin uid/gid/fsGroup 10001: the image runs as that uid and a
	// fresh cloud PVC (EBS, root-owned 0755) is unwritable without fsGroup — found
	// by ach-agent on a persistent standalone pod (2026-09-15; kind's local-path
	// provisioner hands out 0777 dirs and never showed it). Standalone: the single
	// container, otherwise unchanged. Distributed: the three-role matrix, whose
	// shared data/IPC emptyDirs and PVC subPaths rely on the same fsGroup, plus
	// dropped kubelet Service-link env (~90 ACH_*_SERVICE_* vars from the ach-*
	// Services in the namespace would otherwise land in every container, engine
	// included — discovery is explicit env + DNS, this is not a network control).
	var enableServiceLinks *bool
	uid := agentUID
	podSC := &corev1.PodSecurityContext{
		RunAsNonRoot: &trueVal, RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid,
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
	containers := []corev1.Container{{
		Name:  agentContainerName,
		Image: agentrender.ResolveImage(a.Spec.Image, p.Spec.Achagent.Image),
		// The health port doubles as the harness /metrics port (same server as
		// /healthz + /readyz). Declared named so the PodMonitor
		// (monitoring.coreos.com/v1) can reference it by name for scraping.
		Ports:          []corev1.ContainerPort{{Name: "health", ContainerPort: port, Protocol: corev1.ProtocolTCP}},
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
	if resolvePlacement(a, p) == achv1alpha1.PlacementDistributed {
		volumes = distributedVolumes(a, p)
		containers = distributedContainers(a, p, env, resources)
		enableServiceLinks = &falseVal
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: agentSelectorLabels(a.Name)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: map[string]string{configHashAnnotation: configHash},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            agentResourceName(a.Name),
					AutomountServiceAccountToken:  &falseVal,
					EnableServiceLinks:            enableServiceLinks,
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
		tmpl, err := applyPodTemplateOverlay(dep.Spec.Template, p.Spec.PodTemplate.Raw, a.Name, configHash)
		if err != nil {
			return nil, err
		}
		dep.Spec.Template = tmpl
	}
	return dep, nil
}

// applyPodTemplateOverlay strategic-merges the profile's raw podTemplate over the operator-built
// pod template. Pass-through by design (ponytail: no field guardrails — the profile author already
// controls spec.achagent.image (agent-overridable via ACHAgent.spec.image); a broken overlay is PodTemplateInvalid or a failing rollout, both the
// author's problem). Only operator bookkeeping is re-pinned post-merge: the selector label
// (Deployment selector is immutable) and the config-hash annotation (rolls + WorkloadReady
// staleness detection).
func applyPodTemplateOverlay(base corev1.PodTemplateSpec, overlay []byte, agentName, configHash string) (corev1.PodTemplateSpec, error) {
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
	for k, v := range agentSelectorLabels(agentName) {
		merged.Labels[k] = v
	}
	if merged.Annotations == nil {
		merged.Annotations = map[string]string{}
	}
	merged.Annotations[configHashAnnotation] = configHash
	return merged, nil
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
	case *appsv1.Deployment:
		d := desired.(*appsv1.Deployment)
		e.Labels = d.Labels
		if e.Spec.Selector == nil { // immutable — set only on create
			e.Spec.Selector = d.Spec.Selector
		}
		e.Spec.Replicas = d.Spec.Replicas
		e.Spec.Strategy = d.Spec.Strategy
		e.Spec.Template = d.Spec.Template
	case *corev1.Service:
		d := desired.(*corev1.Service)
		e.Labels = d.Labels
		e.Spec.Type = d.Spec.Type
		e.Spec.Selector = d.Spec.Selector
		if len(e.Spec.Ports) == 0 {
			e.Spec.Ports = d.Spec.Ports
		} else {
			e.Spec.Ports[0].Name = d.Spec.Ports[0].Name
			e.Spec.Ports[0].Port = d.Spec.Ports[0].Port
			e.Spec.Ports[0].Protocol = d.Spec.Ports[0].Protocol
			e.Spec.Ports[0].TargetPort = d.Spec.Ports[0].TargetPort
		}
	case *networkingv1.NetworkPolicy:
		d := desired.(*networkingv1.NetworkPolicy)
		e.Labels = d.Labels
		e.Spec = d.Spec
	}
}
