// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/agentrender"
)

func mkEnv(name, val string) []corev1.EnvVar { return []corev1.EnvVar{{Name: name, Value: val}} }

// achIdentity is the test-fixture ach block: required identity, optional baseUrl/environment.
func achIdentity(baseURL, environment string) *achv1alpha1.AchSpec {
	return &achv1alpha1.AchSpec{
		BaseURL: baseURL, Environment: &environment,
		Identity: &achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
	}
}

func TestComputeConfigHash_ChangesWithInputs(t *testing.T) {
	base := computeConfigHash([]byte(`{"a":1}`), []byte(`[]`), nil, "img:1", "sec1")
	if len(base) != 16 {
		t.Fatalf("hash len = %d", len(base))
	}
	for name, h := range map[string]string{
		"config":      computeConfigHash([]byte(`{"a":2}`), []byte(`[]`), nil, "img:1", "sec1"),
		"env":         computeConfigHash([]byte(`{"a":1}`), []byte(`[{}]`), nil, "img:1", "sec1"),
		"podTemplate": computeConfigHash([]byte(`{"a":1}`), []byte(`[]`), []byte(`{"spec":{}}`), "img:1", "sec1"),
		"image":       computeConfigHash([]byte(`{"a":1}`), []byte(`[]`), nil, "img:2", "sec1"),
		"secret":      computeConfigHash([]byte(`{"a":1}`), []byte(`[]`), nil, "img:1", "sec2"),
	} {
		if h == base {
			t.Errorf("%s change did not alter hash", name)
		}
	}
}

func TestBuildAgentEnv_EkSecretRefAndReservedFilter(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Ach = achIdentity("", "prod")
	a.Spec.Ach.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img"
	p.Spec.Achagent.Ach = &achv1alpha1.AchSpec{BaseURL: "https://ach"}
	p.Spec.Env = mkEnv("CUSTOM_VAR", "http://p")

	env := buildAgentEnv(a, p, "")

	var token, base, extra, reserved bool
	for _, e := range env {
		switch e.Name {
		case agentrender.AchIdentityAliasEnv:
			token = e.Value == "" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil && e.ValueFrom.SecretKeyRef.Name == "demo-ek"
		case "ACH_BASE_URL":
			base = e.Value == "https://ach"
		case "CUSTOM_VAR":
			extra = true
		}
		if e.Name == "ACH_BASE_URL" && e.Value != "https://ach" {
			reserved = true
		}
	}
	if !token {
		t.Errorf("%s must be a secretKeyRef to demo-ek", agentrender.AchIdentityAliasEnv)
	}
	if !base || !extra || reserved {
		t.Errorf("env assembly wrong: base=%v extra=%v reservedHijack=%v", base, extra, reserved)
	}
}

// TestBuildAgentEnv_BaseURLResolution proves ACH_BASE_URL follows the same
// agent ?? profile ?? operator-default chain as the rendered config.
func TestBuildAgentEnv_BaseURLResolution(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name = "d"
	a.Spec.Ach = achIdentity("", "")
	p := &achv1alpha1.AgentProfile{} // no profile baseUrl

	get := func(env []corev1.EnvVar) string {
		for _, e := range env {
			if e.Name == "ACH_BASE_URL" {
				return e.Value
			}
		}
		return ""
	}
	if v := get(buildAgentEnv(a, p, "https://env")); v != "https://env" {
		t.Errorf("ACH_BASE_URL = %q, want operator default", v)
	}
	p.Spec.Achagent.Ach = &achv1alpha1.AchSpec{BaseURL: "https://profile"}
	if v := get(buildAgentEnv(a, p, "https://env")); v != "https://profile" {
		t.Errorf("ACH_BASE_URL = %q, want profile over default", v)
	}
	a.Spec.Ach.BaseURL = "https://agent"
	if v := get(buildAgentEnv(a, p, "https://env")); v != "https://agent" {
		t.Errorf("ACH_BASE_URL = %q, want agent override", v)
	}
}

