# Environment

An Environment bundles what a set of LiteLLM teams may use (runtime) and what gets
installed into their agent tool (context). Short name: `env`.

## Full example

```yaml
# Backend developers: library docs over MCP, the shared model tiers, and the
# review plugin from the internal marketplace. Read-only tools only.
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Environment
metadata:
  name: dev-backend
spec:
  description: |
    Backend dev: library docs (Context7), code-review plugin, PDF skill.
  notice: "Re-run ach-cli env hydrate after the weekly plugin update."
  authorizedTeams:
    - backend
    - platform
  runtime:
    models:
      - my-org.smart
      - my-org.fast
    modelGroups:
      - anthropic            # every model LiteLLM tags `anthropic`, now and later
    mcpServers:
      - mcp-context7
    mcpServerGroups: []
    a2aAgents: []
    agentGroups: []
    guardrails: []           # non-empty needs a LiteLLM Enterprise licence
  context:
    plugins:
      - code-review@my-org-plugins   # from PluginMarketplace my-org-plugins
      - caveman                      # standalone Plugin CR
    skills:
      - pdf@anthropic-skills         # from SkillMarketplace anthropic-skills
      - finops-audit                 # standalone Skill CR
    prompts:
      - backend-system-prompt        # Prompt CR
    artifacts:
      - service-templates            # Artifact CR
  budget:
    maxBudget: 200          # USD, pooled across every ek- key of this Environment
    budgetDuration: 30d
```

## Fields

| Field | Required | Meaning |
|-------|----------|---------|
| `spec.authorizedTeams` | yes, >= 1 | LiteLLM team **aliases** whose members may use the Environment |
| `spec.runtime` | no (defaults `{}`) | What LiteLLM lets callers reach |
| `spec.context` | no (defaults `{}`) | What hydrate installs |
| `spec.description` | no, <= 2048 chars | Catalog text shown by `ach-cli env list` (truncated) and `env describe` |
| `spec.notice` | no, <= 2048 chars | Shown only after `ach-cli env hydrate`; operational reminders |
| `spec.budget.maxBudget` | when `budget` is set, >= 0 | Spend ceiling in USD |
| `spec.budget.budgetDuration` | no | Reset window, e.g. `30d`; empty means it never resets |

Every list field defaults to `[]`, so omit what you do not use. An empty Environment
with only `authorizedTeams` is valid.

### runtime

| Field | Holds | Resolution |
|-------|-------|------------|
| `models` | LiteLLM `model_name` values | Exact string match. Provider-prefixed names (`openai/gpt-4o`) are allowed |
| `modelGroups` | LiteLLM model access-group tags (`model_info.access_groups`) | Passed to LiteLLM as-is, expanded at request time. Never resolved by ACH, so tag-granted models do not appear in hydrate output or the console |
| `mcpServers` | LiteLLM MCP `server_name` values | Must exist in LiteLLM. No `/` allowed: the name is the `/mcp/<name>` route |
| `mcpServerGroups` | MCP access-group tags (`mcp_access_groups`) | The operator expands them every reconcile; extras show in `status.expandedRuntime.mcpServers` |
| `a2aAgents` | LiteLLM A2A agent names | Must exist in LiteLLM. Same no-`/` rule as MCP |
| `agentGroups` | Agent access-group tags | Expanded like MCP groups, but stock LiteLLM does not store agent tags, so on a stock proxy this grants nothing |
| `guardrails` | LiteLLM `guardrail_name` values | Max 50, entries unique. See below |

A group tag that matches nothing is ignored, not an error. Tags widen automatically:
tagging a new model or server in LiteLLM grants it to every Environment naming the tag,
with no Git change (MCP and agents within the operator's 5-minute LiteLLM snapshot,
models immediately). Prefer explicit names when you need the Git history to be the
record of who can reach what.

A LiteLLM `model_group_alias` is not a model in LiteLLM's catalog. Listing an alias in
`models` works for inference but leaves `ExecutionResourcesResolved=False` forever, so
the Environment never reports `Available`. List the real model name instead.

### Guardrails

`guardrails` attaches LiteLLM guardrails to the Environment's `ek-` traffic. Know four
things before adding a name:

- **It requires a LiteLLM Enterprise licence.** Unlicensed, a non-empty list 403s when
  the operator writes it (`AccessGroupSynced=False`, the Environment never goes
  Available, new `ek-` keys cannot be minted) and again on every request those keys
  make. Empty is always safe.
- **Global `default_on` guardrails already run everywhere** with no licence and no ACH
  config. `ach-cli admin list guardrails` shows DEFAULT-ON; naming one here changes
  nothing except exposing you to the licence gate.
- **Coverage is `ek-` keys only.** People using their personal login reach the same
  models through the access group, which carries no guardrails.
