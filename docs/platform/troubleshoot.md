# Troubleshoot an ACH install

Start from the symptom. A component that does not start names the missing or
wrong setting in its first log lines:

```bash
kubectl -n ach get pods,jobs
kubectl -n ach logs deploy/ach-platform-api --tail=50
kubectl -n ach logs deploy/ach-forwarder --tail=50
kubectl -n ach logs deploy/ach-operator -c manager --tail=50
kubectl -n ach logs deploy/ach-operator -c content-service --tail=50
kubectl -n ach describe pod <pod>          # a missing Secret, an image that does not pull
```

For a resource such as an Environment, read its conditions:

```bash
kubectl -n ach describe environment <name>
ach-cli admin list environments
```

## Render and install failures

| Message | Cause | Do |
|---|---|---|
| `profile="…" is no longer supported` | A `profile` key in the values | Delete it |
| `platformApi.admins is a map` | `platformApi.admins` is a list | Move the emails to `platformApi.admins.users` |
| `litellmConnection.endpoint is required`, `litellmConnection.masterKeySecretRef.name is required` | An empty `litellmConnection` value | Fill [`litellmConnection`](connect-litellm.md#point-ach-at-litellm) |
| `security.keyEncryptionKey.secretRef.name is required`, and the same for `credentialHashPepper` | An emptied Secret reference | Restore the names; the defaults work |
| `platformApi.observability.projectIds is required with clickhouseUrl`, `…secretName is required with clickhouseUrl` | Observability half configured | Set all three, or clear `clickhouseUrl` |
| `classifier.secretName is required with classifier.enabled`, and the same for `clickhouseUrl` and `modelUrl` | Classifier half configured | Set them, or disable the classifier |
| `no matches for kind "ServiceMonitor"` or `"PrometheusRule"` | The Prometheus Operator is not installed | Install it, or leave `metrics.serviceMonitor` and `metrics.prometheusRule` disabled |
| `no matches for kind "LiteLLMConnection"` | One of your manifests creates that resource in the same release | Remove it; the operator creates `LiteLLMConnection/default` |
| Helm times out during `pre-install` or `pre-upgrade` | The Job `ach-migrate-<revision>` fails | Read `kubectl -n ach logs job/ach-migrate-<revision>`. Usual causes: `ACH_DB_URL` is not in the top-level `extraEnv`, wrong credentials, or Postgres cannot be reached |

## Pods that do not start

| In the log or the Pod events | Cause | Do |
|---|---|---|
| `ACH_BASE_URL required`, `ACH_BASE_URL must be http(s)://` | Not set, set in one component's `extraEnv` only, or without a scheme | Set the full origin in the top-level `extraEnv` |
| `config: ACH_DB_URL is empty or unset`, or the same for `ACH_REDIS_ADDR`, `ACH_DEX_ISSUER_URL`, `ACH_DEX_CLIENT_ID`, `ACH_DEX_CLIENT_SECRET` | The variable is missing | Add it to the top-level `extraEnv` |
| An error while parsing the database URL | A short URL | Use the full form: `postgres://user:password@host:5432/db?sslmode=…` |
| `oidc.NewProvider: …` | platform-api cannot read Dex | The issuer must be reachable from the Pod and equal Dex's `issuer` exactly |
| `ACH_KEY_ENCRYPTION_KEY decoded to N bytes: must be 32 bytes` | The key Secret has the wrong content | Store `openssl rand -base64 32` in it |
| `…the placeholder value` | A template Secret was never filled | Put real random values in both Secrets |
| `CreateContainerConfigError`, `secret "…" not found` | A Secret named in the values or in `extraEnv` does not exist in the namespace | Create it, or correct the name and key |
| Forwarder: `jwt secret LoadOnce (refuse-to-start)` | The operator has not created `ach-jwt-signing-keys` yet | Normal for a minute on a first install. If it lasts, the operator is failing: read its log |
| `ACH_TEAMS_DEFAULT: at least one default team required` | `platformApi.teams.default` is empty | List at least one team |
| `credential headers: authorization is not configurable`, `…mode must be resolve or passthrough`, `…listed twice` | A wrong entry in `forwarder.headers` or `platformApi.headers` | Correct the list |
| `platform-api only resolves (passthrough is a forwarder mode)` | `passthrough` in `platformApi.headers` | Remove it there |
| `ACH_TRUSTED_IDP_AUDIENCES: a non-empty JSON list is required` | `forwarder.trustedIdP.issuer` without `audiences` | Add the audiences, or clear the issuer |
| `…is Enterprise configuration: set Helm enterprise.enabled=true` | An Enterprise variable in `extraEnv` with Enterprise off | Remove it, or [enable Enterprise](enable-enterprise.md) |
| The Pod runs, printed the command help and stopped | Custom `args` without the mode | Keep the mode (`operator`, `platform-api`, …) as the first argument |

## Sign-in failures

| Symptom | Cause | Do |
|---|---|---|
| Dex shows `Unregistered redirect_uri` | The Dex client lacks ACH's address | Add `https://ach.example.com/platform/oauth/as-callback` exactly |
| `no refresh_token in the Dex response` | The Dex connector issues no refresh token | Use a connector that does |
| `default team unreachable` on the first sign-in after an install | The operator has not created the team `default` yet, or a team in `platformApi.teams.default` does not exist in LiteLLM | Try again after a minute; create your own default teams in LiteLLM |
| `this browser did not start the authorization request` | The sign-in link was opened in another browser, cookies are blocked, or a proxy removes `Cookie` or `Set-Cookie` on `/platform/oauth/` | Start again from the tool, in the same browser; correct the proxy |
| `invalid_redirect_uri` when a web application registers | Its host is not allowed | Add it to [`platformApi.oauth.redirectHosts`](configure-sso.md#oauth-redirect-hosts) |
| A user signs in and sees no Environment | See [Users without access](#users-without-access) | |
| A group mapping never applies | The sign-in carries no `groups`, or the team does not exist in LiteLLM | Configure groups on the Dex connector; create the team |
| Everyone is signed out after an upgrade | One of the [values that must not change](upgrade.md#values-that-must-not-change) changed | Restore the old value |
| `ach-cli login` refuses an `http://` address | ACH is served without TLS | Use `https`. For a local test only, set `ACH_INSECURE=1` |

## Verification failures

| Check that fails | Meaning |
|---|---|
| `kubectl -n ach get litellmconnection default` is not `True` | The operator cannot reach LiteLLM, or the master key is wrong. Read the operator log |
| `/.well-known/oauth-authorization-server` or `/.well-known/jwks.json` answers 404 or 5xx | The request does not reach the forwarder: check the Ingress, and that the forwarder is ready |
| `/.well-known/opencode` answers 404 | Without the gateway, the rule that sends this path to platform-api is missing |
| `/` answers `console not built` | An image built without the console. Use the released image |
| `ach-cli whoami` works and `ach-cli environment list` is empty | See [Users without access](#users-without-access) |

## Symptoms after install

| Symptom | Cause | Do |
|---|---|---|
| Model requests answer 401 for every user, and the forwarder logs `key material decrypt failed` | The encryption key changed, or platform-api and the forwarder read different ones | Restore the key. If it is lost, users sign in again and environment keys are created again |
| `/ui`, `/key/list` or `/health` on the ACH address show the ACH console | ACH does not serve LiteLLM's own pages | Use LiteLLM's own host |
| Creating an environment key answers 403 `key_limit_reached` | `platformApi.userDefaults.maxKeys` is 0, or the user reached their limit | `ach-cli admin users limits <email> --max-keys N`, or raise the default |
| Requests answer 422 `Budget has been exceeded! Tag=user:…` | The user's ceiling | `ach-cli admin users budget <email> --max-budget N`. The chart default applies to new users only |
| Requests answer 422 `Budget has been exceeded! Tag=environment:…` | The Environment's budget is spent | Raise the budget of the Environment, or wait for it to reset |
| A new grant takes minutes to work | LiteLLM caches grants for up to about 10 minutes | Wait |
| Dashboards do not appear in Grafana | The Grafana sidecar does not watch their namespace | Set `metrics.dashboards.namespace`, or widen the sidecar's search |

## Resources

After you correct a resource, check that the operator has read it:
`status.observedGeneration` equals `metadata.generation`. With Flux or Argo
CD, first confirm that the change was applied.

### Admission errors

The API server refuses the resource and prints one of these:

| Message contains | Do |
|---|---|
| `must include the subobject matching spec.type` | Add the block named by `type`: `type: gitlab` needs `gitlab:` |
| `refresh.maxStaleness is REQUIRED` | Add `spec.refresh.maxStaleness` |
| `refresh.interval must be <= refresh.maxStaleness` | Lower `interval` or raise `maxStaleness` |
| `type=http requires scope=object` | An `http` Artifact cannot be a directory |
| `authorizedTeams must contain at least one team` | Add a team |
| `forwardIdentityJWT is REQUIRED` | Set it to `true` or `false` |
| `LiteLLMConnection name must be 'default'` | Do not create this resource; the operator does |

### `SourceReachable=False`

A Plugin, Skill, Prompt or Artifact could not be fetched. The reason says why:

| Reason | Cause | Do |
|---|---|---|
| `Unauthorized` | A private repository without credentials, a wrong token, a missing Secret, or a repository or `ref` that does not exist. Git cannot tell these apart | Check that the repository and `ref` exist; add `authSecretRef`; check that the Secret is in the resource's namespace |
| `NotFound` | The file, key or `ref` is missing at the source | Correct `ref`, `path` or the address |
| `UpstreamInvalid` | Fetched, but unusable: a plugin without `.claude-plugin/plugin.json`, a skill without a valid `SKILL.md`, a wrong `path`, or an unsafe archive | Point `path` at the real root, or correct the source |
| `PluginTooLarge` | Larger than the limit, 50 MiB by default | Make the source smaller, or raise `ACH_PLUGIN_MAX_SIZE_MIB` or `ACH_SKILL_MAX_SIZE_MIB` |
| `Unreachable` | A network error or a failing source | Usually passes; the operator retries. If it lasts, check outbound access and the host name |
| `StaleCacheExpired` | Fetching failed for longer than `maxStaleness`, and ACH stopped serving the content | Correct the fetch error |

`ach-cli admin refresh <kind> <name>` fetches again at once.

### Marketplaces

| Symptom | Cause | Do |
|---|---|---|
| `Synced=False`, reason `InvalidConfig` | A `filters` pattern is not a valid regular expression | Correct it |
| `Synced=False`, `filters.include matched zero plugins` | The patterns match nothing | Compare them with the plugin names in `marketplace.json`; a pattern matches from the start of the name |
| `Synced=False`, reason `UpstreamInvalid` | The repository has no `.claude-plugin/marketplace.json`, or it is not valid JSON | Correct the repository |
| `Synced=True`, message `stage-2: N plugin(s) failed` | Some entries failed; the others are served | Correct those entries in `marketplace.json` |
| A SkillMarketplace is `Synced=True` with 0 skills | A wrong `path` (`skills` when unset), or a `SKILL.md` whose `name` differs from its folder | Set `path` to the folder that holds the skill folders; `"."` for the repository root |

### Environment conditions

An Environment is `Available` when `ExecutionResourcesResolved` and
`AccessGroupSynced` are both true.

| Condition and reason | Cause | Do |
|---|---|---|
| `ExecutionResourcesResolved=False`, `ResourceUnresolved` | A model, MCP server or agent is not in LiteLLM, or a plugin or skill was never fetched. `status.unresolvedRuntime` and `status.unresolvedContextPlugins` name them | Compare with `ach-cli admin list models` (or `mcp`, `a2a`). Write a marketplace item as `name@marketplace` |
| The same, for a model that answers requests | The name is a LiteLLM model alias | List the model the alias points to |
| The same, with `snapshot stale (LiteLLM unreachable)` | The operator cannot read LiteLLM | Check `kubectl -n ach get litellmconnection default` |
| `AccessGroupSynced=False`, `UnresolvedReferences` | An MCP server, agent, team or guardrail is not in LiteLLM; the message lists them | Correct the name, or create it in LiteLLM |
| `AccessGroupSynced=False`, `ShellTeamFailed` | LiteLLM refused the Environment's team. With guardrails, LiteLLM has no Enterprise licence | Remove `guardrails`, or license LiteLLM; otherwise read the message |
| `AccessGroupSynced=False`, `AccessGroupCreateFailed` or `MirrorUnconverged` | LiteLLM failed during the write | Usually passes. If it lasts, LiteLLM itself is failing |
| `BudgetSynced=False`, `TagWriteFailed` | Writing the Environment's budget to LiteLLM failed | Read the message |
| `Available=False`, `SubConditionsNotReady` | One of the two conditions above is false | Correct that one |

While `AccessGroupSynced` is false, new environment keys are refused. Existing
keys, installs and requests keep the last grants that worked.

### Users without access

| Symptom | Likely cause |
|---|---|
| `ach-cli environment list` does not show an Environment | The user is in none of its `authorizedTeams`, or a team name there does not match LiteLLM |
| Access is lost after a team was renamed in LiteLLM | `authorizedTeams` holds team aliases: change both together. Never rename `default` |
| A new user reaches nothing for a few minutes | Their access is attached when the Environment is next processed |
| An environment key answers 403 `unauthorized_team` | The key's owner left every authorized team. The key no longer works |
| A plugin or skill is missing after an install | It is listed without `@marketplace`, or the user has not updated since the change |

## Related

- [Troubleshoot ach-cli](../using/troubleshoot.md) for the developer's side.
- [Helm values](../reference/helm-values.md)