// TestControlPort_FixedRegardlessOfLegacyHealthOverride is the Important review finding 7
// regression: workspace-v1 fixes control HTTP at 8080 (Render2 has no health block at all);
// the legacy agent-config-v1 health.host/port CRD knob is compatibility-only now and must be
// completely ignored by the real k8s objects this controller builds — the probe port, the
// Service targetPort, and the containerPort must all stay 8080 even when an agent/profile
// sets a health override.
func TestControlPort_FixedRegardlessOfLegacyHealthOverride(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Ach = achIdentity("", "e")
	a.Spec.Model = &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}
	a.Spec.Channels = []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}}
	a.Spec.Health = &achv1alpha1.HealthSpec{Port: 9137} // legacy override — must be ignored
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img"
	p.Spec.Achagent.Ach = &achv1alpha1.AchSpec{BaseURL: "https://ach"}
	p.Spec.Achagent.Health = &achv1alpha1.HealthSpec{Port: 8000} // legacy override — must be ignored

	const want = int32(8080)
	if tp := buildService(a, p).Spec.Ports[0].TargetPort.IntVal; tp != want {
		t.Errorf("service targetPort = %d, want fixed %d", tp, want)
	}
	dep, err := buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatalf("buildStatefulSet: %v", err)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if cp := c.Ports[0].ContainerPort; cp != want {
		t.Errorf("containerPort = %d, want fixed %d", cp, want)
	}
	if c.StartupProbe.HTTPGet.Port.IntVal != want || c.ReadinessProbe.HTTPGet.Port.IntVal != want || c.LivenessProbe.HTTPGet.Port.IntVal != want {
		t.Errorf("probes must all target the fixed port %d", want)
	}
	// Important review finding 7: an explicit control launch command, not an unverified
	// arbitrary image entrypoint.
	wantCmd := []string{"python", "-m", "ach_runtime", "control", "--config", "/etc/ach-runtime/config.json"}
	if len(c.Command) != len(wantCmd) {
		t.Fatalf("command = %v, want %v", c.Command, wantCmd)
	}
	for i := range wantCmd {
		if c.Command[i] != wantCmd[i] {
			t.Fatalf("command = %v, want %v", c.Command, wantCmd)
		}
	}
}

func TestBuildAgentEnv_RejectsReservedEnv(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name = "d"
	a.Spec.Ach = achIdentity("u", "")
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Env = mkEnv("ACH_TOKEN", "ek_LEAK") // reserved — must be dropped
	for _, e := range buildAgentEnv(a, p, "") {
		if e.Name == "ACH_TOKEN" && e.Value == "ek_LEAK" {
			t.Fatal("reserved ACH_* env leaked into the pod spec")
		}
	}
}

func TestBuildAgentEnv_AgentOverridesProfileEnv(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Spec.Ach = achIdentity("", "")
	a.Spec.Env = mkEnv("SHARED", "agent")
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Env = append(mkEnv("PROFILE", "p"), mkEnv("SHARED", "profile")...)

	values := map[string]string{}
	for _, e := range buildAgentEnv(a, p, "") {
		values[e.Name] = e.Value
	}
	if values["PROFILE"] != "p" || values["SHARED"] != "agent" {
		t.Fatalf("merged Pod env = %v", values)
	}
}

