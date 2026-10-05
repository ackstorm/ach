// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"maps"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

const (
	testWorkspaceAgentUID  = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	testWorkspaceAgentName = "demo"
	testWorkspaceRef       = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testWorkspaceName      = "ach-ws-demo-0123456789abcdef0123"
	testVerifyKey          = "lZLI1hcEx9ydNM7EoaQ213ri9oNsmILvrFE6AB2YO94"
)

func workspaceTestObjects() (*achv1alpha1.Workspace, *achv1alpha1.ACHAgent, *achv1alpha1.AgentProfile) {
	workspace := &achv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: testWorkspaceName, Namespace: "agents"},
		Spec: achv1alpha1.WorkspaceResourceSpec{
			AgentRef:     achv1alpha1.WorkspaceAgentReference{Name: testWorkspaceAgentName, UID: testWorkspaceAgentUID},
			WorkspaceRef: testWorkspaceRef,
			Replicas:     1,
		},
	}
	agent := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: testWorkspaceAgentName, Namespace: "agents", UID: types.UID(testWorkspaceAgentUID)},
	}
	shutdown := int64(20)
	grace := int64(100)
	profile := &achv1alpha1.AgentProfile{
		Spec: achv1alpha1.AgentProfileSpec{
			Execution: achv1alpha1.ExecutionInfraSpec{
				Image: "registry.example/execution:v1",
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("250m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
				EphemeralStorage: "3Gi",
				ImagePullSecrets: []corev1.LocalObjectReference{{Name: "registry"}},
				NodeSelector:     map[string]string{"pool": "execution"},
				Tolerations: []corev1.Toleration{{
					Key: "dedicated", Value: "execution", Effect: corev1.TaintEffectNoSchedule,
				}},
				TerminationGracePeriodSeconds: &grace,
			},
			Achagent: achv1alpha1.AgentDefaults{
				Workspace: &achv1alpha1.WorkspaceSpec{ShutdownTimeoutSeconds: &shutdown},
			},
		},
	}
	return workspace, agent, profile
}

func TestWorkspaceResourceNameMatchesRuntime018Vectors(t *testing.T) {
	ref := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tt := range []struct{ agentName, want string }{
		{"demo", "ach-ws-demo-0123456789abcdef0123"},
		{"demo.agent", "ach-ws-demo-agent-0123456789abcdef0123"},
		{strings.Repeat("a", 24), "ach-ws-aaaaaaaaaaaaaaaaaaaaaaaa-0123456789abcdef0123"},
		{strings.Repeat("a", 25), "ach-ws-aaaaaaaaaaaaaaa-2f521e2a-0123456789abcdef0123"},
		{"abcdefghijklmn-abcdefghij", "ach-ws-abcdefghijklmn-c58c443f-0123456789abcdef0123"},
		{"abcdefghijklmn.abcdefghij", "ach-ws-abcdefghijklmn-bacb9c3c-0123456789abcdef0123"},
	} {
		got := workspaceResourceName(tt.agentName, ref)
		if got != tt.want {
			t.Errorf("workspaceResourceName(%q) = %q, want %q", tt.agentName, got, tt.want)
		}
		if len(got) > 52 {
			t.Errorf("workspaceResourceName(%q) length = %d, want at most 52", tt.agentName, len(got))
		}
	}
	if got := workspaceResourceName(testWorkspaceAgentName, strings.Repeat("f", 64)); got != "ach-ws-demo-ffffffffffffffffffff" {
		t.Fatalf("workspaceResourceName() with another full digest = %q", got)
	}
}

