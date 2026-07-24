# Plan — Per-agent FinOps from LiteLLM's authoritative spend

**Goal:** source per-agent (and per-model / per-capability) cost in Grafana from
LiteLLM's real spend, instead of the agent's opencode estimate
(`ach_agent_turn_cost_usd_total`, which reads `_usage.cost` from opencode and is
$0 for models opencode's catalog doesn't price).

## Why LiteLLM as the source of truth

- LiteLLM is the billing authority. Empirically it returns an exact
  `x-litellm-response-cost` header (e.g. `0.0005218`) on non-streaming calls,
  and always tracks spend server-side.
- The agent's cost via opencode is an independent estimate (different price
  table → drifts ~20% and reads 0 for unrecognized model aliases).
- Reading the header at the agent's model-proxy does **not** work as-is because
  opencode streams (SSE), and on streaming responses the cost header is `0.0`
  and the final usage chunk carries tokens but no cost. So the clean path is to
  attribute LiteLLM's own spend per agent.

## Ground truth (observed in prod, ns `litellm`)

- Two metric sources:
  - `litellm_spend_metric_total` (job `litellm`, proxy-native) — **rich labels**:
    `api_key_alias`, `hashed_api_key`, `team`, `team_alias`, `user`,
    `user_email`, `end_user`, `model`, `requested_model`, `api_provider`. **Use
    this one.**
  - `litellm_total_spend` (job `litellm/litellm-exporter`) — only `model`. Too
    coarse.
- **No per-agent dimension today**: agent traffic lands under `team=dream`,
  `user=juancarlos.moreno@ackstorm.com`, `api_key_alias=lk-…`. Cannot tell
  `classifier` from `zohodesk-joan`.
- `end_user` label exists but is `None` — it is the hook: if the request carries
  `user` / end-user, LiteLLM stamps `end_user` on the spend metric.
- Request path: `opencode → agent model-proxy (ach_agent/engine/mcp_proxy.py) →
  forwarder (/v1, /gemini — Go, already authenticates the ek and knows the
  agent) → litellm`. The **forwarder** is the natural, agent-aware injection
  point.

## Phase 0 — Decisions (before any change)

- **Identity mechanism** (pick one):
  - **A) `end_user` / `user` per request** (recommended, light): inject
    `user=<agent>` → `litellm_spend_metric_total{end_user="classifier"}`. No key
    provisioning.
  - **B) Per-agent virtual key** (`key_alias=<agent>`): stronger — enables
    per-agent budgets / rate-limits, but the operator must create/rotate a
    LiteLLM key per ACHAgent. More moving parts.
  - → Start with **A**; add **B** later only if per-agent budgets are needed.
- **Injection point** (pick one):
  - **Forwarder (Go, repo `ach`)** — recommended: already validates the `ek`,
    knows the agent, single place, no agent-image release.
  - Model-proxy (Python, repo `ach-agent`) — alternative; needs an agent image
    release.

## Phase 1 — Stamp agent identity toward LiteLLM

- In the forwarder, on the model routes (`/v1`, `/gemini`): resolve the agent
  from the `ek` and inject on the outbound request to LiteLLM:
  - Option A: set the OpenAI `user` body field = agent name, and/or the
    `x-litellm-end-user` header.
  - (Optional) add `metadata.tags: ["agent:<name>", "capability:<x>"]` for extra
    breakdowns.
- Confirm LiteLLM config emits `end_user` (and tags) on
  `litellm_spend_metric_total` — the label already exists, it just needs to be
  populated.
- Must not break either wire: openai-compat (`type: openai`) and gemini
  passthrough (`type: gemini`). Test both.

## Phase 2 — Validate attribution end-to-end

- Fire one agent invocation (webhook curl).
- Confirm `litellm_spend_metric_total{end_user="<agent>"}` increments with the
  real cost.
- Success: LiteLLM per-agent spend matches the bill (and is ≥ as trustworthy as
  opencode's `cost_usd`).

## Phase 3 — FinOps dashboard on LiteLLM spend (repo `ach`, `examples/grafana`)

- New `ach-finops-litellm.json` (or migrate the current `ach-finops`) querying
  `litellm_spend_metric_total`:
  - Cost $/h and range by **agent** (`end_user`), by **model** (`model`), by
    **team / capability**.
  - Top spenders, cost per request, split by `api_provider`
    (gemini / anthropic / bedrock / openrouter).
- Advantage: figures equal real billing and cover **all** models, not only the
  ones opencode prices.

## Phase 4 — What to do with the opencode cost path (decision)

- **Keep** `ach_agent_turn_cost_usd_total` as an in-agent estimate (useful but
  imprecise / 0 for some models), and label the panel "estimated (opencode)"
  vs "actual (litellm)".
- Or deprecate it if the LiteLLM view suffices. Not urgent — Phase 3 makes it
  non-critical.

## Risks / notes

- Forwarder injection must not break the openai-compat wire nor the gemini
  passthrough — test both `type: openai` and `type: gemini`.
- `end_user` cardinality is fine for 7 agents; revisit if it grows to hundreds.
- Independent of the per-agent model fix and of the `ek` identity incident —
  orthogonal.

## Minimal viable scope

**Phase 1 (forwarder, option A) + Phase 3.**