func TestBuildDeployment_MountsConfigProbesAndHash(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Ach = achIdentity("", "")
	a.Spec.Ach.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "ghcr.io/ackstorm/ach-agent:latest"
	p.Spec.Achagent.Ach = &achv1alpha1.AchSpec{BaseURL: "https://ach"}

	env := buildAgentEnv(a, p, "")
	dep, err := buildStatefulSet(a, p, "cfghash", env)
	if err != nil {
		t.Fatalf("buildStatefulSet: %v", err)
	}

	if *dep.Spec.Replicas != 1 {
		t.Errorf("replicas = %d", *dep.Spec.Replicas)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if c.Image != p.Spec.Achagent.Image {
		t.Errorf("image = %q", c.Image)
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet == nil || c.ReadinessProbe.HTTPGet.Path != "/readyz" {
		t.Error("readinessProbe /readyz missing")
	}
	if c.LivenessProbe == nil || c.LivenessProbe.HTTPGet.Path != "/healthz" {
		t.Error("livenessProbe /healthz missing")
	}
	if c.StartupProbe == nil {
		t.Error("startupProbe missing")
	}
	// Named control port (fixed 8080, workspace-v1 contract §11) — the PodMonitor scrapes
	// /metrics on it BY NAME, so a rename here silently breaks agent metrics collection.
	var hp *corev1.ContainerPort
	for i := range c.Ports {
		if c.Ports[i].Name == "health" {
			hp = &c.Ports[i]
		}
	}
	if hp == nil || hp.ContainerPort != 8080 {
		t.Errorf("container must declare named control port 8080, got %+v", c.Ports)
	}
	// config.json is served by the directory mount at /etc/ach-runtime (alongside ca.crt),
	// not a SubPath file mount (Important review finding 9: "implement the specified
	// directory projection").
	found := false
	for _, mnt := range c.VolumeMounts {
		if mnt.MountPath == "/etc/ach-runtime" && mnt.SubPath == "" {
			found = true
		}
	}
	if !found {
		t.Error("config.json subPath mount missing")
	}
	if dep.Spec.Template.Annotations["ach.ackstorm.ai/config-hash"] != "cfghash" {
		t.Error("config-hash annotation missing")
	}
}

func TestBuildAgentEnv_ChannelSecretInjectedAsEnv(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Ach = achIdentity("", "")
	a.Spec.Ach.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	a.Spec.Channels = []achv1alpha1.ChannelSpec{{
		Name: "gitlab-mr-review", Type: "webhook",
		Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "gitlab_token", SecretRef: &achv1alpha1.SecretKeyRef{Name: "gl-hook", Key: "secret"}}},
	}}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img"
	p.Spec.Achagent.Ach = &achv1alpha1.AchSpec{BaseURL: "https://ach"}

	// Auth secret must be a secretKeyRef env var, never an inline value.
	var found bool
	for _, e := range buildAgentEnv(a, p, "") {
		if e.Name != "ACH_SECRET_GITLAB_MR_REVIEW_WEBHOOK" {
			continue
		}
		found = true
		if e.Value != "" || e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil ||
			e.ValueFrom.SecretKeyRef.Name != "gl-hook" || e.ValueFrom.SecretKeyRef.Key != "secret" {
			t.Errorf("channel secret env must be a secretKeyRef to gl-hook/secret, got %+v", e)
		}
	}
	if !found {
		t.Fatal("channel auth secret not injected as env var")
	}

	// And it must NOT be mounted as a file (fsGroup exists for the PVC, not for secrets).
	dep, err := buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatalf("buildStatefulSet: %v", err)
	}
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Secret != nil {
			t.Errorf("secret volume present; auth secrets are env-injected now: %+v", v)
		}
	}
}

func TestBuildAgentEnv_HandoffSecretGetsGeneratedAlias(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Spec.Ach = achIdentity("", "")
	a.Spec.Ach.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	a.Spec.Env = []corev1.EnvVar{{Name: "GITLAB_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "gl-clone"}, Key: "token",
	}}}}
	a.Spec.Channels = []achv1alpha1.ChannelSpec{{
		Name: "gitlab-mr-review", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"},
		Handoff: &achv1alpha1.HandoffSpec{PrepareSpec: achv1alpha1.PrepareSpec{Script: "true", ForwardEnv: []string{"GITLAB_TOKEN"}}, Destination: "handoff"},
	}}
	p := &achv1alpha1.AgentProfile{}

	var original, handoffAlias *corev1.EnvVar
	env := buildAgentEnv(a, p, "")
	for i := range env {
		e := &env[i]
		switch e.Name {
		case "GITLAB_TOKEN":
			original = e
		case "ACH_SECRET_GITLAB_MR_REVIEW_HANDOFF_GITLAB_TOKEN":
			handoffAlias = e
		}
	}
	if handoffAlias == nil || handoffAlias.ValueFrom == nil || handoffAlias.ValueFrom.SecretKeyRef == nil ||
		handoffAlias.ValueFrom.SecretKeyRef.Name != "gl-clone" ||
		handoffAlias.ValueFrom.SecretKeyRef.Key != "token" {
		t.Fatalf("handoff alias=%+v original=%+v", handoffAlias, original)
	}
}

// POD_NAMESPACE feeds the ach-memory project slug ({POD_NAMESPACE}-{agent.name}).
// Without it the harness degrades to the bare agent name — same bank, different
// key, no error anywhere — so assert the downward-API ref explicitly.
func TestBuildAgentEnv_PodNamespaceFromDownwardAPI(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Ach = achIdentity("", "")
	a.Spec.Ach.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img"

	for _, e := range buildAgentEnv(a, p, "") {
		if e.Name != "POD_NAMESPACE" {
			continue
		}
		if e.Value != "" || e.ValueFrom == nil || e.ValueFrom.FieldRef == nil ||
			e.ValueFrom.FieldRef.FieldPath != "metadata.namespace" {
			t.Fatalf("POD_NAMESPACE must be a downward-API fieldRef to metadata.namespace, got %+v", e)
		}
		return
	}
	t.Fatal("POD_NAMESPACE not injected")
}

