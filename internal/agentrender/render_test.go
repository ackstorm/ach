// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"encoding/json"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func ptr[T any](v T) *T                      { return &v }
func rawJSON(s string) *apiextensionsv1.JSON { return &apiextensionsv1.JSON{Raw: []byte(s)} }

func TestRender_FullGolden(t *testing.T) {
	profile := achv1alpha1.AgentProfile{
		Spec: achv1alpha1.AgentProfileSpec{
			Image: "ghcr.io/ackstorm/ach-agent:latest",
			Ach:   achv1alpha1.AchEndpointSpec{BaseURL: "https://ach.ackstorm.ai"},
			Model: &achv1alpha1.ModelSpec{Name: "openai.gpt-5", Type: "openai"},
			Engine: &achv1alpha1.EngineSpec{
				Home: "/var/lib/ach-agent/home", ForwardEnv: []string{"HTTPS_PROXY"},
				IdleTTLSeconds: ptr(int64(30)), StartupTimeoutSeconds: ptr(int64(30)),
			},
			Limits:      &achv1alpha1.LimitsSpec{MaxConcurrentInvocations: ptr(int64(8)), MaxSteps: ptr(int64(50))},
			Health:      &achv1alpha1.HealthSpec{Host: "0.0.0.0", Port: 8000},
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
			Model:  &achv1alpha1.ModelSpec{Name: "openai.gpt-5", Type: "openai", Params: rawJSON(`{"temperature":1}`)},
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

	cfg, err := Render(profile, agent)
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
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{Image: "x", Ach: achv1alpha1.AchEndpointSpec{BaseURL: "u"}, Model: &achv1alpha1.ModelSpec{Name: "profile-model", Type: "openai"}}}
	a := achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "a"},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "p"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "e"},
			Model:      &achv1alpha1.ModelSpec{Name: "agent-model", Type: "gemini"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	}
	cfg, err := Render(p, a)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Name != "agent-model" || cfg.Model.Type != "gemini" {
		t.Errorf("agent model must override profile: %+v", cfg.Model)
	}
}

func TestRender_NoModel_Errors(t *testing.T) {
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{Image: "x", Ach: achv1alpha1.AchEndpointSpec{BaseURL: "u"}}}
	a := achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "a"},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "p"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "e"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	}
	if _, err := Render(p, a); err == nil {
		t.Error("expected error when no model set")
	}
}

func TestReferencedSecrets_NameToKeys(t *testing.T) {
	a := achv1alpha1.ACHAgent{Spec: achv1alpha1.ACHAgentSpec{Channels: []achv1alpha1.ChannelSpec{
		{Name: "w1", Type: "webhook", Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "hmac", SecretRef: &achv1alpha1.SecretKeyRef{Name: "s1", Key: "kb"}}}},
		{Name: "w2", Type: "webhook", Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "hmac", SecretRef: &achv1alpha1.SecretKeyRef{Name: "s1", Key: "ka"}}}},
		{Name: "cr", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}},
	}}}
	got := ReferencedSecrets(a)
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
	got := ChannelSecretEnv(a)
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
	if keys := ReferencedSecrets(withAuth)["hs"]; len(keys) != 1 || keys[0] != "token" {
		t.Errorf("ReferencedSecrets missing memory secret: %v", ReferencedSecrets(withAuth))
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
		Image: "x", Ach: achv1alpha1.AchEndpointSpec{BaseURL: "u"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
		Engine: &achv1alpha1.EngineSpec{ForwardEnv: []string{"HTTPS_PROXY", "ACH_SECRET_X_WEBHOOK", "ACH_TOKEN"}},
	}}
	a := achv1alpha1.ACHAgent{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: achv1alpha1.ACHAgentSpec{
		ProfileRef: achv1alpha1.LocalObjectRef{Name: "p"},
		Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
		Capability: achv1alpha1.CapabilitySpec{Environment: "e"},
		Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
	}}
	cfg, err := Render(p, a)
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
	cb := renderChannel(&achv1alpha1.ChannelSpec{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}})
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
	})
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
	})
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
