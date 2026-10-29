// api-types.ts — TypeScript models of the CURRENT backend JSON shapes.
//
// Every type below mirrors what the FastAPI service actually emits today (field
// names + nullability), NOT a speculative or normalized shape. Each type points
// at its backend source so the contract stays verifiable. Do NOT add fields that
// the backend does not return (YAGNI) — if the backend gains a field, model it
// here in the same PR that ships it.

// ---------------------------------------------------------------------------
// GET /platform/console/bootstrap — internal/platformapi/console/handlers.go
// bootstrap. The first call the SPA makes; cookie-authenticated.
// ---------------------------------------------------------------------------

export interface SessionMe {
  email: string;
  /** IdP display name from the console session; the store falls back to email when empty. */
  name: string;
  /** Hosted chat UI (chart platformApi.console.chatUrl); "" hides the Chat button. */
  chat_url: string;
  /** OpenCode provider id users sign in with (chart genai.providerName). */
  provider_name: string;
  is_admin: boolean;
  /** Helm enterprise.enabled: false = Enterprise pages are marked "Enterprise only". */
  enterprise: boolean;
  openwork_enabled: boolean;
  /** UI notice bound for suspend propagation (spec §8.2). */
  suspend_propagation_seconds: number;
  /** Number of non-revoked ek_ keys held by the caller, or null if unavailable. */
  keys_used: number | null;
  /** Caller ceiling for non-revoked ek_ keys, or null if unavailable. */
  max_keys: number | null;
  /**
   * Gateway base URL for curl snippets / the A2A card. Not in the payload:
   * ACH serves the console and /v1 from ONE origin, so the store fills it
   * with window.location.origin.
   */
  endpoint: string;
  /**
   * The caller's own `user:<email>` budget tag (pk callers only); null when
   * the tag is absent or unreadable. Same figures as /stats budget.
   */
  budget?: {
    spend: number;
    max_budget: number | null;
    budget_duration: string | null;
    reset_at: string | null;
  } | null;
}

// ---------------------------------------------------------------------------
// GET /platform/keys — ACH's own key-lifecycle surface
// (internal/platformapi/envkeys + internal/platformapi/environments).
// Replaces the alitellm-auth /api/session/{keys,teams} surface: ACH has no
// team concept in the console — a key is scoped to an Environment.
// ---------------------------------------------------------------------------

/**
 * One row of GET /platform/keys (render.KeyListRow,
 * internal/platformapi/render/keylist.go). `status` is the EFFECTIVE state
 * (already resolved server-side — active/suspended/expired/invalid/revoked;
 * D-30), not something the UI derives. `environment`/`name`/`last_used_at`/
 * `revoked_at` carry Go `omitempty` on the wire: ABSENT (not null) when unset,
 * so they are typed optional here rather than `| null`. `expires_at` has no
 * `omitempty` — always present, `null` = perpetual (ek_-only per D-24).
 */
export interface KeyRow {
  key_id: string;
  type: 'pk' | 'ek';
  owner_email: string;
  environment?: string;
  name?: string;
  status: 'active' | 'suspended' | 'expired' | 'invalid' | 'revoked';
  /** Secondary facts behind `status` (e.g. "suspended", "no_access"); shown in a tooltip. */
  reasons?: string[];
  created_at: string;
  last_used_at?: string;
  revoked_at?: string;
  expires_at: string | null;
}

/** GET /platform/keys response — {items,next_cursor} per Hub §15.5. */
export interface KeysResponse {
  items: KeyRow[];
  next_cursor: string | null;
}

// ---------------------------------------------------------------------------
// POST /platform/keys — internal/platformapi/envkeys/handler.go CreateRequest/
// CreateResponse (§8.2 ek_ create flow).
// ---------------------------------------------------------------------------

/** POST /platform/keys request body. `environment` + `name` are REQUIRED (400
 * `invalid_argument` otherwise); `expires_at` is an optional future RFC3339
 * instant — omit for a perpetual key. */
export interface CreateKeyBody {
  environment: string;
  name: string;
  expires_at?: string;
}

/**
 * POST /platform/keys response — the only place the plaintext ek- secret is
 * returned, once, and never stored server-side.
 */
export interface CreateKeyResponse {
  key_id: string;
  plaintext: string;
  environment: string;
  name: string;
  owner_email: string;
  created_at: string;
  expires_at: string | null;
}

