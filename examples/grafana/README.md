# Grafana dashboards for ACH

Six importable dashboards for the ACH observability surface. All share a
`datasource` + `namespace` template variable; the agent-scoped ones add an
`agent` variable.

| File | uid | Covers |
|------|-----|--------|
| `ach-agents.json` | `ach-agents` | Fleet overview: agents up, sessions/turns/tokens/cost, latency by model, tool calls, channel events, reliability counters (router drops, engine failures, degraded) — grouped **by agent** |
| `ach-agent-detail.json` | `ach-agent-detail` | Single-agent drilldown (`agent` selector): sessions by channel, tokens by direction, cost by model, duration p50/p90/p99, per-tool calls + latency heatmap |
| `ach-finops.json` | `ach-finops` | Cost & tokens: $/h and range cost by agent/model/channel, cost per 1k sessions, cache-read token ratio, tokens by direction, cost share pies |
| `ach-tools.json` | `ach-tools` | Tools & MCP: call rate by tool/type, MCP share, tool errors (`status!="completed"`), p50/p95 tool latency + heatmap, top tools |
| `ach-routing.json` | `ach-routing` | Forwarder + content-service + keys: request rate by route/outcome/key_type, `pk`-on-runtime security signal, latencies, bytes by kind, external-ref refresh, key-cache hit ratio, LiteLLM reachability |
| `ach-control-plane.json` | `ach-control-plane` | Control-plane health: environments available, forwarder/content-service rates & latency, LiteLLM, external-ref, key cache, operator internals (reconcile rate/latency/errors, workqueue depth), orphan cleanup |

## Import

Grafana → **Dashboards → New → Import → Upload JSON file** (or paste). Pick your
Prometheus datasource when prompted (the `datasource` template variable drives
every panel). Requires Prometheus scraping ACH — enable
`metrics.serviceMonitor.enabled` (control-plane) and `metrics.podMonitor.enabled`
(agents) in the Helm chart.

The agent dashboards group by a clean `agent` label. The chart's PodMonitor now
relabels the operator-set pod label `ach.ackstorm.ai/agent` into an `agent`
series label, so `label_values(ach_agent_sessions_total, agent)` returns
`classifier`, `finops-advisor`, … instead of hashed pod names.

## ⚠ Metric names track the CURRENTLY-DEPLOYED state (mixed prefixes)

These queries match what the running cluster emits **today**, which is mixed:

- Already prefixed live → used as-is: `ach_agent_*`, `ach_orphan_cleanup_*`.
- Bare live → used bare here: `forwarder_*`, `content_service_*`,
  `platform_api_*`, `key_resolution_cache_*`, `litellm_unreachable_total`,
  `operator_external_ref_refresh_total`, `environment_available`, and the agent
  `router_*` / `engine_*` / `channel_inbound_events_total` /
  `memory_degraded_total`.

## After the `ach_` rename is deployed

The metric-name normalization (control-plane → `ach_`) is committed but **not yet
deployed**. Once it ships, migrate the control-plane dashboards with a mechanical
find/replace on the bare names above (prefix `ach_`). The agent `ach_agent_*`
names are unaffected.

## Proposed metrics & label improvements

See [`PROPOSED-METRICS.md`](./PROPOSED-METRICS.md) for a grounded list of missing
metrics/labels (capability/environment labels, session outcome, tool failure
status, engine in-flight gauge, model-name normalization, cost attribution) and
a dashboard-provisioning recommendation.
