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
