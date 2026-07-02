// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func mkEnv(name, val string) []corev1.EnvVar { return []corev1.EnvVar{{Name: name, Value: val}} }

func TestComputeConfigHash_ChangesWithInputs(t *testing.T) {
	base := computeConfigHash([]byte(`{"a":1}`), []byte(`[]`), "img:1", "sec1")
	if len(base) != 16 {
		t.Fatalf("hash len = %d", len(base))
	}
	for name, h := range map[string]string{
		"config": computeConfigHash([]byte(`{"a":2}`), []byte(`[]`), "img:1", "sec1"),
		"env":    computeConfigHash([]byte(`{"a":1}`), []byte(`[{}]`), "img:1", "sec1"),
		"image":  computeConfigHash([]byte(`{"a":1}`), []byte(`[]`), "img:2", "sec1"),
		"secret": computeConfigHash([]byte(`{"a":1}`), []byte(`[]`), "img:1", "sec2"),
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
	dep := buildDeployment(a, p, "cfghash", env, nil)

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

func TestBuildDeployment_ChannelSecretVolumeModeAndFSGroup(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Image = "img"
	p.Spec.Ach.BaseURL = "https://ach"
	fsg := int64(10001)
	p.Spec.Security = &achv1alpha1.PodSecuritySpec{FSGroup: &fsg}

	dep := buildDeployment(a, p, "h", buildAgentEnv(a, p), map[string][]string{"gitlab-webhook": {"secret"}})

	// fsGroup (from the profile) must own the secret files so the non-root
	// harness can read them via the group bit. RunAsNonRoot stays enforced.
	sc := dep.Spec.Template.Spec.SecurityContext
	if sc == nil || sc.FSGroup == nil || *sc.FSGroup != 10001 {
		t.Errorf("pod fsGroup not propagated from profile (securityContext=%+v)", sc)
	}
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("RunAsNonRoot must stay enforced")
	}

	var found bool
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Secret == nil || v.Secret.SecretName != "gitlab-webhook" {
			continue
		}
		found = true
		// 0440 = owner+group read, no world/other. Group is fsGroup (harness).
		if v.Secret.DefaultMode == nil || *v.Secret.DefaultMode != 0o440 {
			t.Errorf("channel secret DefaultMode = %v, want 0440", v.Secret.DefaultMode)
		}
	}
	if !found {
		t.Fatal("channel secret volume not built")
	}
}
