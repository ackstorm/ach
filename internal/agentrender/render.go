// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"encoding/json"
	"fmt"
	"sort"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
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

func secretPath(name, key string) string { return SecretMountRoot + "/" + name + "/" + key }

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
	return &EngineBlock{Home: e.Home, WorkDir: e.WorkDir, ForwardEnv: e.ForwardEnv, IdleTTLSeconds: e.IdleTTLSeconds, StartupTimeoutSeconds: e.StartupTimeoutSeconds, MaxToolCalls: e.MaxToolCalls}
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
			out.Hindsight = &HindsightBlock{Endpoint: m.Hindsight.Endpoint, Bank: m.Hindsight.Bank, MentalModels: m.Hindsight.MentalModels}
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

func renderHealth(h *achv1alpha1.HealthSpec) *HealthBlock {
	if h == nil {
		return nil
	}
	return &HealthBlock{Host: h.Host, Port: h.Port}
}

func renderChannel(ch *achv1alpha1.ChannelSpec) ChannelBlock {
	cb := ChannelBlock{Name: ch.Name, Type: ch.Type, Source: ch.Source, Concurrency: ch.Concurrency, Session: ch.Session, Prompt: ch.Prompt}
	switch ch.Type {
	case "webhook":
		if ch.Webhook != nil {
			w := &WebhookBlock{Auth: WebhookAuthBlock{Type: ch.Webhook.Auth.Type, Header: ch.Webhook.Auth.Header}, GitlabEvents: ch.Webhook.GitlabEvents}
			if ch.Webhook.Auth.SecretRef != nil {
				w.Auth.SecretPath = secretPath(ch.Webhook.Auth.SecretRef.Name, ch.Webhook.Auth.SecretRef.Key)
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
	case "a2a":
		if ch.A2A != nil {
			cb.A2A = &A2ABlock{Mode: "async", Auth: A2AAuthBlock{Header: ch.A2A.Auth.Header, SecretPath: secretPath(ch.A2A.Auth.SecretRef.Name, ch.A2A.Auth.SecretRef.Key)}}
		}
	}
	return cb
}

// ReferencedSecrets returns channel-secret NAME → sorted KEYS (for key-projected volume mounts
// and key-existence checks). The ek identity secret is injected as ACH_TOKEN via secretKeyRef —
// NOT included here.
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
		case "webhook":
			if ch.Webhook != nil && ch.Webhook.Auth.SecretRef != nil {
				add(ch.Webhook.Auth.SecretRef.Name, ch.Webhook.Auth.SecretRef.Key)
			}
		case "a2a":
			if ch.A2A != nil {
				add(ch.A2A.Auth.SecretRef.Name, ch.A2A.Auth.SecretRef.Key)
			}
		}
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
