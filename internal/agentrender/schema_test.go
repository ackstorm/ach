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
	// Coordination guard (CONTRACT_v3 mcpServers addendum, 2026-07-07): the top-level
	// mcpServers union is being landed in ach-agent. Until upstream regenerates its
	// frozen schema with a top-level mcpServers property, the vendored copy is the
	// hand-authored pre-landing contract and will NOT byte-match. Skip the drift check
	// while upstream is pre-addendum; the skip falls away (and drift resumes, forcing a
	// re-vendor byte-swap) the moment upstream gains the property.
	if !hasTopLevelMcpServers(up) {
		t.Skip("upstream schema pre-mcpServers-addendum; vendored is hand-authored — byte-swap at coordination")
	}
	if sha256.Sum256(up) != sha256.Sum256(vend) {
		t.Fatalf("vendored schema drifted from %s — re-copy it", upstream)
	}
}

// hasTopLevelMcpServers reports whether the schema declares a top-level mcpServers
// property (i.e. the addendum has landed upstream). The pre-addendum schema only has
// mcpServers nested under capability.filter.exclude, so a substring check won't do.
func hasTopLevelMcpServers(schema []byte) bool {
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &doc); err != nil {
		return false
	}
	_, ok := doc.Properties["mcpServers"]
	return ok
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
	memH.agent.Spec.Memory = &achv1alpha1.MemorySpec{Type: "hindsight", Hindsight: &achv1alpha1.HindsightSpec{
		Endpoint: "http://h", Bank: "b", Mission: "reviewer",
		Auth: &achv1alpha1.SecretKeyRef{Name: "hs", Key: "token"},
		MentalModels: []achv1alpha1.MentalModelSpec{
			{ID: "arch", Name: "Arch", SourceQuery: "what arch?", AutoRefresh: true, MaxTokens: ptr(int64(2048))},
			{ID: "conv", Name: "Conv", SourceQuery: "what conventions?"},
		},
	}}
	m["memory-hindsight"] = memH
	memHmin := base("mhmin", cron)
	memHmin.agent.Spec.Memory = &achv1alpha1.MemorySpec{Type: "hindsight", Hindsight: &achv1alpha1.HindsightSpec{
		Endpoint: "http://h", MentalModels: []achv1alpha1.MentalModelSpec{{ID: "x", Name: "X", SourceQuery: "q?"}},
	}}
	m["memory-hindsight-minimal"] = memHmin
	memC := base("mc", cron)
	memC.agent.Spec.Memory = &achv1alpha1.MemorySpec{Type: "codemem"}
	m["memory-codemem-bare"] = memC
	full := base("full", cron)
	full.profile.Spec.Engine = &achv1alpha1.EngineSpec{
		Home: "/h", ForwardEnv: []string{"HTTPS_PROXY"}, Type: "pi",
		Pi: &achv1alpha1.PiEngineSpec{BinaryPath: "pi"},
	}
	full.profile.Spec.Model.Thinking = &achv1alpha1.ThinkingSpec{Enabled: true, Effort: "high"}
	full.profile.Spec.Limits = &achv1alpha1.LimitsSpec{MaxSteps: ptr(int64(50))}
	full.profile.Spec.Health = &achv1alpha1.HealthSpec{Host: "0.0.0.0", Port: 8000}
	full.profile.Spec.Persistence = &achv1alpha1.PersistenceSpec{Enabled: true, MountPath: "/var/lib/ach-agent"}
	full.agent.Spec.Capability.Filter = &achv1alpha1.FilterSpec{Exclude: &achv1alpha1.ExcludeSpec{Skills: []string{"send-email"}}}
	m["full"] = full
	sess := base("cs", []achv1alpha1.ChannelSpec{{
		Name: "cs", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"},
		Session: &achv1alpha1.SessionSpec{Type: "custom", Key: ptr("{{ payload.thread }}"), MaxTokens: ptr(int64(8000)), Overflow: "rotate"},
	}})
	m["session-custom"] = sess
	mcp := base("mcp", cron)
	mcp.agent.Spec.MCPServers = []achv1alpha1.McpServerSpec{
		{Name: "repo-checkout", Type: "repoCheckout", RepoCheckout: &achv1alpha1.RepoCheckoutSpec{
			SourceMcpServerID: "mcp-gitlab-ro", TmpBase: "/tmp/gitlab", TTLSeconds: ptr(int64(3600))}},
		{Name: "filesystem", Type: "local", Local: &achv1alpha1.LocalMcpSpec{
			Command: "docker", Args: []string{"run", "-i", "--rm", "mcp/filesystem", "/projects"}, Env: []string{"SOME_VAR"}}},
		{Name: "other", Type: "remote", Remote: &achv1alpha1.RemoteMcpSpec{
			URL: "https://mcp.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer ${env:OTHER_MCP_TOKEN}"}}},
	}
	m["mcp-servers"] = mcp
	return m
}

func TestRender_ConformsToSchema(t *testing.T) {
	schema := compileSchema(t)
	for name, tc := range renderMatrix() {
		t.Run(name, func(t *testing.T) {
			cfg, err := Render(tc.profile, tc.agent, "")
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