// ---------------------------------------------------------------------------
// GET /platform/environments — internal/platformapi/environments/handler.go
// ---------------------------------------------------------------------------

/**
 * One row of GET /platform/environments (store.EnvironmentView, trimmed to
 * the console's needs). `status` carries Go `omitempty` (deriveStatus can
 * return "" for a not-yet-reconciled Environment, which is then omitted from
 * the wire entirely) — only the literal `"Available"` is selectable when
 * creating a key.
 */
export interface EnvironmentRow {
  name: string;
  status?: string;
  description?: string;
  /**
   * The row's condition array (Go metav1.Condition), when the backend sends
   * one. The real key-mint gate is `AccessGroupSynced`, not the collapsed
   * `status` — see lib/env-status.ts. Absent on an older platform-api.
   */
  conditions?: Array<{ type: string; status: string; reason?: string; message?: string }>;
  /** spec.authorizedTeams (store.EnvironmentView); omitted when empty. */
  authorizedTeams?: string[];
  /** Declared runtime names (store.RowToView); each list omitted when empty. */
  runtime?: {
    models?: string[];
    mcpServers?: string[];
    a2aAgents?: string[];
    guardrails?: string[];
  };
  /** Declared context names (store.RowToView); each list omitted when empty. */
  context?: {
    plugins?: string[];
    skills?: string[];
    prompts?: string[];
    artifacts?: string[];
  };
}

/** GET /platform/environments response — {items,next_cursor} per Hub §15.5. */
export interface EnvironmentsResponse {
  items: EnvironmentRow[];
  next_cursor: string | null;
}

// ---------------------------------------------------------------------------
// GET /platform/console/stats — internal/platformapi/console/stats.go::stats
// (contract assembled by internal/observability.BuildStatsContract; field
// names/shape carried over verbatim from the imported alitellm-auth contract)
// ---------------------------------------------------------------------------

/** stats.py::build_stats_contract range_meta.compare sub-block. */
export interface StatsRangeCompare {
  start: string;
  end: string;
}

/** stats.py range_meta. {start, end, days, compare}. */
export interface StatsRange {
  start: string;
  end: string;
  days: number;
  compare: StatsRangeCompare;
}

/**
 * Period-over-period deltas. stats.py::compute_deltas. Each *_pct is null when
 * the prior-window denominator is 0/None (D-08 null-vs-0), else a ratio.
 */
export interface StatsDeltas {
  requests_pct: number | null;
  tokens_pct: number | null;
  spend_pct: number | null;
  avg_cost_per_1m_tokens_pct: number | null;
}

/** stats.py totals block. avg_cost_per_1m_tokens is null when tokens===0 (D-08). */
export interface StatsTotals {
  requests: number;
  tokens: number;
  spend: number;
  /** Total failed requests in the window (metadata.total_failed_requests). */
  failed_requests: number;
  /** Input (prompt) tokens in the window (metadata.total_prompt_tokens). */
  input_tokens: number;
  /** Output (completion) tokens in the window (metadata.total_completion_tokens). */
  output_tokens: number;
  /** Cache-read input tokens in the window. */
  cache_read_tokens: number;
  /** cache_read/prompt fraction; null when prompt_tokens===0 (D-08). */
  cache_hit_pct: number | null;
  avg_cost_per_1m_tokens: number | null;
  deltas: StatsDeltas;
}

/** One series point (one per in-window day). stats.py::aggregate_window. */
export interface StatsSeriesPoint {
  date: string | null;
  spend: number;
  requests: number;
  /** Per-day total tokens (prompt + completion). */
  tokens: number;
  /** Per-day input (prompt) tokens. */
  input_tokens: number;
  /** Per-day output (completion) tokens. */
  output_tokens: number;
  /** Per-day failed requests (metrics.failed_requests). */
  failed: number;
}

/**
 * Per-model breakdown row. stats.py::build_stats_contract.
 * spend_pct is null when total model spend is 0 (D-08); last_used is null when
 * no source data was available for that model.
 */
export interface StatsModelRow {
  model: string | null;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  /** Cache-read (cached input) tokens for this model in the window. */
  cache_read_tokens: number;
  spend: number;
  spend_pct: number | null;
  /** Spend in the comparison window: 0 if unused there, null if unreadable. */
  prev_spend?: number | null;
  last_used: string | null;
}

