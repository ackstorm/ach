// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"maps"
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/agentrender"
)

const (
	workspaceAgentNameLabel  = "ach.ackstorm.ai/agent"
	workspaceAgentUIDLabel   = "runtime.ach.ackstorm.ai/agent-uid"
	workspaceNameLabel       = "runtime.ach.ackstorm.ai/workspace-name"
	workspaceRefAnnotation   = "runtime.ach.ackstorm.ai/workspace-ref"
	workspaceVerifyKeyEnv    = "ACH_SANDBOX_VERIFY_KEY"
	workspaceEngineSeedLabel = "ach-sandbox-engine"
	workspaceBootstrapVolume = "bootstrap"
	workspaceConfigPath      = "/etc/ach-runtime/bootstrap.json"
	workspaceProbePort       = int32(8080)
	workspaceRuntimeUID      = int64(10001)
)

var (
	workspaceCanonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	workspaceDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// workspaceResourceName matches runtime 0.1.8: the human agent name is normalized and
// bounded independently of UID identity; longer names are disambiguated using the full
// original name before truncation.
func workspaceResourceName(agentName, workspaceRef string) string {
	namePart := strings.ReplaceAll(agentName, ".", "-")
	if len(namePart) > 24 {
		hash := sha256.Sum256([]byte(agentName))
		namePart = strings.TrimRight(namePart[:15], "-") + fmt.Sprintf("-%x", hash[:4])
	}
	refPrefix := workspaceRef
	if len(refPrefix) > 20 {
		refPrefix = refPrefix[:20]
	}
	return "ach-ws-" + namePart + "-" + refPrefix
}

// workspaceEngineVerifyKey returns the public Ed25519 verification key used by the
// execution container. It intentionally consumes the stored Secret bytes as-is; the
// key is neither hex-decoded nor mutated, and no private seed is returned.
func workspaceEngineVerifyKey(key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(workspaceEngineSeedLabel))
	seed := mac.Sum(nil)
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return base64.RawURLEncoding.EncodeToString(publicKey)
}

// buildWorkspaceService builds the headless Service used by the Workspace StatefulSet.
func buildWorkspaceService(w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent) *corev1.Service {
	name := workspaceResourceName(a.Name, w.Spec.WorkspaceRef)
	labels := workspaceLabels(w.Spec.AgentRef.UID, name)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: w.Namespace,
			Labels:          labels,
			Annotations:     map[string]string{workspaceRefAnnotation: w.Spec.WorkspaceRef},
			OwnerReferences: []metav1.OwnerReference{workspaceOwnerReference(a)},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP:                corev1.ClusterIPNone,
			PublishNotReadyAddresses: true,
			Selector:                 maps.Clone(labels),
			Ports:                    []corev1.ServicePort{{Port: workspaceProbePort, TargetPort: intstr.FromInt32(workspaceProbePort)}},
		},
	}
}

