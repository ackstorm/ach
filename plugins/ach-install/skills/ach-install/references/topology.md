# Topology

## Contents

- Components and Services
- Public routing: gateway (default) or per-service
- Content-service: sidecar or standalone
- Replicas and HA
- Metrics and dashboards

## Components and Services

| Deployment | Service:port | Role | Scale |
|------------|--------------|------|-------|
| `ach-operator` | `ach-operator-metrics:8080` (metrics only) | Reconciles CRDs, writes Postgres and LiteLLM, mints JWT keys, owns the artifact cache | 1 (Recreate) |
| content-service (sidecar in `ach-operator` by default) | `ach-content-service:8082` | Streams cached archives to hydrate | 1 as sidecar; N standalone |
| `ach-platform-api` | `ach-platform-api:80` | REST API, OAuth server, console, OpenCode endpoints | N |
| `ach-forwarder` | `ach-forwarder:80` (+ `8081` health) | `/v1`, `/gemini`, `/mcp`, `/a2a`, `/.well-known`, `/v2/model/info` | N |
| `ach-gateway` (optional) | `ach-gateway:80` | Path router in front of the three above | N |
| `ach-migrate-<revision>` Job | — | Schema migrations, Helm pre-install/pre-upgrade hook | — |
| `ach-classifier` (Enterprise, optional) | — | Conversation classifier | 1 |

Every Deployment runs the same image with `args: [<mode>]`. Do not drop the
mode from a custom `args` list: the binary without a subcommand prints help and
exits 0.

## Public routing

### Default: through `ach-gateway`

The gateway is a logic-free reverse proxy with a fixed route table:

| Path | Upstream |
|------|----------|
| `/platform/` | platform-api |
| `/content/` | content-service |
| `/v1/`, `/gemini/`, `/mcp/`, `/a2a/` | forwarder |
| `/.well-known/opencode` | platform-api |
| `/.well-known/` (everything else) | forwarder |
| `/v2/model/info` (exact) | forwarder |
| `/` | platform-api (the console) |
| `/metrics`, `/metrics/` | 404, always |

Point ONE public rule at it. With the chart's Ingress:

```yaml
ingress:
  enabled: true
  className: nginx
  host: ach.example.com
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"   # long SSE/MCP streams
    nginx.ingress.kubernetes.io/proxy-body-size: 32m         # forwarder caps bodies at 32 MiB
  tls:
    - hosts: [ach.example.com]
      secretName: ach-tls
```

With Gateway API instead, keep `ingress.enabled: false` and route `/` to the
`ach-gateway` Service on port 80:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: ach
  namespace: ach
spec:
  parentRefs:
    - name: <your-gateway>
      namespace: <gateway-namespace>
      sectionName: https
  hostnames: [ach.example.com]
  rules:
    - matches:
        - path: {type: PathPrefix, value: /}
      backendRefs:
        - name: ach-gateway
          port: 80
```

### Without the gateway

Set `gateway.enabled: false` and reproduce the table above in your own Ingress
or HTTPRoute, one rule per prefix, all on the same host. Keep the
`/.well-known/opencode` rule ahead of `/.well-known/`, and never route
`/metrics`. Do this only when the edge must split traffic itself (separate
timeouts, WAF rules per path); otherwise the gateway is less to get wrong.

## Content-service: sidecar or standalone

The operator is the only writer of the artifact cache PVC; content-service
reads it. Default: content-service runs as a second container in the operator
Pod, sharing a ReadWriteOnce volume. That is one replica, and hydrate pauses
while the operator Pod restarts.

For several content-service replicas, give the cache an RWX volume and split it
out:

```yaml
operator:
  cache:
    size: 20Gi
    accessMode: ReadWriteMany
    storageClassName: efs-sc        # any RWX class: EFS, NFS, CephFS
contentService:
  standalone: true
  replicas: 3
```

Changing `accessMode` or `storageClassName` on an existing install means a new
PVC: PVC specs are immutable, so delete `ach-operator-cache` (the operator
re-fetches everything into the new one) as part of that change.

## Replicas and HA

- `platformApi.replicas`, `forwarder.replicas`, `gateway.replicas`: stateless,
  scale freely; state lives in Postgres and Valkey. Add PodDisruptionBudgets
  and anti-affinity yourself if needed (the chart ships none).
- `operator.replicas`: keep 1. The Deployment uses the Recreate strategy and
  the RWO cache; leader election is off by default.
- Per-replica caches converge within minutes: resolver caches are shared in
  Valkey, Environment/BIP caches refresh from Postgres notifications plus a
  5-minute safety refresh.

## Metrics and dashboards

Each service serves unauthenticated `/metrics` on its pod port (operator 8080,
platform-api 8080, forwarder 8080, content-service 8082). Never publish it.
Pods and the content-service Service carry `prometheus.io/scrape` annotations.

With the Prometheus Operator:

```yaml
metrics:
  serviceMonitor:
    enabled: true
    labels: {release: kube-prometheus-stack}   # match your serviceMonitorSelector
  prometheusRule:
    enabled: true
    labels: {release: kube-prometheus-stack}
  dashboards:
    enabled: true
    namespace: monitoring      # or make the Grafana sidecar search all namespaces
```

Both `serviceMonitor` and `prometheusRule` fail the install when the
Prometheus Operator CRDs are absent. Dashboards are plain ConfigMaps; if they
never appear in Grafana, the sidecar is not watching the namespace they landed
in (there is no error).