/**
 * Per-key (top-keys) breakdown row, ranked by spend desc. stats.py.
 * `id` is the LiteLLM key hash; key_alias may be null; spend_pct null when total
 * key spend is 0 (D-08).
 */
/**
 * GET /platform/console/latency (observability.LatencyContract): LiteLLM's
 * request log for the window, capped at the latest rows (`sampled`). A figure
 * no row carries is null; `available: false` means the log could not be read.
 */
export interface GatewayLatency {
  available: boolean;
  reason?: string;
  sampled: boolean;
  row_count: number;
  latency: { p50_ms: number | null; p95_ms: number | null; p99_ms: number | null; avg_ms: number | null; ttft_p50_ms: number | null; ttft_p95_ms: number | null; tokens_per_sec_p50: number | null } | null;
  outcomes: { status: string; count: number }[];
}

export interface StatsKeyRow {
  id: string | null;
  key_alias: string | null;
  requests: number;
  spend: number;
  spend_pct: number | null;
}

/**
 * Source of the spend figure. 'tag' is the caller's own LiteLLM budget tag
 * ("user:<email>") — the ceiling the forwarder actually enforces, covering
 * their pk_ and every ek_ they own. 'unknown' means no budget is configured
 * or the read failed; the panel shows no ceiling rather than a fake zero.
 */
export type SpendSource = 'tag' | 'unknown';

/**
 * Budget block. stats.py::build_stats_contract. max_budget null when none is
 * configured; pct null when max_budget is null/0 (D-08); has_budget mirrors that.
 */
export interface StatsBudget {
  /** This cycle's spend; null when it could not be read (never shown as $0.00). */
  current: number | null;
  max_budget: number | null;
  budget_duration: string | null;
  /** When LiteLLM next zeroes `current` (its budget_reset_at, ISO 8601); null without a ceiling. */
  reset_at: string | null;
  /**
   * The cycle's spend carried linearly to reset_at; shared by Overview and
   * Cost. Absent from platform-api releases before it shipped.
   */
  projection?: BudgetProjection;
  /**
   * The cycle's spend per UTC day, oldest first, from the same tag read as
   * `current` (sums to it). Absent when that read failed or from older releases.
   */
  cycle_series?: { date: string; spend: number }[];
  source: SpendSource;
  pct: number | null;
  has_budget: boolean;
}

/**
 * Linear projection of the budget cycle to reset_at (horizon_end). spend is
 * null with a reason: no_cycle (no resetting budget), too_early (under a day in)
 * or spend_unknown (the cycle's spend could not be read).
 */
export interface BudgetProjection {
  basis: 'linear_cycle_rate';
  cycle_start: string | null;
  horizon_end: string | null;
  spend: number | null;
  pct: number | null;
  reason: 'no_cycle' | 'too_early' | 'spend_unknown' | null;
}

/**
 * Backend capability flags. stats.py / session.py::_CAPABILITY_DEFAULTS.
 * per_model_last_used flips to false when no last-used data is available.
 */
export interface StatsCapabilities {
  token_split: boolean;
  per_model_last_used: boolean;
  deltas: boolean;
  per_key_spend: boolean;
}

/**
 * GET /platform/console/stats response. `data_scope` is ALWAYS "user" (D-13/
 * AC-16, console.withStatsScope) — the window folds in every ek_ the caller
 * owns, never Environment-scoped.
 */
export interface StatsResponse {
  range: StatsRange;
  totals: StatsTotals;
  series: StatsSeriesPoint[];
  models: StatsModelRow[];
  keys: StatsKeyRow[];
  budget: StatsBudget;
  capabilities: StatsCapabilities;
  data_scope: 'user';
}

// ---------------------------------------------------------------------------
// AppConfig — presentation config (compile-time DEFAULT_CONFIG in stores/config.ts; ACH has no /api/config)
// (defaults mirrored in src/ui/app.js DEFAULT_CONFIG)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// GET /platform/console/capabilities?scope=personal —
// internal/platformapi/console/handlers.go::personal. One fetch backs the
// Models/MCP/A2A pages (ui/src/hooks/use-capabilities.ts); `models` mirrors
// litellm.ModelGroupInfo verbatim (field names unchanged from the imported
// alitellm-auth contract), `mcp_servers`/`a2a_agents` mirror handlers.go's
// mcpServerRow/a2aAgentRow public projections.
// ---------------------------------------------------------------------------

