// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func ptr[T any](v T) *T                      { return &v }
func rawJSON(s string) *apiextensionsv1.JSON { return &apiextensionsv1.JSON{Raw: []byte(s)} }

func TestRender_FullGolden(t *testing.T) {
	profile := achv1alpha1.AgentProfile{
		Spec: achv1alpha1.AgentProfileSpec{
			Achagent: achv1alpha1.AgentDefaults{
				Image: "ghcr.io/ackstorm/ach-agent:latest",
				Ach:   &achv1alpha1.AchEndpointSpec{BaseURL: "https://ach.ackstorm.ai"},
				Model: &achv1alpha1.ModelSpec{Name: "openai.gpt-5", Type: "openai"},
				Engine: &achv1alpha1.EngineSpec{
					Home: "/var/lib/ach-agent/home", ForwardEnv: []string{"HTTPS_PROXY"},
					IdleTTLSeconds: ptr(int64(30)), StartupTimeoutSeconds: ptr(int64(30)),
				},
				Limits: &achv1alpha1.LimitsSpec{MaxConcurrentInvocations: ptr(int64(8)), MaxSteps: ptr(int64(50))},
				Health: &achv1alpha1.HealthSpec{Host: "0.0.0.0", Port: 8000},
			},
			Persistence: &achv1alpha1.PersistenceSpec{Enabled: true, MountPath: "/var/lib/ach-agent"},
		},
	}
	agent := achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "gitlab-ackstorm"},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "gemini-small"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ops-ek", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{
				Environment: "engineering-prod",
				Filter:      &achv1alpha1.FilterSpec{Exclude: &achv1alpha1.ExcludeSpec{Skills: []string{"send-email"}}},
			},
			AgentDefaults: achv1alpha1.AgentDefaults{
				Model: &achv1alpha1.ModelSpec{Name: "openai.gpt-5", Type: "openai", Params: rawJSON(`{"temperature":1}`)},
			},
			Prompt: &achv1alpha1.AgentPromptSpec{System: achv1alpha1.PromptSystemSpec{Type: "text", Text: "You are a reviewer."}, Compose: "append"},
			Channels: []achv1alpha1.ChannelSpec{
				{
					Name: "gitlab-mr-review", Type: "webhook", Source: "gitlab",
					Concurrency: ptr(int64(4)), Session: &achv1alpha1.SessionSpec{Type: "auto"}, Prompt: "Review: {{ payload.object_attributes.url }}",
					Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "gitlab_token", SecretRef: &achv1alpha1.SecretKeyRef{Name: "gitlab-webhook", Key: "secret"}}},
				},
				{Name: "daily", Type: "cron", Concurrency: ptr(int64(1)), Session: &achv1alpha1.SessionSpec{Type: "none"}, Prompt: "Scan for CVEs.", Cron: &achv1alpha1.CronSpec{Schedule: "0 8 * * 1-5", Timezone: "Europe/Madrid"}},
			},
		},
	}

	cfg, err := Render(profile, agent, "")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["schemaVersion"] != "1" {
		t.Errorf("schemaVersion = %v", m["schemaVersion"])
	}
	ab := m["agent"].(map[string]any)
	if ab["name"] != "gitlab-ackstorm" || len(ab) != 1 {
		t.Errorf("agent block = %v (must be {name} only)", ab)
	}
	capBlock := m["capability"].(map[string]any)
	if capBlock["type"] != "ach" {
		t.Errorf("capability.type = %v", capBlock["type"])
	}
	achBlock := capBlock["ach"].(map[string]any)
	if achBlock["baseUrl"] != "https://ach.ackstorm.ai" || achBlock["environment"] != "engineering-prod" {
		t.Errorf("capability.ach = %v", achBlock)
	}
	ch0auth := m["channels"].([]any)[0].(map[string]any)["webhook"].(map[string]any)["auth"].(map[string]any)
	ch0secret, ok := ch0auth["secret"].(map[string]any)
	if !ok || ch0secret["env"] != "ACH_SECRET_GITLAB_MR_REVIEW_WEBHOOK" {
		t.Errorf("webhook auth.secret.env = %v (want ACH_SECRET_GITLAB_MR_REVIEW_WEBHOOK)", ch0auth["secret"])
	}
	if _, leaked := ch0auth["secretPath"]; leaked {
		t.Errorf("secretPath leaked (auth secrets are env-injected now)")
	}
	if _, leaked := ch0auth["secretRef"]; leaked {
		t.Errorf("secretRef leaked into rendered config")
	}
	if _, hasFile := ch0secret["file"]; hasFile {
		t.Errorf("auth.secret.file set; operator defaults to env")
	}
	ch0sess, ok := m["channels"].([]any)[0].(map[string]any)["session"].(map[string]any)
	if !ok || ch0sess["type"] != "auto" {
		t.Errorf("channel[0].session = %v (want {type: auto})", m["channels"].([]any)[0].(map[string]any)["session"])
	}
}

