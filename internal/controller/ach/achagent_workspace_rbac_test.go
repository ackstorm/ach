// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/agentrender"
)

// TestWorkspaceCreatorRBAC pins the exact Harness workspace-creator grants (contract §11
// creator contract): apps/statefulsets full CRUD + statefulsets/scale, core pods
// get/list/watch/delete, core services full CRUD — nothing else (no Secret/RBAC CRUD, no
// TokenReview, no pod create/exec). The Role/RoleBinding are named after and bind only the
// control (Harness) ServiceAccount, with ordinary automount=true; the execution
// ServiceAccount is never a subject of this or any other binding.
func TestWorkspaceCreatorRBAC(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	wantName := "ach-harness-" + string(a.UID)

	role := buildWorkspaceRole(a)
	if role.Name != wantName || role.Namespace != a.Namespace {
		t.Fatalf("Role = %s/%s, want %s/%s", role.Namespace, role.Name, a.Namespace, wantName)
	}

	assertWorkspaceRoleRules(t, role)

	rb := buildWorkspaceRoleBinding(a, agentrender.HarnessName(string(a.UID)))
	if rb.Name != wantName || rb.Namespace != a.Namespace {
		t.Fatalf("RoleBinding = %s/%s, want %s/%s", rb.Namespace, rb.Name, a.Namespace, wantName)
	}
	if rb.RoleRef.APIGroup != rbacv1.GroupName || rb.RoleRef.Kind != "Role" || rb.RoleRef.Name != wantName {
		t.Errorf("RoleBinding.RoleRef = %+v, want Role %q in group %q", rb.RoleRef, wantName, rbacv1.GroupName)
	}
	if len(rb.Subjects) != 1 || rb.Subjects[0].Kind != "ServiceAccount" || rb.Subjects[0].Name != wantName || rb.Subjects[0].Namespace != a.Namespace {
		t.Fatalf("RoleBinding.Subjects = %+v, want exactly the control ServiceAccount %s/%s", rb.Subjects, a.Namespace, wantName)
	}

	// Execution is never a subject of this (or any other) binding.
	executionName := agentrender.ExecutionServiceAccountName(string(a.UID))
	for _, s := range rb.Subjects {
		if s.Name == executionName {
			t.Fatal("execution ServiceAccount must never be bound to the Harness workspace-creator Role")
		}
	}

	// Ordinary control automount is true (contract §11 creator cutover: the Harness needs an
	// ordinary API token to use the workspace-creator Role/RoleBinding granted above —
	// execution, not control, is the SA that stays unbound/unmounted).
	sa := buildControlServiceAccount(a)
	if sa.Name != wantName || sa.AutomountServiceAccountToken == nil || !*sa.AutomountServiceAccountToken {
		t.Errorf("control ServiceAccount = %+v, want name %q and automount=true", sa, wantName)
	}
}

