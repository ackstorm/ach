// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
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
// defaults (model, limits, ach.baseUrl, health). Errors only on structurally impossible
// states (defense in depth behind admission CEL).
func Render(p achv1alpha1.AgentProfile, a achv1alpha1.ACHAgent, defaultBaseURL string) (AgentConfig, error) {
	model := ResolveModel(a.Spec.Model, p.Spec.Achagent.Model)
	if model == nil {
		return AgentConfig{}, fmt.Errorf("no model: set ACHAgent.spec.model or AgentProfile.spec.achagent.model")
	}
	if ResolveImage(a.Spec.Image, p.Spec.Achagent.Image) == "" {
		return AgentConfig{}, fmt.Errorf("no image: set ACHAgent.spec.image or AgentProfile.spec.achagent.image")
	}
	baseURL := ResolveAchBaseURL(a.Spec.Ach, p.Spec.Achagent.Ach, defaultBaseURL)
	if baseURL == "" {
		return AgentConfig{}, fmt.Errorf("no ACH base URL: set ACHAgent.spec.ach.baseUrl, AgentProfile.spec.achagent.ach.baseUrl, or operator ACH_BASE_URL")
	}
	params, err := decodeParams(model.Params)
	if err != nil {
		return AgentConfig{}, fmt.Errorf("model.params: %w", err)
	}
	var thinking *ThinkingBlock
	if model.Thinking != nil {
		thinking = &ThinkingBlock{Enabled: model.Thinking.Enabled, Effort: model.Thinking.Effort}
	}

	cfg := AgentConfig{
		SchemaVersion: "1",
		Agent:         AgentBlock{Name: a.Name},
		Model:         ModelBlock{Name: model.Name, Type: model.Type, Params: params, Thinking: thinking},
		Capability: CapabilityBlock{
			Type:   "ach",
			Ach:    AchBlock{BaseURL: baseURL, Environment: a.Spec.Capability.Environment},
			Filter: renderFilter(a.Spec.Capability.Filter),
		},
		Engine:      renderEngine(ResolveEngine(a.Spec.Engine, p.Spec.Achagent.Engine)),
		Prompt:      renderPrompt(a.Spec.Prompt),
		Memory:      renderMemory(a.Spec.Memory),
		Limits:      renderLimits(ResolveLimits(a.Spec.Limits, p.Spec.Achagent.Limits)),
		Persistence: renderPersistence(p.Spec.Persistence),
		Health:      renderHealth(a.Spec.Health, p.Spec.Achagent.Health),
		Cost:        renderCost(ResolveCost(a.Spec.Cost, p.Spec.Achagent.Cost)),
	}
	for i := range a.Spec.Channels {
		cfg.Channels = append(cfg.Channels, renderChannel(&a.Spec.Channels[i]))
	}
	cfg.McpServers = renderMcpServers(a.Spec.MCPServers)
	return cfg, nil
}