func TestBuildWorkspaceServicePinsIdentityOwnerSelectorAndPort(t *testing.T) {
	w, a, _ := workspaceTestObjects()
	svc := buildWorkspaceService(w, a)
	labels := map[string]string{
		"runtime.ach.ackstorm.ai/agent-uid":      testWorkspaceAgentUID,
		"runtime.ach.ackstorm.ai/workspace-name": testWorkspaceName,
	}
	if svc.Name != testWorkspaceName || svc.Namespace != "agents" {
		t.Fatalf("service identity = %s/%s", svc.Namespace, svc.Name)
	}
	if !mapsEqual(svc.Labels, labels) || svc.Annotations["runtime.ach.ackstorm.ai/workspace-ref"] != testWorkspaceRef {
		t.Fatalf("service metadata = labels %v annotations %v", svc.Labels, svc.Annotations)
	}
	if svc.Spec.ClusterIP != corev1.ClusterIPNone || !svc.Spec.PublishNotReadyAddresses {
		t.Fatalf("service addressing = clusterIP %q publishNotReady %v", svc.Spec.ClusterIP, svc.Spec.PublishNotReadyAddresses)
	}
	if !mapsEqual(svc.Spec.Selector, labels) || len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 || svc.Spec.Ports[0].TargetPort.IntVal != 8080 {
		t.Fatalf("service selector/ports = %v / %v", svc.Spec.Selector, svc.Spec.Ports)
	}
	if len(svc.OwnerReferences) != 1 || svc.OwnerReferences[0].APIVersion != "ach.ackstorm.ai/v1alpha1" || svc.OwnerReferences[0].Kind != "ACHAgent" || svc.OwnerReferences[0].Name != a.Name || svc.OwnerReferences[0].UID != a.UID || svc.OwnerReferences[0].Controller == nil || !*svc.OwnerReferences[0].Controller {
		t.Fatalf("service ownerReferences = %+v", svc.OwnerReferences)
	}
}

func TestBuildWorkspaceStatefulSetMatchesExecutionTemplate(t *testing.T) {
	w, a, p := workspaceTestObjects()
	for _, replicas := range []int32{0, 1} {
		w.Spec.Replicas = replicas
		sts, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
		if err != nil {
			t.Fatalf("buildWorkspaceStatefulSet(replicas=%d): %v", replicas, err)
		}
		assertWorkspaceStatefulSet(t, sts, w, a, p, replicas, 100)
	}
}

func TestWorkspacePodAgentNameLabelMatchesRuntime018(t *testing.T) {
	for _, tt := range []struct {
		name      string
		wantLabel bool
	}{
		{"demo.agent", true},
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w, a, p := workspaceTestObjects()
			a.Name = tt.name
			w.Spec.AgentRef.Name = tt.name
			w.Name = workspaceResourceName(tt.name, w.Spec.WorkspaceRef)
			svc := buildWorkspaceService(w, a)
			sts, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
			if err != nil {
				t.Fatalf("buildWorkspaceStatefulSet: %v", err)
			}
			identity := workspaceLabels(testWorkspaceAgentUID, w.Name)
			if !mapsEqual(svc.Labels, identity) || !mapsEqual(svc.Spec.Selector, identity) ||
				!mapsEqual(sts.Labels, identity) || sts.Spec.Selector == nil || !mapsEqual(sts.Spec.Selector.MatchLabels, identity) {
				t.Fatalf("child metadata/selectors include non-identity labels: Service=%v/%v StatefulSet=%v/%v", svc.Labels, svc.Spec.Selector, sts.Labels, sts.Spec.Selector)
			}
			podLabels := maps.Clone(identity)
			if tt.wantLabel {
				podLabels["ach.ackstorm.ai/agent"] = tt.name
			}
			if !mapsEqual(sts.Spec.Template.Labels, podLabels) {
				t.Fatalf("pod-template labels = %v, want %v", sts.Spec.Template.Labels, podLabels)
			}
			if _, found := sts.Spec.Template.Labels["ach.ackstorm.ai/component"]; found {
				t.Fatal("execution pod must not carry the control component label")
			}
		})
	}
}

