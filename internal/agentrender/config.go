// SPDX-License-Identifier: Apache-2.0

// Package agentrender collapses an AgentProfile + ACHAgent into the single
// agent-config-v1 config the ach-agent harness self-boots from. Pure: no API
// calls, no side effects. Output struct JSON tags MUST match
// ../ach-agent/docs/schemas/agent-config-v1.schema.json (validated by schema_test.go).
package agentrender

// AgentConfig is the top-level rendered config (schema $id agent-config-v1).
type AgentConfig struct {
	SchemaVersion string          `json:"schemaVersion"`
	Agent         AgentBlock      `json:"agent"`
	Model         ModelBlock      `json:"model"`
	Capability    CapabilityBlock `json:"capability"`
	Engine        *EngineBlock    `json:"engine,omitempty"`
	Prompt        *PromptBlock    `json:"prompt,omitempty"`
	Memory        *MemoryBlock    `json:"memory,omitempty"`
	Limits        *LimitsBlock    `json:"limits,omitempty"`
	Persistence   *PersistBlock   `json:"persistence,omitempty"`
	Health        *HealthBlock    `json:"health,omitempty"`
	Channels      []ChannelBlock  `json:"channels,omitempty"`
}

type AgentBlock struct {
	Name string `json:"name"`
}

type ModelBlock struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Params map[string]any `json:"params,omitempty"`
}

type CapabilityBlock struct {
	Type   string       `json:"type"` // always "ach"
	Ach    AchBlock     `json:"ach"`
	Filter *FilterBlock `json:"filter,omitempty"`
}

type AchBlock struct {
	BaseURL     string `json:"baseUrl,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type FilterBlock struct {
	Exclude *ExcludeBlock `json:"exclude,omitempty"`
}

type ExcludeBlock struct {
	Tools      []string `json:"tools,omitempty"`
	McpServers []string `json:"mcpServers,omitempty"`
	Skills     []string `json:"skills,omitempty"`
}

type EngineBlock struct {
	Home                  string   `json:"home,omitempty"`
	WorkDir               string   `json:"workDir,omitempty"`
	ForwardEnv            []string `json:"forwardEnv,omitempty"`
	IdleTTLSeconds        *int64   `json:"idleTtlSeconds,omitempty"`
	StartupTimeoutSeconds *int64   `json:"startupTimeoutSeconds,omitempty"`
	MaxToolCalls          *int64   `json:"maxToolCalls,omitempty"`
}

type PromptBlock struct {
	System  PromptSystemBlock `json:"system"`
	Compose string            `json:"compose,omitempty"`
}

// PromptSystemBlock — the render sets ONLY the active variant's fields; SystemAch legally
// carries an optional `file` (subpath), so type=ach may emit both ach and file.
type PromptSystemBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	File string `json:"file,omitempty"`
	Ach  string `json:"ach,omitempty"`
}

type MemoryBlock struct {
	Type      string          `json:"type"`
	Hindsight *HindsightBlock `json:"hindsight,omitempty"`
	Codemem   *CodememBlock   `json:"codemem,omitempty"`
}

type HindsightBlock struct {
	Endpoint     string             `json:"endpoint"`
	Bank         string             `json:"bank,omitempty"`
	Auth         *SecretSourceBlock `json:"auth,omitempty"`
	Mission      string             `json:"mission,omitempty"`
	MentalModels []MentalModelBlock `json:"mentalModels,omitempty"`
}

// MentalModelBlock is one rendered memory.hindsight.mentalModels[] entry.
type MentalModelBlock struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SourceQuery string `json:"sourceQuery"`
	AutoRefresh bool   `json:"autoRefresh,omitempty"`
	MaxTokens   *int64 `json:"maxTokens,omitempty"`
}

type CodememBlock struct {
	DBPath  string `json:"dbPath,omitempty"`
	Project string `json:"project,omitempty"`
}

type LimitsBlock struct {
	MaxConcurrentInvocations *int64 `json:"maxConcurrentInvocations,omitempty"`
	MaxInvocationSeconds     *int64 `json:"maxInvocationSeconds,omitempty"`
	MaxQueuedTotal           *int64 `json:"maxQueuedTotal,omitempty"`
	IdempotencyWindowSeconds *int64 `json:"idempotencyWindowSeconds,omitempty"`
	MaxSteps                 *int64 `json:"maxSteps,omitempty"`
	TerminalOutputRetries    *int64 `json:"terminalOutputRetries,omitempty"`
}

type PersistBlock struct {
	Enabled   bool   `json:"enabled"`
	MountPath string `json:"mountPath,omitempty"`
}

type HealthBlock struct {
	Host string `json:"host,omitempty"`
	Port int32  `json:"port,omitempty"`
}

type ChannelBlock struct {
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Source      string        `json:"source,omitempty"`
	Concurrency *int64        `json:"concurrency,omitempty"`
	Session     *SessionBlock `json:"session,omitempty"`
	Prompt      string        `json:"prompt,omitempty"`
	Webhook     *WebhookBlock `json:"webhook,omitempty"`
	Cron        *CronBlock    `json:"cron,omitempty"`
	Queue       *QueueBlock   `json:"queue,omitempty"`
	A2A         *A2ABlock     `json:"a2a,omitempty"`
}

// SessionBlock is the rendered channels[].session (schema $defs/SessionBlock).
// Key is emitted only for type==custom (empty otherwise → omitempty drops it).
type SessionBlock struct {
	Type      string `json:"type,omitempty"`
	Key       string `json:"key,omitempty"`
	MaxTokens *int64 `json:"maxTokens,omitempty"`
	Overflow  string `json:"overflow,omitempty"`
}

type WebhookBlock struct {
	Auth         WebhookAuthBlock `json:"auth"`
	GitlabEvents []string         `json:"gitlabEvents,omitempty"`
}

type WebhookAuthBlock struct {
	Type   string             `json:"type"`
	Header string             `json:"header,omitempty"`
	Secret *SecretSourceBlock `json:"secret,omitempty"`
}

// SecretSourceBlock is an inbound-auth secret source (schema SecretSource). The
// operator only ever emits env: the value lives in the harness process env
// (unreadable by the same-uid agent under PR_SET_DUMPABLE=0), never on a
// same-uid-readable mounted file. The contract's file variant was dropped as the
// weaker path, so the operator has no file to render.
type SecretSourceBlock struct {
	Env string `json:"env,omitempty"`
}

type CronBlock struct {
	Schedule string `json:"schedule"`
	Timezone string `json:"timezone,omitempty"`
}

type QueueBlock struct {
	Type    string `json:"type"` // always "redis"
	Key     string `json:"key"`
	AckMode string `json:"ackMode"` // always "onComplete"
}

type A2ABlock struct {
	Mode string       `json:"mode"` // always "async"
	Auth A2AAuthBlock `json:"auth"`
}

type A2AAuthBlock struct {
	Header string             `json:"header,omitempty"`
	Secret *SecretSourceBlock `json:"secret,omitempty"`
}
