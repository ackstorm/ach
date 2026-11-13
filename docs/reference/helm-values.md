# Helm values

The chart is `oci://ghcr.io/ackstorm/charts/ach`. This command prints every
key with its default and is authoritative for the version you install:

```bash
helm show values oci://ghcr.io/ackstorm/charts/ach --version <X.Y.Z>
```

The sections follow the order of the chart's `values.yaml`. `operator`,
`platformApi`, `forwarder`, `gateway` and `contentService` also take
`replicas`, `resources`, `args` and `initContainers`; they are listed only
where the default matters.

## `installCRDs`

| Key | Default | Purpose |
|---|---|---|
| `installCRDs` | `true` | Install the CRDs with the release. `helm upgrade` upgrades them and `helm uninstall` keeps them. Set `false` when another tool applies the CRDs. |

## `enterprise`

| Key | Default | Purpose |
|---|---|---|
| `enterprise.enabled` | `false` | Turn on the Enterprise features: conversation classification, observability analytics and traces. Requires a signed agreement with ACKstorm. While `false`, the Enterprise values below render nothing. |

## `image`

| Key | Default | Purpose |
|---|---|---|
| `image.repo` | `ghcr.io/ackstorm/ach` | The one image every component runs. |
| `image.tag` | the chart's release | Image tag. |
| `image.pullPolicy` | `IfNotPresent` | Pull policy of every component. |
| `image.rebuildId` | `""` | Development only: a new value on each upgrade rolls every pod when the same tag was rebuilt. |

## `operator`

Reconciles the ACH custom resources. In the default topology its Pod also
runs content-service.

| Key | Default | Purpose |
|---|---|---|
| `operator.enabled` | `true` | Deploy the operator. |
| `operator.replicas` | `1` | Replica count. |
| `operator.podLabels` | `{}` | Extra labels on the operator Pod. |
| `operator.args` | `operator`, `--metrics-bind-address=:8080`, `--metrics-secure=false` | Process arguments. Override them to serve metrics over TLS on `:8443`. |
| `operator.cache.size` | `10Gi` | Size of the artifact cache volume `ach-operator-cache`. |
| `operator.cache.accessMode` | `ReadWriteOnce` | Access mode of the cache volume. `ReadWriteMany` is required with `contentService.standalone`. |
| `operator.cache.storageClassName` | `""` | Storage class of the cache volume; empty uses the cluster default. |
| `operator.initContainers` | `[]` | Init containers of the operator Pod; they run before content-service too. |

## `platformApi`

The REST API, the OAuth server and the console.

| Key | Default | Purpose |
|---|---|---|
| `platformApi.enabled` | `true` | Deploy platform-api. |
| `platformApi.admins.users` | `[]` | Emails of the platform admins. Read at start, matched case-sensitively. |
| `platformApi.admins.sso` | `[]` | Identity-provider groups whose members are admins. Re-read at login and at every token refresh. With both lists empty, every admin call answers 403. |
| `platformApi.headers` | `x-ach-key`, `x-api-key`, both `resolve` | Request headers read for a credential, in order. Only `mode: resolve` is accepted. |
| `platformApi.userDefaults.maxBudget` | `""` | Spend ceiling in USD given to each user at first login. `""` is no ceiling; `0` refuses every request. Changing it does not alter users who already have a ceiling. |
| `platformApi.userDefaults.budgetDuration` | `"30d"` | Window after which the spend resets; `""` never resets. |
| `platformApi.userDefaults.maxKeys` | `0` | Environment keys one user may hold. `0` allows none until an admin grants an allowance. |
| `platformApi.keyExchange.tokenTTL` | `15m` | Lifetime of the token a ServiceAccount exchange returns, from `1m` to `1h`. |
| `platformApi.oauth.redirectHosts` | `[]` | Hosts an OAuth client may redirect to over HTTPS. Loopback is always allowed. |
| `platformApi.teams.default` | `[default]` | LiteLLM team aliases every user joins at login. A missing team fails the login. |
| `platformApi.teams.users` | `{}` | Email → team aliases joined at login. |
| `platformApi.teams.sso` | `{}` | Identity-provider group → team aliases joined at login. |
| `platformApi.console.chatUrl` | `""` | URL the console's Chat button opens; empty hides the button. |
| `platformApi.console.theme` | `""` | Default console skin: `dark`, `light`, `pastel` or `red`; empty follows the browser. |
| `platformApi.console.apps` | `[]` | Tiles of the console's Apps page: `name` and `url` required; `group`, `subtitle`, `icon`, `teams`, `admin` optional. Empty hides the page. |
| `platformApi.observability.clickhouseUrl` | `""` | Enterprise. ClickHouse the observability routes read; empty leaves them answering 503. |
| `platformApi.observability.secretName` | `""` | Enterprise. Secret with `ACH_OBSERVABILITY_CLICKHOUSE_USER` and `ACH_OBSERVABILITY_CLICKHOUSE_PASSWORD` of a read-only user. Required with `clickhouseUrl`. |
| `platformApi.observability.projectIds` | `[]` | Enterprise. Langfuse projects the routes may read. Required with `clickhouseUrl`. |
| `platformApi.observability.thanosUrl` | `""` | Enterprise. Prometheus query API for the gateway metrics. |