func TestBuildAgentEnv_MemoryAuthInjectedAsEnv(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Ach = achIdentity("", "")
	a.Spec.Ach.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	a.Spec.Channels = []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}}
	a.Spec.Memory = &achv1alpha1.MemorySpec{Type: "ach-memory", AchMemory: &achv1alpha1.AchMemorySpec{
		Endpoint: "http://ach-memory.ach.svc:8000/mcp/", Auth: &achv1alpha1.AchMemoryAuthSpec{Type: "bearer", SecretRef: &achv1alpha1.SecretKeyRef{Name: "hs-admin", Key: "token"}},
	}}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img"
	p.Spec.Achagent.Ach = &achv1alpha1.AchSpec{BaseURL: "https://ach"}

	// The ach-memory user key rides in env (secretKeyRef), never inline, never a file.
	var found bool
	for _, e := range buildAgentEnv(a, p, "") {
		if e.Name != "ACH_SECRET_MEMORY_AUTH" {
			continue
		}
		found = true
		if e.Value != "" || e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil ||
			e.ValueFrom.SecretKeyRef.Name != "hs-admin" || e.ValueFrom.SecretKeyRef.Key != "token" {
			t.Errorf("memory secret env must be a secretKeyRef to hs-admin/token, got %+v", e)
		}
	}
	if !found {
		t.Fatal("memory auth secret not injected as env var")
	}

	// No auth → no such env var.
	a.Spec.Memory.AchMemory.Auth = nil
	for _, e := range buildAgentEnv(a, p, "") {
		if e.Name == "ACH_SECRET_MEMORY_AUTH" {
			t.Errorf("no-auth ach-memory must not inject ACH_SECRET_MEMORY_AUTH")
		}
	}
}

func TestBuildDeployment_PodTemplateOverlay(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Ach = achIdentity("", "")
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img:1"
	p.Spec.PodTemplate = &apiextensionsv1.JSON{Raw: []byte(`{
		"metadata": {"labels": {"ach.ackstorm.ai/agent": "hijack", "team": "x"}},
		"spec": {
			"securityContext": {"fsGroup": 1000, "fsGroupChangePolicy": "OnRootMismatch"},
			"containers": [{"name": "agent", "envFrom": [{"secretRef": {"name": "extra"}}]}]
		}
	}`)}

	dep, err := buildStatefulSet(a, p, "hash1", mkEnv("A", "1"))
	if err != nil {
		t.Fatalf("buildStatefulSet: %v", err)
	}
	tmpl := dep.Spec.Template
	sc := tmpl.Spec.SecurityContext
	if sc == nil || sc.FSGroup == nil || *sc.FSGroup != 1000 {
		t.Error("fsGroup 1000 not merged into pod securityContext")
	}
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("operator runAsNonRoot lost in securityContext map merge")
	}
	if len(tmpl.Spec.Containers) != 1 || tmpl.Spec.Containers[0].Name != agentContainerName {
		t.Fatalf("containers = %+v, want single %q merged by name", tmpl.Spec.Containers, agentContainerName)
	}
	c := tmpl.Spec.Containers[0]
	if len(c.EnvFrom) != 1 || c.EnvFrom[0].SecretRef == nil || c.EnvFrom[0].SecretRef.Name != "extra" {
		t.Error("envFrom overlay not merged into agent container")
	}
	if len(c.Env) == 0 || len(c.VolumeMounts) == 0 || c.LivenessProbe == nil {
		t.Error("operator env/mounts/probes lost in container merge")
	}
	if tmpl.Labels[agentLabelKey] != "demo" {
		t.Errorf("selector label = %q, want re-pinned %q (immutable StatefulSet selector)", tmpl.Labels[agentLabelKey], "demo")
	}
	if tmpl.Labels["team"] != "x" {
		t.Error("user label dropped")
	}
	if tmpl.Annotations[configHashAnnotation] != "hash1" {
		t.Error("config-hash annotation not re-pinned after merge")
	}
}

