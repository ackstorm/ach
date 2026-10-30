# Chart values

A grouped tour of `deploy/helm/ach/values.yaml` with defaults and what each
value turns into. The chart's own `values.yaml` is authoritative; when the
chart version in use differs from the one this was written against, run
`helm show values oci://ghcr.io/ackstorm/charts/ach --version <X.Y.Z>` and
trust that.

## Contents

- Minimal production values
- Global
- Components (operator, platformApi, forwarder, gateway, contentService, migrate)
- Integrations (litellmConnection, security, genai, openwork, ingress, metrics)
- Enterprise (classifier, platformApi.observability)
- Environment variables without a chart value

## Minimal production values

Everything not listed keeps its default. Pair this with the Secrets from
`references/prerequisites.md`.

```yaml
image:
  tag: v<X.Y.Z>              # defaults to the chart's appVersion; pin it explicitly in GitOps

litellmConnection:
  endpoint: http://litellm.litellm.svc.cluster.local:4000
  masterKeySecretRef:
    name: litellm-master-key
    key: master_key

platformApi:
  admins:
    - admin@example.com
  userDefaults:
    maxBudget: 100           # USD per user per window; "" = uncapped
    budgetDuration: "30d"
    maxKeys: 5               # ek- keys per user; 0 = nobody until an admin grants
  teams:
    default: [default]

genai:
  providerName: my-org       # OpenCode provider id; choose once, never change

ingress:
  enabled: true
  className: nginx
  host: ach.example.com
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
  tls:
    - hosts: [ach.example.com]
      secretName: ach-tls

extraEnv:
  - name: ACH_BASE_URL
    value: https://ach.example.com
  - name: DB_PASSWORD
    valueFrom:
      secretKeyRef: {name: ach-postgresql, key: password}
  - name: ACH_DB_URL
    value: postgresql://ach:$(DB_PASSWORD)@ach-postgresql:5432/ach?sslmode=disable
  - name: ACH_REDIS_ADDR
    value: ach-valkey-primary:6379
  - name: ACH_DEX_ISSUER_URL
    value: https://auth.example.com
  - name: ACH_DEX_CLIENT_ID
    value: ach
  - name: ACH_DEX_CLIENT_SECRET
    valueFrom:
      secretKeyRef: {name: ach-dex-client-secret, key: ACH_DEX_CLIENT_SECRET}
```

## Global

| Key | Default | Meaning |
|-----|---------|---------|
| `installCRDs` | `true` | Ship the CRDs as managed templates, upgraded with the release and kept on `helm uninstall` (`helm.sh/resource-policy: keep`). Set `false` only when CRDs are applied out of band. |
| `enterprise.enabled` | `false` | Turns on the Enterprise features; see `references/enterprise.md`. |
| `image.repo` | `ghcr.io/ackstorm/ach` | The single image. |
| `image.tag` | the chart's version | Pin it. |
| `image.pullPolicy` | `IfNotPresent` | `Always` is fine in production. |
| `image.rebuildId` | `""` | Dev only: a changing value forces a roll when the same tag is rebuilt. Leave empty. |
| `extraEnv` | `[]` | `{name, value|valueFrom}` items added to every component and the migrate Job. Where `ACH_BASE_URL`, `ACH_DB_URL`, Redis and Dex settings live. |
| `initContainers` | `[]` | Raw init containers added to every ACH Deployment (not the migrate Job). Use a bounded wait, never an endless loop. |

## Components

### operator

| Key | Default | Meaning |
|-----|---------|---------|
| `operator.enabled` | `true` | Reconciler. Required for a working install. |
| `operator.replicas` | `1` | Keep 1 (Recreate strategy, RWO cache). |
| `operator.podLabels` | `{}` | e.g. `istio.io/dataplane-mode: ambient`. |
| `operator.resources` | `{}` | |
| `operator.initContainers` | `[]` | Also gate the content-service sidecar. |
| `operator.args` | `[operator, --metrics-bind-address=:8080, --metrics-secure=false]` | Override to serve metrics over TLS on `:8443`. |
| `operator.cache.size` | `10Gi` | Artifact cache PVC (`ach-operator-cache`). |
| `operator.cache.accessMode` | `ReadWriteOnce` | `ReadWriteMany` for a standalone content-service. |
| `operator.cache.storageClassName` | `""` | Cluster default when empty. |

Orphan-key reaper knobs go in `extraEnv` (operator-only):
`ACH_ORPHAN_CLEANUP_INTERVAL` (default `1h`, floor `5m`),
`ACH_ORPHAN_CLEANUP_DRY_RUN` (default `false`; a malformed value fails
startup), `ACH_ORPHAN_CLEANUP_MAX_REVOKE` (default `10`, a tick with more
candidates aborts). The reaper only revokes LiteLLM keys stamped with this
release's `ACH_BASE_URL`, never foreign keys.

### platformApi