func TestRender_ModelOverride(t *testing.T) {
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{Achagent: achv1alpha1.AgentDefaults{Image: "x", Ach: &achv1alpha1.AchEndpointSpec{BaseURL: "u"}, Model: &achv1alpha1.ModelSpec{Name: "profile-model", Type: "openai"}}}}
	a := achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "a"},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef:    achv1alpha1.LocalObjectRef{Name: "p"},
			Identity:      achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
			Capability:    achv1alpha1.CapabilitySpec{Environment: "e"},
			AgentDefaults: achv1alpha1.AgentDefaults{Model: &achv1alpha1.ModelSpec{Name: "agent-model", Type: "gemini"}},
			Channels:      []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	}
	cfg, err := Render(p, a, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Name != "agent-model" || cfg.Model.Type != "gemini" {
		t.Errorf("agent model must override profile: %+v", cfg.Model)
	}
}

func TestRender_NoModel_Errors(t *testing.T) {
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{Achagent: achv1alpha1.AgentDefaults{Image: "x", Ach: &achv1alpha1.AchEndpointSpec{BaseURL: "u"}}}}
	a := achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "a"},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "p"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "e"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	}
	if _, err := Render(p, a, ""); err == nil {
		t.Error("expected error when no model set")
	}
}

func TestResolveAchBaseURL_Precedence(t *testing.T) {
	agent := &achv1alpha1.AchEndpointSpec{BaseURL: "https://agent"}
	profile := &achv1alpha1.AchEndpointSpec{BaseURL: "https://profile"}
	cases := []struct {
		name    string
		agent   *achv1alpha1.AchEndpointSpec
		profile *achv1alpha1.AchEndpointSpec
		def     string
		want    string
	}{
		{"agent wins", agent, profile, "https://env", "https://agent"},
		{"profile when no agent block", nil, profile, "https://env", "https://profile"},
		{"profile when agent block empty", &achv1alpha1.AchEndpointSpec{}, profile, "https://env", "https://profile"},
		{"env default when both empty", nil, nil, "https://env", "https://env"},
		{"empty when all empty", nil, nil, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveAchBaseURL(c.agent, c.profile, c.def); got != c.want {
				t.Errorf("ResolveAchBaseURL = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveHealth_FieldMerge(t *testing.T) {
	cases := []struct {
		name     string
		agent    *achv1alpha1.HealthSpec
		profile  *achv1alpha1.HealthSpec
		wantHost string
		wantPort int32
	}{
		{"defaults", nil, nil, DefaultHealthHost, DefaultHealthPort},
		{"profile used when no agent", nil, &achv1alpha1.HealthSpec{Host: "1.2.3.4", Port: 9000}, "1.2.3.4", 9000},
		// Field-level merge: agent port wins, unset agent host INHERITS the profile's.
		{"agent port merges over profile host", &achv1alpha1.HealthSpec{Port: 9100}, &achv1alpha1.HealthSpec{Host: "1.2.3.4", Port: 9000}, "1.2.3.4", 9100},
		{"agent host merges over profile port", &achv1alpha1.HealthSpec{Host: "127.0.0.1"}, &achv1alpha1.HealthSpec{Port: 9000}, "127.0.0.1", 9000},
		{"agent-only falls to defaults for unset", &achv1alpha1.HealthSpec{Port: 9100}, nil, DefaultHealthHost, 9100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			host, port := ResolveHealth(c.agent, c.profile)
			if host != c.wantHost || port != c.wantPort {
				t.Errorf("ResolveHealth = (%q,%d), want (%q,%d)", host, port, c.wantHost, c.wantPort)
			}
		})
	}
}

func TestRender_BaseURLResolutionAndBlock(t *testing.T) {
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{Achagent: achv1alpha1.AgentDefaults{Image: "x", Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}}}}
	a := achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "a"},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "p"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "e"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	}
	// No ach anywhere → blocked.
	if _, err := Render(p, a, ""); err == nil {
		t.Error("expected block when no ACH base URL anywhere")
	}
	// Operator default resolves it.
	if _, err := Render(p, a, "https://env"); err != nil {
		t.Errorf("operator default should satisfy base URL: %v", err)
	}
	// Agent override wins even with an empty operator default.
	a.Spec.Ach = &achv1alpha1.AchEndpointSpec{BaseURL: "https://agent"}
	cfg, err := Render(p, a, "")
	if err != nil {
		t.Fatalf("agent override should satisfy: %v", err)
	}
	if cfg.Capability.Ach.BaseURL != "https://agent" {
		t.Errorf("capability.ach.baseUrl = %q, want agent override", cfg.Capability.Ach.BaseURL)
	}
}

func TestReferencedSecrets_NameToKeys(t *testing.T) {
	a := achv1alpha1.ACHAgent{Spec: achv1alpha1.ACHAgentSpec{Channels: []achv1alpha1.ChannelSpec{
		{Name: "w1", Type: "webhook", Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "hmac", SecretRef: &achv1alpha1.SecretKeyRef{Name: "s1", Key: "kb"}}}},
		{Name: "w2", Type: "webhook", Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "hmac", SecretRef: &achv1alpha1.SecretKeyRef{Name: "s1", Key: "ka"}}}},
		{Name: "cr", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}},
	}}}
	got := ReferencedSecrets(achv1alpha1.AgentProfile{}, a)
	if len(got) != 1 || len(got["s1"]) != 2 || got["s1"][0] != "ka" || got["s1"][1] != "kb" {
		t.Errorf("ReferencedSecrets = %v, want {s1:[ka kb]}", got)
	}
}