func assertWorkspaceRoleRules(t *testing.T, role *rbacv1.Role) {
	t.Helper()
	type rule struct {
		group, resource string
		verbs           []string
	}
	got := make([]rule, 0, len(role.Rules))
	for _, r := range role.Rules {
		if len(r.APIGroups) != 1 || len(r.Resources) != 1 {
			t.Fatalf("rule must name exactly one group and one resource (no wildcard grants): %+v", r)
		}
		verbs := append([]string(nil), r.Verbs...)
		sort.Strings(verbs)
		got = append(got, rule{r.APIGroups[0], r.Resources[0], verbs})
	}
	want := []rule{
		{"apps", "statefulsets", []string{"create", "delete", "get", "list", "patch", "update", "watch"}},
		{"apps", "statefulsets/scale", []string{"get", "patch", "update"}},
		{"", "pods", []string{"delete", "get", "list", "watch"}},
		{"", "services", []string{"create", "delete", "get", "list", "patch", "update", "watch"}},
	}
	if len(got) != len(want) {
		t.Fatalf("Role.Rules = %+v, want exactly %+v", got, want)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g.group == w.group && g.resource == w.resource {
				found = true
				if len(g.verbs) != len(w.verbs) {
					t.Errorf("%s/%s verbs = %v, want %v", w.group, w.resource, g.verbs, w.verbs)
					continue
				}
				for i := range w.verbs {
					if g.verbs[i] != w.verbs[i] {
						t.Errorf("%s/%s verbs = %v, want %v", w.group, w.resource, g.verbs, w.verbs)
						break
					}
				}
			}
		}
		if !found {
			t.Errorf("missing expected rule for %s/%s", w.group, w.resource)
		}
	}
	// No Secret/RBAC CRUD, no TokenReview, no pod create/exec, no cross-namespace grant.
	for _, r := range role.Rules {
		for _, res := range r.Resources {
			switch res {
			case "secrets", "roles", "rolebindings", "clusterroles", "clusterrolebindings", "tokenreviews", "pods/exec":
				t.Errorf("Role must never grant resource %q", res)
			}
		}
		for _, v := range r.Verbs {
			if r.Resources[0] == "pods" && v == "create" {
				t.Error("Role must never grant pods create")
			}
		}
	}
}