| Key | Default | Meaning |
|-----|---------|---------|
| `platformApi.enabled` | `true` | REST API, OAuth server, console. |
| `platformApi.replicas` | `1` | |
| `platformApi.resources` | `{}` | |
| `platformApi.admins` | `[]` | List of admin emails, rendered into the `ach-platform-api-admins` ConfigMap. Empty = every admin route 403. |
| `platformApi.headers` | `x-ach-key`, `x-api-key` (both `resolve`) | `ACH_CREDENTIAL_HEADERS`. `resolve` only; `passthrough` or `authorization` refuse to start. |
| `platformApi.userDefaults.maxBudget` | `""` | Per-user spend ceiling seeded at login (`ACH_USER_MAX_BUDGET`). `""` = no cap, `0` = refuse everything. |
| `platformApi.userDefaults.budgetDuration` | `"30d"` | Reset window (`ACH_USER_BUDGET_DURATION`), rendered only with a `maxBudget`. |
| `platformApi.userDefaults.maxKeys` | `0` | `ek-` keys per user (`ACH_USER_MAX_KEYS`). |
| `platformApi.oauth.redirectHosts` | `[]` | https redirect hosts allowed for OAuth clients (`ACH_OAUTH_REDIRECT_HOSTS`). Loopback always allowed. |
| `platformApi.teams.default` | `[default]` | LiteLLM team aliases every user joins at login (`ACH_TEAMS_DEFAULT`). At least one. |
| `platformApi.teams.users` | `{}` | email → team aliases (`ACH_TEAMS_USER`). |
| `platformApi.teams.sso` | `{}` | Dex `groups` value → team aliases (`ACH_TEAMS_SSO`). |
| `platformApi.keyExchange.tokenTTL` | `15m` | Lifetime of the JWT a ServiceAccount token exchange returns (`ACH_KEY_EXCHANGE_TOKEN_TTL`), minimum `1m`. Needs the audit policy noted in `SKILL.md`. |
| `platformApi.console.chatUrl` | `""` | Chat button target in the console (`ACH_CONSOLE_CHAT_URL`). |
| `platformApi.observability.*` | empty | Enterprise; see `references/enterprise.md`. |
| `platformApi.initContainers` | `[]` | |

Detail on admins, teams, budgets and redirect hosts: `references/auth.md`.

### forwarder

| Key | Default | Meaning |
|-----|---------|---------|
| `forwarder.enabled` | `true` | Model/MCP/A2A data plane. |
| `forwarder.replicas` | `1` | Stateless; scale freely. |
| `forwarder.resources`, `forwarder.initContainers` | | |
| `forwarder.jwtSecretName` | `ach-jwt-signing-keys` | The operator-minted signing key Secret. Also mounted by platform-api and content-service. |
| `forwarder.extraEnv` | `[]` | Forwarder-only env. |
| `forwarder.headers` | `x-ach-key`, `x-api-key` (`resolve`) | Credential slots (`ACH_CREDENTIAL_HEADERS`). |
| `forwarder.gemini.stripModelPrefixes` | `[]` | `ACH_GEMINI_STRIP_MODEL_PREFIXES`. |
| `forwarder.trustedIdP.issuer` / `.audiences` / `.claim` | `""` / `[]` / `email` | `ACH_TRUSTED_IDP_*`. |

Detail: `references/litellm.md` (headers, Gemini) and `references/auth.md`
(trusted IdP).

### gateway, contentService, migrate

| Key | Default | Meaning |
|-----|---------|---------|
| `gateway.enabled` | `true` | Single-origin reverse proxy, Service `ach-gateway:80`. |
| `gateway.replicas`, `.resources`, `.initContainers` | | |
| `gateway.extraEnv` | `[]` | e.g. `ACH_GATEWAY_ACCESS_LOG` = `combined` (default) / `common` / `off`. |
| `contentService.enabled` | `true` | Serves plugin/skill/prompt/artifact archives to hydrate. |
| `contentService.standalone` | `false` | `false` = sidecar in the operator Pod; `true` = own Deployment (needs RWX cache). |
| `contentService.replicas` | `1` | Only with `standalone: true`. |
| `contentService.resources`, `.initContainers` | | `initContainers` only apply standalone. |
| `migrate.enabled` | `true` | Pre-install/pre-upgrade migration Job. |

Detail: `references/topology.md`.

## Integrations

### litellmConnection and security

```yaml
litellmConnection:
  enabled: true
  endpoint: http://litellm.litellm.svc.cluster.local:4000
  masterKeySecretRef:
    name: litellm-master-key
    key: master_key
    namespace: ""            # set only when the Secret lives in another namespace

security:
  keyEncryptionKey:          # ACH_KEY_ENCRYPTION_KEY: base64 of exactly 32 bytes
    secretRef: {name: ach-key-encryption-key, key: dek}
  credentialHashPepper:      # ACH_CREDENTIAL_HASH_PEPPER
    secretRef: {name: ach-credential-hash-pepper, key: pepper}
```