// TestBuildStatefulSet_PodTemplateOverlay_CannotRemoveReservedInfra is the October 1 TLS
// ruling's "fixed infra mounts cannot be replaced by profile overlays" gate: a profile
// podTemplate overlay must never be able to DELETE the config volume or its container mount
// via a strategic-merge $patch:delete directive.
func TestBuildStatefulSet_PodTemplateOverlay_CannotRemoveReservedInfra(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	a.Spec.Ach = achIdentity("", "")
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img"
	p.Spec.PodTemplate = &apiextensionsv1.JSON{Raw: []byte(`{
		"spec": {
			"volumes": [
				{"name": "ach-agent-config", "$patch": "delete"}
			],
			"containers": [{"name": "agent", "volumeMounts": [
				{"mountPath": "/etc/ach-runtime", "$patch": "delete"}
			]}]
		}
	}`)}

	sts, err := buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatal(err)
	}
	ps := sts.Spec.Template.Spec
	for _, name := range []string{configVolumeName} {
		found := false
		for _, v := range ps.Volumes {
			if v.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("overlay deleted reserved volume %q and it was not re-pinned", name)
		}
	}
	wantPaths := []string{configMountDir}
	for _, wantPath := range wantPaths {
		found := false
		for _, m := range ps.Containers[0].VolumeMounts {
			if m.MountPath == wantPath {
				found = true
			}
		}
		if !found {
			t.Errorf("overlay deleted the mount at reserved path %q and it was not re-pinned", wantPath)
		}
	}
}

// TestBuildStatefulSet_PodTemplateOverlay_CannotRedirectReservedVolumeSource covers the
// "collision" half: an overlay that keeps a reserved volume's NAME but attacks its SOURCE
// (a different backing ConfigMap) must not survive the merge.
func TestBuildStatefulSet_PodTemplateOverlay_CannotRedirectReservedVolumeSource(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	a.Spec.Ach = achIdentity("", "")
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img"
	p.Spec.PodTemplate = &apiextensionsv1.JSON{Raw: []byte(`{
		"spec": {
			"volumes": [
				{"name": "ach-agent-config", "configMap": {"name": "attacker-controlled-configmap"}}
			]
		}
	}`)}

	sts, err := buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatal(err)
	}
	ps := sts.Spec.Template.Spec
	wantConfigMap := agentResourceName(a.Name)
	for _, v := range ps.Volumes {
		if v.Name == configVolumeName {
			if v.ConfigMap == nil || v.ConfigMap.Name != wantConfigMap {
				t.Errorf("overlay redirected the config volume to %+v, want ConfigMap.Name %q", v.ConfigMap, wantConfigMap)
			}
		}
	}
}

// TestBuildStatefulSet_PodTemplateOverlay_CannotChangeControlIdentity: the control SA name
// and AutomountServiceAccountToken=true are part of the same closed invariant — an overlay
// must not be able to run the control pod under a different identity or disable the ordinary
// API automount the creator cutover requires.
func TestBuildStatefulSet_PodTemplateOverlay_CannotChangeControlIdentity(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	a.Spec.Ach = achIdentity("", "")
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img"
	p.Spec.PodTemplate = &apiextensionsv1.JSON{Raw: []byte(`{
		"spec": {"serviceAccountName": "attacker-sa", "automountServiceAccountToken": false}
	}`)}

	sts, err := buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatal(err)
	}
	ps := sts.Spec.Template.Spec
	wantSA := "ach-harness-" + string(a.UID)
	if ps.ServiceAccountName != wantSA {
		t.Errorf("overlay changed serviceAccountName to %q, want re-pinned %q", ps.ServiceAccountName, wantSA)
	}
	if ps.AutomountServiceAccountToken == nil || !*ps.AutomountServiceAccountToken {
		t.Error("overlay disabled automountServiceAccountToken; must stay re-pinned true")
	}
}

func TestBuildDeployment_PodTemplateOverlayInvalid(t *testing.T) {
	for name, raw := range map[string]string{
		"malformed-json": `{"spec":`,
		"type-mismatch":  `{"spec": {"containers": {"not": "a-list"}}}`,
	} {
		a := &achv1alpha1.ACHAgent{}
		a.Name = "demo"
		a.Spec.Ach = achIdentity("", "")
		p := &achv1alpha1.AgentProfile{}
		p.Spec.Achagent.Image = "img"
		p.Spec.PodTemplate = &apiextensionsv1.JSON{Raw: []byte(raw)}
		if _, err := buildStatefulSet(a, p, "h", nil); err == nil {
			t.Errorf("%s: invalid podTemplate overlay must error", name)
		}
	}
}