// renderMcpServers turns the spec.mcpServers[] list into the config map keyed by name.
// repoCheckout params pass through; local.env is sanitized (ACH_*/ek_ stripped, same
// rule as engine.forwardEnv); remote.headers pass through verbatim as ${env:NAME} refs.
func renderMcpServers(servers []achv1alpha1.McpServerSpec) map[string]McpServerBlock {
	if len(servers) == 0 {
		return nil
	}
	out := make(map[string]McpServerBlock, len(servers))
	for i := range servers {
		s := &servers[i]
		b := McpServerBlock{Type: s.Type}
		switch s.Type {
		case "repoCheckout":
			if s.RepoCheckout != nil {
				b.RepoCheckout = &RepoCheckoutParamsBlock{
					SourceMcpServerID: s.RepoCheckout.SourceMcpServerID,
					TmpBase:           s.RepoCheckout.TmpBase,
					TTLSeconds:        s.RepoCheckout.TTLSeconds,
				}
			}
		case "local":
			if s.Local != nil {
				b.Command = s.Local.Command
				b.Args = s.Local.Args
				b.Env = sanitizeForwardEnv(s.Local.Env)
			}
		case "remote":
			if s.Remote != nil {
				b.URL = s.Remote.URL
				b.Headers = s.Remote.Headers
			}
		}
		out[s.Name] = b
	}
	return out
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

// prepareSecretEnvName is the deterministic env var name carrying one
// channels[].prepare.secretEnv entry: ACH_SECRET_<CHANNEL>_PREPARE_<VAR>. Collision-free
// vs ACH_SECRET_<CHANNEL>_<TYPE>, whose last segment is always a channel type enum
// (WEBHOOK/CRON/QUEUE/A2A) and so can never be PREPARE_<something>. In the ACH_SECRET_
// namespace so sanitizeForwardEnv strips it from engine.forwardEnv for free.
func prepareSecretEnvName(ch *achv1alpha1.ChannelSpec, varName string) string {
	return "ACH_SECRET_" + sanitizeEnvSegment(ch.Name) + "_PREPARE_" + sanitizeEnvSegment(varName)
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
		// prepare.secretEnv — the OUTBOUND credentials (clone/push). Valid on every channel
		// type, so it sits outside the switch. Sorted: map order is random in Go, and an
		// unstable env list would rewrite the PodSpec on every reconcile.
		if ch.Prepare != nil {
			for _, v := range slices.Sorted(maps.Keys(ch.Prepare.SecretEnv)) {
				ref := ch.Prepare.SecretEnv[v]
				out = append(out, ChannelSecretEnvRef{EnvName: prepareSecretEnvName(ch, v), SecretName: ref.Name, Key: ref.Key})
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
	b := &EngineBlock{
		Home: e.Home, WorkDir: e.WorkDir, ForwardEnv: sanitizeForwardEnv(e.ForwardEnv),
		IdleTTLSeconds: e.IdleTTLSeconds, StartupTimeoutSeconds: e.StartupTimeoutSeconds,
		MaxToolCalls: e.MaxToolCalls, Type: e.Type,
	}
	if e.Pi != nil {
		b.Pi = &PiBlock{BinaryPath: e.Pi.BinaryPath, McpAdapterPath: e.Pi.McpAdapterPath}
	}
	return b
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

func renderLimits(l *achv1alpha1.LimitsSpec) *LimitsBlock {
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
// operator probes (no reliance on the harness default). Resolution is shared with
// the controller's probe/Service port via ResolveHealth, so config and probes agree
// by construction.
func renderHealth(agent, profile *achv1alpha1.HealthSpec) *HealthBlock {
	host, port := ResolveHealth(agent, profile)
	return &HealthBlock{Host: host, Port: port}
}

// renderCost emits the block ONLY when the resolved source is non-empty, so an operator
// upgrade alone changes no rendered config and no config hash.
func renderCost(c *achv1alpha1.CostSpec) *CostBlock {
	if c == nil || c.Source == "" {
		return nil
	}
	return &CostBlock{Source: c.Source}
}

// ResolveImage is the per-agent image resolution: agent wins when set, else the
// profile's spec.achagent.image. Empty result blocks the agent (Render errors).
func ResolveImage(agent, profile string) string {
	if agent != "" {
		return agent
	}
	return profile
}

// ResolveModel deep-merges the agent model over the profile model per field.
// Params and Thinking are atomic: present on the agent → replace as a whole;
// omitted → inherit the profile's (deliberate: an agent that changes name/type
// but omits params inherits profile params). Result may alias profile memory —
// read-only.
func ResolveModel(agent, profile *achv1alpha1.ModelSpec) *achv1alpha1.ModelSpec {
	if agent == nil {
		return profile
	}
	if profile == nil {
		return agent
	}
	out := *profile
	if agent.Name != "" {
		out.Name = agent.Name
	}
	if agent.Type != "" {
		out.Type = agent.Type
	}
	if agent.Params != nil {
		out.Params = agent.Params
	}
	if agent.Thinking != nil {
		out.Thinking = agent.Thinking
	}
	return &out
}

// ResolveEngine deep-merges the agent engine over the profile engine per field.
// ForwardEnv and Pi are atomic (replace as a whole when present on the agent).
// Result may alias profile memory — read-only.
func ResolveEngine(agent, profile *achv1alpha1.EngineSpec) *achv1alpha1.EngineSpec {
	if agent == nil {
		return profile
	}
	if profile == nil {
		return agent
	}
	out := *profile
	if agent.Home != "" {
		out.Home = agent.Home
	}
	if agent.WorkDir != "" {
		out.WorkDir = agent.WorkDir
	}
	if agent.ForwardEnv != nil {
		out.ForwardEnv = agent.ForwardEnv
	}
	if agent.IdleTTLSeconds != nil {
		out.IdleTTLSeconds = agent.IdleTTLSeconds
	}
	if agent.StartupTimeoutSeconds != nil {
		out.StartupTimeoutSeconds = agent.StartupTimeoutSeconds
	}
	if agent.MaxToolCalls != nil {
		out.MaxToolCalls = agent.MaxToolCalls
	}
	if agent.Type != "" {
		out.Type = agent.Type
	}
	if agent.Pi != nil {
		out.Pi = agent.Pi
	}
	return &out
}

// ResolveLimits deep-merges the agent limits over the profile limits per field.
// Result may alias profile memory — read-only.
func ResolveLimits(agent, profile *achv1alpha1.LimitsSpec) *achv1alpha1.LimitsSpec {
	if agent == nil {
		return profile
	}
	if profile == nil {
		return agent
	}
	out := *profile
	if agent.MaxConcurrentInvocations != nil {
		out.MaxConcurrentInvocations = agent.MaxConcurrentInvocations
	}
	if agent.MaxInvocationSeconds != nil {
		out.MaxInvocationSeconds = agent.MaxInvocationSeconds
	}
	if agent.MaxQueuedTotal != nil {
		out.MaxQueuedTotal = agent.MaxQueuedTotal
	}
	if agent.IdempotencyWindowSeconds != nil {
		out.IdempotencyWindowSeconds = agent.IdempotencyWindowSeconds
	}
	if agent.MaxSteps != nil {
		out.MaxSteps = agent.MaxSteps
	}
	if agent.TerminalOutputRetries != nil {
		out.TerminalOutputRetries = agent.TerminalOutputRetries
	}
	return &out
}

// ResolveHealth is the SINGLE health host/port resolution: per-field deep merge
// (agent field wins, else profile, else DefaultHealthHost/Port). The controller
// MUST use this for the Service targetPort and container probes so they never
// drift from the rendered config health block.
func ResolveHealth(agent, profile *achv1alpha1.HealthSpec) (host string, port int32) {
	host, port = DefaultHealthHost, DefaultHealthPort
	if profile != nil {
		if profile.Host != "" {
			host = profile.Host
		}
		if profile.Port != 0 {
			port = profile.Port
		}
	}
	if agent != nil {
		if agent.Host != "" {
			host = agent.Host
		}
		if agent.Port != 0 {
			port = agent.Port
		}
	}
	return host, port
}

// ResolveCost resolves the cost block. ATOMIC BLOCK: a block present on the agent replaces
// the profile's wholly, matching the "nested blocks are atomic" rule on AgentDefaults. With
// a single field this is indistinguishable from a per-field merge; ADDING A SECOND FIELD TO
// CostSpec REQUIRES CONVERTING THIS TO A PER-FIELD MERGE AND UPDATING THE DOCS.
func ResolveCost(agent, profile *achv1alpha1.CostSpec) *achv1alpha1.CostSpec {
	if agent != nil {
		return agent
	}
	return profile
}

// ResolveAchBaseURL is the SINGLE ACH base-URL resolution: ACHAgent.spec.ach ??
// AgentProfile.spec.achagent.ach ?? operator default (ACH_BASE_URL). Empty
// result => the agent has no ACH to hydrate against and Render blocks it. Used
// for both the config capability.ach.baseUrl and the container ACH_BASE_URL env.
func ResolveAchBaseURL(agentAch, profileAch *achv1alpha1.AchEndpointSpec, envDefault string) string {
	if agentAch != nil && agentAch.BaseURL != "" {
		return agentAch.BaseURL
	}
	if profileAch != nil && profileAch.BaseURL != "" {
		return profileAch.BaseURL
	}
	return envDefault
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

// renderPrepare emits channels[].prepare. secretEnv becomes {VAR: {env: NAME}} — the NAME
// the operator injects via secretKeyRef in the PodSpec; the credential value itself never
// touches the ConfigMap.
func renderPrepare(ch *achv1alpha1.ChannelSpec) *PrepareBlock {
	if ch.Prepare == nil {
		return nil
	}
	pb := &PrepareBlock{Script: ch.Prepare.Script, Env: ch.Prepare.Env, TimeoutSeconds: ch.Prepare.TimeoutSeconds}
	if len(ch.Prepare.SecretEnv) > 0 {
		pb.SecretEnv = make(map[string]SecretSourceBlock, len(ch.Prepare.SecretEnv))
		for v := range ch.Prepare.SecretEnv {
			pb.SecretEnv[v] = SecretSourceBlock{Env: prepareSecretEnvName(ch, v)}
		}
	}
	return pb
}

func renderChannel(ch *achv1alpha1.ChannelSpec) ChannelBlock {
	cb := ChannelBlock{Name: ch.Name, Type: ch.Type, Source: ch.Source, Concurrency: ch.Concurrency, Session: renderSession(ch.Session), Prompt: ch.Prompt, Prepare: renderPrepare(ch)}
	switch ch.Type {
	case channelTypeWebhook:
		if ch.Webhook != nil {
			w := &WebhookBlock{Auth: WebhookAuthBlock{Type: ch.Webhook.Auth.Type, Header: ch.Webhook.Auth.Header}, GitlabEvents: ch.Webhook.GitlabEvents, BotUsername: ch.Webhook.BotUsername, TriggerUsers: ch.Webhook.TriggerUsers}
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
		if ch.Prepare != nil {
			for _, ref := range ch.Prepare.SecretEnv {
				add(ref.Name, ref.Key)
			}
		}
	}
	if ref := MemorySecretEnv(a); ref != nil {
		add(ref.SecretName, ref.Key)
	}
	out := make(map[string][]string, len(set))
	for name, keys := range set {
		out[name] = slices.Sorted(maps.Keys(keys))
	}
	return out
}