func TestChannelSecretEnv_NamesAndRefs(t *testing.T) {
	a := achv1alpha1.ACHAgent{Spec: achv1alpha1.ACHAgentSpec{Channels: []achv1alpha1.ChannelSpec{
		{Name: "gitlab-mr-review", Type: "webhook", Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "gitlab_token", SecretRef: &achv1alpha1.SecretKeyRef{Name: "gl", Key: "secret"}}}},
		{Name: "peer-intake", Type: "a2a", A2A: &achv1alpha1.A2ASpec{Auth: achv1alpha1.A2AAuthSpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "peer", Key: "apikey"}}}},
		{Name: "daily", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}, // no secret
	}}}
	got := ChannelSecretEnv(achv1alpha1.AgentProfile{}, a)
	if len(got) != 2 {
		t.Fatalf("ChannelSecretEnv len = %d, want 2", len(got))
	}
	if got[0].EnvName != "ACH_SECRET_GITLAB_MR_REVIEW_WEBHOOK" || got[0].SecretName != "gl" || got[0].Key != "secret" {
		t.Errorf("webhook ref = %+v", got[0])
	}
	if got[1].EnvName != "ACH_SECRET_PEER_INTAKE_A2A" || got[1].SecretName != "peer" || got[1].Key != "apikey" {
		t.Errorf("a2a ref = %+v", got[1])
	}
}

func TestMemorySecretEnv_HindsightAuth(t *testing.T) {
	withAuth := achv1alpha1.ACHAgent{Spec: achv1alpha1.ACHAgentSpec{Memory: &achv1alpha1.MemorySpec{
		Type: "hindsight", Hindsight: &achv1alpha1.HindsightSpec{Endpoint: "http://h", Auth: &achv1alpha1.SecretKeyRef{Name: "hs", Key: "token"}},
	}}}
	ref := MemorySecretEnv(withAuth)
	if ref == nil || ref.EnvName != "ACH_SECRET_MEMORY_HINDSIGHT" || ref.SecretName != "hs" || ref.Key != "token" {
		t.Errorf("MemorySecretEnv = %+v, want ACH_SECRET_MEMORY_HINDSIGHT → hs/token", ref)
	}
	// It must join ReferencedSecrets so the reconciler key-check + hash + watch cover it.
	if keys := ReferencedSecrets(achv1alpha1.AgentProfile{}, withAuth)["hs"]; len(keys) != 1 || keys[0] != "token" {
		t.Errorf("ReferencedSecrets missing memory secret: %v", ReferencedSecrets(achv1alpha1.AgentProfile{}, withAuth))
	}
	// No auth → no ref (internal/no-auth Hindsight URL).
	noAuth := achv1alpha1.ACHAgent{Spec: achv1alpha1.ACHAgentSpec{Memory: &achv1alpha1.MemorySpec{
		Type: "hindsight", Hindsight: &achv1alpha1.HindsightSpec{Endpoint: "http://h"},
	}}}
	if ref := MemorySecretEnv(noAuth); ref != nil {
		t.Errorf("MemorySecretEnv(no auth) = %+v, want nil", ref)
	}
	// Non-hindsight memory → no ref.
	if ref := MemorySecretEnv(achv1alpha1.ACHAgent{Spec: achv1alpha1.ACHAgentSpec{Memory: &achv1alpha1.MemorySpec{Type: "codemem"}}}); ref != nil {
		t.Errorf("MemorySecretEnv(codemem) = %+v, want nil", ref)
	}
}

func TestRenderMemory_HindsightRichBlock(t *testing.T) {
	out := renderMemory(&achv1alpha1.MemorySpec{Type: "hindsight", Hindsight: &achv1alpha1.HindsightSpec{
		Endpoint: "http://h", Bank: "b", Mission: "reviewer",
		Auth: &achv1alpha1.SecretKeyRef{Name: "hs", Key: "token"},
		MentalModels: []achv1alpha1.MentalModelSpec{
			{ID: "arch", Name: "Arch", SourceQuery: "what arch?", AutoRefresh: true, MaxTokens: ptr(int64(4096))},
			{ID: "conv", Name: "Conv", SourceQuery: "what conventions?"},
		},
	}})
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hs := m["hindsight"].(map[string]any)
	// auth renders ONLY the operator-generated env name, never the secretRef.
	if auth := hs["auth"].(map[string]any); auth["env"] != "ACH_SECRET_MEMORY_HINDSIGHT" {
		t.Errorf("auth = %v, want {env: ACH_SECRET_MEMORY_HINDSIGHT}", auth)
	}
	if hs["mission"] != "reviewer" || hs["bank"] != "b" {
		t.Errorf("mission/bank = %v/%v", hs["mission"], hs["bank"])
	}
	mm := hs["mentalModels"].([]any)
	if len(mm) != 2 {
		t.Fatalf("mentalModels len = %d, want 2", len(mm))
	}
	m0 := mm[0].(map[string]any)
	if m0["id"] != "arch" || m0["sourceQuery"] != "what arch?" || m0["autoRefresh"] != true || m0["maxTokens"] != float64(4096) {
		t.Errorf("mentalModels[0] = %v", m0)
	}
	// optional fields omitted when unset (harness owns the defaults).
	m1 := mm[1].(map[string]any)
	if _, present := m1["autoRefresh"]; present {
		t.Errorf("mentalModels[1].autoRefresh emitted when unset: %v", m1)
	}
	if _, present := m1["maxTokens"]; present {
		t.Errorf("mentalModels[1].maxTokens emitted when unset: %v", m1)
	}
	// no-auth hindsight → no auth key at all.
	noAuth := renderMemory(&achv1alpha1.MemorySpec{Type: "hindsight", Hindsight: &achv1alpha1.HindsightSpec{Endpoint: "http://h"}})
	nb, _ := json.Marshal(noAuth)
	var nm map[string]any
	_ = json.Unmarshal(nb, &nm)
	if _, present := nm["hindsight"].(map[string]any)["auth"]; present {
		t.Errorf("no-auth hindsight emitted an auth block: %s", nb)
	}
}