func TestNeedsNetworkPolicy_PresenceIsTheOptIn(t *testing.T) {
	p := &achv1alpha1.AgentProfile{}
	if needsNetworkPolicy(p) {
		t.Error("nil networkPolicy must render no policy (pre-feature behaviour)")
	}
	p.Spec.NetworkPolicy = &achv1alpha1.NetworkPolicySpec{}
	if !needsNetworkPolicy(p) {
		t.Error("empty networkPolicy block must render a deny-all-except-DNS policy")
	}
}

func TestBuildNetworkPolicy_EmptyBlockIsDNSOnlyAndEgressOnly(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	p := &achv1alpha1.AgentProfile{}
	p.Spec.NetworkPolicy = &achv1alpha1.NetworkPolicySpec{}

	np := buildNetworkPolicy(a, p)

	if np.Name != "achagent-demo" || np.Namespace != "ns" {
		t.Fatalf("np name/ns = %q/%q, want achagent-demo/ns", np.Name, np.Namespace)
	}
	if got := np.Spec.PodSelector.MatchLabels[agentLabelKey]; got != "demo" {
		t.Errorf("podSelector = %v, want %s=demo", np.Spec.PodSelector.MatchLabels, agentLabelKey)
	}
	// Ingress must stay untouched or expose.service/gateway routing breaks.
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("policyTypes = %v, want [Egress] only", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Egress) != 1 {
		t.Fatalf("egress rules = %d, want 1 (DNS only)", len(np.Spec.Egress))
	}
	dns := np.Spec.Egress[0]
	if len(dns.To) != 0 {
		t.Errorf("DNS rule To = %v, want empty (port-53-to-anywhere)", dns.To)
	}
	if len(dns.Ports) != 2 {
		t.Fatalf("DNS rule ports = %d, want 2 (udp + tcp)", len(dns.Ports))
	}
	protos := map[corev1.Protocol]bool{}
	for _, pt := range dns.Ports {
		if pt.Port == nil || pt.Port.IntValue() != 53 {
			t.Errorf("DNS port = %v, want 53", pt.Port)
		}
		if pt.Protocol == nil {
			t.Fatal("DNS port protocol must be explicit")
		}
		protos[*pt.Protocol] = true
	}
	if !protos[corev1.ProtocolUDP] || !protos[corev1.ProtocolTCP] {
		t.Errorf("DNS protocols = %v, want both UDP and TCP", protos)
	}
}

func TestBuildNetworkPolicy_ProfileRulesAppendedAfterDNS(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt(443)
	p := &achv1alpha1.AgentProfile{}
	p.Spec.NetworkPolicy = &achv1alpha1.NetworkPolicySpec{
		Egress: []networkingv1.NetworkPolicyEgressRule{{
			To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/8"}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
		}},
	}

	np := buildNetworkPolicy(a, p)

	if len(np.Spec.Egress) != 2 {
		t.Fatalf("egress rules = %d, want 2 (dns + profile rule)", len(np.Spec.Egress))
	}
	if len(np.Spec.Egress[0].To) != 0 {
		t.Error("DNS rule must stay first — a profile rule was prepended")
	}
	if np.Spec.Egress[1].To[0].IPBlock.CIDR != "10.0.0.0/8" {
		t.Errorf("profile rule lost: %+v", np.Spec.Egress[1])
	}
	// The profile comes from the informer cache — the builder must never append into it.
	if len(p.Spec.NetworkPolicy.Egress) != 1 {
		t.Errorf("builder mutated the cached profile's egress slice: len = %d, want 1", len(p.Spec.NetworkPolicy.Egress))
	}
}

