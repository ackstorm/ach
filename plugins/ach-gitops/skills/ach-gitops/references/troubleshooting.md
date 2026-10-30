# Troubleshooting ACH resources

Start with the conditions; the message usually names the exact entry.

```bash
kubectl get <kind> <name> -n <ns> -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
kubectl describe <kind> <name> -n <ns>
ach-cli admin list <kind>     # environments, plugins, prompts, artifacts, skills, marketplaces, skill-marketplaces, bips
```

Check `status.observedGeneration` against `metadata.generation`: if it lags, the operator
has not processed the latest spec yet (or the GitOps tool has not applied it). Confirm
the GitOps sync succeeded before debugging ACH.

## Admission errors (apply rejected)

| Message contains | Fix |
|------------------|-----|
| `must include the subobject matching spec.type` | `type: gitlab` needs a `gitlab:` block (and so on) |
| `refresh.maxStaleness is REQUIRED` | Add `spec.refresh.maxStaleness` |
| `refresh.interval must be <= refresh.maxStaleness` | Lower `interval` or raise `maxStaleness` |
| `type=http requires scope=object` | An `http` Artifact cannot be `directory` |
| `authorizedTeams must contain at least one team` | Add a team alias |
| `guardrails entries must be unique` | Remove the duplicate |
| `forwardIdentityJWT is REQUIRED` | Set it explicitly, `true` or `false` |
| `consentBroker and consentAudience are set together` | Set both or neither |
| `consentBroker is only valid for MCPServer targets` | Remove it from the `A2AAgent` policy |
| `LiteLLMConnection name must be 'default'` | Do not manage this CR from Git; see `identity.md` |
| pattern / should match `^[^/\\?#%...` | A name in an Environment list contains `/`, whitespace, `?`, `#` or `%` |

## Content kinds: `SourceReachable=False`

| Reason | Cause | Fix |
|--------|-------|-----|
| `Unauthorized` | Private repo without `authSecretRef`, wrong token, missing Secret or Secret key, **or** a repo/`ref` that does not exist (git cannot tell them apart) | Check the repo and `ref` exist; add `authSecretRef`; check the Secret is in the CR's namespace and has the key (`GITHUB_TOKEN`/`GITLAB_TOKEN`/`BITBUCKET_TOKEN` when `key` is omitted) |
| `NotFound` | Object, key or ref missing upstream | Fix `ref`, `path`, bucket key or URL |
| `UpstreamInvalid` | Fetched, but not usable: Plugin without `.claude-plugin/plugin.json`; Skill without a valid `SKILL.md` (frontmatter `name` kebab-case <= 64, `description` 1-1024); `path` missing or escaping the repo; unsafe archive entries (`..`, absolute paths, hardlinks, devices, out-of-tree symlinks); not gzip where a `.tar.gz` was expected | Point `path` at the real root; fix the upstream content |
| `PluginTooLarge` | Filtered plugin or skill above the operator size cap (50 MiB default) | Slim the upstream, or raise the cap in the chart |
| `Unreachable` | Network error, DNS, timeout, upstream 5xx | Usually transient; retries with backoff. Persistent: egress, proxy, host name |
| `StaleCacheExpired` | Fetches kept failing longer than `maxStaleness`; the content is no longer served (hydrate 503 `stale_cache_expired`) | Fix the underlying fetch error; raise `maxStaleness` only if long outages are acceptable |

After fixing a Secret or the upstream, `ach-cli admin refresh <kind> <name>` retries now.

## Marketplaces

| Symptom | Cause | Fix |
|---------|-------|-----|
| `Synced=False` `InvalidConfig` | A `filters` pattern is not valid RE2 | Fix the regex |
| `Synced=False` `UpstreamInvalid` `filters.include matched zero plugins` | Include patterns match nothing | Check plugin names in `marketplace.json`; patterns are anchored at the start |
| `Synced=False` `UpstreamInvalid` on parse | No `.claude-plugin/marketplace.json` in the repo, or malformed JSON | Fix the repo, or point an `http`/`s3`/`gcs` source at the file itself |
| `Synced=True` but message `stage-2: N plugin(s) failed` | Individual entries failed (`UpstreamInvalid`: entry `source` is not the plugin root; `UnsupportedPluginSource`: e.g. `npm`); `DuplicateName` for a repeated name | Fix the entry in `marketplace.json`; the other plugins keep serving |
| SkillMarketplace `Synced=True`, `Skills` 0 | Wrong `path` (unset means `skills`), or each `SKILL.md` frontmatter `name` does not equal its folder name | Set `path` to the parent of the skill folders, `"."` for repo root |
| A skill missing from an otherwise good SkillMarketplace | That folder's `SKILL.md` failed discovery (name, description, nesting deeper than one level) | Fix its frontmatter, or reference it with a standalone `Skill` |

