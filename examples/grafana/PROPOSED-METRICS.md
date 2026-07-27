# ACH — Proposed metrics & label improvements

Grounded in a live audit of the `pro-ack-ai-platform` cluster (namespace `ach`,
7 agents). Each item lists the gap observed, the proposal, and why it matters.
Ordered by value/effort.

> Naming note: every metric family now carries a prefix — control-plane metrics
> use `ach_` (Go rename `37c4234`) and the Python agent families use `ach_agent_`
> (ach-agent `112256f`, shipped in v0.10.0), **including** the router, channel,
> engine, and memory metrics. Bare names (`forwarder_*`, `router_*`,
> `memory_degraded_total`, …) no longer exist in Prometheus; a dashboard still
> querying them renders "No data", which on a fail-open counter reads as a false
> green.

## 0. `agent` series label — DONE (ach-agent v0.10.1, app-native)

**Gap (was):** agent metrics carried only the hashed `pod` label (e.g.
`achagent-classifier-7fc457cfb9-8bskq`), so dashboards could not group or filter
by agent name.

**Tried and reverted:** the chart's `PodMonitor` relabelled the operator-set pod
label `ach.ackstorm.ai/agent` into an `agent` series label; that block was removed
from `deploy/helm/ach/templates/podmonitor.yaml`.

**Shipped:** the harness stamps identity at exposition time —
`src/ach_agent/http/metrics.py` (`IdentityRegistry`) adds `agent` **and**
`environment` to every sample of every `ach_agent_*` family, so the labels
survive any change to pod labelling. Verified in prod 2026-07-27:
`ach_agent_sessions_total{agent="zohodesk-joan",environment="zohodesk",…}`.

## 1. `environment` label — DONE; `capability` — OPEN

**Shipped (`environment`):** the same `IdentityRegistry` from §0 stamps
`environment` on every `ach_agent_*` series, straight from the `ACH_ENVIRONMENT`
env var the operator injects (`achagent_workload.go`). No relabel config needed.

**Still open (`capability`), and still worth little today:** no `capability`
label is emitted, and every prod ACHAgent leaves `spec.capability.name` unset,
so even a shipped label would be empty. Add it app-native (same registry) when
agents actually set the field.

**Dashboards do not use it yet:** the six JSONs here expose only `$namespace`
(+ `$agent` in the detail one). Adding an `$environment` template variable and
threading `environment=~"$environment"` through every expr is the follow-up that
cashes in this label.

**Why:** unlocks per-capability and per-environment (dev/stage/prod) dashboards,
alerts, and FinOps chargeback — the "control de capabilities" the dashboards are
meant to provide. This is the direct answer to "should we tag agent metrics with
environment so we can group by env?" — yes, via `ACH_ENVIRONMENT`.

## 2. `outcome` label on `ach_agent_sessions_total` — success/error rate

**Gap:** sessions have no success/failure dimension, so agent **error rate** and
SLOs cannot be computed. Labels today: `channel`, `model`, `agent`.

**Proposal:** add `outcome="success|error|timeout|refused"`.

**Why:** the single most important reliability signal for an agent — "what % of
invocations succeed". Panels are trivial once the label exists.

## 3. Tool-call failure status — `ach_agent_tool_calls_total`

**Gap:** the only observed `status` value is `completed`. Tool failures are
invisible. The Tools dashboard already queries `status!="completed"` and shows a
flat 0 — it will light up the moment failures are emitted.

**Proposal:** emit `status="failed|error|timeout|denied"` on tool-call
completion. Optionally an `error_kind` label for the top failure reasons.

**Why:** MCP/tool reliability is a leading indicator of agent quality; today a
failing MCP server is silent in metrics.

## 4. Engine in-flight / queue-depth gauge

**Gap:** we have `ach_agent_engine_launch_failures_total`,
`ach_agent_engine_watchdog_kills_total`, `ach_agent_engine_drain_completed_total`
(events) but **no gauge** of concurrent in-flight invocations or queued work.
Backpressure is only visible after it rejects
(`ach_agent_router_backpressure_rejects_total`).

**Proposal:** `ach_agent_inflight_invocations` (gauge) and
`ach_agent_queue_depth` (gauge), per agent.

**Why:** capacity planning and early backpressure warning before drops happen.

## 5. `ach_platform_api_login_total` — referenced but never emitted

**Gap:** the original control-plane dashboard had a "Platform-API Login /s by
outcome" panel querying `platform_api_login_total`, which **does not exist** in
Prometheus (the panel was always empty; it has been removed from the rebuilt
dashboard).