func TestRender_ForwardEnvStripsACHPrefix(t *testing.T) {
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{
		Achagent: achv1alpha1.AgentDefaults{
			Image: "x", Ach: &achv1alpha1.AchEndpointSpec{BaseURL: "u"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
			Engine: &achv1alpha1.EngineSpec{ForwardEnv: []string{"HTTPS_PROXY", "ACH_SECRET_X_WEBHOOK", "ACH_TOKEN"}},
		},
	}}
	a := achv1alpha1.ACHAgent{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: achv1alpha1.ACHAgentSpec{
		ProfileRef: achv1alpha1.LocalObjectRef{Name: "p"},
		Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
		Capability: achv1alpha1.CapabilitySpec{Environment: "e"},
		Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
	}}
	cfg, err := Render(p, a, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range cfg.Engine.ForwardEnv {
		if len(n) >= 4 && n[:4] == "ACH_" {
			t.Errorf("forwardEnv leaked reserved var %q (harness hard-fails on secret env in forwardEnv)", n)
		}
	}
	if len(cfg.Engine.ForwardEnv) != 1 || cfg.Engine.ForwardEnv[0] != "HTTPS_PROXY" {
		t.Errorf("forwardEnv = %v, want [HTTPS_PROXY]", cfg.Engine.ForwardEnv)
	}
}

func TestRenderChannel_NilSessionOmitted(t *testing.T) {
	cb := renderChannel(&achv1alpha1.ChannelSpec{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}, nil)
	b, err := json.Marshal(cb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := m["session"]; present {
		t.Errorf("nil ChannelSpec.Session must be omitted, not emitted as {}: %s", b)
	}
}

func TestRenderChannel_GitlabWebhookLoopGuard(t *testing.T) {
	bot := "ackbot"
	cb := renderChannel(&achv1alpha1.ChannelSpec{
		Name: "gl", Type: "webhook", Source: "gitlab",
		Webhook: &achv1alpha1.WebhookSpec{
			Auth:         achv1alpha1.WebhookAuthSpec{Type: "gitlab_token", SecretRef: &achv1alpha1.SecretKeyRef{Name: "gl", Key: "secret"}},
			GitlabEvents: []string{"merge_request"},
			BotUsername:  &bot,
			TriggerUsers: []string{"alice", "bob"},
		},
	}, nil)
	if cb.Webhook.BotUsername == nil || *cb.Webhook.BotUsername != bot {
		t.Errorf("botUsername not passed through verbatim: %+v", cb.Webhook.BotUsername)
	}
	if got := cb.Webhook.TriggerUsers; len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Errorf("triggerUsers not passed through verbatim: %v", got)
	}

	// Omitted → absent from JSON (absent → null → prior behavior), not empty/null keys.
	plain := renderChannel(&achv1alpha1.ChannelSpec{
		Name: "gl2", Type: "webhook", Source: "gitlab",
		Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "none"}},
	}, nil)
	b, err := json.Marshal(plain.Webhook)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["botUsername"]; ok {
		t.Errorf("omitted botUsername must be absent, got: %s", b)
	}
	if _, ok := m["triggerUsers"]; ok {
		t.Errorf("omitted triggerUsers must be absent, got: %s", b)
	}
}

func TestRenderEnginePi(t *testing.T) {
	e := &achv1alpha1.EngineSpec{
		Type: "pi",
		Pi:   &achv1alpha1.PiEngineSpec{BinaryPath: "pi", McpAdapterPath: "/opt/adapter"},
	}
	b := renderEngine(e)
	if b.Type != "pi" {
		t.Fatalf("Type = %q, want pi", b.Type)
	}
	if b.Pi == nil || b.Pi.BinaryPath != "pi" || b.Pi.McpAdapterPath != "/opt/adapter" {
		t.Fatalf("Pi = %+v, want binaryPath=pi mcpAdapterPath=/opt/adapter", b.Pi)
	}
}

