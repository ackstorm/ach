# ACH — Proposed metrics & label improvements

Grounded in a live audit of the `pro-ack-ai-platform` cluster (namespace `ach`,
7 agents). Each item lists the gap observed, the proposal, and why it matters.
Ordered by value/effort.

> Naming note: this document uses the **bare** metric names currently emitted in
> production (pre-`ach_` rename, commit `37c4234`). After that rename ships, the
> control-plane names gain the `ach_` prefix; the agent metrics (`ach_agent_*`,
> `router_*`, `channel_*`, `engine_*`, `memory_degraded_*`) are emitted by the
> Python agent image and are **not** covered by the Go rename.

## 0. `agent` series label — DONE (shipped in this change)

**Gap:** agent metrics carried only the hashed `pod` label (e.g.
`achagent-classifier-7fc457cfb9-8bskq`), so dashboards could not group or filter
by agent name.

**Done:** the `PodMonitor` now relabels the operator-set pod label
`ach.ackstorm.ai/agent` into an `agent` series label
(`deploy/helm/ach/templates/podmonitor.yaml`). All `ach_agent_*` series now
carry `agent="classifier"` etc.

**Follow-up (optional, more robust):** have the agent app emit `agent` natively
as a metric label instead of relying on scrape-time relabeling — survives any
change to pod labelling.

## 1. `environment` + `capability` labels — enables real capability control

**Gap:** the whole point of the "capabilities" view is missing. Every ACHAgent
in prod has `spec.capability.environment` and `spec.capability.name` **unset**
(`<none>`), and neither reaches the pod labels or the metrics. There is no way
to slice cost/traffic/errors by capability or environment today.

**Proposal (either path — the app path is preferred):**
- **App-native (preferred):** the agent container already receives the
  `ACH_ENVIRONMENT` env var (`achagent_workload.go` sets it from
  `spec.capability.environment`). Have the agent emit `environment` (and
  `capability`) as a label on every `ach_agent_*` metric, straight from that env
  var. Grouping/filtering by environment then works with zero relabel config.
- **Scrape-time:** operator copies `capability.name` / `capability.environment`
  onto pod labels, and the PodMonitor relabels them into series labels (same
  one-line mechanism as `agent`).

Either way, the values are only meaningful once agents actually **set**
`spec.capability.environment` (all prod agents currently leave it empty).

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

**Gap:** we have `engine_launch_failures_total`, `engine_watchdog_kills_total`,
`engine_drain_completed_total` (events) but **no gauge** of concurrent in-flight
invocations or queued work. Backpressure is only visible after it rejects
(`router_backpressure_rejects_total`).

**Proposal:** `ach_agent_inflight_invocations` (gauge) and
`ach_agent_queue_depth` (gauge), per agent.

**Why:** capacity planning and early backpressure warning before drops happen.

## 5. `platform_api_login_total` — referenced but never emitted

**Gap:** the original control-plane dashboard had a "Platform-API Login /s by
outcome" panel querying `platform_api_login_total`, which **does not exist** in
Prometheus (the panel was always empty; it has been removed from the rebuilt
dashboard).

**Proposal:** either add `platform_api_login_total{outcome="success|failure"}`
(SSO login observability is security-relevant) or leave the panel out. Currently
only `platform_api_hydrate_duration_seconds_*` is emitted.

## 6. FinOps: cost attribution labels + verify cost is captured

**Gap:** `ach_agent_turn_cost_usd_total` carries `agent`, `model`, `channel` —
good — but no `capability`/`environment`/`tenant`. Chargeback per capability or
tenant is not possible.

**Proposal:** add the labels from #1 to the cost counter (they propagate for free
once the environment/capability labels exist).

**⚠ Under investigation:** agent-reported cost (`ach_agent_turn_cost_usd_total`)
reads ~$0 while LiteLLM records real spend (`litellm_spend_metric_total`,
`litellm_total_spend`) for the same calls. The agent's cost accounting appears to
under-report — tracked separately from this dashboard work.

---

## Dashboard provisioning (ops recommendation)

These dashboards live as JSON examples and were imported into Grafana by hand.
To make them GitOps-managed, provision them via a ConfigMap carrying the
`grafana_dashboard: "1"` label (the kube-prometheus-stack Grafana sidecar
auto-loads them). This can be added to the chart behind a
`metrics.dashboards.enabled` value so the dashboards ship and update with the
release instead of drifting from the repo.
