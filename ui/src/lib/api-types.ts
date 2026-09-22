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
  /** No display name in ACH's identity — mirrors email (set by the store). */
  name: string;
  is_admin: boolean;
  openwork_enabled: boolean;
  /** UI notice bound for suspend propagation (spec §8.2). */
  suspend_propagation_seconds: number;
  /**
   * Gateway base URL for curl snippets / the A2A card. Not in the payload:
   * ACH serves the console and /v1 from ONE origin, so the store fills it
   * with window.location.origin.
   */
  endpoint: string;
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
  last_used: string | null;
}

/**
 * Per-key (top-keys) breakdown row, ranked by spend desc. stats.py.
 * `id` is the LiteLLM key hash; key_alias may be null; spend_pct null when total
 * key spend is 0 (D-08).
 */
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
  current: number;
  max_budget: number | null;
  budget_duration: string | null;
  source: SpendSource;
  pct: number | null;
  has_budget: boolean;
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
// GET /platform/console/latency — internal/platformapi/console/stats.go::latency
// (contract assembled by internal/observability.ComputeLatencyContract from
//  raw LiteLLM /spend/logs/v2 rows over the caller's selected range; ONLY
//  computed metrics are returned — never the raw rows)
// ---------------------------------------------------------------------------

/** The fixed latency window echoed back. latency.py window_meta. */
export interface LatencyWindow {
  start: string;
  end: string;
  days: number;
}

/**
 * Latency figures. Every field is null when no row carries the datum (D-08):
 * an all-error window has no durations; a non-streaming window has no TTFT.
 * `*_ms` are milliseconds; tokens_per_sec_p50 is output tokens / wall-second.
 */
export interface LatencyMetrics {
  p50_ms: number | null;
  p95_ms: number | null;
  p99_ms: number | null;
  avg_ms: number | null;
  ttft_p50_ms: number | null;
  ttft_p95_ms: number | null;
  tokens_per_sec_p50: number | null;
}

/** One request-outcome bucket (LiteLLM `status`: "success" | "failure" | …). */
export interface LatencyOutcome {
  status: string;
  count: number;
}

/** Per-model latency + error split, ranked by request count desc (top 8). */
export interface LatencyByModel {
  model: string;
  requests: number;
  failed: number;
  p50_ms: number | null;
  p95_ms: number | null;
}

/**
 * GET /platform/console/latency response (console.latency + withLatencyScope).
 * `available` is false (calm degrade, still a 200) when the /spend/logs/v2
 * fetch 401s (role=unknown) or fails for any other reason — the panel shows
 * a "not available" state. When true but the window is empty, the latency
 * figures are null and outcomes/by_model are empty. `data_scope` is ALWAYS
 * "user" (D-13/AC-16), same as StatsResponse.
 */
export interface LatencyResponse {
  available: boolean;
  /** Why it degraded, when available is false ("unavailable" | "fetch_failed"). */
  reason?: string;
  /** True when the row cap truncated the sample (figures are a sample, not exhaustive). */
  sampled: boolean;
  row_count: number;
  window: LatencyWindow | null;
  latency: LatencyMetrics | null;
  outcomes: LatencyOutcome[];
  by_model: LatencyByModel[];
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
 * (the `__deny_all__` sentinel alone in the raw model list) — the Models page
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
  // Explicit hosted-chat URL. Empty string => the SPA derives chat.<domain> from
  // the gateway host (deriveSubdomainUrl over me.endpoint).
  chat_public_url: string;
  providers: ConfigProvider[];
  links: Record<string, string>;
}
