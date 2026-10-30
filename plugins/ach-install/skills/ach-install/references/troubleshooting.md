# Troubleshooting an ACH deployment

Start from the symptom. Most startup failures name the missing or bad setting
in the first log lines:

```bash
NS=ach
kubectl -n $NS get pods,jobs
kubectl -n $NS logs deploy/ach-platform-api --tail=50
kubectl -n $NS logs deploy/ach-forwarder --tail=50
kubectl -n $NS logs deploy/ach-operator -c manager --tail=50
kubectl -n $NS logs deploy/ach-operator -c content-service --tail=50   # sidecar topology
kubectl -n $NS describe pod <pod>                                     # mount / image errors
```

## Contents

- Render and install failures
- Pods that will not start
- Login failures
- Verification failures
- Runtime symptoms after install

## Render and install failures

| Message | Cause | Fix |
|---------|-------|-----|
| `profile="..." is no longer supported` | A `profile:` key in the values | Delete it; there is one topology. |
| `litellmConnection.endpoint is required ...` / `masterKeySecretRef.* is required` | Empty LiteLLM block | Fill `litellmConnection`. |
| `security.*.secretRef.* is required` | Blanked Secret reference | Restore the names (defaults are fine). |
| `platformApi.observability.projectIds is required with clickhouseUrl` / `secretName is required ...` | Enterprise observability half set | Set both or clear `clickhouseUrl`. |
| `classifier.secretName/clickhouseUrl/modelUrl is required with classifier.enabled` | Classifier half set | Fill them or disable it. |
| `no matches for kind "ServiceMonitor"` / `"PrometheusRule"` | Prometheus Operator CRDs missing | Install them or keep `metrics.serviceMonitor`/`prometheusRule` disabled. |
| `no matches for kind "LiteLLMConnection"` | A manifest of yours creates that CR in the same release | Remove it; the operator creates `LiteLLMConnection/default`. |
| Helm times out on `pre-install`/`pre-upgrade` | `ach-migrate-*` Job failing | `kubectl logs job/ach-migrate-<rev>`: usually `ACH_DB_URL` missing (must be in top-level `extraEnv`), wrong credentials, or Postgres unreachable. |

## Pods that will not start

| Log / event | Cause | Fix |
|-------------|-------|-----|
| `ACH_BASE_URL required` / `must be http(s)://` | Not set, or set only in a component's `extraEnv` | Top-level `extraEnv`, full origin with scheme. |
| `ACH_DB_URL` missing, or a panic parsing the DSN | DSN absent or short form | Full DSN `postgres://user:pass@host:5432/db?sslmode=...`. |
| `ACH_REDIS_ADDR` missing | Not set | `host:port` in `extraEnv`. |
| `ACH_DEX_ISSUER_URL` / `_CLIENT_ID` / `_CLIENT_SECRET` missing | Dex settings absent | See `references/auth.md`. |
| `oidc.NewProvider: ...` | platform-api cannot fetch Dex discovery | Issuer must be reachable from the pod and equal Dex's `issuer` exactly (scheme, host, path). |
| `ACH_KEY_ENCRYPTION_KEY is not set` / `decoded to N bytes: must be 32 bytes` / `must be base64` | DEK Secret missing or wrong | `openssl rand -base64 32` into the Secret named by `security.keyEncryptionKey`. |
| `... still holds the placeholder value` | A template Secret was never filled | Put real random values in DEK and pepper. |
| `ACH_CREDENTIAL_HASH_PEPPER required` | Pepper Secret missing | Create it (`references/prerequisites.md`). |
| `CreateContainerConfigError`, `secret "..." not found` | A Secret referenced by values or `extraEnv` does not exist in the namespace | Create it, or fix the name/key. |
| forwarder: `load JWT signing keys: secret "ach-jwt-signing-keys" not found` | Operator has not minted it yet | Expected for a minute on first install. If it persists, the operator itself is failing: check its logs. |
| content-service restarting, JWT files missing | Same as above (its volume is optional) | Same. |
| `ACH_TEAMS_DEFAULT: at least one default team required` | `platformApi.teams.default: []` | List at least one team alias. |
| `ACH_GENAI_PROVIDER_NAME "..."` / `ACH_OPENCODE_EXTRA_PROVIDERS "..."` | Id breaks `^[a-z0-9][a-z0-9-]{0,63}$` (or `<name>-ach` exceeds 64) | Lowercase letters, digits, `-`. |
| `credential headers: authorization is not configurable` / `mode must be resolve or passthrough` / `listed twice` | Bad `forwarder.headers` / `platformApi.headers` | Fix the list. |
| `platform-api only resolves (passthrough is a forwarder mode)` | `passthrough` in `platformApi.headers` | Remove it there; keep it on the forwarder. |
| forwarder refuses: trusted IdP audiences required | `trustedIdP.issuer` without `audiences` | Add the audiences or clear the issuer. |
| `... is Enterprise configuration: set Helm enterprise.enabled=true` | An Enterprise-only env var in `extraEnv` while Enterprise is off | Remove it or enable Enterprise. |
| `ACH_ORPHAN_CLEANUP_DRY_RUN` parse error | Value is not a boolean | `true` or `false`. |
| Pod Running but the binary printed help and exited | Custom `args` without the mode | Keep the mode (`operator`, `platform-api`, ...) as the first arg. |