/**
 * One public model-group row (litellm.ModelGroupInfo, internal/litellm/
 * userview.go). `mode` is e.g. "chat" | "embedding" | "rerank" | null.
 * Costs are per-token floats (often scientific notation); token caps are floats
 * or null (LiteLLM emits them as floats, e.g. 128000.0).
 */
export interface ModelRow {
  name: string | null;
  providers: string[];
  mode: string | null;
  max_input_tokens: number | null;
  max_output_tokens: number | null;
  input_cost_per_token: number | null;
  output_cost_per_token: number | null;
  supports_vision: boolean;
  supports_function_calling: boolean;
  supports_reasoning: boolean;
  supports_web_search: boolean;
}

/**
 * One configured MCP server (public projection, handlers.go::projectMCP).
 * `transport` is "sse" | "http" | "stdio"; `status` is "healthy" | "unhealthy"
 * | "unknown" | null; `auth_type` is the type LABEL only (e.g. "oauth2"),
 * never a secret. `tool_count` mirrors tools.length (LiteLLM has no numeric
 * count field).
 */
export interface McpServerRow {
  id: string | null;
  name: string | null;
  description: string | null;
  url: string | null;
  transport: string | null;
  auth_type: string | null;
  status: string | null;
  tools: string[];
  tool_count: number;
  access_groups: string[];
}

/**
 * One configured A2A (Agent-to-Agent) agent (public projection, handlers.go::
 * projectA2A). Display fields come from the agent card: `transport` is the
 * preferred transport, `version` the card version, `skills` the agent's skill
 * names (`skill_count` mirrors length), `streaming` the card's streaming
 * capability. No secret/header field is surfaced.
 */
export interface A2aAgentRow {
  id: string | null;
  name: string | null;
  description: string | null;
  url: string | null;
  transport: string | null;
  version: string | null;
  skills: string[];
  skill_count: number;
  streaming: boolean;
}

/**
 * GET /platform/console/capabilities?scope=personal response
 * (handlers.go::personal). `provisioning` is true when the caller's
 * ach-user-<email> shell team has not yet been attached to any access group
 * (a deny-all sentinel alone in the raw model list, or an empty one — LiteLLM
 * filters the `no-default-models` sentinel out) — the Models page
 * shows a calm "being provisioned" card instead of an empty catalog; MCP/A2A
 * use it the same way when their own list is also empty.
 */
export interface CapabilitiesResponse {
  scope: 'personal';
  models: ModelRow[];
  mcp_servers: McpServerRow[];
  a2a_agents: A2aAgentRow[];
  provisioning: boolean;
}

/** One BACKED-BY provider chip. public.py::_PROVIDERS. */
export interface ConfigProvider {
  label: string;
}

/**
 * Public, non-secret presentation config.
 * public.py::public_config. `links` only carries keys whose target is set
 * (real-links-only rule, D-02/D-03), so every link key is optional.
 */
export interface AppConfig {
  brand: string;
  brand_short: string;
  tagline: string;
  accent_segment: string;
  public_host: string;
  providers: ConfigProvider[];
  links: Record<string, string>;
}

// ---------------------------------------------------------------------------
// GET /platform/observability/traces[/{trace_id}] —
// ee/observability/evidence/traces.go (TracePage / TraceDetail).
// ---------------------------------------------------------------------------

export interface EvidenceProvenance {
  state: string;
  reason: string | null;
  sampled: boolean;
  observed_at?: string | null;
  observed_start?: string | null;
  observed_end?: string | null;
  formula?: string;
}

export interface TraceSummary {
  trace_id: string;
  project_id: string;
  started_at: string | null;
  ended_at: string | null;
  span_count: number;
  status: 'error' | 'success' | 'unknown';
  kind?: 'llm' | 'mcp' | null;
  request_id?: string | null;
  model?: string | null;
  duration_ms?: number;
  input_tokens?: number | null;
  output_tokens?: number | null;
  cost?: number | null;
  key_alias?: string | null;
  /** The generation's session; null for sessionless calls. Absent before #52. */
  session_id?: string | null;
}

// GET /platform/observability/sessions[/{session_id}] — the caller's sessions
// (its generations grouped by session_id); a turn is one trace.
export interface SessionSummary {
  session_id: string;
  project_id: string;
  started_at: string;
  last_activity_at: string;
  duration_ms: number;
  turns: number;
  generations: number;
  input_tokens: number | null;
  output_tokens: number | null;
  cost: number | null;
  /** Up to 3 distinct models, most used first. */
  models: string[];
  status: 'error' | 'success' | 'unknown';
}

