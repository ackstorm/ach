# operator contract

`ach operator`: reconciles the ACH CRDs and is the sole writer of LiteLLM access groups, shell-team attachments and tag budgets. Read before changing `internal/controller/`.

## Budgets and the orphan-key reaper

Reconciles ACH CRDs; `Environment.spec.budget` → the `environment:<name>` tag budget (condition `BudgetSynced`; absent budget publishes no condition, and the §6.5 drain reaps the tag + its budget object unconditionally); hourly LiteLLM orphan-key reaper scoped to keys stamped `metadata.ach_issuer == ACH_BASE_URL` (required) — never another release's or foreign keys; managed set = every non-revoked row across `personal_keys`+`environment_keys` — suspended/expired/invalid `ek_` rows are never reaped, only a revoked row's lingering LiteLLM key is (that reap IS the revoke retry)

## Environment status (two axes)

- **Environment two-axis status**: `ExecutionResourcesResolved`
  (Plugin/Prompt/Artifact/**Skill** closed-set; `context.skills` is
  content-gated like plugins) + `AccessGroupSynced` (LiteLLM: names →
  IDs each reconcile, then `POST /v1/access_group`) — plus the per-Environment deny-all shell team (`ShellTeamFailed` when it cannot be provisioned/repaired). Composite `Available=True`
  rolls both up — that's what `ach-cli environment hydrate` / the demo gate on.
  `spec.runtime.{modelGroups,mcpServerGroups,agentGroups}` take LiteLLM
  access-group TAGS: model tags pass through to LiteLLM (never resolved by
  ACH, so absent from hydrate/console); MCP/agent tags are expanded by the
  operator from the LiteLLM snapshot into `runtime_*` + the access group
  (`status.expandedRuntime`); no-match tags are ignored; stock LiteLLM stores
  no `agent_access_groups` — see `references/litellm-permission-model.md` §17.
  `spec.runtime.guardrails` (LiteLLM guardrail names) **requires a LiteLLM
  Enterprise licence** — team-scoped guardrails are premium-gated, so on an
  unlicensed proxy a non-empty list 403s at attach (the Environment never goes
  Available) AND on every request from a key in that team. Empty is exempt;
  global `default_on` guardrails run ungated and need no ACH config at all.
  It resolves against LiteLLM
  like the other runtime names; an unresolved one fails `AccessGroupSynced`,
  blocking new `ek_` mints but not hydrate or forwarded traffic; while it is unresolved, removals
  still land (teams and models shrink to existing ∩ desired, an unresolved team leaves
  `assigned_team_ids`, an MCP or agent id leaves only when LiteLLM lists it and the spec drops it) and nothing is added. Guardrail coverage is **EK-only**. `ek_` keys live in the
  Environment's shell team and inherit its guardrails; `pk_` keys live in
  `ach-user-<email>` and reach the Environment through the access group,
  which carries no guardrail field, so **human CLI traffic to the same
  models is unguarded**. This is a known v1 limitation, not a bug.

## Skill content kind

- **Skill content kind**: a `Skill` CR (agentskills.io `SKILL.md` directory)
  mirrors **Plugin** end-to-end (fetch → `SKILL.md` Stage-2 validation gate →
  `skill/<name>.tar.gz` → `skills` projection → content-service
  `/content/skill/{name}` gzip). On hydrate it rides the plugin-mirrored stage
  root: extract to `<tmp>/skill/<name>`, nest under a synthetic `skills/<name>/`,
  then the EXISTING claudecode `skills/**/* → .claude/skills/**/*` rule projects
  it via `route.Project` (`projectSkills`). `SkillMarketplace` is a follow-up.

## BIP and Environment read path

- **BIP + Environment forwarder read-path (Postgres-as-SoT)**: operator
  projects BIPs/Environments → tables, emitting `NOTIFY ach_*_changed` from the
  same tx via `with_tx_notify`. The forwarder's `internal/forwarder/bipcache` +
  `internal/forwarder/envstore` each run a `db.Listener` + 5-min periodic
  refresh (LISTEN/NOTIFY is at-most-once on session loss). JWT mint reads the
  in-memory cache — no per-request Postgres/k8s hit.

## Plugin manifests and marketplace parsing

- **Claude Code plugin / marketplace schemas**: JSON Schemas at schemastore.org
  (narrative: code.claude.com/docs/en/plugin-marketplaces). The parser
  (`internal/controller/ach/marketplace_parse.go`) follows the real schema with
  one drift ack: `url`-Kind entries carry an optional `path` (→ `git-subdir`).
  Plugin manifests (`.claude-plugin/plugin.json`) are **optional** per the
  schema; the Stage-2 gate (`verifyPluginContents`, `marketplace_manifest.go`)
  accepts a plugin that has the manifest OR ≥1 convention component
  (`commands/`/`agents/`/`skills/`/`hooks/`/`output-styles/`/`themes/`/
  `monitors/`, or root `SKILL.md`/`.mcp.json`/`.lsp.json`). Only a tar with
  none of these fails `UpstreamInvalid`.

## SkillMarketplace discovery

- **SkillMarketplace discovery is convention-based, NOT index-based**: unlike
  `PluginMarketplace` (which parses `.claude-plugin/marketplace.json`), a
  `SkillMarketplace` fetches the repo as one tar.gz and tree-walks it
  (`skillmarketplace_discover.go` `discoverSkillsInTree`) for every directory
  one level under the skills-root (`spec.<git>.path`) whose
  `<dir>/SKILL.md` frontmatter `name` == `<dir>` (agentskills.io ships no index).
  For git sources an **unset `path` defaults to `skills`** (controller-side, in
  `skillMarketplaceSubPath`) — the dominant `anthropics/skills`-style monorepo
  layout; skill dirs at the **repo root** opt out with `path: "."`. The default
  is NOT a `+kubebuilder:default` on the shared `GitHubSource.Path` (that would
  wrongly fetch-narrow the OBJECT kinds Plugin/Artifact/Prompt/Skill).
  A `SkillMarketplace` is a **discovery** kind (like `PluginMarketplace`), NOT a
  narrow-at-fetch object: it strips `spec.<git>.path` before fetch (whole-repo
  tar, via `withoutGitPath`) and uses `path` only as the POST-FETCH skills-root
  tree-walk hint (`discoverSkillsInTree` + `sliceSkillSubtree` per skill). Each
  discovered skill is sliced into its own `skill-marketplace/<mkt>/<name>.tar.gz`
  and folded into the admin SKILLS inventory as `<skill>@<marketplace>` (mirrors
  the plugin merge).

## `spec.<git>.path` fetch narrowing

- **`spec.<git>.path` narrows at FETCH time for OBJECT kinds**: the shared
  per-provider fetchers honor `spec.path` — a **directory** narrows to that
  subtree's contents (git on-disk via `git.tarSubtree`; legacy REST via
  `sources.NarrowArchiveSubtree`), a single **file** returns its raw bytes
  (Prompt, Artifact `scope=object`). Applies to the served/hydrated objects:
  `Plugin`, `Skill`, `Artifact`, `Prompt`. The fetcher infers file-vs-dir from
  the path shape (Artifact `scope` is orthogonal — it only drives cache-file
  naming; a CR's `scope` should match its path target). DISCOVERY kinds
  (`PluginMarketplace`, `SkillMarketplace`) opt OUT via `withoutGitPath` and
  walk the whole repo. Symlinked / traversal / missing paths →
  `UpstreamInvalid`.

## Content pinning (`spec.<git>.commit`)

- Every git fetch (`gitprovider.FetchViaProvider`) resolves `ref` with
  `ls-remote` and reports it as `LatestUpstreamRev`; the served revision is
  `spec.<git>.commit` when set, else that head. `PriorRev` equal to the served
  revision is `NotModified`, so a pinned CR clones once per commit and then
  costs one `ls-remote` per refresh.
- `status.latestUpstreamRev` + condition `UpdateAvailable` (`NewerRevision` /
  `UpToDate`, present only on a pinned CR) are set by `applyUpdateAvailable` in
  the shared driver and both marketplace `markSyncedTrue`s. On `NotModified` the
  driver writes `external_refs.latest_upstream_rev` alone
  (`SetExternalRefLatestRev`), since the full upsert does not run.
- Marketplaces: the catalog's served commit lands in `status.upstreamRev`, and
  `local-path` entries are fetched at that commit (`buildGitSpecForEntry` reads
  `mp.Status.UpstreamRev`), so they are the same snapshot as `marketplace.json`.
  External entries keep their own `sha`/`ref`; their heads are not tracked.
- Revisions reach the admin inventory through `external_refs` (standalone kinds,
  read by scalar subquery in `db.List{Plugins,Skills,Prompts,Artifacts}`) and the
  `marketplaces` / `skill_marketplaces` `upstream_rev` + `latest_upstream_rev`
  columns. Content-service, hydrate and Environments never see a revision.