func TestRenderModelThinking(t *testing.T) {
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{
		Achagent: achv1alpha1.AgentDefaults{
			Image: "img",
			Ach:   &achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"},
			Model: &achv1alpha1.ModelSpec{
				Name: "openai.gpt-5", Type: "openai",
				Thinking: &achv1alpha1.ThinkingSpec{Enabled: true, Effort: "high"},
			},
		},
	}}
	a := achv1alpha1.ACHAgent{ObjectMeta: metav1.ObjectMeta{Name: "t"}}
	cfg, err := Render(p, a, "")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if cfg.Model.Thinking == nil || !cfg.Model.Thinking.Enabled || cfg.Model.Thinking.Effort != "high" {
		t.Fatalf("Model.Thinking = %+v, want enabled=true effort=high", cfg.Model.Thinking)
	}
}

func TestRenderModelThinkingAbsent(t *testing.T) {
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{
		Achagent: achv1alpha1.AgentDefaults{
			Image: "img",
			Ach:   &achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"},
			Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
		},
	}}
	a := achv1alpha1.ACHAgent{ObjectMeta: metav1.ObjectMeta{Name: "t"}}
	cfg, err := Render(p, a, "")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if cfg.Model.Thinking != nil {
		t.Fatalf("Model.Thinking = %+v, want nil (omitted from JSON)", cfg.Model.Thinking)
	}
}

func TestResolveImage(t *testing.T) {
	if got := ResolveImage("", "img:profile"); got != "img:profile" {
		t.Errorf("empty agent image must inherit profile, got %q", got)
	}
	if got := ResolveImage("img:agent", "img:profile"); got != "img:agent" {
		t.Errorf("agent image must win, got %q", got)
	}
	if got := ResolveImage("", ""); got != "" {
		t.Errorf("both empty must resolve empty, got %q", got)
	}
}

func TestResolveEngine(t *testing.T) {
	profile := &achv1alpha1.EngineSpec{
		Home: "/h", ForwardEnv: []string{"HTTPS_PROXY"},
		IdleTTLSeconds: ptr(int64(30)), StartupTimeoutSeconds: ptr(int64(600)), Type: "opencode",
	}
	t.Run("nil agent inherits profile", func(t *testing.T) {
		if got := ResolveEngine(nil, profile); got != profile {
			t.Errorf("ResolveEngine(nil, p) = %+v, want profile", got)
		}
	})
	t.Run("nil profile returns agent", func(t *testing.T) {
		agent := &achv1alpha1.EngineSpec{Type: "pi"}
		if got := ResolveEngine(agent, nil); got != agent {
			t.Errorf("ResolveEngine(a, nil) = %+v, want agent", got)
		}
	})
	t.Run("nil engine on both sides", func(t *testing.T) {
		if got := ResolveEngine(nil, nil); got != nil {
			t.Errorf("ResolveEngine(nil, nil) = %+v, want nil", got)
		}
	})
	t.Run("agent type pi inherits profile fields, pi block atomic", func(t *testing.T) {
		agent := &achv1alpha1.EngineSpec{Type: "pi", Pi: &achv1alpha1.PiEngineSpec{BinaryPath: "pi"}}
		got := ResolveEngine(agent, profile)
		if got.Type != "pi" || got.Pi == nil || got.Pi.BinaryPath != "pi" {
			t.Errorf("agent type/pi must win: %+v", got)
		}
		if got.Home != "/h" || len(got.ForwardEnv) != 1 || got.ForwardEnv[0] != "HTTPS_PROXY" {
			t.Errorf("unset agent fields must inherit profile: %+v", got)
		}
		if got.IdleTTLSeconds == nil || *got.IdleTTLSeconds != 30 || got.StartupTimeoutSeconds == nil || *got.StartupTimeoutSeconds != 600 {
			t.Errorf("pointer fields must inherit profile: %+v", got)
		}
	})
	t.Run("agent forwardEnv replaces atomically", func(t *testing.T) {
		agent := &achv1alpha1.EngineSpec{ForwardEnv: []string{"NO_PROXY"}}
		got := ResolveEngine(agent, profile)
		if len(got.ForwardEnv) != 1 || got.ForwardEnv[0] != "NO_PROXY" {
			t.Errorf("forwardEnv must replace as a whole: %v", got.ForwardEnv)
		}
	})
}

func TestResolveLimits_FieldMerge(t *testing.T) {
	profile := &achv1alpha1.LimitsSpec{MaxConcurrentInvocations: ptr(int64(8)), MaxSteps: ptr(int64(50))}
	agent := &achv1alpha1.LimitsSpec{MaxSteps: ptr(int64(10))}
	got := ResolveLimits(agent, profile)
	if got.MaxSteps == nil || *got.MaxSteps != 10 {
		t.Errorf("agent maxSteps must win: %+v", got)
	}
	if got.MaxConcurrentInvocations == nil || *got.MaxConcurrentInvocations != 8 {
		t.Errorf("unset agent field must inherit profile: %+v", got)
	}
	if got := ResolveLimits(nil, profile); got != profile {
		t.Errorf("nil agent must inherit profile")
	}
	if got := ResolveLimits(agent, nil); got != agent {
		t.Errorf("nil profile must return agent")
	}
}