- **Mode matters.** A `pre_call`/`post_call` guardrail inspects LLM completions, not
  `/mcp` traffic; MCP needs `pre_mcp_call` or `during_mcp_call`. The admin list shows
  each guardrail's MODE.

An unknown guardrail name is a fail-open hole in LiteLLM, so ACH reports it as
unresolved and blocks new `ek-` minting until you fix the name. Existing keys, hydrate
and traffic keep working, unprotected.

### context

| Field | Bare `name` resolves to | `name@marketplace` resolves to |
|-------|------------------------|-------------------------------|
| `plugins` | a `Plugin` CR | that `PluginMarketplace`'s plugin |
| `skills` | a `Skill` CR | that `SkillMarketplace`'s skill |
| `prompts` | a `Prompt` CR | (not supported) |
| `artifacts` | an `Artifact` CR | (not supported) |

Names may not contain `/`, `\`, `?`, `#`, `%` or whitespace. All referenced CRs must
live in the ACH namespace. A plugin or skill must not only exist but have content
fetched at least once; until then it is listed in `status.unresolvedContextPlugins` /
`status.unresolvedContextSkills` and the Environment stays not Available.

### authorizedTeams

Team aliases, as LiteLLM stores them (`ach-cli admin list teams`). ACH never creates
these teams: create them in LiteLLM (or let the chart's login enrolment add users to
them) first. An alias LiteLLM does not know makes `AccessGroupSynced=False` with
`UnresolvedReferences`.

What happens behind it, so you can reason about access:

- The operator writes one LiteLLM access group `ach-env-<environment>` holding the
  runtime grants, bound to every authorized team.
- It also creates a deny-all shell team `ach-env-<environment>`. Every `ek-` key for this
  Environment is minted into that shell team, which is what caps the key to this
  Environment only.
- A person's own login key lives in a per-user shell team; the operator attaches it to
  the access group of every Environment whose authorized teams the person belongs to.
  One person therefore reaches the union of all their Environments.

Hydration scopes what a client loads; `authorizedTeams` is the actual access control.

### budget

`budget` caps the **pooled** spend of every `ek-` key issued for this Environment, all
owners together. ACH writes it onto the LiteLLM tag `environment:<name>`, which the
forwarder stamps on each `ek-` request. It is independent of the per-user and per-key
ceilings: whichever is crossed first refuses the request (HTTP 429). It does not cap
people using their personal login.

- Enforcement is post-paid: the request that crosses the line is served, the next one
  is refused.
- `budgetDuration` follows LiteLLM's calendar grid: `30d` resets on the 1st of the
  month, `7d` on Monday, `1d` at midnight.
- Removing `spec.budget` from a live Environment does not remove the ceiling; it stays
  until the Environment is deleted. To lift it, set a high `maxBudget` instead.
- Without `budget`, no `BudgetSynced` condition is published at all.

## Status

| Condition | True means | Common False reasons |
|-----------|-----------|----------------------|
| `ExecutionResourcesResolved` | Every runtime name exists in LiteLLM and every plugin/skill has content | `ResourceUnresolved` (message lists counts and names) |
| `AccessGroupSynced` | The LiteLLM access group and shell team match the spec | `UnresolvedReferences`, `ShellTeamFailed`, `AccessGroupCreateFailed`, `AccessGroupUpdateFailed`, `ResolveFailed`, `MirrorUnconverged` |
| `Available` | Both of the above are True | `SubConditionsNotReady`, `PendingSubConditions` |
| `BudgetSynced` | The `environment:<name>` tag carries the budget | `TagWriteFailed` |

Useful status fields: `status.unresolvedRuntime.{models,mcpServers,a2aAgents,guardrails}`,
`status.unresolvedContextPlugins`, `status.unresolvedContextSkills`,
`status.expandedRuntime.{mcpServers,a2aAgents}` (granted only through a group).

What each condition blocks:

- `AccessGroupSynced=False` blocks minting **new** `ek-` keys (503 `not_ready`). Existing
  keys, hydrate and forwarded traffic keep working on the last good grants.
- `ExecutionResourcesResolved=False` only drops `Available`; LiteLLM gets the grants
  anyway.

## Patterns

- **Baseline plus add-ons.** One Environment everyone gets (`authorizedTeams: [default]`)
  with the daily tools, then one per department that lists only what it adds; grants
  are a union, so do not repeat the baseline.
- **Read-only vs read-write.** Keep `-ro` and `-rw` MCP servers in separate
  Environments and authorize the write one to fewer teams.
- **One Environment per service agent.** An automated agent gets its own Environment,
  its own team, a `budget`, and one `ek-` key; revoking it never touches people.
- **Multiple documents per file** are fine (`---`), grouped by audience, with a comment
  above each explaining who it is for and why.
