# Grafana dashboards for ACH

Two importable dashboards for the ACH observability surface. Both share a
`datasource` + `namespace` template variable.

> **The JSON lives in [`deploy/helm/ach/dashboards/`](../../deploy/helm/ach/dashboards/)**,
> inside the chart — Helm cannot read files outside its own directory, and a
> second copy here would just be 120 KB of duplication behind a sync gate.
> Edit the dashboards there. This directory holds their docs and validator.

| File | uid | Covers |
|------|-----|--------|
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
`metrics.serviceMonitor.enabled` (control-plane) in the Helm chart.

## Metric names

These dashboards target the current normalized metric names. Control-plane
metrics use `ach_` (for example `ach_forwarder_requests_total`,
`ach_content_service_requests_total`, and `ach_platform_api_hydrate_duration_seconds_*`)
and the `ach_orphan_cleanup_*` family keeps its name.

Validate the JSON and metric-name contract with:

```bash
make qa-dashboards      # = bash examples/grafana/check-metric-names.sh
```

It also runs in CI, so a metric rename that leaves a dashboard querying a dead
name fails the build instead of shipping a silently empty panel.
