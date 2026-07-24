# Grafana dashboards for ACH

Two importable dashboards for the ACH observability surface:

| File | Covers |
|------|--------|
| `ach-control-plane.json` | operator / forwarder / content-service / platform-api: request rates & latency, LiteLLM reachability, external-ref refresh, key-cache hit ratio, orphan cleanup, login rate, JWT suppression, Environment availability |
| `ach-agents.json` | per-agent (`pod` selector): sessions, turns, tokens, cost (USD), turn & tool latency, tool calls, inbound events, router drops, degraded signals |

## Import

Grafana → **Dashboards → New → Import → Upload JSON file** (or paste). Pick your
Prometheus datasource when prompted (the `datasource` template variable drives
every panel). Requires Prometheus scraping ACH — enable
`metrics.serviceMonitor.enabled` (control-plane) and `metrics.podMonitor.enabled`
(agents) in the Helm chart, or the pod-scrape annotation fallback.

The per-agent breakdown uses the `pod` label (`pod=~"achagent-.*"`) because the
PodMonitor does not currently copy the `ach.ackstorm.ai/agent` pod label onto the
series — see "Polish", below.

## ⚠ Metric names track the CURRENTLY-DEPLOYED state (mixed prefixes)

These queries match what the running cluster emits **today**, which is mixed:

- Already prefixed live → used as-is: `ach_agent_*` (agent stats), `ach_orphan_cleanup_*`.
- Bare live → used bare here: `forwarder_*`, `content_service_*`, `platform_api_*`,
  `key_resolution_cache_*`, `litellm_unreachable_total`,
  `operator_external_ref_refresh_total`, `environment_available`, and the agent
  `router_*` / `engine_*` / `channel_inbound_events_total` / `memory_degraded_total`.

## Polish (after the `ach_` rename is deployed)

The metric-name normalization (all control-plane → `ach_`, all agent → `ach_agent_`)
is committed but **not yet deployed**. Once it ships, migrate these dashboards with a
mechanical find/replace on the bare names above (add `ach_` / `ach_agent_`), and
optionally:

- add `podTargetLabels: ["ach.ackstorm.ai/agent"]` to the PodMonitor so panels can
  select a clean `agent` label instead of `pod=~"achagent-.*"`;
- wire these JSONs into the chart as a `grafana_dashboard: "1"`-labeled ConfigMap for
  sidecar auto-import instead of manual upload.