// buildWorkspaceStatefulSet builds the execution StatefulSet for a Workspace. It is pure:
// all profile and resolved policy inputs are passed by the caller, and it never reads a
// Secret or performs Kubernetes I/O.
func buildWorkspaceStatefulSet(w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile, verifyKey string) (*appsv1.StatefulSet, error) {
	if w == nil || a == nil || p == nil {
		return nil, fmt.Errorf("Workspace, ACHAgent, and AgentProfile are required")
	}
	if a.UID == "" || !workspaceCanonicalUUID.MatchString(string(a.UID)) {
		return nil, fmt.Errorf("ACHAgent UID must be a canonical lowercase UUID")
	}
	if w.Namespace == "" || w.Namespace != a.Namespace || w.Spec.AgentRef.Name != a.Name || w.Spec.AgentRef.UID != string(a.UID) {
		return nil, fmt.Errorf("Workspace agentRef and namespace must match the owning ACHAgent")
	}
	if !workspaceDigestPattern.MatchString(w.Spec.WorkspaceRef) {
		return nil, fmt.Errorf("Workspace workspaceRef must be a 64-character lowercase hex digest")
	}
	name := workspaceResourceName(a.Name, w.Spec.WorkspaceRef)
	if w.Name != name {
		return nil, fmt.Errorf("Workspace metadata.name %q does not match identity-derived name %q", w.Name, name)
	}
	if w.Spec.Replicas != 0 && w.Spec.Replicas != 1 {
		return nil, fmt.Errorf("Workspace replicas must be 0 or 1, got %d", w.Spec.Replicas)
	}
	if p.Spec.Execution.Image == "" {
		return nil, fmt.Errorf("AgentProfile spec.execution.image is required")
	}
	if verifyKey == "" {
		return nil, fmt.Errorf("execution verification key is required")
	}

	workspacePolicy := agentrender.ResolveWorkspace(a.Spec.Workspace, p.Spec.Achagent.Workspace)
	if workspacePolicy == nil || workspacePolicy.ShutdownTimeoutSeconds == nil || *workspacePolicy.ShutdownTimeoutSeconds < 0 {
		return nil, fmt.Errorf("resolved workspace.shutdownTimeoutSeconds is required and must be nonnegative")
	}
	ephemeralStorage, err := parsePositiveQuantity("spec.execution.ephemeralStorage", p.Spec.Execution.EphemeralStorage)
	if err != nil {
		return nil, err
	}
	resources, err := validatedExecutionResources(p.Spec.Execution.Resources)
	if err != nil {
		return nil, err
	}
	controlSA := agentrender.ControlServiceAccountName(string(a.UID), p)
	infra, err := agentrender.RenderInfrastructureV1(a.Name, string(a.UID), a.Namespace, controlSA, &p.Spec.Execution)
	if err != nil {
		return nil, fmt.Errorf("resolve execution infrastructure: %w", err)
	}
	grace := infra.Execution.TerminationGracePeriodSeconds
	shutdownGrace := *workspacePolicy.ShutdownTimeoutSeconds + 15
	if shutdownGrace > grace {
		grace = shutdownGrace
	}

	labels := workspaceLabels(string(a.UID), name)
	podLabels := maps.Clone(labels)
	if len(validation.IsValidLabelValue(a.Name)) == 0 {
		podLabels[workspaceAgentNameLabel] = a.Name
	}
	annotations := map[string]string{workspaceRefAnnotation: w.Spec.WorkspaceRef}
	zero := w.Spec.Replicas
	falseValue := false
	trueValue := true
	seccomp := corev1.SeccompProfileTypeRuntimeDefault
	uid := workspaceRuntimeUID
	securityContext := &corev1.PodSecurityContext{
		RunAsNonRoot:   &trueValue,
		RunAsUser:      &uid,
		RunAsGroup:     &uid,
		FSGroup:        &uid,
		SeccompProfile: &corev1.SeccompProfile{Type: seccomp},
	}
	container := corev1.Container{
		Name:    "execution",
		Image:   p.Spec.Execution.Image,
		Command: []string{"python", "-m", "ach_runtime", "execution", "--config", workspaceConfigPath},
		Ports:   []corev1.ContainerPort{{ContainerPort: workspaceProbePort}},
		Env: []corev1.EnvVar{
			{Name: workspaceVerifyKeyEnv, Value: verifyKey},
			{Name: "ACH_WORKSPACE_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.labels['" + workspaceNameLabel + "']"}}},
			{Name: "ACH_WORKSPACE_REF", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.annotations['" + workspaceRefAnnotation + "']"}}},
			{Name: "ACH_POD_UID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"}}},
			{Name: "ACH_POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}},
		},
		Resources: *resources,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &falseValue,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromInt32(workspaceProbePort)}}},
		LivenessProbe:  &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(workspaceProbePort)}}},
		VolumeMounts: []corev1.VolumeMount{
			{Name: workspaceBootstrapVolume, MountPath: "/etc/ach-runtime", ReadOnly: true},
			{Name: "workspace", MountPath: "/workspace"},
			{Name: "sessions", MountPath: "/var/lib/ach-runtime/sessions"},
		},
	}
	var emptyDirSize resource.Quantity = ephemeralStorage
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: w.Namespace,
			Labels:          maps.Clone(labels),
			Annotations:     maps.Clone(annotations),
			OwnerReferences: []metav1.OwnerReference{workspaceOwnerReference(a)},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:       &zero,
			ServiceName:    name,
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType},
			Selector:       &metav1.LabelSelector{MatchLabels: maps.Clone(labels)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels, Annotations: maps.Clone(annotations)},
				Spec: corev1.PodSpec{
					ServiceAccountName:            agentrender.ExecutionServiceAccountName(string(a.UID)),
					AutomountServiceAccountToken:  &falseValue,
					EnableServiceLinks:            &falseValue,
					TerminationGracePeriodSeconds: &grace,
					ImagePullSecrets:              append([]corev1.LocalObjectReference(nil), p.Spec.Execution.ImagePullSecrets...),
					NodeSelector:                  maps.Clone(p.Spec.Execution.NodeSelector),
					Tolerations:                   copyTolerations(p.Spec.Execution.Tolerations),
					SecurityContext:               securityContext,
					Containers:                    []corev1.Container{container},
					Volumes: []corev1.Volume{
						{Name: workspaceBootstrapVolume, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "ach-execution-" + string(a.UID)}}}},
						{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &emptyDirSize}}},
						{Name: "sessions", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &emptyDirSize}}},
					},
				},
			},
		},
	}, nil
}

func workspaceLabels(agentUID, name string) map[string]string {
	return map[string]string{workspaceAgentUIDLabel: agentUID, workspaceNameLabel: name}
}

func workspaceOwnerReference(a *achv1alpha1.ACHAgent) metav1.OwnerReference {
	controller := true
	return metav1.OwnerReference{
		APIVersion: "ach.ackstorm.ai/v1alpha1",
		Kind:       "ACHAgent",
		Name:       a.Name,
		UID:        a.UID,
		Controller: &controller,
	}
}

func parsePositiveQuantity(field, value string) (resource.Quantity, error) {
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("%s %q is not a valid Kubernetes quantity: %w", field, value, err)
	}
	if quantity.Sign() <= 0 {
		return resource.Quantity{}, fmt.Errorf("%s must be positive", field)
	}
	return quantity, nil
}

func validatedExecutionResources(input *corev1.ResourceRequirements) (*corev1.ResourceRequirements, error) {
	if input == nil {
		return &corev1.ResourceRequirements{}, nil
	}
	resources := input.DeepCopy()
	for field, values := range map[string]corev1.ResourceList{"requests": resources.Requests, "limits": resources.Limits} {
		for name, quantity := range values {
			parsed, err := resource.ParseQuantity(quantity.String())
			if err != nil {
				return nil, fmt.Errorf("spec.execution.resources.%s.%s is invalid: %w", field, name, err)
			}
			if parsed.Sign() < 0 {
				return nil, fmt.Errorf("spec.execution.resources.%s.%s must be nonnegative", field, name)
			}
		}
	}
	return resources, nil
}

func copyTolerations(input []corev1.Toleration) []corev1.Toleration {
	if input == nil {
		return nil
	}
	out := make([]corev1.Toleration, len(input))
	for i := range input {
		input[i].DeepCopyInto(&out[i])
	}
	return out
}