func TestResolveModel_InheritsParams(t *testing.T) {
	profile := &achv1alpha1.ModelSpec{
		Name: "openai.gpt-5", Type: "openai",
		Params:   rawJSON(`{"temperature":1}`),
		Thinking: &achv1alpha1.ThinkingSpec{Enabled: true, Effort: "high"},
	}
	agent := &achv1alpha1.ModelSpec{Name: "gemini.pro", Type: "gemini"}
	got := ResolveModel(agent, profile)
	if got.Name != "gemini.pro" || got.Type != "gemini" {
		t.Errorf("agent name/type must win: %+v", got)
	}
	if got.Params == nil || string(got.Params.Raw) != `{"temperature":1}` {
		t.Errorf("omitted agent params must inherit profile params: %+v", got.Params)
	}
	if got.Thinking == nil || got.Thinking.Effort != "high" {
		t.Errorf("omitted agent thinking must inherit profile thinking: %+v", got.Thinking)
	}
	// Atomic replacement when the agent DOES set params.
	agent2 := &achv1alpha1.ModelSpec{Name: "m", Type: "openai", Params: rawJSON(`{"top_p":0.5}`)}
	if got := ResolveModel(agent2, profile); string(got.Params.Raw) != `{"top_p":0.5}` {
		t.Errorf("agent params must replace as a whole: %s", got.Params.Raw)
	}
}

func TestRender_NoImage_Errors(t *testing.T) {
	// Empty achagent (the live-migration shape of an old stored profile) → clear error.
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{Achagent: achv1alpha1.AgentDefaults{
		Ach: &achv1alpha1.AchEndpointSpec{BaseURL: "u"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
	}}}
	a := achv1alpha1.ACHAgent{ObjectMeta: metav1.ObjectMeta{Name: "a"}}
	if _, err := Render(p, a, ""); err == nil {
		t.Error("expected error when no image resolves")
	}
}

func TestRender_AgentEngineAndImageOverride(t *testing.T) {
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{Achagent: achv1alpha1.AgentDefaults{
		Image:  "img:profile",
		Ach:    &achv1alpha1.AchEndpointSpec{BaseURL: "u"},
		Model:  &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
		Engine: &achv1alpha1.EngineSpec{Type: "opencode", ForwardEnv: []string{"HTTPS_PROXY"}},
	}}}
	a := achv1alpha1.ACHAgent{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: achv1alpha1.ACHAgentSpec{
		ProfileRef:    achv1alpha1.LocalObjectRef{Name: "p"},
		Identity:      achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
		AgentDefaults: achv1alpha1.AgentDefaults{Image: "img:agent", Engine: &achv1alpha1.EngineSpec{Type: "pi", Pi: &achv1alpha1.PiEngineSpec{BinaryPath: "pi"}}},
		Channels:      []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
	}}
	cfg, err := Render(p, a, "")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if cfg.Engine == nil || cfg.Engine.Type != "pi" || cfg.Engine.Pi == nil || cfg.Engine.Pi.BinaryPath != "pi" {
		t.Errorf("agent engine type/pi must win: %+v", cfg.Engine)
	}
	if len(cfg.Engine.ForwardEnv) != 1 || cfg.Engine.ForwardEnv[0] != "HTTPS_PROXY" {
		t.Errorf("rendered engine must inherit profile forwardEnv: %v", cfg.Engine.ForwardEnv)
	}
}

func TestResolveCost(t *testing.T) {
	profile := &achv1alpha1.CostSpec{Source: "litellm_usage"}
	t.Run("nil agent inherits profile", func(t *testing.T) {
		if got := ResolveCost(nil, profile); got != profile {
			t.Errorf("ResolveCost(nil, p) = %+v, want profile", got)
		}
	})
	t.Run("nil profile returns agent", func(t *testing.T) {
		agent := &achv1alpha1.CostSpec{Source: "none"}
		if got := ResolveCost(agent, nil); got != agent {
			t.Errorf("ResolveCost(a, nil) = %+v, want agent", got)
		}
	})
	t.Run("nil cost on both sides", func(t *testing.T) {
		if got := ResolveCost(nil, nil); got != nil {
			t.Errorf("ResolveCost(nil, nil) = %+v, want nil", got)
		}
	})
	t.Run("agent block replaces profile atomically", func(t *testing.T) {
		agent := &achv1alpha1.CostSpec{Source: "litellm_headers"}
		if got := ResolveCost(agent, profile); got.Source != "litellm_headers" {
			t.Errorf("agent block must win wholly: %+v", got)
		}
	})
}

// Unset everywhere ⇒ no cost key at all, so an operator upgrade alone leaves every
// rendered config byte-identical and every agent config hash unchanged.
func TestRenderCost_OmitWhenUnset(t *testing.T) {
	tc := renderMatrix()["minimal"]
	cfg, err := Render(tc.profile, tc.agent, "")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if cfg.Cost != nil {
		t.Fatalf("cost block emitted with nothing set: %+v", cfg.Cost)
	}
	b, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), `"cost"`) {
		t.Fatalf("rendered config must not contain a cost key: %s", b)
	}
}