// TestBuildDeployment_AgentImageAndEngineOverride: profile engine forwardEnv; agent
// startupTimeoutSeconds + image override. The container image must be the agent's,
// the startup budget must come from the RESOLVED engine, and the rendered engine
// must inherit the profile's forwardEnv (per-field deep merge).
func TestBuildDeployment_AgentImageAndEngineOverride(t *testing.T) {
	st := int64(600)
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Ach = achIdentity("", "e")
	a.Spec.Channels = []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}}
	a.Spec.AgentDefaults.Image = "img:agent"
	a.Spec.AgentDefaults.Engine = &achv1alpha1.EngineSpec{StartupTimeoutSeconds: ptrInt64(45)}
	a.Spec.Env = []corev1.EnvVar{{Name: "OPENCODE_ENABLE_EXA", Value: "true"}}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent = achv1alpha1.AgentDefaults{
		Image:  "img:profile",
		Ach:    &achv1alpha1.AchSpec{BaseURL: "https://ach"},
		Model:  &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
		Engine: &achv1alpha1.EngineSpec{ForwardEnv: []string{"OPENCODE_ENABLE_EXA"}, StartupTimeoutSeconds: &st},
	}

	dep, err := buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatalf("buildStatefulSet: %v", err)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if c.Image != "img:agent" {
		t.Errorf("container image = %q, want agent override img:agent", c.Image)
	}
	if got := c.StartupProbe.FailureThreshold; got != int32(45/5)+1 {
		t.Errorf("startup FailureThreshold = %d, want %d (from resolved engine startupTimeoutSeconds)", got, int32(45/5)+1)
	}

	eng := agentrender.ResolveEngine(a.Spec.Engine, p.Spec.Achagent.Engine)
	if eng == nil || len(eng.ForwardEnv) != 1 || eng.ForwardEnv[0] != "OPENCODE_ENABLE_EXA" {
		t.Errorf("resolved engine forwardEnv = %+v, want inherited [OPENCODE_ENABLE_EXA]", eng)
	}
}

func TestBuildDeployment_Shape(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Ach = achIdentity("", "prod")
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "ghcr.io/ackstorm/ach-agent:role"
	p.Spec.Achagent.Ach = &achv1alpha1.AchSpec{BaseURL: "https://ach"}
	p.Spec.Persistence = &achv1alpha1.PersistenceSpec{Enabled: true, Size: "1Gi", MountPath: "/var/lib/ach-agent"}

	dep, err := buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatal(err)
	}
	ps := dep.Spec.Template.Spec
	if len(ps.Containers) != 1 || ps.Containers[0].Name != agentContainerName || ps.Containers[0].Args != nil {
		t.Fatalf("must render the single %q container with no args: %+v", agentContainerName, ps.Containers)
	}
	if ps.Containers[0].StartupProbe.HTTPGet == nil || ps.Containers[0].StartupProbe.HTTPGet.Port.IntVal != 8080 {
		t.Error("probes must stay HTTP on the fixed control port 8080 (workspace-v1 contract §11)")
	}
	// fsGroup: a fresh root-owned cloud PVC is otherwise unwritable by uid 10001
	// (ach-agent finding, 2026-09-15).
	for name, got := range map[string]*int64{"runAsUser": ps.SecurityContext.RunAsUser, "runAsGroup": ps.SecurityContext.RunAsGroup, "fsGroup": ps.SecurityContext.FSGroup} {
		if got == nil || *got != agentUID {
			t.Errorf("pod %s = %v, want %d", name, got, agentUID)
		}
	}
	if ps.EnableServiceLinks != nil {
		t.Error("enableServiceLinks must stay unset")
	}
	var pvcMnt *corev1.VolumeMount
	for i := range ps.Containers[0].VolumeMounts {
		if ps.Containers[0].VolumeMounts[i].Name == pvcVolumeName {
			pvcMnt = &ps.Containers[0].VolumeMounts[i]
		}
	}
	if got := pvcMnt; got == nil || got.MountPath != "/var/lib/ach-agent" || got.SubPath != "" {
		t.Errorf("PVC mount must stay the whole base dir, got %+v", got)
	}
	if tp := buildService(a, p).Spec.Ports[0].TargetPort.IntVal; tp != 8080 {
		t.Errorf("Service targetPort must stay the fixed control port 8080, got %d", tp)
	}
}

func ptrInt64(v int64) *int64 { return &v }

// buildTestControlSTS is the shared fixture for the control-pod naming/volume regression
// tests below — split out of one over-complex test into several focused ones.
func buildTestControlSTS(t *testing.T) (*appsv1.StatefulSet, *achv1alpha1.ACHAgent) {
	t.Helper()
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	a.Spec.Ach = achIdentity("", "")
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img"

	sts, err := buildStatefulSet(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatal(err)
	}
	return sts, a
}

// TestBuildStatefulSet_ControlNaming is the contract §11 regression guard found by root's
// review of f66d476: the control StatefulSet's own metadata.name must be ach-control-<uid>
// (matching ServiceName, not the legacy achagent-<name> scheme other children keep).
func TestBuildStatefulSet_ControlNaming(t *testing.T) {
	sts, a := buildTestControlSTS(t)
	wantName := "ach-control-" + string(a.UID)
	if sts.Name != wantName {
		t.Errorf("StatefulSet name = %q, want %q (contract §11)", sts.Name, wantName)
	}
	if sts.Spec.ServiceName != wantName {
		t.Errorf("StatefulSet.ServiceName = %q, want %q (must match metadata.name)", sts.Spec.ServiceName, wantName)
	}
}