// TestACHAgent_VolatileWithoutBrokerOrCA asserts the full child-object build pipeline
// (config/SA/Role/RoleBinding/Service/control STS) succeeds with no broker/CA input and no
// issued Secret of any kind (contract §11 scope reset) — a pure build, no k8s API, no
// Secret ever read or created by any of these builders.
func TestACHAgent_VolatileWithoutBrokerOrCA(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	a.Spec.Ach = achIdentity("", "prod")
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img:test"
	p.Spec.Achagent.Ach = &achv1alpha1.AchSpec{BaseURL: "https://ach"}
	p.Spec.Execution = achv1alpha1.ExecutionInfraSpec{Image: "registry.test/exec:0.1.0", EphemeralStorage: "1Gi"}

	infra, err := agentrender.RenderInfrastructureV1(a.Name, string(a.UID), a.Namespace, agentrender.HarnessName(string(a.UID)), &p.Spec.Execution)
	if err != nil {
		t.Fatalf("RenderInfrastructureV1 (no broker/CA argument any more): %v", err)
	}

	env := buildAgentEnv(a, p, "")
	configJSON := []byte(`{"schemaVersion":"workspace-v1"}`)
	cm := buildConfigMap(a, configJSON)
	controlSA := buildControlServiceAccount(a)
	executionSA := buildExecutionServiceAccount(a)
	svc := buildControlService(a)
	role := buildWorkspaceRole(a)
	rb := buildWorkspaceRoleBinding(a, agentrender.HarnessName(string(a.UID)))
	bootCM, err := buildExecutionBootstrapConfigMap(a, infra)
	if err != nil {
		t.Fatalf("buildExecutionBootstrapConfigMap: %v", err)
	}
	sts, err := buildStatefulSet(a, p, "h", env)
	if err != nil {
		t.Fatalf("buildStatefulSet: %v", err)
	}

	for _, obj := range []struct {
		name string
		got  any
	}{{"ConfigMap", cm}, {"controlSA", controlSA}, {"executionSA", executionSA}, {"Service", svc}, {"Role", role}, {"RoleBinding", rb}, {"bootstrapCM", bootCM}, {"StatefulSet", sts}} {
		if obj.got == nil {
			t.Fatalf("%s: build returned nil", obj.name)
		}
	}

	// No broker/CA/TLS material anywhere in the data carried by the two ConfigMaps.
	for key := range cm.Data {
		if key != "config.json" {
			t.Errorf("control ConfigMap carries unexpected key %q (contract §11 scope reset: config.json only)", key)
		}
	}
	for key := range bootCM.Data {
		if key != bootstrapFileName {
			t.Errorf("bootstrap ConfigMap carries unexpected key %q (contract §11 scope reset: bootstrap.json only)", key)
		}
	}

	// No broker/CA env name anywhere in the rendered env or the pod's actual env.
	forbidden := []string{"ACH_RUNTIME_BROKER", "ACH_RUNTIME_CA", "BROKER", "CA_SECRET"}
	for _, e := range append(append([]corev1.EnvVar{}, env...), sts.Spec.Template.Spec.Containers[0].Env...) {
		for _, f := range forbidden {
			if contains(e.Name, f) {
				t.Errorf("env %q must not exist any more (contract §11 scope reset)", e.Name)
			}
		}
	}

	// No Secret volume of any kind on the control pod.
	for _, v := range sts.Spec.Template.Spec.Volumes {
		if v.Secret != nil {
			t.Errorf("control pod must mount no Secret volume: %+v", v)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestControlWorkload_ConfigAndProfile is the consolidated control-workload regression: fixed
// command/config path/probes/internal Service, single replica/container, and the profile's
// image/resources/scheduling/pull-secrets/grace/optional PVC/fsGroup all land on the built
// StatefulSet.
func TestControlWorkload_ConfigAndProfile(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	a.Spec.Ach = achIdentity("", "prod")
	grace := int64(45)
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "ghcr.io/ackstorm/ach-agent:profile"
	p.Spec.Achagent.Ach = &achv1alpha1.AchSpec{BaseURL: "https://ach"}
	p.Spec.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resourceMustParse("250m"), corev1.ResourceMemory: resourceMustParse("512Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resourceMustParse("1"), corev1.ResourceMemory: resourceMustParse("1Gi")},
	}
	p.Spec.NodeSelector = map[string]string{"pool": "agents"}
	p.Spec.Tolerations = []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists}}
	p.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "regcred"}}
	p.Spec.TerminationGracePeriodSeconds = &grace
	p.Spec.Persistence = &achv1alpha1.PersistenceSpec{Enabled: true, Size: "1Gi", MountPath: "/var/lib/ach-agent"}

	sts, err := buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatalf("buildStatefulSet: %v", err)
	}
	ps := sts.Spec.Template.Spec
	wantName := "ach-control-" + a.Name
	if sts.Name != wantName || sts.Spec.ServiceName != wantName {
		t.Errorf("control StatefulSet naming = %q/%q, want %q", sts.Name, sts.Spec.ServiceName, wantName)
	}
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 || len(ps.Containers) != 1 {
		t.Fatalf("control workload must be single-replica/single-container: replicas=%v containers=%d", sts.Spec.Replicas, len(ps.Containers))
	}
	c := ps.Containers[0]
	assertControlContainer(t, c, p.Spec.Achagent.Image)
	if c.Resources.Requests.Cpu().String() != "250m" || c.Resources.Limits.Memory().String() != "1Gi" {
		t.Errorf("profile resources not applied: %+v", c.Resources)
	}
	if ps.NodeSelector["pool"] != "agents" {
		t.Errorf("profile nodeSelector not applied: %+v", ps.NodeSelector)
	}
	if len(ps.Tolerations) != 1 || ps.Tolerations[0].Key != "dedicated" {
		t.Errorf("profile tolerations not applied: %+v", ps.Tolerations)
	}
	if len(ps.ImagePullSecrets) != 1 || ps.ImagePullSecrets[0].Name != "regcred" {
		t.Errorf("profile imagePullSecrets not applied: %+v", ps.ImagePullSecrets)
	}
	if ps.TerminationGracePeriodSeconds == nil || *ps.TerminationGracePeriodSeconds != grace {
		t.Errorf("profile terminationGracePeriodSeconds not applied: %v", ps.TerminationGracePeriodSeconds)
	}
	if ps.SecurityContext == nil || ps.SecurityContext.FSGroup == nil || *ps.SecurityContext.FSGroup != 10001 {
		t.Errorf("pod fsGroup must be pinned to 10001: %+v", ps.SecurityContext)
	}
	var pvcMount *corev1.VolumeMount
	for i := range c.VolumeMounts {
		if c.VolumeMounts[i].Name == pvcVolumeName {
			pvcMount = &c.VolumeMounts[i]
		}
	}
	if pvcMount == nil || pvcMount.MountPath != "/var/lib/ach-agent" {
		t.Errorf("optional control PVC mount missing/wrong: %+v", pvcMount)
	}

	// Internal control Service: headless, port 8081.
	svc := buildControlService(a)
	if svc.Name != wantName || svc.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Errorf("control Service = %+v, want headless %q", svc, wantName)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != controlServicePort {
		t.Errorf("control Service ports = %+v, want single port %d", svc.Spec.Ports, controlServicePort)
	}
}

