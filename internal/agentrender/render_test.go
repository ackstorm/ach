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
					Concurrency: ptr(int64(4)), Session: "auto", Prompt: "Review: {{ payload.object_attributes.url }}",
					Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "gitlab_token", SecretRef: &achv1alpha1.SecretKeyRef{Name: "gitlab-webhook", Key: "secret"}}},
				},
				{Name: "daily", Type: "cron", Concurrency: ptr(int64(1)), Session: "none", Prompt: "Scan for CVEs.", Cron: &achv1alpha1.CronSpec{Schedule: "0 8 * * 1-5", Timezone: "Europe/Madrid"}},
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
	if ch0auth["secretPath"] != "/etc/ach-agent/secrets/gitlab-webhook/secret" {
		t.Errorf("webhook secretPath = %v", ch0auth["secretPath"])
	}
	if _, leaked := ch0auth["secretRef"]; leaked {
		t.Errorf("secretRef leaked into rendered config")
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
