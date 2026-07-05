// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// Inbound channel types that carry an auth secret.
const (
	channelTypeWebhook = "webhook"
	channelTypeA2A     = "a2a"
)

// Health-server defaults. The operator OWNS the probe port: it always pins
// health.host/port in the rendered config so the harness binds exactly what the
// Deployment probes — never falling back to the harness's own default (which
// would silently drift). Kept in this package (the config-contract owner) so the
// controller's probe port and the config agree by construction.
const (
	DefaultHealthHost = "0.0.0.0"
	DefaultHealthPort = int32(8000)
)

// Render collapses profile + agent into an AgentConfig. Agent fields override profile
// defaults (model, limits). Errors only on structurally impossible states (defense in depth
// behind admission CEL).
func Render(p achv1alpha1.AgentProfile, a achv1alpha1.ACHAgent) (AgentConfig, error) {
	model := a.Spec.Model
	if model == nil {
		model = p.Spec.Model
	}
	if model == nil {
		return AgentConfig{}, fmt.Errorf("no model: set ACHAgent.spec.model or AgentProfile.spec.model")
	}
	params, err := decodeParams(model.Params)
	if err != nil {
		return AgentConfig{}, fmt.Errorf("model.params: %w", err)
	}

	cfg := AgentConfig{
		SchemaVersion: "1",
		Agent:         AgentBlock{Name: a.Name},
		Model:         ModelBlock{Name: model.Name, Type: model.Type, Params: params},
		Capability: CapabilityBlock{
			Type:   "ach",
			Ach:    AchBlock{BaseURL: p.Spec.Ach.BaseURL, Environment: a.Spec.Capability.Environment},
			Filter: renderFilter(a.Spec.Capability.Filter),
		},
		Engine:      renderEngine(p.Spec.Engine),
		Prompt:      renderPrompt(a.Spec.Prompt),
		Memory:      renderMemory(a.Spec.Memory),
		Limits:      renderLimits(a.Spec.Limits, p.Spec.Limits),
		Persistence: renderPersistence(p.Spec.Persistence),
		Health:      renderHealth(p.Spec.Health),
	}
	for i := range a.Spec.Channels {
		cfg.Channels = append(cfg.Channels, renderChannel(&a.Spec.Channels[i]))
	}
	return cfg, nil
}

// Marshal serializes an AgentConfig (Go struct field order is stable).
func Marshal(cfg AgentConfig) ([]byte, error) { return json.Marshal(cfg) }

// memoryHindsightSecretEnvName is the fixed env var carrying the hindsight admin-auth
// secret. In the ACH_SECRET_ namespace so sanitizeForwardEnv strips it from
// engine.forwardEnv for free; collision-free vs ACH_SECRET_<CH>_<TYPE> (TYPE is never HINDSIGHT).
const memoryHindsightSecretEnvName = "ACH_SECRET_MEMORY_HINDSIGHT" // #nosec G101 -- env var NAME, not a credential value

// channelSecretEnvName is the deterministic env var name carrying a channel's
// inbound-auth secret (webhook/a2a). Named ACH_SECRET_<CHANNEL>_<TYPE> (upper-
// snake, non-alnum → _). The name is NOT sensitive (only the value is, and the
// agent can't read the harness env); it just has to be a valid C identifier,
// unique (channel name is unique via listMapKey), and never collide with the
// reserved ACH_* vars (the ACH_SECRET_ prefix guarantees that).
func channelSecretEnvName(ch *achv1alpha1.ChannelSpec) string {
	return "ACH_SECRET_" + sanitizeEnvSegment(ch.Name) + "_" + sanitizeEnvSegment(ch.Type)
}

