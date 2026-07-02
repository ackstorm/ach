// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

const vendoredSchema = "testdata/agent-config-v1.schema.json"

// TestSchema_NoDrift fails if the vendored schema differs from ach-agent's copy.
// Skips when the sibling repo is absent (e.g. CI without ach-agent checked out).
func TestSchema_NoDrift(t *testing.T) {
	upstream := "../../../ach-agent/docs/schemas/agent-config-v1.schema.json"
	up, err := os.ReadFile(upstream)
	if err != nil {
		t.Skipf("upstream schema not present (%v); skipping drift check", err)
	}
	vend, err := os.ReadFile(vendoredSchema)
	if err != nil {
		t.Fatalf("read vendored schema: %v", err)
	}
	if sha256.Sum256(up) != sha256.Sum256(vend) {
		t.Fatalf("vendored schema drifted from %s — re-copy it", upstream)
	}
}

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	f, err := os.Open(vendoredSchema)
	if err != nil {
		t.Fatalf("open schema: %v", err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(vendoredSchema, doc); err != nil {
		t.Fatalf("add schema resource: %v", err)
	}
	s, err := c.Compile(vendoredSchema)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return s
}

type renderCase struct {
	profile achv1alpha1.AgentProfile
	agent   achv1alpha1.ACHAgent
}

// renderMatrix covers: minimal, every optional block, each channel type, each prompt.system
// variant, each memory variant (incl. bare codemem).
func renderMatrix() map[string]renderCase {
	base := func(name string, ch []achv1alpha1.ChannelSpec) renderCase {
		return renderCase{
			profile: achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{Image: "img", Ach: achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}}},
			agent: achv1alpha1.ACHAgent{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: achv1alpha1.ACHAgentSpec{
				ProfileRef: achv1alpha1.LocalObjectRef{Name: "p"},
				Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
				Capability: achv1alpha1.CapabilitySpec{Environment: "e"},
				Channels:   ch,
			}},
		}
	}
	cron := []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}}
	m := map[string]renderCase{
		"minimal": base("minimal", cron),
		"webhook": base("wh", []achv1alpha1.ChannelSpec{{Name: "w", Type: "webhook", Source: "gitlab", Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "gitlab_token", SecretRef: &achv1alpha1.SecretKeyRef{Name: "s", Key: "secret"}}}}}),
		"queue":   base("q", []achv1alpha1.ChannelSpec{{Name: "q", Type: "queue", Queue: &achv1alpha1.QueueSpec{Key: "k"}}}),
		"a2a":     base("a", []achv1alpha1.ChannelSpec{{Name: "a", Type: "a2a", A2A: &achv1alpha1.A2ASpec{Auth: achv1alpha1.A2AAuthSpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "s", Key: "k"}}}}}),
	}
	promptText := base("pt", cron)
	promptText.agent.Spec.Prompt = &achv1alpha1.AgentPromptSpec{System: achv1alpha1.PromptSystemSpec{Type: "text", Text: "hi"}}
	m["prompt-text"] = promptText
	promptFile := base("pf", cron)
	promptFile.agent.Spec.Prompt = &achv1alpha1.AgentPromptSpec{System: achv1alpha1.PromptSystemSpec{Type: "file", File: "prompts/x/y.md"}}
	m["prompt-file"] = promptFile
	promptAch := base("pa", cron)
	promptAch.agent.Spec.Prompt = &achv1alpha1.AgentPromptSpec{System: achv1alpha1.PromptSystemSpec{Type: "ach", Ach: "persona", AchFile: "main.md"}}
	m["prompt-ach"] = promptAch
	memH := base("mh", cron)
	memH.agent.Spec.Memory = &achv1alpha1.MemorySpec{Type: "hindsight", Hindsight: &achv1alpha1.HindsightSpec{Endpoint: "http://h"}}
	m["memory-hindsight"] = memH
	memC := base("mc", cron)
	memC.agent.Spec.Memory = &achv1alpha1.MemorySpec{Type: "codemem"}
	m["memory-codemem-bare"] = memC
	full := base("full", cron)
	full.profile.Spec.Engine = &achv1alpha1.EngineSpec{Home: "/h", ForwardEnv: []string{"HTTPS_PROXY"}}
	full.profile.Spec.Limits = &achv1alpha1.LimitsSpec{MaxSteps: ptr(int64(50))}
	full.profile.Spec.Health = &achv1alpha1.HealthSpec{Host: "0.0.0.0", Port: 8000}
	full.profile.Spec.Persistence = &achv1alpha1.PersistenceSpec{Enabled: true, MountPath: "/var/lib/ach-agent"}
	full.agent.Spec.Capability.Filter = &achv1alpha1.FilterSpec{Exclude: &achv1alpha1.ExcludeSpec{Skills: []string{"send-email"}}}
	m["full"] = full
	return m
}

func TestRender_ConformsToSchema(t *testing.T) {
	schema := compileSchema(t)
	for name, tc := range renderMatrix() {
		t.Run(name, func(t *testing.T) {
			cfg, err := Render(tc.profile, tc.agent)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			b, err := Marshal(cfg)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var v any
			if err := json.Unmarshal(b, &v); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := schema.Validate(v); err != nil {
				t.Fatalf("rendered config violates agent-config-v1:\n%v", err)
			}
		})
	}
}