**Proposal:** either add `ach_platform_api_login_total{outcome="success|failure"}`
(SSO login observability is security-relevant) or leave the panel out. Currently
only `ach_platform_api_hydrate_duration_seconds_*` is emitted.

## 6. FinOps: cost attribution labels + verify cost is captured

**Gap:** `ach_agent_turn_cost_usd_total` carries `agent`, `model`, `channel` —
good — but no `capability`/`environment`/`tenant`. Chargeback per capability or
tenant is not possible.

**Proposal:** add the labels from #1 to the cost counter (they propagate for free
once the environment/capability labels exist).

**✅ Resolved (2026-07-27) — the ~$0 cost was a configuration gap, not a bug.**
`cost.source` (ACH v0.6.23 CRD field, harness ≥ v0.10.0) selects where the
figure comes from: `engine` (default, the engine's own price table), `litellm_usage`
(harness prices the per-response usage against `GET /v2/model/info`),
`litellm_headers` (reads `x-litellm-response-cost`), or `none`. Measured on
`zohodesk-joan`:

- `litellm_usage` matches LiteLLM's own billing **exactly** — 3 identical calls
  gave an `x-litellm-key-spend` delta of `0.0012131` each, and the local math
  (`4002 × 3e-07 + 5 × 2.5e-06`) lands on the same `0.0012131`.
- It only prices when `spec.model.name` is the **namespaced LiteLLM deployment
  name** (`gemini.gemini-flash-latest`). The bare `gemini-flash-latest` routes
  fine but `?model=` returns no catalog entry → `no_entry` → unpriced turns.
- `litellm_headers` is a constant 0 on the `/gemini` passthrough: LiteLLM emits
  the cost headers on the `/v1` router path only. Gemini-wire agents must use
  `litellm_usage`.

**⚠ Cost and token counters do not share a basis** under `litellm_usage`: the
harness replaces only the `cost` field of the engine-reported usage, so
`ach_agent_turn_tokens_total` still carries the engine's numbers while the cost
accumulates over every upstream call of the turn (observed: 9 822 input tokens
on the metric vs a cost implying ~161 k billable input tokens). Do **not** build
$/token panels from these two families.

**⚠ `increase()` misses a counter's first sample:** a fresh
`ach_agent_turn_cost_usd_total` series appears already carrying the first turn's
cost, and Prometheus treats that first observation as the baseline — the very
first invocation after a pod roll reads as `0` in the range panels.

## 7. Duration histogram buckets are too small — every high quantile saturates

**Gap:** `ach_agent_turn_duration_seconds` and `ach_agent_tool_duration_seconds`
are declared without `buckets=`, so they use the `prometheus_client` defaults —
`.005 … 10, +Inf`. Agents run with `limits.maxInvocationSeconds: 1800` and MCP
tool calls routinely pass 10 s, so everything above 10 s lands in `+Inf` and the
p90/p95/p99 panels read `10s`/`+Inf` regardless of the real latency.

**Proposal:** explicit buckets on both histograms, e.g.
`(0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600, 1800)`.

**Why:** every latency panel in `ach-agents`, `ach-agent-detail` and `ach-tools`
is currently unusable for anything slower than 10 s — which is the normal case.

## 8. `ach_agent_channel_inbound_events_total` counts a2a only

**Gap:** the counter is incremented in exactly one place —
`channels/a2a.py` — so webhook and cron events never reach it. The "Channel
inbound events /s" panel therefore undercounts to a2a traffic (and no series
exists at all in prod, where the traffic is webhook).

**Proposal:** increment it in every channel adapter at event ingress, keeping
the `type` label (`webhook|a2a|cron|…`).

---

## Dashboard provisioning — SHIPPED (chart `metrics.dashboards.enabled`)

`examples/grafana/` is the source of truth; `make helm-sync` copies the JSON
into `deploy/helm/ach/dashboards/` and `templates/grafana-dashboards.yaml`
renders one labelled ConfigMap per dashboard, which the kube-prometheus-stack
Grafana sidecar auto-loads. The pre-push gate (`make helm-sync-check`) fails on
drift between the two directories.

```yaml
metrics:
  dashboards:
    enabled: true       # default false
    label: grafana_dashboard   # must match the sidecar's LABEL
    labelValue: "1"
    folder: ACH         # needs sidecar.dashboards.folderAnnotation=grafana_folder
```

Dashboards loaded this way are provisioned, hence read-only in the UI — edits go
through this repo, which is the point.
