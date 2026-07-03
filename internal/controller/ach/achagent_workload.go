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
)

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

func resolveHealthPort(p *achv1alpha1.AgentProfile) int32 {
	if p.Spec.Health != nil && p.Spec.Health.Port != 0 {
		return p.Spec.Health.Port
	}
	return agentrender.DefaultHealthPort
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
// (never inline); reserved ACH_* names in extraEnv are dropped (the operator owns them).
func buildAgentEnv(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "ACH_BASE_URL", Value: p.Spec.Ach.BaseURL},
		{Name: "ACH_ENVIRONMENT", Value: a.Spec.Capability.Environment},
		{Name: "ACH_CONFIG_PATH", Value: configFilePath},
		{Name: "ACH_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: a.Spec.Identity.SecretRef.Name},
			Key:                  a.Spec.Identity.SecretRef.Key,
		}}},
	}
	for _, e := range p.Spec.ExtraEnv {
		if strings.HasPrefix(e.Name, "ACH_") {
			continue // reserved — defense-in-depth behind the CEL marker
		}
		env = append(env, e)
	}
	// Inbound channel-auth secrets (webhook/a2a) are injected as env vars, NOT
	// mounted files: the agent runs same-uid as the harness and can read mounted
	// secret files, but not the harness process env (PR_SET_DUMPABLE=0). Value via
	// secretKeyRef only — never an inline literal in the PodSpec.
	for _, ref := range agentrender.ChannelSecretEnv(*a) {
		env = append(env, corev1.EnvVar{Name: ref.EnvName, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: ref.SecretName},
			Key:                  ref.Key,
		}}})
	}
	return env
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

func buildService(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: agentSelectorLabels(a.Name),
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(int(resolveHealthPort(p)))}},
		},
	}
}

func needsService(a *achv1alpha1.ACHAgent) bool {
	for i := range a.Spec.Channels {
		switch a.Spec.Channels[i].Type {
		case channelTypeWebhook, "a2a":
			return true
		}
	}
	return false
}

// buildDeployment builds the single-replica agent Deployment. env is built once by the caller
// (buildAgentEnv) so what's hashed equals what's deployed. Inbound channel-auth secrets ride in
// env (secretKeyRef), never as mounted files.
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

	port := resolveHealthPort(p)
	probe := func(path string) corev1.ProbeHandler {
		return corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt(int(port))}}
	}
	// startupProbe budget tracks engine.startupTimeoutSeconds; liveness only arms after startup
	// succeeds, so a long hydration is not killed.
	startupFail := int32(30) // 30 * 5s = 150s
	if p.Spec.Engine != nil && p.Spec.Engine.StartupTimeoutSeconds != nil && *p.Spec.Engine.StartupTimeoutSeconds > 0 {
		startupFail = int32(*p.Spec.Engine.StartupTimeoutSeconds/5) + 1
	}

	podLabels := agentLabels(a)
	maps.Copy(podLabels, agentSelectorLabels(a.Name))

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
					TerminationGracePeriodSeconds: &grace,
					ImagePullSecrets:              p.Spec.ImagePullSecrets,
					NodeSelector:                  p.Spec.NodeSelector,
					Tolerations:                   p.Spec.Tolerations,
					SecurityContext:               &corev1.PodSecurityContext{RunAsNonRoot: &trueVal, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
					Volumes:                       volumes,
					Containers: []corev1.Container{{
						Name:  agentContainerName,
						Image: p.Spec.Image,
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
					}},
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
// controls spec.image; a broken overlay is PodTemplateInvalid or a failing rollout, both the
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
	}
}