func TestRenderCost_AgentWinsProfileInherits(t *testing.T) {
	t.Run("profile only inherits", func(t *testing.T) {
		tc := renderMatrix()["minimal"]
		tc.profile.Spec.Achagent.Cost = &achv1alpha1.CostSpec{Source: "litellm_usage"}
		cfg, err := Render(tc.profile, tc.agent, "")
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if cfg.Cost == nil || cfg.Cost.Source != "litellm_usage" {
			t.Fatalf("omitted agent cost must inherit the profile's: %+v", cfg.Cost)
		}
	})
	t.Run("agent wins", func(t *testing.T) {
		tc := renderMatrix()["minimal"]
		tc.profile.Spec.Achagent.Cost = &achv1alpha1.CostSpec{Source: "litellm_usage"}
		tc.agent.Spec.Cost = &achv1alpha1.CostSpec{Source: "none"}
		cfg, err := Render(tc.profile, tc.agent, "")
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if cfg.Cost == nil || cfg.Cost.Source != "none" {
			t.Fatalf("agent cost must win: %+v", cfg.Cost)
		}
	})
}

func TestResolveEnv_AgentWinsByName(t *testing.T) {
	profile := []corev1.EnvVar{{Name: "PROFILE", Value: "p"}, {Name: "SHARED", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "profile-secret"}, Key: "value"}}}}
	agent := []corev1.EnvVar{{Name: "SHARED", Value: "agent"}, {Name: "AGENT", Value: "a"}}
	got := ResolveEnv(agent, profile)
	want := []corev1.EnvVar{{Name: "PROFILE", Value: "p"}, {Name: "SHARED", Value: "agent"}, {Name: "AGENT", Value: "a"}}
	if !slices.Equal(got, want) {
		t.Fatalf("ResolveEnv = %+v, want %+v", got, want)
	}
}

func TestPrepare_ForwardEnvResolvesLiteralsSecretsAndMissing(t *testing.T) {
	tc := renderMatrix()["minimal"]
	tc.profile.Spec.Env = []corev1.EnvVar{{Name: "GITLAB_BASE_URL", Value: "https://gitlab.example.com"}}
	tc.agent.Spec.Env = []corev1.EnvVar{{Name: "GITLAB_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "gl"}, Key: "token",
	}}}}
	tc.agent.Spec.Channels[0].Prepare = &achv1alpha1.PrepareSpec{
		Script: "true", ForwardEnv: []string{"GITLAB_BASE_URL", "GITLAB_TOKEN", "DOES_NOT_EXIST"},
	}

	cfg, err := Render(tc.profile, tc.agent, "")
	if err != nil {
		t.Fatal(err)
	}
	prep := cfg.Channels[0].Prepare
	if prep == nil || prep.Env["GITLAB_BASE_URL"] != "https://gitlab.example.com" {
		t.Fatalf("literal prepare env not resolved: %+v", prep)
	}
	if got := prep.SecretEnv["GITLAB_TOKEN"].Env; got != "ACH_SECRET_C_PREPARE_GITLAB_TOKEN" {
		t.Fatalf("secret prepare alias = %q", got)
	}
	if _, ok := prep.Env["DOES_NOT_EXIST"]; ok {
		t.Fatal("missing forwardEnv name must remain unset")
	}
	if _, ok := prep.SecretEnv["DOES_NOT_EXIST"]; ok {
		t.Fatal("missing forwardEnv name must not become secretEnv")
	}

	refs := ChannelSecretEnv(tc.profile, tc.agent)
	if len(refs) != 1 || refs[0].EnvName != "ACH_SECRET_C_PREPARE_GITLAB_TOKEN" || refs[0].SecretName != "gl" || refs[0].Key != "token" {
		t.Fatalf("prepare Pod secret alias = %+v", refs)
	}
	if got := ReferencedSecrets(tc.profile, tc.agent)["gl"]; len(got) != 1 || got[0] != "token" {
		t.Fatalf("referenced env secret = %v", ReferencedSecrets(tc.profile, tc.agent))
	}
}

func TestWebhookScript_RendersAuthScriptAndForwardEnv(t *testing.T) {
	tc := renderMatrix()["minimal"]
	tc.profile.Spec.Env = []corev1.EnvVar{{Name: "GITLAB_BASE_URL", Value: "https://git.example.com"}}
	tc.agent.Spec.Env = []corev1.EnvVar{{Name: "GITLAB_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "gitlab-api"}, Key: "token",
	}}}}
	tc.agent.Spec.Channels = []achv1alpha1.ChannelSpec{{
		Name: "gitlab-register", Type: "webhook-script", Source: "gitlab",
		Webhook: &achv1alpha1.WebhookSpec{
			Auth:         achv1alpha1.WebhookAuthSpec{Type: "gitlab_token", SecretRef: &achv1alpha1.SecretKeyRef{Name: "system-hook", Key: "secret"}},
			GitlabEvents: []string{"project_create", "push", "merge_request"},
		},
		Script: &achv1alpha1.PrepareSpec{
			Script: "payload=$(cat)", ForwardEnv: []string{"GITLAB_BASE_URL", "GITLAB_TOKEN", "MISSING"},
		},
	}}

	cfg, err := Render(tc.profile, tc.agent, "https://ach")
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Channels[0]
	if got.Webhook == nil || got.Script == nil || got.Script.Env["GITLAB_BASE_URL"] != "https://git.example.com" {
		t.Fatalf("webhook-script did not render: %+v", got)
	}
	if got.Script.SecretEnv["GITLAB_TOKEN"].Env != "ACH_SECRET_GITLAB_REGISTER_SCRIPT_GITLAB_TOKEN" {
		t.Fatalf("script secret alias = %#v", got.Script.SecretEnv)
	}
	if _, found := got.Script.Env["MISSING"]; found {
		t.Fatal("missing forwardEnv name must remain unset")
	}
	refs := ChannelSecretEnv(tc.profile, tc.agent)
	if len(refs) != 2 {
		t.Fatalf("webhook-script auth + script refs = %+v", refs)
	}
}