export interface SessionPage {
  window: { start: string; end: string; timezone: string };
  sessions: SessionSummary[];
  next_cursor: number | null;
  total_count: number | null;
  /** The caller's traces in the window without a session (null if the count failed). */
  sessionless_traces: number | null;
  provenance: EvidenceProvenance;
}

export interface SessionTurn {
  trace_id: string;
  started_at: string;
  ended_at?: string | null;
  model: string | null;
  /** Tools the model asked for in this turn, in order (first 20). */
  tools?: string[];
  input_tokens: number | null;
  output_tokens: number | null;
  cost: number | null;
  duration_ms: number;
  status: 'error' | 'success' | 'unknown';
}

export interface SessionDetail extends Omit<SessionSummary, 'models'> {
  classification: TraceClassification;
  /** Ordered by started_at ascending, capped like trace spans. */
  turns_list: SessionTurn[];
  turns_truncated: boolean;
  provenance: EvidenceProvenance;
}

export interface TraceSpan {
  span_id: string;
  parent_span_id: string | null;
  type: 'SPAN' | 'GENERATION' | 'TOOL' | string;
  name: string;
  start_time: string | null;
  end_time: string | null;
  status: 'error' | 'unknown';
  model: string | null;
  request_id?: string | null;
}

export interface TracesResponse {
  total_count?: number | null;
  window: { start: string; end: string; timezone: string };
  traces: TraceSummary[];
  next_cursor: number | null;
  provenance: EvidenceProvenance;
}

export interface TraceDetailResponse {
  trace_id: string;
  request_id?: string | null;
  status?: 'error' | 'success' | 'unknown';
  model?: string | null;
  cost?: number | null;
  input_tokens?: number | null;
  output_tokens?: number | null;
  project_id: string;
  spans: TraceSpan[];
  provenance: EvidenceProvenance;
  /** The work category of the trace's session. Absent before it shipped. */
  classification?: TraceClassification;
}

/**
 * TraceDetailResponse.classification (evidence.TraceClassification): the
 * classifier's label for the trace's session. Only `classified` carries a
 * category; `no_session` = the trace has no session id; `unavailable` = the
 * scores read failed (the detail still answers).
 */
export interface TraceClassification {
  session_id: string | null;
  category: string | null;
  domain: string | null;
  confidence: number | null;
  state: 'classified' | 'not_classified' | 'no_session' | 'unavailable';
}

/**
 * One row of GET /platform/observability/workloads `runtimes`
 * (evidence.RuntimeUsage): generations per client runtime with their summed
 * input/output tokens and recorded cost. Zero-cost (subscription) traffic is
 * cost 0. Optional fields: absent from releases before they shipped.
 */
export interface WorkloadRuntime {
  name: string;
  count: number;
  input_tokens?: number;
  output_tokens?: number;
  cost?: number;
  /** Over the generations that carry each figure; null when none does. */
  median_input?: number | null;
  cache_read_share?: number | null;
  /** Recorded cost over priced (cost > 0) generations. */
  cost_per_generation?: number | null;
}

/**
 * GET /platform/observability/classification (evidence.ClassificationReport):
 * the caller's sessions in the window by the classifier's work category.
 * coverage is null (and provenance carries the reason) when the source is
 * unavailable; sessions = 0 with labelled = 0 means nothing classified yet.
 */
export interface ClassificationReport {
  window: { start: string; end: string; timezone: string };
  provenance: EvidenceProvenance;
  taxonomy: 'v3';
  /** Labelled categories only, ranked by sessions then id. */
  categories: ClassificationCategory[];
  coverage: ClassificationCoverage | null;
}

export interface ClassificationCategory {
  /** Taxonomy id, e.g. 'technology.genai_development', 'other', 'trivial'. */
  category: string;
  /** Its prefix, e.g. 'technology'. */
  domain: string;
  sessions: number;
  generations: number;
  input_tokens: number;
  output_tokens: number;
  cost: number;
}

export interface ClassificationCoverage {
  /** Sessions with a session id in the window, labelled or not. */
  sessions: number;
  labelled: number;
  /** labelled / sessions; null with no sessions. */
  share: number | null;
  /** Generations with no session id: never classifiable, outside both counts. */
  sessionless_generations: number;
}
