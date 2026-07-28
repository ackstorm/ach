# Grafana dashboards for ACH

Six importable dashboards for the ACH observability surface. All share a
`datasource` + `namespace` template variable; the agent-scoped ones add an
`agent` variable.

> **The JSON lives in [`deploy/helm/ach/dashboards/`](../../deploy/helm/ach/dashboards/)**,
> inside the chart — Helm cannot read files outside its own directory, and a
> second copy here would just be 120 KB of duplication behind a sync gate.
> Edit the dashboards there. This directory holds their docs and validator.

| File | uid | Covers |
|------|-----|--------|
| `ach-agents.json` | `ach-agents` | Fleet overview: agents up, sessions/turns/tokens/cost, latency by model, tool calls, channel events, reliability counters (router drops, engine failures, degraded) — grouped **by agent** |
| `ach-agent-detail.json` | `ach-agent-detail` | Single-agent drilldown (`agent` selector): sessions by channel, tokens by direction, cost by model, duration p50/p90/p99, per-tool calls + latency heatmap |
| `ach-finops.json` | `ach-finops` | Cost & tokens: $/h and range cost by agent/model/channel, cost per 1k sessions, cache-read token ratio, tokens by direction, cost share pies |
| `ach-tools.json` | `ach-tools` | Tools & MCP: call rate by tool/type, MCP share, tool errors (`status!="completed"`), p50/p95 tool latency + heatmap, top tools |
| `ach-routing.json` | `ach-routing` | Forwarder + content-service + keys: request rate by route/outcome/key_type, `pk`-on-runtime security signal, latencies, bytes by kind, external-ref refresh, key-cache hit ratio, LiteLLM reachability |
| `ach-control-plane.json` | `ach-control-plane` | Control-plane health: environments available, forwarder/content-service rates & latency, LiteLLM, external-ref, key cache, operator internals (reconcile rate/latency/errors, workqueue depth), orphan cleanup |

## Import

**Preferred — ship them with the release.** Set `metrics.dashboards.enabled=true`
in the chart: `templates/grafana-dashboards.yaml` renders one ConfigMap per JSON
in `deploy/helm/ach/dashboards/`, labelled `grafana_dashboard: "1"` for the
kube-prometheus-stack Grafana sidecar to auto-load. No Prometheus Operator CRD is
involved (plain ConfigMaps) and the dashboards update with every release.
Provisioned dashboards are read-only in the UI — edit the JSON in the chart.

⚠ The sidecar only picks them up if it watches the namespace they land in.
kube-prometheus-stack defaults `sidecar.dashboards.searchNamespace` to null —
its OWN namespace — so with ACH and Prometheus in different namespaces either
run the sidecar with `searchNamespace=ALL` or set
`metrics.dashboards.namespace`. Nothing errors when this is wrong; the
dashboards just never appear. The `grafana_folder` annotation likewise needs
both `sidecar.dashboards.folderAnnotation=grafana_folder` and
`sidecar.dashboards.provider.foldersFromFilesStructure=true`.

**Manual** — Grafana → **Dashboards → New → Import → Upload JSON file** (or
paste). Pick your Prometheus datasource when prompted (the `datasource` template
variable drives every panel).

Either way this requires Prometheus scraping ACH — enable
`metrics.serviceMonitor.enabled` (control-plane) and `metrics.podMonitor.enabled`
(agents) in the Helm chart.

The agent dashboards group by a clean `agent` label, which the harness emits
natively since ach-agent **v0.10.1** (`http/metrics.py` stamps `agent` **and**
`environment` on every `ach_agent_*` sample), so `by (agent)` and the `$agent`
variable work without any scrape-time relabeling — the chart's PodMonitor
deliberately does none. `environment` is available but not yet wired into any
template variable (PROPOSED-METRICS.md §1).

**On ach-agent older than v0.10.1** the samples carry no `agent` label at all,
so the `$agent` variable comes up empty and every `by (agent)` panel reads
`No data` — indistinguishable from an idle agent. Nothing in the chart pins or
checks the harness version (the image comes from the CR), so if the agent
dashboards are blank, check the harness version first. Either upgrade the
harness, or group by `pod` instead, or re-add a PodMonitor `relabelings` block
deriving `agent` from the pod name.

## Metric names

These dashboards target the current normalized metric names. Control-plane
metrics use `ach_` (for example `ach_forwarder_requests_total`,
`ach_content_service_requests_total`, and `ach_platform_api_hydrate_duration_seconds_*`)
and all Python-agent metric families use `ach_agent_` (including router,
channel, engine, and memory metrics). The existing `ach_orphan_cleanup_*`
family is unchanged.

Validate the JSON and metric-name contract with:

```bash
make qa-dashboards      # = bash examples/grafana/check-metric-names.sh
```

It also runs in CI, so a metric rename that leaves a dashboard querying a dead
name fails the build instead of shipping a silently empty panel.

## Proposed metrics & label improvements

See [`PROPOSED-METRICS.md`](./PROPOSED-METRICS.md) for a grounded list of missing
metrics/labels (capability/environment labels, session outcome, tool failure
status, engine in-flight gauge, model-name normalization, cost attribution) and
a dashboard-provisioning recommendation.