func TestCleanup_ForwardEnvResolvesLiteralsSecretsAndMissing(t *testing.T) {
	tc := renderMatrix()["minimal"]
	tc.profile.Spec.Env = []corev1.EnvVar{
		{Name: "GITLAB_BASE_URL", Value: "https://git.example.com"},
		{Name: "GITLAB_TOKEN", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "gitlab"},
				Key:                  "token",
			},
		}},
	}
	tc.agent.Spec.Channels[0].Prepare = &achv1alpha1.PrepareSpec{Script: "true"}
	tc.agent.Spec.Channels[0].Cleanup = &achv1alpha1.PrepareSpec{
		Script:     "rm -rf -- \"$ACH_WORKSPACE\"",
		ForwardEnv: []string{"GITLAB_BASE_URL", "GITLAB_TOKEN", "MISSING"},
	}

	cfg, err := Render(tc.profile, tc.agent, "https://ach")
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Channels[0].Cleanup
	if got == nil || got.Env["GITLAB_BASE_URL"] != "https://git.example.com" {
		t.Fatalf("cleanup literal env = %#v", got)
	}
	if got.SecretEnv["GITLAB_TOKEN"].Env != "ACH_SECRET_C_CLEANUP_GITLAB_TOKEN" {
		t.Fatalf("cleanup secret env = %#v", got.SecretEnv)
	}
	if _, found := got.Env["MISSING"]; found {
		t.Fatal("missing forwardEnv name must remain absent")
	}
	if _, found := got.SecretEnv["MISSING"]; found {
		t.Fatal("missing forwardEnv name must not become secretEnv")
	}
	refs := ChannelSecretEnv(tc.profile, tc.agent)
	if len(refs) != 1 || refs[0].EnvName != "ACH_SECRET_C_CLEANUP_GITLAB_TOKEN" ||
		refs[0].SecretName != "gitlab" || refs[0].Key != "token" {
		t.Fatalf("cleanup Pod secret alias = %+v", refs)
	}
}

func TestRender_RejectsCollidingChannelSecretAliases(t *testing.T) {
	secret := func(name, secret, key string) corev1.EnvVar {
		return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: key,
		}}}
	}
	tc := renderMatrix()["minimal"]
	tc.profile.Spec.Env = []corev1.EnvVar{
		secret("TOKEN_CLEANUP_X", "prepare-credential", "prepare-key"),
		secret("X", "cleanup-credential", "cleanup-key"),
	}
	tc.agent.Spec.Channels = []achv1alpha1.ChannelSpec{
		{
			Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"},
			Prepare: &achv1alpha1.PrepareSpec{Script: "true", ForwardEnv: []string{"TOKEN_CLEANUP_X"}},
		},
		{
			Name: "c-prepare-token", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"},
			Prepare: &achv1alpha1.PrepareSpec{Script: "true"},
			Cleanup: &achv1alpha1.PrepareSpec{Script: "true", ForwardEnv: []string{"X"}},
		},
	}

	_, err := Render(tc.profile, tc.agent, "https://ach")
	if err == nil || !strings.Contains(err.Error(), "duplicate generated channel secret env alias") {
		t.Fatalf("Render error = %v", err)
	}
	for _, secretData := range []string{"prepare-credential", "prepare-key", "cleanup-credential", "cleanup-key"} {
		if strings.Contains(err.Error(), secretData) {
			t.Fatalf("Render error leaked secret reference data: %v", err)
		}
	}
}

func TestPrepare_SecretAliasesDoNotCollapseEnvNameCase(t *testing.T) {
	secret := func(name, secret string) corev1.EnvVar {
		return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: "key"}}}
	}
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{Env: []corev1.EnvVar{secret("token", "lower"), secret("TOKEN", "upper")}}}
	a := achv1alpha1.ACHAgent{Spec: achv1alpha1.ACHAgentSpec{Channels: []achv1alpha1.ChannelSpec{{
		Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"},
		Prepare: &achv1alpha1.PrepareSpec{Script: "true", ForwardEnv: []string{"token", "TOKEN"}},
	}}}}
	refs := ChannelSecretEnv(p, a)
	if len(refs) != 2 || refs[0].EnvName == refs[1].EnvName {
		t.Fatalf("case-distinct env names collapsed to the same alias: %+v", refs)
	}
}

// TestPrepare_OnCronChannel: prepare is not tied to webhook — a scheduled agent may want a
// workspace too, so it must render outside the type switch.
func TestPrepare_OnCronChannel(t *testing.T) {
	ch := achv1alpha1.ChannelSpec{Name: "nightly", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "0 8 * * *"},
		Prepare: &achv1alpha1.PrepareSpec{Script: "true"}}
	if renderChannel(&ch, nil).Prepare == nil {
		t.Fatal("prepare must render for a cron channel")
	}
}