Detail: `references/litellm.md`, `references/prerequisites.md`.

### genai and openwork

```yaml
genai:
  providerName: ai-platform
  opencodePluginSpec: "git+https://github.com/ackstorm/opencode-oidc-provider.git#v<tag>"
  opencodeExtraProviders: []
  defaultModel: ""
  defaultSmallModel: ""
  defaultEnvironment: ""

openwork:
  enabled: false             # OpenWork desktop organisation server under /openwork and /api/den
  orgName: ACH
  orgSlug: ach
  brandAppName: ACH
  brandLogoUrl: ""
  brandIconUrl: ""
  accentColor: ""
  blockedCommands: []        # non-empty disables interactive terminals in the desktop app
  blockBrowserUploads: false
```

Detail on `genai`: `references/opencode.md`.

### ingress

| Key | Default | Meaning |
|-----|---------|---------|
| `ingress.enabled` | `false` | Renders Ingress `ach-public`. |
| `ingress.className` | `""` | Cluster default class when empty. |
| `ingress.host` | `""` | Empty = catch-all rule. Set it. |
| `ingress.annotations` | `{}` | cert-manager, nginx tuning. |
| `ingress.tls` | `[]` | Passed verbatim to `spec.tls`. |
| `ingress.path` / `pathType` / `service` / `port` | `/` / `Prefix` / `ach-gateway` / `80` | Leave unless the edge is unusual. |

### metrics

| Key | Default | Meaning |
|-----|---------|---------|
| `metrics.serviceMonitor.enabled` | `false` | Needs the Prometheus Operator CRDs or the install fails. |
| `metrics.serviceMonitor.interval` / `scrapeTimeout` / `labels` | `30s` / `""` / `{}` | `labels` must match your Prometheus `serviceMonitorSelector` (often `release: <name>`). |
| `metrics.prometheusRule.enabled` | `false` | Recommended alerts; same CRD requirement. |
| `metrics.prometheusRule.labels` / `includeP1Alerts` | `{}` / `true` | |
| `metrics.dashboards.enabled` | `false` | Grafana dashboards as ConfigMaps for the Grafana sidecar. |
| `metrics.dashboards.namespace` / `label` / `labelValue` / `folder` / `labels` | `""` / `grafana_dashboard` / `"1"` / `ACH` / `{}` | The sidecar must watch the namespace they land in. |

Without the ServiceMonitor, pods carry `prometheus.io/scrape` annotations.

## Enterprise

`classifier.*` and `platformApi.observability.*` render nothing unless
`enterprise.enabled: true`. See `references/enterprise.md`.

## Environment variables without a chart value

Set through `extraEnv` (or the component's own `extraEnv`).

| Variable | Required | Read by | Notes |
|----------|----------|---------|-------|
| `ACH_BASE_URL` | yes | operator, platform-api, forwarder, content-service | Public origin, `http(s)://`. |
| `ACH_DB_URL` | yes | all + migrate Job | Full Postgres DSN. |
| `ACH_REDIS_ADDR` | yes | platform-api, forwarder, content-service | `host:port`. |
| `ACH_REDIS_PASSWORD`, `ACH_REDIS_TLS`, `ACH_REDIS_DB` | no | same | |
| `ACH_DEX_ISSUER_URL`, `ACH_DEX_CLIENT_ID`, `ACH_DEX_CLIENT_SECRET` | yes | platform-api | |
| `ACH_OAUTH_ACCESS_TTL` | no | platform-api | Default `1h`, minimum `1m`. |
| `ACH_OAUTH_REFRESH_TTL` | no | platform-api | Default `720h`, minimum `1h`. |
| `ACH_DISABLE_UI_WRITES` | no | platform-api | `true` makes every UI Objects API write (`/platform/objects`) answer 403 `ui_writes_disabled`: Environments come only from GitOps. |
| `ACH_PLUGIN_MAX_SIZE_MIB`, `ACH_SKILL_MAX_SIZE_MIB` | no | operator | Fetched archive caps, default `50`. |
| `ACH_ORPHAN_CLEANUP_*` | no | operator | See operator above. |

Set by the chart, never by hand: `ACH_KEY_ENCRYPTION_KEY`,
`ACH_CREDENTIAL_HASH_PEPPER`, `ACH_LITELLM_BASE_URL`, `ACH_LITELLM_MASTER_KEY`,
`ACH_LITELLM_CONNECTION_*`, `ACH_JWT_SECRET_DIR`, `ACH_JWT_SECRET_NAME`,
`ACH_CREDENTIAL_HEADERS`, `ACH_TEAMS_*`, `ACH_USER_*`, `ACH_GENAI_*`,
`ACH_OPENCODE_*`, `ACH_ENTERPRISE`, `ACH_OBSERVABILITY_*`, `ACH_CLASSIFIER_*`.