## `forwarder`

The data path for model, MCP and A2A requests.

| Key | Default | Purpose |
|---|---|---|
| `forwarder.enabled` | `true` | Deploy the forwarder. |
| `forwarder.replicas` | `1` | Replica count. |
| `forwarder.jwtSecretName` | `ach-jwt-signing-keys` | Secret with the JWT signing keys. The operator creates it; it survives `helm uninstall`. |
| `forwarder.extraEnv` | `[]` | Environment variables for the forwarder only. |
| `forwarder.headers` | `x-ach-key`, `x-api-key`, both `resolve` | Request headers read for a credential, in order. `resolve` takes an ACH credential; `passthrough` forwards the backend's own key unchanged. |
| `forwarder.gemini.stripModelPrefixes` | `[]` | Prefixes removed from the model name on the `/gemini` route; first match wins. |
| `forwarder.trustedIdP.issuer` | `""` | External OIDC issuer whose access tokens are accepted as `Authorization: Bearer`; empty turns it off. |
| `forwarder.trustedIdP.audiences` | `[]` | Accepted `aud` values. Required with an issuer. |
| `forwarder.trustedIdP.claim` | `email` | Token claim that names the ACH user. |

## `litellmConnection`

The LiteLLM endpoint and master key, set once for every component.

| Key | Default | Purpose |
|---|---|---|
| `litellmConnection.enabled` | `true` | Configure the connection from these values. |
| `litellmConnection.endpoint` | `http://litellm.litellm.svc.cluster.local:4000` | LiteLLM base URL. |
| `litellmConnection.masterKeySecretRef.name` | `litellm-master-key` | Secret with the LiteLLM master key. It must exist before the install. |
| `litellmConnection.masterKeySecretRef.key` | `master_key` | Key inside that Secret. |
| `litellmConnection.masterKeySecretRef.namespace` | `""` | Namespace of the Secret; empty is the release namespace. |

## `security`

Two Secrets that must exist before the install and must never change:
replacing the encryption key makes stored LiteLLM keys unreadable, and
replacing the pepper invalidates every issued credential.

| Key | Default | Purpose |
|---|---|---|
| `security.keyEncryptionKey.secretRef.name` | `ach-key-encryption-key` | Secret with the encryption key. |
| `security.keyEncryptionKey.secretRef.key` | `dek` | Key inside it; the value is base64 of exactly 32 bytes. |
| `security.credentialHashPepper.secretRef.name` | `ach-credential-hash-pepper` | Secret with the credential hash pepper. |
| `security.credentialHashPepper.secretRef.key` | `pepper` | Key inside it. |

## `genai`

What ACH tells GenAI clients (OpenCode) about itself.