// TestBuildStatefulSet_ConfigMount: the control pod must mount the config ConfigMap as a
// single read-only directory at configMountDir (contract §11 scope reset — no broker-token
// or leaf-TLS volumes exist any more) and must automount its ordinary ServiceAccount token
// (contract §11 creator cutover: the Harness needs it to use the workspace-creator grant).
func TestBuildStatefulSet_ConfigMount(t *testing.T) {
	sts, _ := buildTestControlSTS(t)
	ps := sts.Spec.Template.Spec
	if ps.AutomountServiceAccountToken == nil || !*ps.AutomountServiceAccountToken {
		t.Fatal("control pod must automount its ServiceAccount token")
	}
	var mnt *corev1.VolumeMount
	for i := range ps.Containers[0].VolumeMounts {
		if ps.Containers[0].VolumeMounts[i].Name == configVolumeName {
			mnt = &ps.Containers[0].VolumeMounts[i]
		}
	}
	if mnt == nil || mnt.MountPath != configMountDir || !mnt.ReadOnly || mnt.SubPath != "" {
		t.Errorf("config directory mount = %+v, want MountPath %q read-only, no SubPath", mnt, configMountDir)
	}
}

// TestBuildExecutionBootstrapConfigMap_StrictShape pins bootstrap.json to the exact shape
// ../ach-agent/tests/config/fixtures/bootstrap.json and the contract doc require: bootstrapVersion
// MUST be "workspace-v1" (root-flagged regression — this repo previously emitted the wrong
// literal "v1", confirmed incompatible with the accepted Python BootstrapConfig), agent
// identity and control/facade endpoints, copied verbatim from the already-rendered
// infrastructure.execution block (never recomputed). Contract §11 scope reset: no auth
// block and no ca.crt — D2 owns authenticated application calls, out of this task's scope.
func TestBuildExecutionBootstrapConfigMap_StrictShape(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	infra := agentrender.WSInfrastructureBlock{
		Execution: agentrender.WSExecutionInfraBlock{
			ControlEndpoint: "http://ach-control-" + string(a.UID) + ".ns.svc:8081",
			FacadeEndpoint:  "http://ach-control-" + string(a.UID) + ".ns.svc:8081/facades",
		},
	}

	cm, err := buildExecutionBootstrapConfigMap(a, infra)
	if err != nil {
		t.Fatal(err)
	}
	if cm.Name != "ach-execution-"+string(a.UID) {
		t.Errorf("bootstrap ConfigMap name = %q, want ach-execution-<uid>", cm.Name)
	}
	raw, ok := cm.Data[bootstrapFileName]
	if !ok {
		t.Fatalf("no %q key in bootstrap ConfigMap data: %v", bootstrapFileName, cm.Data)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("bootstrap.json invalid JSON: %v", err)
	}

	if got["bootstrapVersion"] != "workspace-v1" {
		t.Errorf("bootstrapVersion = %v, want \"workspace-v1\"", got["bootstrapVersion"])
	}
	agentBlock, _ := got["agent"].(map[string]any)
	if agentBlock["name"] != "demo" || agentBlock["namespace"] != "ns" || agentBlock["uid"] != string(a.UID) {
		t.Errorf("agent = %v", agentBlock)
	}
	if got["controlEndpoint"] != infra.Execution.ControlEndpoint {
		t.Errorf("controlEndpoint = %v, want %v", got["controlEndpoint"], infra.Execution.ControlEndpoint)
	}
	if got["facadeEndpoint"] != infra.Execution.FacadeEndpoint {
		t.Errorf("facadeEndpoint = %v, want %v", got["facadeEndpoint"], infra.Execution.FacadeEndpoint)
	}
	// No model/prompts/engine.env/configVersion — bootstrap is a separate, strict,
	// secret-free document, not a PublicConfig fragment (contract §11).
	for _, forbidden := range []string{"model", "prompts", "engine", "configVersion", "auth"} {
		if _, present := got[forbidden]; present {
			t.Errorf("bootstrap.json must not carry %q", forbidden)
		}
	}
	if _, present := cm.Data["ca.crt"]; present {
		t.Fatal("bootstrap ConfigMap must never carry ca.crt (contract §11 scope reset)")
	}
}
