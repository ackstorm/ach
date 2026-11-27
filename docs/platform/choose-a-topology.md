# Choose a topology

The chart's defaults give a working install: one public rule to the gateway,
one replica of each component, and content-service inside the operator Pod.
This page covers the choices beyond that: how traffic reaches ACH, how to run
more replicas, and how to collect metrics.

You need a running install, or the `values.yaml` of one you are preparing; see
[Install ACH with Helm](install-the-chart.md).

## Components

| Deployment | Service and port | Replicas |
|---|---|---|
| `ach-operator` | `ach-operator-metrics:8080`, metrics only | Always 1 |
| content-service, in the `ach-operator` Pod by default | `ach-content-service:8082` | 1; more when [standalone](#content-service-sidecar-or-standalone) |
| `ach-platform-api` | `ach-platform-api:80` | Any number |
| `ach-forwarder` | `ach-forwarder:80` | Any number |
| `ach-gateway` | `ach-gateway:80` | Any number |

## Through `ach-gateway`

The gateway is a reverse proxy with a fixed route table and no logic of its
own. Send all public traffic for the ACH host to the Service `ach-gateway` on
port 80.

With the chart's Ingress:

```yaml
ingress:
  enabled: true
  className: nginx
  host: ach.example.com
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"   # long model and MCP streams
    nginx.ingress.kubernetes.io/proxy-body-size: 32m         # large prompts and attachments
  tls:
    - hosts: [ach.example.com]
      secretName: ach-tls
```

With the Gateway API, leave `ingress.enabled: false` and write the route
yourself:

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

## Without the gateway

Use this only when your edge must treat paths differently, for example with
separate timeouts or firewall rules. Set `gateway.enabled: false` and
reproduce the gateway's routes in your own Ingress or HTTPRoute, all on one
host:

| Path | Service |
|---|---|
| `/platform/` | `ach-platform-api:80` |
| `/content/` | `ach-content-service:8082` |
| `/v1/`, `/gemini/`, `/mcp/`, `/a2a/` | `ach-forwarder:80` |
| `/.well-known/opencode` | `ach-platform-api:80` |
| `/.well-known/` (everything else) | `ach-forwarder:80` |
| `/v2/model/info` (exact) | `ach-forwarder:80` |
| `/` | `ach-platform-api:80` |

The `/.well-known/opencode` rule must win over `/.well-known/`. Do not route
`/metrics`.

## Restrict the console by source address

The web console uses two path prefixes: `/console/` for its pages and
`/platform/console/` for its sign-in and its API. To limit it to a network, add
a second rule for those two prefixes that carries your controller's address
filter. With ingress-nginx:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ach-console
  namespace: ach
  annotations:
    nginx.ingress.kubernetes.io/whitelist-source-range: 10.0.0.0/8
spec:
  ingressClassName: nginx
  tls:
    - hosts: [ach.example.com]
      secretName: ach-tls
  rules:
    - host: ach.example.com
      http:
        paths:
          - path: /console
            pathType: Prefix
            backend: {service: {name: ach-gateway, port: {number: 80}}}
          - path: /platform/console
            pathType: Prefix
            backend: {service: {name: ach-gateway, port: {number: 80}}}
```

The console also calls `/platform/keys` and `/platform/environments`, which it
shares with `ach-cli`. The filter does not cover them: they stay reachable and
answer only a signed-in caller.

## Content-service: sidecar or standalone

Content-service serves the plugin and skill files that the operator fetches
into a cache volume. By default it runs as a second container in the operator
Pod and shares a `ReadWriteOnce` volume with it. That is one replica, and
installing an Environment pauses while the operator Pod restarts.

To run several replicas, give the cache a `ReadWriteMany` volume and split
content-service out:

```yaml
operator:
  cache:
    size: 20Gi
    accessMode: ReadWriteMany
    storageClassName: efs-sc        # any ReadWriteMany class
contentService:
  standalone: true
  replicas: 3
```

On an existing install this needs a new volume, because Kubernetes does not
allow changing the access mode or class of a PersistentVolumeClaim. Delete the
claim `ach-operator-cache` as part of the change; the operator fetches
everything again.

## Replicas

```yaml
platformApi: {replicas: 2}
forwarder: {replicas: 3}
gateway: {replicas: 2}
```

- These three keep no state of their own, so any number works. Every
  Deployment prefers to spread its pods across nodes, and above 1 replica the
  chart adds a PodDisruptionBudget (`maxUnavailable: 1`). Run 2 or more on a
  cluster with at least 2 nodes.
- All replicas share one Postgres and one Valkey, which the chart does not
  deploy: ACH stays up only as long as they do, so make them highly available
  where they run.
- During a rollout the old Pod serves until the new one is ready, and then
  lets open requests finish for
  [`shutdown.timeoutSeconds`](../reference/helm-values.md#shutdown), 120
  seconds by default.
- Keep `operator.replicas` at 1.
- A change such as a new Environment reaches every replica within a few
  minutes.

## Metrics and dashboards

Each component serves `/metrics` without authentication on its Pod port: 8080
for the operator, platform-api and the forwarder, 8082 for content-service.
Keep it inside the cluster. The operator, platform-api and forwarder Pods and
the content-service Service carry `prometheus.io/scrape` annotations.

With the Prometheus Operator:

```yaml
metrics:
  serviceMonitor:
    enabled: true
    labels: {release: kube-prometheus-stack}   # must match your Prometheus selector
  prometheusRule:
    enabled: true
    labels: {release: kube-prometheus-stack}
  dashboards:
    enabled: true
    namespace: monitoring      # where the Grafana sidecar looks
```

`serviceMonitor` and `prometheusRule` fail the install on a cluster without
the Prometheus Operator resource definitions. The dashboards are ConfigMaps:
if they do not appear in Grafana, its sidecar is not watching the namespace
they are in, and nothing reports an error.

## Verify

```bash
kubectl -n ach get deploy,svc,ingress
curl -fsS https://ach.example.com/.well-known/oauth-authorization-server | jq .issuer
curl -s -o /dev/null -w '%{http_code}\n' https://ach.example.com/metrics      # 404
```

The first `curl` prints your ACH address, and `/metrics` answers 404.

## Related

- [`ingress`](../reference/helm-values.md#ingress),
  [`metrics`](../reference/helm-values.md#metrics) and
  [`contentService`](../reference/helm-values.md#contentservice) in the Helm
  values reference.
- [Architecture](../overview/architecture.md)