func TestBuildWorkspaceStatefulSetUsesResolvedShutdownTimeoutForGrace(t *testing.T) {
	w, a, p := workspaceTestObjects()
	shutdown := int64(200)
	a.Spec.Workspace = &achv1alpha1.WorkspaceSpec{ShutdownTimeoutSeconds: &shutdown}
	sts, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
	if err != nil {
		t.Fatalf("buildWorkspaceStatefulSet: %v", err)
	}
	if got := *sts.Spec.Template.Spec.TerminationGracePeriodSeconds; got != 215 {
		t.Fatalf("terminationGracePeriodSeconds = %d, want max(100, 200+15)=215", got)
	}
}

func TestBuildWorkspaceStatefulSetRejectsInvalidExecutionQuantities(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*achv1alpha1.AgentProfile)
	}{
		{name: "ephemeral storage", mutate: func(p *achv1alpha1.AgentProfile) { p.Spec.Execution.EphemeralStorage = "not-a-quantity" }},
		{name: "negative resource request", mutate: func(p *achv1alpha1.AgentProfile) {
			q := resource.Quantity{}
			q.SetMilli(-1)
			p.Spec.Execution.Resources.Requests[corev1.ResourceCPU] = q
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, a, p := workspaceTestObjects()
			tt.mutate(p)
			if _, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey); err == nil {
				t.Fatal("buildWorkspaceStatefulSet() succeeded for invalid execution quantity")
			}
		})
	}
}

func TestWorkspaceEngineVerifyKeyMatchesPythonVector(t *testing.T) {
	got := workspaceEngineVerifyKey([]byte(strings.Repeat("0", 64)))
	if got != testVerifyKey {
		t.Fatalf("workspaceEngineVerifyKey() = %q, want Python vector %q", got, testVerifyKey)
	}
}

func assertWorkspaceStatefulSet(t *testing.T, sts *appsv1.StatefulSet, w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile, replicas int32, grace int64) {
	t.Helper()
	assertWorkspaceObjectIdentity(t, sts, w, a, replicas)
	pod := sts.Spec.Template
	assertWorkspacePodInfrastructure(t, pod, w, grace)
	assertWorkspacePodVolumes(t, pod)
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %d, want one execution container", len(pod.Spec.Containers))
	}
	c := pod.Spec.Containers[0]
	assertWorkspaceExecutionContainer(t, c, p)
	assertWorkspaceExecutionMounts(t, c)
	assertWorkspaceExecutionEnv(t, c)
}

func assertWorkspaceObjectIdentity(t *testing.T, sts *appsv1.StatefulSet, w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent, replicas int32) {
	t.Helper()
	labels := map[string]string{
		"runtime.ach.ackstorm.ai/agent-uid":      testWorkspaceAgentUID,
		"runtime.ach.ackstorm.ai/workspace-name": testWorkspaceName,
	}
	if sts.Name != testWorkspaceName || sts.Namespace != w.Namespace || !mapsEqual(sts.Labels, labels) || sts.Annotations["runtime.ach.ackstorm.ai/workspace-ref"] != testWorkspaceRef {
		t.Fatalf("StatefulSet metadata = %+v", sts.ObjectMeta)
	}
	if len(sts.OwnerReferences) != 1 || sts.OwnerReferences[0].Kind != "ACHAgent" || sts.OwnerReferences[0].Name != a.Name || sts.OwnerReferences[0].UID != a.UID || sts.OwnerReferences[0].Controller == nil || !*sts.OwnerReferences[0].Controller {
		t.Fatalf("StatefulSet ownerReferences = %+v", sts.OwnerReferences)
	}
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != replicas || sts.Spec.ServiceName != testWorkspaceName || sts.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType || !mapsEqual(sts.Spec.Selector.MatchLabels, labels) {
		t.Fatalf("StatefulSet spec identity = replicas %v service %q strategy %q selector %v", sts.Spec.Replicas, sts.Spec.ServiceName, sts.Spec.UpdateStrategy.Type, sts.Spec.Selector.MatchLabels)
	}
}