## Environment

| Condition / reason | Cause | Fix |
|--------------------|-------|-----|
| `ExecutionResourcesResolved=False` `ResourceUnresolved` | A runtime name is not in LiteLLM (see `status.unresolvedRuntime`), or a plugin/skill has never been fetched (see `status.unresolvedContextPlugins` / `unresolvedContextSkills`) | Correct the name with `ach-cli admin list models|mcp|a2a|guardrails`; register it in LiteLLM; fix the Plugin/Skill/marketplace first; use `name@marketplace` for marketplace items |
| same, for a model that works for inference | The name is a LiteLLM `model_group_alias`, which is not in LiteLLM's model catalog | List the underlying model name |
| same, message starts `snapshot stale (LiteLLM unreachable)` | The operator cannot read LiteLLM | Check `kubectl get llmconn default` |
| `AccessGroupSynced=False` `UnresolvedReferences` | An MCP server, A2A agent, team alias or guardrail is not in LiteLLM; the message lists which | Fix the name or create it in LiteLLM (ACH never creates teams or guardrails) |
| `AccessGroupSynced=False` `ShellTeamFailed` with a licence hint | `guardrails` set on a LiteLLM without an Enterprise licence | Empty `guardrails`, or license the proxy |
| `AccessGroupSynced=False` `ShellTeamFailed` (other) | LiteLLM refused to create or repair the `ach-env-<name>` team | Read the message; check LiteLLM health and the master key |
| `AccessGroupSynced=False` `AccessGroupCreateFailed` / `AccessGroupUpdateFailed` / `ResolveFailed` | LiteLLM API error during the write or a lookup | Usually transient; persistent means LiteLLM itself is failing |
| `AccessGroupSynced=False` `MirrorUnconverged` | LiteLLM's team/group mirror would not converge after repair; further repairs paused | Investigate LiteLLM; any spec change re-arms the repair |
| `BudgetSynced=False` `TagWriteFailed` | Writing the `environment:<name>` tag budget failed (e.g. a LiteLLM that refuses tags) | Read the message; check the LiteLLM edition and health |
| `Available=False` `SubConditionsNotReady` | One of the two sub-conditions is False | Fix that one |
| `Available` `PendingSubConditions` | First reconcile still running | Wait a few seconds |

What still works while an Environment is unhealthy: `AccessGroupSynced=False` blocks only
**new** `ek-` keys (503 `not_ready`); existing keys, hydrate and forwarded traffic keep
the last good grants. A typo in a guardrail name therefore leaves existing keys running
without that guardrail.

## Users report access problems

| Symptom | Likely cause |
|---------|--------------|
| `env list` does not show the Environment | The user is in none of its `authorizedTeams`, or the team alias in the CR does not match LiteLLM (check the conditions) |
| Grant is `Synced` but some requests 403 for a few minutes | LiteLLM caches access-group grants per replica (600 s default) |
| A brand-new user reaches nothing for a few minutes | Their personal shell team is attached on the next Environment reconcile |
| `403 unauthorized_team` on an `ek-` key | The key's owner is no longer in an authorized team; the key is now invalid |
| MCP backend sees no identity / tool list empty | No BackendIdentityPolicy, `forwardIdentityJWT: false`, the caller uses an `ek-` key, or LiteLLM lacks `extra_headers: ["authorization"]` for that server |
| Plugin or skill missing after hydrate | It is listed bare but lives in a marketplace (needs `@marketplace`), or the user has not re-hydrated since the change |
| Budget 429 `Budget has been exceeded! Tag=environment:<name>` | The Environment's pooled `budget` is spent; raise `maxBudget` (takes ~10 s to a couple of minutes) or wait for the reset |
