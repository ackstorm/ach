// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/agentrender"
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
	p.Spec.Achagent.Image = "img"
	p.Spec.Achagent.Ach = &achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}
	p.Spec.Env = mkEnv("HTTPS_PROXY", "http://p")

	env := buildAgentEnv(a, p, "")

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

// TestBuildAgentEnv_BaseURLResolution proves ACH_BASE_URL follows the same
// agent ?? profile ?? operator-default chain as the rendered config.
func TestBuildAgentEnv_BaseURLResolution(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name = "d"
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}
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
	p.Spec.Achagent.Ach = &achv1alpha1.AchEndpointSpec{BaseURL: "https://profile"}
	if v := get(buildAgentEnv(a, p, "https://env")); v != "https://profile" {
		t.Errorf("ACH_BASE_URL = %q, want profile over default", v)
	}
	a.Spec.Ach = &achv1alpha1.AchEndpointSpec{BaseURL: "https://agent"}
	if v := get(buildAgentEnv(a, p, "https://env")); v != "https://agent" {
		t.Errorf("ACH_BASE_URL = %q, want agent override", v)
	}
}

// TestHealthOverride_ConfigProbeServiceAgree is the drift guard: an ACHAgent
// health override must move the config health block, the probe port, the Service
// targetPort, and the containerPort together — all resolved via the one shared
// agentrender.ResolveHealth, so they can never disagree.
func TestHealthOverride_ConfigProbeServiceAgree(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}
	a.Spec.Capability.Environment = "e"
	a.Spec.Model = &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}
	a.Spec.Channels = []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}}
	a.Spec.Health = &achv1alpha1.HealthSpec{Port: 9137} // agent override
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "img"
	p.Spec.Achagent.Ach = &achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}
	p.Spec.Achagent.Health = &achv1alpha1.HealthSpec{Port: 8000} // profile default, must lose

	const want = int32(9137)
	cfg, err := agentrender.Render(*p, *a, "")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if cfg.Health.Port != want {
		t.Errorf("config health.port = %d, want %d", cfg.Health.Port, want)
	}
	if got := resolveHealthPort(a, p); got != want {
		t.Errorf("probe port = %d, want %d", got, want)
	}
	if tp := buildService(a, p).Spec.Ports[0].TargetPort.IntVal; tp != want {
		t.Errorf("service targetPort = %d, want %d", tp, want)
	}
	dep, err := buildDeployment(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatalf("buildDeployment: %v", err)
	}
	if cp := dep.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort; cp != want {
		t.Errorf("containerPort = %d, want %d", cp, want)
	}
}

func TestBuildAgentEnv_RejectsReservedEnv(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name = "d"
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Ach = &achv1alpha1.AchEndpointSpec{BaseURL: "u"}
	p.Spec.Env = mkEnv("ACH_TOKEN", "ek_LEAK") // reserved — must be dropped
	for _, e := range buildAgentEnv(a, p, "") {
		if e.Name == "ACH_TOKEN" && e.Value == "ek_LEAK" {
			t.Fatal("reserved ACH_* env leaked into the pod spec")
		}
	}
}

func TestBuildAgentEnv_AgentOverridesProfileEnv(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}
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
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent.Image = "ghcr.io/ackstorm/ach-agent:latest"
	p.Spec.Achagent.Ach = &achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}

	env := buildAgentEnv(a, p, "")
	dep, err := buildDeployment(a, p, "cfghash", env)
	if err != nil {
		t.Fatalf("buildDeployment: %v", err)
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
	p.Spec.Achagent.Image = "img"
	p.Spec.Achagent.Ach = &achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}

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

	// And it must NOT be mounted as a file, nor pull in fsGroup (uid/perms reverted).
	dep, err := buildDeployment(a, p, "h", buildAgentEnv(a, p, ""))
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