func assertWorkspacePodInfrastructure(t *testing.T, pod corev1.PodTemplateSpec, w *achv1alpha1.Workspace, grace int64) {
	t.Helper()
	if !mapsEqual(pod.Labels, map[string]string{
		"runtime.ach.ackstorm.ai/agent-uid":      testWorkspaceAgentUID,
		"runtime.ach.ackstorm.ai/workspace-name": testWorkspaceName,
		"ach.ackstorm.ai/agent":                  testWorkspaceAgentName,
	}) || pod.Annotations["runtime.ach.ackstorm.ai/workspace-ref"] != testWorkspaceRef {
		t.Fatalf("pod template metadata = %+v", pod.ObjectMeta)
	}
	if pod.Spec.ServiceAccountName != "ach-execution-"+testWorkspaceAgentUID || pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken || pod.Spec.TerminationGracePeriodSeconds == nil || *pod.Spec.TerminationGracePeriodSeconds != grace {
		t.Fatalf("pod execution identity = SA %q automount %v grace %v", pod.Spec.ServiceAccountName, pod.Spec.AutomountServiceAccountToken, pod.Spec.TerminationGracePeriodSeconds)
	}
	if len(pod.Spec.ImagePullSecrets) != 1 || pod.Spec.ImagePullSecrets[0].Name != "registry" || pod.Spec.NodeSelector["pool"] != "execution" || len(pod.Spec.Tolerations) != 1 || pod.Spec.Tolerations[0].Key != "dedicated" {
		t.Fatalf("pod scheduling = pull secrets %v node selector %v tolerations %v", pod.Spec.ImagePullSecrets, pod.Spec.NodeSelector, pod.Spec.Tolerations)
	}
	if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.RunAsNonRoot == nil || !*pod.Spec.SecurityContext.RunAsNonRoot || pod.Spec.SecurityContext.RunAsUser == nil || *pod.Spec.SecurityContext.RunAsUser != 10001 || pod.Spec.SecurityContext.RunAsGroup == nil || *pod.Spec.SecurityContext.RunAsGroup != 10001 || pod.Spec.SecurityContext.FSGroup == nil || *pod.Spec.SecurityContext.FSGroup != 10001 || pod.Spec.SecurityContext.SeccompProfile == nil || pod.Spec.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("pod security context = %+v", pod.Spec.SecurityContext)
	}
	if pod.Spec.EnableServiceLinks == nil || *pod.Spec.EnableServiceLinks {
		t.Fatalf("pod EnableServiceLinks = %v, want explicit false", pod.Spec.EnableServiceLinks)
	}
}

func assertWorkspacePodVolumes(t *testing.T, pod corev1.PodTemplateSpec) {
	t.Helper()
	if len(pod.Spec.Volumes) != 3 || !emptyDirBound(t, pod.Spec.Volumes, "workspace", "3Gi") || !emptyDirBound(t, pod.Spec.Volumes, "sessions", "3Gi") {
		t.Fatalf("pod volumes = %+v; workspace and sessions must be bounded emptyDirs", pod.Spec.Volumes)
	}
	var bootstrap *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "bootstrap" {
			bootstrap = &pod.Spec.Volumes[i]
		}
	}
	if bootstrap == nil || bootstrap.ConfigMap == nil || bootstrap.ConfigMap.Name != "ach-execution-"+testWorkspaceAgentUID {
		t.Fatalf("bootstrap volume = %+v", bootstrap)
	}
}