## Login failures

| Symptom | Cause | Fix |
|---------|-------|-----|
| Dex shows `Invalid redirect URI` | Dex client lacks `https://<ACH_BASE_URL>/platform/oauth/as-callback` | Add it exactly. |
| Login fails at the callback mentioning `offline_access` / no refresh token | Dex connector does not issue refresh tokens | Use a connector that does; allow `offline_access`. |
| `500 default_team_missing` on the very first login | Operator has not created team `default` yet, or a `teams.default` alias other than `default` does not exist in LiteLLM | Retry after the operator converges; create any custom default team in LiteLLM. |
| `400 this browser did not start the authorization request` | The login URL was opened in another browser/profile, cookies blocked, or a proxy strips `Set-Cookie`/`Cookie` on `/platform/oauth/*` | Retry from the tool in the same browser; fix the proxy. |
| `invalid_redirect_uri` registering a web client (e.g. claude.ai) | https redirect host not allowed | Add the host to `platformApi.oauth.redirectHosts`. |
| User signs in but sees no Environments | Not in any team the Environments authorize, or the Environment has not reconciled since they joined | Check `platformApi.teams`, the LiteLLM team, and `kubectl get env`. |
| SSO-group mapping never applies | ID token has no `groups` claim, or the team does not exist in LiteLLM (log `login: team does not exist`) | Configure the Dex connector's groups; create the team. |
| Everyone signed out after an upgrade | DEK, pepper, JWT keys, `ACH_BASE_URL` or `genai.providerName` changed | Restore the old value (`references/upgrade.md`). |
| `ach-cli login` refuses `http://` | Plain-http base | Use https, or `--insecure` / `ACH_INSECURE=1` for a local test. |

## Verification failures

| Check | Failing means |
|-------|---------------|
| `kubectl get llmconn default` not `READY=True` | LiteLLM endpoint unreachable from the operator, or the master key is wrong. Check the operator log and `curl` LiteLLM from inside the cluster. |
| `/.well-known/opencode` 404 | Request is not reaching `ach-gateway` (ingress rule), or per-service routing lacks the `/.well-known/opencode` → platform-api rule. |
| `/.well-known/jwks.json` 404 or 5xx | Forwarder not routed, or not Ready (JWT keys missing). |
| `GET /` answers `console not built` | A custom image built without the console; use the released image. |
| Environment `AccessGroupSynced=False reason=UnresolvedReferences` | A model, MCP server, A2A agent, guardrail or team named in it does not exist in LiteLLM. The message lists the names. |
| Environment with guardrails stuck with a 403 | Team guardrails need a LiteLLM Enterprise licence; remove `runtime.guardrails` or license LiteLLM. |
| `ach-cli whoami` works, `env list` empty | See "User signs in but sees no Environments" above. |

## Runtime symptoms after install

| Symptom | Cause | Fix |
|---------|-------|-----|
| LiteLLM 401 for every user; forwarder logs `key material decrypt failed` | platform-api and forwarder use different DEKs, or the DEK changed | Same Secret for both; affected users log in again, `ek-` keys are re-created. |
| `/ui`, `/key/list`, `/health` are 404 through ACH | By design | Use LiteLLM's own host. |
| An `ek-` key cannot be created: 403 `key_limit_reached` | `userDefaults.maxKeys` is 0 or reached | `ach-cli admin users limits <email> --max-keys N` or raise the default. |
| Requests refused with 429 `Budget has been exceeded! Tag=user:...` | The user's ceiling | `ach-cli admin users budget <email> --max-budget N`; the chart default only seeds new users. |
| Newly granted access takes minutes to work | LiteLLM caches access-group grants per replica (up to ~10 min) | Wait; nothing to fix in ACH. |
| Dashboards never appear in Grafana | Grafana sidecar does not watch the namespace | `metrics.dashboards.namespace` or the sidecar's search namespace. |
| Orphan reaper revoked nothing / logs `circuit_breaker` | More candidates than `ACH_ORPHAN_CLEANUP_MAX_REVOKE` | Inspect first with `ACH_ORPHAN_CLEANUP_DRY_RUN=true`, then raise the cap deliberately. |