func sanitizeEnvSegment(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// ChannelSecretEnvRef is one inbound-auth secret → container env var (secretKeyRef).
type ChannelSecretEnvRef struct {
	EnvName    string
	SecretName string
	Key        string
}

// ChannelSecretEnv returns the per-channel inbound-auth secrets to inject as env
// vars (webhook/a2a with a secretRef). The operator wires each via secretKeyRef;
// the rendered config's auth.secret.env matches EnvName. Env injection (not file
// mounts) so a same-uid agent cannot read the value.
func ChannelSecretEnv(a achv1alpha1.ACHAgent) []ChannelSecretEnvRef {
	var out []ChannelSecretEnvRef
	for i := range a.Spec.Channels {
		ch := &a.Spec.Channels[i]
		switch ch.Type {
		case channelTypeWebhook:
			if ch.Webhook != nil && ch.Webhook.Auth.SecretRef != nil {
				out = append(out, ChannelSecretEnvRef{EnvName: channelSecretEnvName(ch), SecretName: ch.Webhook.Auth.SecretRef.Name, Key: ch.Webhook.Auth.SecretRef.Key})
			}
		case channelTypeA2A:
			if ch.A2A != nil {
				out = append(out, ChannelSecretEnvRef{EnvName: channelSecretEnvName(ch), SecretName: ch.A2A.Auth.SecretRef.Name, Key: ch.A2A.Auth.SecretRef.Key})
			}
		}
	}
	return out
}

// MemorySecretEnv returns the hindsight admin-auth secret to inject via secretKeyRef,
// or nil when the agent has no memory auth. Same wiring as channel secrets (env, not file).
func MemorySecretEnv(a achv1alpha1.ACHAgent) *ChannelSecretEnvRef {
	m := a.Spec.Memory
	if m == nil || m.Type != "hindsight" || m.Hindsight == nil || m.Hindsight.Auth == nil {
		return nil
	}
	return &ChannelSecretEnvRef{EnvName: memoryHindsightSecretEnvName, SecretName: m.Hindsight.Auth.Name, Key: m.Hindsight.Auth.Key}
}

func decodeParams(raw *apiextensionsv1.JSON) (map[string]any, error) {
	if raw == nil || len(raw.Raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw.Raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func renderFilter(f *achv1alpha1.FilterSpec) *FilterBlock {
	if f == nil || f.Exclude == nil {
		return nil
	}
	e := f.Exclude
	if len(e.Tools) == 0 && len(e.McpServers) == 0 && len(e.Skills) == 0 {
		return nil
	}
	return &FilterBlock{Exclude: &ExcludeBlock{Tools: e.Tools, McpServers: e.McpServers, Skills: e.Skills}}
}

func renderEngine(e *achv1alpha1.EngineSpec) *EngineBlock {
	if e == nil {
		return nil
	}
	return &EngineBlock{Home: e.Home, WorkDir: e.WorkDir, ForwardEnv: sanitizeForwardEnv(e.ForwardEnv), IdleTTLSeconds: e.IdleTTLSeconds, StartupTimeoutSeconds: e.StartupTimeoutSeconds, MaxToolCalls: e.MaxToolCalls}
}

// sanitizeForwardEnv drops any ACH_*-named var from the harness→engine forward
// allowlist. The operator owns the ACH_* namespace, incl. the ACH_SECRET_*
// inbound-auth env vars; the harness hard-fails at boot if a secret env name
// appears in forwardEnv, so this keeps them out defensively.
func sanitizeForwardEnv(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, n := range in {
		if strings.HasPrefix(n, "ACH_") {
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func renderPrompt(p *achv1alpha1.AgentPromptSpec) *PromptBlock {
	if p == nil {
		return nil
	}
	sys := PromptSystemBlock{Type: p.System.Type}
	switch p.System.Type {
	case "text":
		sys.Text = p.System.Text
	case "file":
		sys.File = p.System.File
	case "ach":
		sys.Ach = p.System.Ach
		sys.File = p.System.AchFile // legal: SystemAch allows optional file subpath
	}
	return &PromptBlock{System: sys, Compose: p.Compose}
}

func renderMemory(m *achv1alpha1.MemorySpec) *MemoryBlock {
	if m == nil {
		return nil
	}
	out := &MemoryBlock{Type: m.Type}
	switch m.Type {
	case "hindsight":
		if m.Hindsight != nil {
			hb := &HindsightBlock{Endpoint: m.Hindsight.Endpoint, Bank: m.Hindsight.Bank, Mission: m.Hindsight.Mission}
			if m.Hindsight.Auth != nil {
				hb.Auth = &SecretSourceBlock{Env: memoryHindsightSecretEnvName}
			}
			for _, mm := range m.Hindsight.MentalModels {
				hb.MentalModels = append(hb.MentalModels, MentalModelBlock{
					ID: mm.ID, Name: mm.Name, SourceQuery: mm.SourceQuery,
					AutoRefresh: mm.AutoRefresh, MaxTokens: mm.MaxTokens,
				})
			}
			out.Hindsight = hb
		}
	case "codemem":
		// codemem block is optional per schema; {"type":"codemem"} is valid.
		if m.Codemem != nil && (m.Codemem.DBPath != "" || m.Codemem.Project != "") {
			out.Codemem = &CodememBlock{DBPath: m.Codemem.DBPath, Project: m.Codemem.Project}
		}
	}
	return out
}

func renderLimits(agent, profile *achv1alpha1.LimitsSpec) *LimitsBlock {
	l := agent
	if l == nil {
		l = profile
	}
	if l == nil {
		return nil
	}
	return &LimitsBlock{MaxConcurrentInvocations: l.MaxConcurrentInvocations, MaxInvocationSeconds: l.MaxInvocationSeconds, MaxQueuedTotal: l.MaxQueuedTotal, IdempotencyWindowSeconds: l.IdempotencyWindowSeconds, MaxSteps: l.MaxSteps, TerminalOutputRetries: l.TerminalOutputRetries}
}

func renderPersistence(p *achv1alpha1.PersistenceSpec) *PersistBlock {
	if p == nil {
		return nil
	}
	pb := &PersistBlock{Enabled: p.Enabled}
	if p.Enabled {
		pb.MountPath = p.MountPath
	}
	return pb
}

// renderHealth ALWAYS emits a health block so the config pins the port the
// operator probes (no reliance on the harness default). Profile fields override
// the defaults; a zero/unset host or port falls back to DefaultHealthHost/Port.
func renderHealth(h *achv1alpha1.HealthSpec) *HealthBlock {
	host, port := DefaultHealthHost, DefaultHealthPort
	if h != nil {
		if h.Host != "" {
			host = h.Host
		}
		if h.Port != 0 {
			port = h.Port
		}
	}
	return &HealthBlock{Host: host, Port: port}
}

func renderSession(s *achv1alpha1.SessionSpec) *SessionBlock {
	if s == nil {
		return nil
	}
	sb := &SessionBlock{Type: s.Type, MaxTokens: s.MaxTokens, Overflow: s.Overflow}
	if s.Key != nil {
		sb.Key = *s.Key
	}
	return sb
}

func renderChannel(ch *achv1alpha1.ChannelSpec) ChannelBlock {
	cb := ChannelBlock{Name: ch.Name, Type: ch.Type, Source: ch.Source, Concurrency: ch.Concurrency, Session: renderSession(ch.Session), Prompt: ch.Prompt}
	switch ch.Type {
	case channelTypeWebhook:
		if ch.Webhook != nil {
			w := &WebhookBlock{Auth: WebhookAuthBlock{Type: ch.Webhook.Auth.Type, Header: ch.Webhook.Auth.Header}, GitlabEvents: ch.Webhook.GitlabEvents}
			if ch.Webhook.Auth.SecretRef != nil {
				w.Auth.Secret = &SecretSourceBlock{Env: channelSecretEnvName(ch)}
			}
			cb.Webhook = w
		}
	case "cron":
		if ch.Cron != nil {
			cb.Cron = &CronBlock{Schedule: ch.Cron.Schedule, Timezone: ch.Cron.Timezone}
		}
	case "queue":
		if ch.Queue != nil {
			cb.Queue = &QueueBlock{Type: "redis", Key: ch.Queue.Key, AckMode: "onComplete"}
		}
	case channelTypeA2A:
		if ch.A2A != nil {
			cb.A2A = &A2ABlock{Mode: "async", Auth: A2AAuthBlock{Header: ch.A2A.Auth.Header, Secret: &SecretSourceBlock{Env: channelSecretEnvName(ch)}}}
		}
	}
	return cb
}

// ReferencedSecrets returns channel-secret NAME → sorted KEYS (for the reconciler's
// key-existence check + the salted secret-content hash). The ek identity secret is injected as
// ACH_TOKEN via secretKeyRef — NOT included here.
func ReferencedSecrets(a achv1alpha1.ACHAgent) map[string][]string {
	set := map[string]map[string]struct{}{}
	add := func(name, key string) {
		if set[name] == nil {
			set[name] = map[string]struct{}{}
		}
		set[name][key] = struct{}{}
	}
	for i := range a.Spec.Channels {
		ch := &a.Spec.Channels[i]
		switch ch.Type {
		case channelTypeWebhook:
			if ch.Webhook != nil && ch.Webhook.Auth.SecretRef != nil {
				add(ch.Webhook.Auth.SecretRef.Name, ch.Webhook.Auth.SecretRef.Key)
			}
		case channelTypeA2A:
			if ch.A2A != nil {
				add(ch.A2A.Auth.SecretRef.Name, ch.A2A.Auth.SecretRef.Key)
			}
		}
	}
	if ref := MemorySecretEnv(a); ref != nil {
		add(ref.SecretName, ref.Key)
	}
	out := make(map[string][]string, len(set))
	for name, keys := range set {
		ks := make([]string, 0, len(keys))
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		out[name] = ks
	}
	return out
}
