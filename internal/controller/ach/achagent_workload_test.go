// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func mkEnv(name, val string) []corev1.EnvVar { return []corev1.EnvVar{{Name: name, Value: val}} }

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
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	a.Spec.Capability.Environment = "prod"
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Image = "img"
	p.Spec.Ach.BaseURL = "https://ach"
	p.Spec.ExtraEnv = mkEnv("HTTPS_PROXY", "http://p")

	env := buildAgentEnv(a, p)

	var token, base, extra, reserved bool
	for _, e := range env {
		switch e.Name {
		case "ACH_TOKEN":
			token = e.Value == "" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil && e.ValueFrom.SecretKeyRef.Name == "demo-ek"
		case "ACH_BASE_URL":
			base = e.Value == "https://ach"
		case "HTTPS_PROXY":
			extra = true
		}
		if e.Name == "ACH_BASE_URL" && e.Value != "https://ach" {
			reserved = true
		}
	}
	if !token {
		t.Error("ACH_TOKEN must be a secretKeyRef to demo-ek")
	}
	if !base || !extra || reserved {
		t.Errorf("env assembly wrong: base=%v extra=%v reservedHijack=%v", base, extra, reserved)
	}
}

func TestBuildAgentEnv_RejectsReservedExtraEnv(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name = "d"
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Ach.BaseURL = "u"
	p.Spec.ExtraEnv = mkEnv("ACH_TOKEN", "ek_LEAK") // reserved — must be dropped
	for _, e := range buildAgentEnv(a, p) {
		if e.Name == "ACH_TOKEN" && e.Value == "ek_LEAK" {
			t.Fatal("reserved ACH_* extraEnv leaked into the pod spec")
		}
	}
}

func TestBuildDeployment_MountsConfigProbesAndHash(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Image = "ghcr.io/ackstorm/ach-agent:latest"
	p.Spec.Ach.BaseURL = "https://ach"

	env := buildAgentEnv(a, p)
	dep, err := buildDeployment(a, p, "cfghash", env)
	if err != nil {
		t.Fatalf("buildDeployment: %v", err)
	}

	if *dep.Spec.Replicas != 1 {
		t.Errorf("replicas = %d", *dep.Spec.Replicas)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if c.Image != p.Spec.Image {
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
	// Named health port (8000) — the PodMonitor scrapes /metrics on it BY NAME,
	// so a rename here silently breaks agent metrics collection.
	var hp *corev1.ContainerPort
	for i := range c.Ports {
		if c.Ports[i].Name == "health" {
			hp = &c.Ports[i]
		}
	}
	if hp == nil || hp.ContainerPort != 8000 {
		t.Errorf("container must declare named health port 8000, got %+v", c.Ports)
	}
	found := false
	for _, mnt := range c.VolumeMounts {
		if mnt.MountPath == "/etc/ach-agent/config.json" && mnt.SubPath == "config.json" {
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
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	a.Spec.Channels = []achv1alpha1.ChannelSpec{{
		Name: "gitlab-mr-review", Type: "webhook",
		Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "gitlab_token", SecretRef: &achv1alpha1.SecretKeyRef{Name: "gl-hook", Key: "secret"}}},
	}}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Image = "img"
	p.Spec.Ach.BaseURL = "https://ach"

	// Auth secret must be a secretKeyRef env var, never an inline value.
	var found bool
	for _, e := range buildAgentEnv(a, p) {
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

	// And it must NOT be mounted as a file, nor pull in fsGroup (uid/perms reverted).
	dep, err := buildDeployment(a, p, "h", buildAgentEnv(a, p))
	if err != nil {
		t.Fatalf("buildDeployment: %v", err)
	}
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Secret != nil {
			t.Errorf("secret volume present; auth secrets are env-injected now: %+v", v)
		}
	}
	if sc := dep.Spec.Template.Spec.SecurityContext; sc == nil || sc.FSGroup != nil {
		t.Errorf("fsGroup should be unset (uid/perms reverted); securityContext=%+v", sc)
	}
}

func TestBuildAgentEnv_MemoryAuthInjectedAsEnv(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	a.Spec.Channels = []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}}
	a.Spec.Memory = &achv1alpha1.MemorySpec{Type: "hindsight", Hindsight: &achv1alpha1.HindsightSpec{
		Endpoint: "http://h", Auth: &achv1alpha1.SecretKeyRef{Name: "hs-admin", Key: "token"},
	}}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Image = "img"
	p.Spec.Ach.BaseURL = "https://ach"

	// The hindsight admin secret rides in env (secretKeyRef), never inline, never a file.
	var found bool
	for _, e := range buildAgentEnv(a, p) {
		if e.Name != "ACH_SECRET_MEMORY_HINDSIGHT" {
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
	a.Spec.Memory.Hindsight.Auth = nil
	for _, e := range buildAgentEnv(a, p) {
		if e.Name == "ACH_SECRET_MEMORY_HINDSIGHT" {
			t.Errorf("no-auth hindsight must not inject ACH_SECRET_MEMORY_HINDSIGHT")
		}
	}
}

func TestBuildDeployment_PodTemplateOverlay(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Image = "img:1"
	p.Spec.PodTemplate = &apiextensionsv1.JSON{Raw: []byte(`{
		"metadata": {"labels": {"ach.ackstorm.ai/agent": "hijack", "team": "x"}},
		"spec": {
			"securityContext": {"fsGroup": 1000, "fsGroupChangePolicy": "OnRootMismatch"},
			"containers": [{"name": "agent", "envFrom": [{"secretRef": {"name": "extra"}}]}]
		}
	}`)}

	dep, err := buildDeployment(a, p, "hash1", mkEnv("A", "1"))
	if err != nil {
		t.Fatalf("buildDeployment: %v", err)
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
		t.Errorf("selector label = %q, want re-pinned %q (immutable Deployment selector)", tmpl.Labels[agentLabelKey], "demo")
	}
	if tmpl.Labels["team"] != "x" {
		t.Error("user label dropped")
	}
	if tmpl.Annotations[configHashAnnotation] != "hash1" {
		t.Error("config-hash annotation not re-pinned after merge")
	}
}

func TestBuildDeployment_PodTemplateOverlayInvalid(t *testing.T) {
	for name, raw := range map[string]string{
		"malformed-json": `{"spec":`,
		"type-mismatch":  `{"spec": {"containers": {"not": "a-list"}}}`,
	} {
		a := &achv1alpha1.ACHAgent{}
		a.Name = "demo"
		a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}
		p := &achv1alpha1.AgentProfile{}
		p.Spec.Image = "img"
		p.Spec.PodTemplate = &apiextensionsv1.JSON{Raw: []byte(raw)}
		if _, err := buildDeployment(a, p, "h", nil); err == nil {
			t.Errorf("%s: invalid podTemplate overlay must error", name)
		}
	}
}