func assertWorkspaceExecutionContainer(t *testing.T, c corev1.Container, p *achv1alpha1.AgentProfile) {
	t.Helper()
	if c.SecurityContext == nil || c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation || c.SecurityContext.Capabilities == nil || len(c.SecurityContext.Capabilities.Add) != 0 || len(c.SecurityContext.Capabilities.Drop) != 1 || c.SecurityContext.Capabilities.Drop[0] != corev1.Capability("ALL") {
		t.Fatalf("execution container security context = %+v", c.SecurityContext)
	}
	if c.Name != "execution" || c.Image != p.Spec.Execution.Image || len(c.Command) != 6 || c.Command[0] != "python" || c.Command[1] != "-m" || c.Command[2] != "ach_runtime" || c.Command[3] != "execution" || c.Command[5] != "/etc/ach-runtime/bootstrap.json" {
		t.Fatalf("execution command/image = %q %v", c.Image, c.Command)
	}
	if len(c.Ports) != 1 || c.Ports[0].ContainerPort != 8080 || c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet == nil || c.ReadinessProbe.HTTPGet.Path != "/readyz" || c.ReadinessProbe.HTTPGet.Port.IntVal != 8080 || c.LivenessProbe == nil || c.LivenessProbe.HTTPGet == nil || c.LivenessProbe.HTTPGet.Path != "/healthz" || c.LivenessProbe.HTTPGet.Port.IntVal != 8080 {
		t.Fatalf("execution port/probes = ports %v readiness %v liveness %v", c.Ports, c.ReadinessProbe, c.LivenessProbe)
	}
	if got := c.Resources; !resourceListEqual(got.Requests, p.Spec.Execution.Resources.Requests) || !resourceListEqual(got.Limits, p.Spec.Execution.Resources.Limits) {
		t.Fatalf("execution resources = %+v, want profile requests/limits %+v", got, p.Spec.Execution.Resources)
	}
}

func assertWorkspaceExecutionMounts(t *testing.T, c corev1.Container) {
	t.Helper()
	if len(c.VolumeMounts) != 3 || !mount(t, c.VolumeMounts, "bootstrap", "/etc/ach-runtime", true) || !mount(t, c.VolumeMounts, "workspace", "/workspace", false) || !mount(t, c.VolumeMounts, "sessions", "/var/lib/ach-runtime/sessions", false) {
		t.Fatalf("execution mounts = %+v", c.VolumeMounts)
	}
}

func assertWorkspaceExecutionEnv(t *testing.T, c corev1.Container) {
	t.Helper()
	if len(c.Env) != 5 || c.Env[0].Name != "ACH_SANDBOX_VERIFY_KEY" || c.Env[0].Value != testVerifyKey ||
		c.Env[1].Name != "ACH_WORKSPACE_NAME" || c.Env[1].ValueFrom == nil || c.Env[1].ValueFrom.FieldRef.FieldPath != "metadata.labels['runtime.ach.ackstorm.ai/workspace-name']" ||
		c.Env[2].Name != "ACH_WORKSPACE_REF" || c.Env[2].ValueFrom == nil || c.Env[2].ValueFrom.FieldRef.FieldPath != "metadata.annotations['runtime.ach.ackstorm.ai/workspace-ref']" ||
		c.Env[3].Name != "ACH_POD_UID" || c.Env[3].ValueFrom == nil || c.Env[3].ValueFrom.FieldRef.FieldPath != "metadata.uid" ||
		c.Env[4].Name != "ACH_POD_NAMESPACE" || c.Env[4].ValueFrom == nil || c.Env[4].ValueFrom.FieldRef.FieldPath != "metadata.namespace" {
		t.Fatalf("execution env = %+v", c.Env)
	}
}

func mapsEqual[K comparable, V comparable](left, right map[K]V) bool {
	if len(left) != len(right) {
		return false
	}
	for k, v := range left {
		if right[k] != v {
			return false
		}
	}
	return true
}

func emptyDirBound(t *testing.T, volumes []corev1.Volume, name, size string) bool {
	t.Helper()
	for i := range volumes {
		if volumes[i].Name == name {
			return volumes[i].EmptyDir != nil && volumes[i].EmptyDir.SizeLimit != nil && volumes[i].EmptyDir.SizeLimit.String() == size
		}
	}
	return false
}

func mount(t *testing.T, mounts []corev1.VolumeMount, name, path string, readOnly bool) bool {
	t.Helper()
	for i := range mounts {
		if mounts[i].Name == name {
			return mounts[i].MountPath == path && mounts[i].ReadOnly == readOnly
		}
	}
	return false
}

func resourceListEqual(left, right corev1.ResourceList) bool {
	if len(left) != len(right) {
		return false
	}
	for k, v := range left {
		if !v.Equal(right[k]) {
			return false
		}
	}
	return true
}