func TestBuildAgentEnv_PrepareSecretGetsGeneratedAlias(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "demo-ek", Key: "ek"}
	a.Spec.Env = []corev1.EnvVar{{Name: "GITLAB_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "gl-clone"}, Key: "token",
	}}}}
	a.Spec.Channels = []achv1alpha1.ChannelSpec{{
		Name: "gitlab-mr-review", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"},
		Prepare: &achv1alpha1.PrepareSpec{Script: "true", ForwardEnv: []string{"GITLAB_TOKEN"}},
	}}
	p := &achv1alpha1.AgentProfile{}

	var original, alias *corev1.EnvVar
	env := buildAgentEnv(a, p, "")
	for i := range env {
		e := &env[i]
		switch e.Name {
		case "GITLAB_TOKEN":
			original = e
		case "ACH_SECRET_GITLAB_MR_REVIEW_PREPARE_GITLAB_TOKEN":
			alias = e
		}
	}
	if original == nil || alias == nil || alias.ValueFrom == nil || alias.ValueFrom.SecretKeyRef == nil ||
		alias.ValueFrom.SecretKeyRef.Name != "gl-clone" || alias.ValueFrom.SecretKeyRef.Key != "token" {
		t.Fatalf("original=%+v alias=%+v", original, alias)
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
	p.Spec.Achagent.Image = "img"
	p.Spec.Achagent.Ach = &achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}

	// The hindsight admin secret rides in env (secretKeyRef), never inline, never a file.
	var found bool
	for _, e := range buildAgentEnv(a, p, "") {
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
	for _, e := range buildAgentEnv(a, p, "") {
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
	p.Spec.Achagent.Image = "img:1"
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
		p.Spec.Achagent.Image = "img"
		p.Spec.PodTemplate = &apiextensionsv1.JSON{Raw: []byte(raw)}
		if _, err := buildDeployment(a, p, "h", nil); err == nil {
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

// TestBuildDeployment_AgentImageAndEngineOverride: profile engine forwardEnv +
// type opencode; agent type pi + image override. The container image must be the
// agent's, the startup budget must come from the RESOLVED engine, and the
// rendered engine must inherit the profile's forwardEnv while taking the
// agent's type (per-field deep merge).
func TestBuildDeployment_AgentImageAndEngineOverride(t *testing.T) {
	st := int64(600)
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.Spec.Identity.SecretRef = achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}
	a.Spec.Capability.Environment = "e"
	a.Spec.Channels = []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}}
	a.Spec.AgentDefaults = achv1alpha1.AgentDefaults{
		Image:  "img:agent",
		Engine: &achv1alpha1.EngineSpec{Type: "pi", Pi: &achv1alpha1.PiEngineSpec{BinaryPath: "pi"}},
	}
	p := &achv1alpha1.AgentProfile{}
	p.Spec.Achagent = achv1alpha1.AgentDefaults{
		Image:  "img:profile",
		Ach:    &achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"},
		Model:  &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
		Engine: &achv1alpha1.EngineSpec{Type: "opencode", ForwardEnv: []string{"HTTPS_PROXY"}, StartupTimeoutSeconds: &st},
	}

	dep, err := buildDeployment(a, p, "h", buildAgentEnv(a, p, ""))
	if err != nil {
		t.Fatalf("buildDeployment: %v", err)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if c.Image != "img:agent" {
		t.Errorf("container image = %q, want agent override img:agent", c.Image)
	}
	if got := c.StartupProbe.FailureThreshold; got != int32(600/5)+1 {
		t.Errorf("startup FailureThreshold = %d, want %d (from resolved engine startupTimeoutSeconds)", got, int32(600/5)+1)
	}

	cfg, err := agentrender.Render(*p, *a, "")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if cfg.Engine == nil || cfg.Engine.Type != "pi" {
		t.Errorf("rendered engine type = %+v, want agent's pi", cfg.Engine)
	}
	if len(cfg.Engine.ForwardEnv) != 1 || cfg.Engine.ForwardEnv[0] != "HTTPS_PROXY" {
		t.Errorf("rendered engine forwardEnv = %v, want inherited [HTTPS_PROXY]", cfg.Engine.ForwardEnv)
	}
}