func assertControlContainer(t *testing.T, c corev1.Container, image string) {
	t.Helper()
	wantCommand := []string{"python", "-m", "ach_runtime", "control", "--config", configFilePath}
	if len(c.Command) != len(wantCommand) {
		t.Fatalf("command = %v, want %v", c.Command, wantCommand)
	}
	for i := range wantCommand {
		if c.Command[i] != wantCommand[i] {
			t.Fatalf("command = %v, want %v", c.Command, wantCommand)
		}
	}
	if c.Image != image {
		t.Errorf("image = %q, want profile image %q", c.Image, image)
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet.Port.IntVal != 8080 || c.LivenessProbe.HTTPGet.Port.IntVal != 8080 || c.StartupProbe.HTTPGet.Port.IntVal != 8080 {
		t.Errorf("probes must target fixed control port 8080: %+v / %+v / %+v", c.StartupProbe, c.ReadinessProbe, c.LivenessProbe)
	}
}

func resourceMustParse(s string) resource.Quantity { return resource.MustParse(s) }

// TestControlServiceAccount_ProfileOverride: a profile-named stable ServiceAccount becomes the
// control pod identity and the RoleBinding subject, while Role/RoleBinding keep their
// per-agent names; unset keeps ach-harness-<uid>.
func TestControlServiceAccount_ProfileOverride(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	a.Spec.Ach = achIdentity("", "prod")
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img:test"
	p.Spec.Achagent.Ach = &achv1alpha1.AchSpec{BaseURL: "https://ach"}
	perAgent := "ach-harness-" + string(a.UID)

	if got := effectiveControlServiceAccountName(a, p); got != perAgent {
		t.Fatalf("unset: effective SA = %q, want %q", got, perAgent)
	}
	p.Spec.ControlServiceAccountName = "ach-sandboxed-agent"
	sa := effectiveControlServiceAccountName(a, p)
	if sa != "ach-sandboxed-agent" {
		t.Fatalf("set: effective SA = %q", sa)
	}

	sts, err := buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatalf("buildStatefulSet: %v", err)
	}
	if got := sts.Spec.Template.Spec.ServiceAccountName; got != sa {
		t.Errorf("StatefulSet SA = %q, want %q", got, sa)
	}

	p.Spec.PodTemplate = &apiextensionsv1.JSON{Raw: []byte(`{"spec":{"serviceAccountName":"evil"}}`)}
	sts, err = buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatalf("buildStatefulSet overlay: %v", err)
	}
	if got := sts.Spec.Template.Spec.ServiceAccountName; got != sa {
		t.Errorf("overlay must not override SA: got %q, want %q", got, sa)
	}

	rb := buildWorkspaceRoleBinding(a, sa)
	if rb.Name != perAgent || rb.RoleRef.Name != perAgent {
		t.Errorf("RoleBinding/Role names must stay per-agent: %s / %s", rb.Name, rb.RoleRef.Name)
	}
	if len(rb.Subjects) != 1 || rb.Subjects[0].Name != sa || rb.Subjects[0].Namespace != a.Namespace {
		t.Errorf("RoleBinding subject = %+v, want SA %s/%s", rb.Subjects, a.Namespace, sa)
	}
}