| Key | Default | Purpose |
|---|---|---|
| `genai.providerName` | `ai-platform` | Provider id users sign in with. Lowercase letters, digits and `-`. Changing it signs every user out. |
| `genai.opencodePluginSpec` | a pinned `git+https://github.com/ackstorm/opencode-oidc-provider.git` tag | Plugin OpenCode installs at sign-in; empty turns it off. |
| `genai.opencodeExtraProviders` | `[]` | Other provider ids OpenCode may enable next to `providerName`. |
| `genai.defaultModel` | `""` | Default model for the client, when the user has access to it. |
| `genai.defaultSmallModel` | `""` | Default small model, under the same condition. |
| `genai.defaultEnvironment` | `""` | Environment the onboarding skill tells users to hydrate globally. |

## `gateway`

The reverse proxy that puts platform-api, content-service and the forwarder
behind one Service, `ach-gateway`. It serves ACH routes only: LiteLLM's own
UI and API need their own host.

| Key | Default | Purpose |
|---|---|---|
| `gateway.enabled` | `true` | Deploy the gateway. `false` requires one Ingress per service. |
| `gateway.replicas` | `1` | Replica count. |
| `gateway.extraEnv` | `[]` | Environment variables for the gateway only. |

## `contentService`

Serves plugin, skill, prompt and artifact archives.

| Key | Default | Purpose |
|---|---|---|
| `contentService.enabled` | `true` | Run content-service. |
| `contentService.standalone` | `false` | `false` runs it inside the operator Pod. `true` runs its own Deployment and requires `operator.cache.accessMode: ReadWriteMany`. |
| `contentService.replicas` | `1` | Replica count when standalone. |
| `contentService.initContainers` | `[]` | Init containers of the standalone Deployment only. |

## `classifier`

Enterprise. A job that labels idle sessions with one category.

| Key | Default | Purpose |
|---|---|---|
| `classifier.enabled` | `false` | Deploy the classifier. |
| `classifier.secretName` | `""` | Secret with the ClickHouse user, the model key and the Langfuse key pair. Required when enabled. |
| `classifier.clickhouseUrl` | `""` | ClickHouse the classifier reads sessions from. Required when enabled. |
| `classifier.projectId` | `default` | Langfuse project to classify. |
| `classifier.modelUrl` | `""` | OpenAI-compatible base URL of the model. Required when enabled. |
| `classifier.model` | `ackstorm.smart` | Model that classifies. |
| `classifier.langfuseUrl` | `""` | Langfuse the labels are written to; empty classifies and logs without writing. |
| `classifier.onlyUser` | `""` | Classify only this user's sessions. |
| `classifier.interval` | `10m` | Time between runs. |
| `classifier.idle` | `30m` | How long a session must be idle before it is classified. |
| `classifier.lookback` | `72h` | How far back sessions are considered. |
| `classifier.batch` | `50` | Sessions per run. |
| `classifier.taxonomy` | `{}` | Custom categories and rules; empty uses the built-in taxonomy. |
| `classifier.resources` | `50m` CPU and `128Mi` requested, `512Mi` limit | Resources of the classifier. |

## `migrate`

| Key | Default | Purpose |
|---|---|---|
| `migrate.enabled` | `true` | Run the schema migration Job before each install and upgrade. |

## `ingress`

| Key | Default | Purpose |
|---|---|---|
| `ingress.enabled` | `false` | Create the Ingress `ach-public`. |
| `ingress.className` | `""` | Ingress class; empty uses the cluster default. |
| `ingress.host` | `""` | Public hostname; empty matches any host. |
| `ingress.annotations` | `{}` | Annotations on the Ingress. |
| `ingress.tls` | `[]` | Copied to `spec.tls`. |
| `ingress.path` | `/` | Path of the single rule. |
| `ingress.pathType` | `Prefix` | Path type of the rule. |
| `ingress.service` | `ach-gateway` | Backend Service. |
| `ingress.port` | `80` | Backend port. |

## `metrics`

The operator, platform-api, the forwarder and content-service serve
`/metrics` without authentication; keep it inside the cluster. They carry
`prometheus.io/scrape` annotations. The gateway serves no metrics.

| Key | Default | Purpose |
|---|---|---|
| `metrics.serviceMonitor.enabled` | `false` | Create a ServiceMonitor. Requires the Prometheus Operator CRDs, or the install fails. |
| `metrics.serviceMonitor.interval` | `30s` | Scrape interval. |
| `metrics.serviceMonitor.scrapeTimeout` | `""` | Scrape timeout; empty uses the Prometheus default. |
| `metrics.serviceMonitor.labels` | `{}` | Labels your Prometheus selects ServiceMonitors by. |
| `metrics.prometheusRule.enabled` | `false` | Create the recommended alert rules. Same CRD requirement. |
| `metrics.prometheusRule.labels` | `{}` | Labels your Prometheus selects rules by. |
| `metrics.prometheusRule.includeP1Alerts` | `true` | Include the two alerts on operator metrics. |
| `metrics.dashboards.enabled` | `false` | Create one ConfigMap per Grafana dashboard for the Grafana sidecar. |
| `metrics.dashboards.namespace` | `""` | Namespace of those ConfigMaps; empty is the release namespace. The sidecar must watch it. |
| `metrics.dashboards.label` | `grafana_dashboard` | Label the sidecar selects by. |
| `metrics.dashboards.labelValue` | `"1"` | Value of that label. |
| `metrics.dashboards.folder` | `ACH` | Grafana folder annotation. |
| `metrics.dashboards.labels` | `{}` | Extra labels on the ConfigMaps. |

## `shutdown`

How the gateway, the forwarder and platform-api stop on a rollout.

| Key | Default | Purpose |
|---|---|---|
| `shutdown.drainSeconds` | `10` | Wait before stopping, so the pod leaves its Service first. |
| `shutdown.timeoutSeconds` | `120` | Time open requests get to finish. The pod grace period is the sum of both plus 5. |

## `extraEnv`

| Key | Default | Purpose |
|---|---|---|
| `extraEnv` | `[]` | Environment variables added to the operator, platform-api, the forwarder, the gateway, content-service and the migration Job. The classifier gets none. |

Settings with no value of their own are set here:

| Variable | Required | Read by | Value |
|---|---|---|---|
| `ACH_BASE_URL` | yes | operator, platform-api, forwarder, content-service | Public origin of the install. |
| `ACH_DB_URL` | yes | operator, platform-api, forwarder, content-service, migration Job | Postgres connection URL. |
| `ACH_REDIS_ADDR` | yes | platform-api, forwarder, content-service | `host:port` of Redis or Valkey. |
| `ACH_REDIS_PASSWORD`, `ACH_REDIS_TLS`, `ACH_REDIS_DB` | no | same | Redis password, TLS switch and database. |
| `ACH_DEX_ISSUER_URL`, `ACH_DEX_CLIENT_ID`, `ACH_DEX_CLIENT_SECRET` | yes | platform-api | The Dex client ACH signs users in with. |
| `ACH_OAUTH_ACCESS_TTL` | no | platform-api | Access token lifetime; default `1h`, minimum `1m`. |
| `ACH_OAUTH_REFRESH_TTL` | no | platform-api | Refresh token lifetime; default `720h`, minimum `1h`. |
| `ACH_DISABLE_UI_WRITES` | no | platform-api | `true` refuses Environments created from the console. |
| `ACH_PLUGIN_MAX_SIZE_MIB`, `ACH_SKILL_MAX_SIZE_MIB` | no | operator | Largest plugin and skill archive fetched; default `50`. |
| `ACH_ORPHAN_CLEANUP_INTERVAL` | no | operator | Time between orphan-key cleanups; default `1h`, minimum `5m`. |
| `ACH_ORPHAN_CLEANUP_DRY_RUN` | no | operator | `true` logs the keys it would revoke and revokes none. |
| `ACH_ORPHAN_CLEANUP_MAX_REVOKE` | no | operator | A cleanup with more candidates than this revokes none; default `10`. |

## `initContainers`

| Key | Default | Purpose |
|---|---|---|
| `initContainers` | `[]` | Init containers added to the operator, platform-api, forwarder, gateway and standalone content-service Deployments, before the component's own. The migration Job and the classifier get none. |
