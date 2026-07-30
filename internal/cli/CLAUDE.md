# CLAUDE.md — `ach-cli` (internal/cli + cmd/ach-cli)

Navigation hub for the **user-facing CLI**. Read this before touching any
`internal/cli/**` or `cmd/ach-cli/**` code. The repo-root `CLAUDE.md` is the
project hub; this is the CLI subsystem hub. Keep it lean; update it IN THE SAME
COMMIT when CLI behavior/contracts change (same hygiene rule as the root doc).

> **Why this file exists:** the CLI's architecture (unified projection,
> adapter transforms, the local package manager) is non-obvious and we kept
> re-deriving it every session. This captures the hard-won detail + the
> external reference provenance so we stop losing it.

## What the CLI is

Two binaries are built from this tree:

| Binary | Built from | Purpose | k8s deps? |
|--------|-----------|---------|-----------|
| `ach`     | `cmd/ach` | long-running services (operator/platform-api/…) | yes |
| `ach-cli` | `cmd/ach-cli` | user CLI (login, env, repo/plugin/skill) | **NO** |

Both share `internal/cli/*`. `make build-cli` → `bin/ach-cli`.

### ⚠ Hard dependency boundary (the reason `internal/gitfetch` + `internal/contentkit` exist)

`ach-cli` MUST NOT import `k8s.io/*` or `sigs.k8s.io/controller-runtime`.
`sigs.k8s.io/yaml` IS allowed (used in `admin.go`). Go resolves imports
per-package, so any code shared with the operator must live in a package whose
WHOLE import closure is k8s-free:

- `internal/gitfetch` — the git clone/checkout/subtree/auth engine (shared with
  the operator; extracted from `internal/sources/git`).
- `internal/contentkit` — marketplace parse, skill discovery, plugin/skill
  verify, tar safety (extracted from the k8s-tainted `internal/controller/ach`).
- `internal/sourceserr` — k8s-free sentinel errors.

Boundary regression test: `go list -deps ./cmd/ach-cli` must show no
`k8s.io/api` / controller-runtime.

## Command surface

Four noun groups. `--target` is the platform selector everywhere.

```
# governed remote object (CR-defined, server-mediated)
ach-cli login | logout | whoami | config | admin | keys
ach-cli config add | list | show | use | remove | rename | rm-ek
ach-cli env list | describe <name> | hydrate <name> --target … [--global] | status | uninstall <name>

# local-first serverless package manager (no k8s, no CRD)
ach-cli repo   add <source> --name <n> [--token] [--auth bearer|oauth2] [--path] | list | remove | update
ach-cli plugin list [--repo] | install <name@repo>… --target … [--global] [--conflict …] [--dry-run] [--verbose] | uninstall [--dry-run] | update | outdated
ach-cli skill  list [--repo] | install <name@repo>… --target … [--dry-run] | uninstall [--dry-run] | update | outdated
```

`config rm-ek <label>` drops a stale local ek_ label after a server-side revoke (revoke-by-ekid can't auto-match the label). `keys list` shows the caller's own pk_ AND ek_ keys (TYPE column); defaults to `--status active` — revoked keys are hidden; use `--status all` (also `revoked`/`expired`) to include them. `--type pk|ek` filters by key type. `keys create <environment>` issues an ek_ (environment is POSITIONAL; `--name` optional, defaults to the env name) — pk_ keys are still not user-creatable (they come from `login`). But you CAN revoke your OWN keys: `keys revoke <pkid_…>` self-revokes a personal key caller-scoped (server `DELETE /platform/keys/{id}`, owner==caller, NOT admin-gated; `--force` to revoke the key authenticating the current session), `keys revoke <ekid_…>` revokes an env key. `keys prune` bulk-revokes old pk_ keys (keeps the newest `--keep N`, default 1; `--dry-run`/`--yes`; the active key is auto-skipped via the server's 409 active-key guard, never force-revoked).

`env*` = the governed path (platform-api → Dex → hydrate). `repo`/`plugin`/
`skill` = the local quick path. Files: `cmd/ach-cli/cmd/{env,repo,plugin,skill}.go`
(parents) + `pkgcmd.go` (shared install/uninstall/update/outdated RunE for plugin+skill).

**Read-only verbs (mirror the governed path's preview/drift affordances):**
`install`/`uninstall --dry-run` resolve + project (or classify) and print the
plan — written/removed nothing. `outdated` re-resolves each installed ref's SHA
(`manager.ResolveWithCache`, read-only — discards the stage) and reports
`up to date` vs `outdated`. Uninstall's act/preview share one classifier:
`manager.classifyUninstall` → `Uninstall` (acts) and `UninstallPlan` (reports the
per-file `remove`/`modify`/`skip`/`absent` verdict), so the `--dry-run` preview
can never drift from the real removal.

## Architecture: the UNIFIED projection engine

**Both** paths converge on `route.Project`:

- `env hydrate` → `internal/cli/hydrate/wiring.go` type-asserts the adapter to
  `route.RuleProvider` → `route.Project(adapter.ProjectionRules(), stageDir)`.
- local `plugin/skill install` → `manager.Project` (`localpkg/manager/manager.go`)
  → same `route.Project(adapter.ProjectionRules(), stageDir)`.

There is essentially ONE transform engine. The only real duplication is the
disk-write mechanics: `hydrate/commit.go` vs `manager/commit.go`. A fix in a
rule/transform corrects BOTH paths at once (this is how the `.toml`-leak and
`.mcp.json`-routing fixes landed in one change).

### The rule model — `internal/cli/adapter/route/`

`route.Rule{FromGlob, ToGlob, Merge, Transform}`. `route.Project` flow
(`route.go`):

1. For each staged file, `topLevel := parts[0]` of its source-relative path.
2. `matchRule(rules, topLevel)` → first matching rule (glob on FromGlob).
3. If `rule.Transform != nil`, run it: `(srcRel, in) -> (out, keys, err)`.
4. Write to `ToGlob` destination with `rule.Merge`.
5. Unrouted top-level kinds in `KnownComponentKinds` (`route/kinds.go`) are
   recorded as **dropped** (reported to the user); anything else is silently
   skipped (manifests, README, LICENSE).

`Merge` kinds (`adapter.MergeKind`): `MergeReplace` (file-owned, default),
`MergeDeep` (keyed JSON/TOML merge — settings/MCP), `MergeComposite`
(marker-bounded prose, e.g. `AGENTS.md`→`CLAUDE.md`).

`KnownComponentKinds`: `rules, commands, agents, skills, mcp, .mcp.json,
prompts, AGENTS.md, hooks`. **INVARIANT** (`kinds_test.go`): every FromGlob
first-segment across all adapters MUST appear here; add in the same commit.

### Containment + conflict policies (local installer only)

Enforced in `manager.Project`, NOT in the shared adapter rules:

- **Containment** (`projection_containment_test.go`): write NOTHING outside the
  target's dot-dir. Any project-root destination (the `AGENTS.md`→`CLAUDE.md`
  composite) is DROPPED — the local installer never touches the user's own root
  files. We deliberately DROP OpenPackage's `root/**/*` catch-all fallback.
- **Read rule**: our inputs start at `plugins/`…`skills/`; a repo-root
  `CLAUDE.md`/`README.md` is NOT a component. A `README.md` INSIDE a skill
  (`skills/<n>/README.md`) IS part of it and rides through.
- **`.md` command scoping**: claude/opencode commands are markdown; a multi-tool
  plugin shipping `commands/*.toml` (gemini format) must not leak `.toml` into
  `.claude/`/`.opencode/`. The `commands/**/*.md` glob enforces this.
- **Conflict** (`manager/conflict.go`): `--conflict` ∈
  {namespace(default), refuse, replace, skip}; only `MergeReplace` clashes
  count. `namespace` renames via `internal/cli/namespace.Leaf`.

### Credential safety — the `.gitignore` block (`internal/cli/gitignore`)

The projected adapter config carries the forwarder bearer / LiteLLM key in
plaintext (project-root `.mcp.json`, `.codex/config.toml`,
`.opencode/opencode.json`, `.gemini/settings.json`, `.pi/mcp.json`). mode `0600`
guards other local users; a `.gitignore` entry guards against an accidental
`git add`/commit. BOTH write paths call `gitignore.Ensure(projectRoot, entries)`
in PROJECT scope only (`--global` writes under the tool's global config dir, no repo):

- `env hydrate` → `commit.go` `step12bGitignore` (entries = `.ach/` + the
  top-level dir/file of every projected/written file).
- local `plugin/skill install`/`update` → `pkgcmd.go` `ensureProjectGitignore`
  (entries = the top-level dir/file of every installed file).

`gitignore.Ensure` maintains a marker-bounded block (`# BEGIN ach-cli …` / `#
END ach-cli`), preserving any pre-existing `.gitignore` verbatim and
accumulating the sorted, deduped union across runs (idempotent — no rewrite of a
byte-identical file). It ignores **whole hydrated dirs** (`.claude/`, …), not
just credential files, so e.g. `.claude/skills/` is also covered. Best-effort:
a write failure warns but never fails the command. `TopLevelEntry` maps a
written path to its pattern (`.claude/agents/x.md` → `.claude/`; `.mcp.json` →
`.mcp.json`). NOT removed on uninstall (an ignored absent dir is harmless;
removal would need cross-env reference counting). The hydrate engine guards
`toolRoot == ""` so direct-struct unit tests never pollute cwd.

## The 5 adapters + their transforms (`internal/cli/adapter/<id>/`)

Each adapter: `ID()`, `Aliases()`, `Detect()`, `RenderRuntime()` (governed-env
MCP/A2A runtime config), `ProjectionRules()` (the plugin→tool routing). The
`RenderRuntime` path (remote MCP via the ACH forwarder: `httpUrl`/`url` +
`x-ach-key` header) is **separate** from `ProjectionRules` (local plugin MCP
merge) — don't conflate them.

| input kind | claude-code | codex | gemini-cli | opencode | pimono |
|---|---|---|---|---|---|
| commands | `.md`→`.claude/commands` | `.md`→`.codex/prompts` | `.md`→`.gemini/commands` **(T→.toml)** | `.md`→`.opencode/commands` (T) | `.md`→`.pi/agent/prompts` |
| agents | →`.claude/agents` | `.md`→`.codex/agents/*.toml` (T) | →`.gemini/agents` | `.md`→`.opencode/agents` (T) | drop |
| skills | →`.claude/skills` | →`.agents/skills` | →`.gemini/skills` | →`.opencode/skills` | →`.pi/agent/skills` |
| mcp + `.mcp.json` | →`.mcp.json` project-root (T) | →`.codex/config.toml` (T) | →`.gemini/settings.json` (T) | →`.opencode/opencode.json` (T) | →`.pi/mcp.json` |
| rules | →`.claude/rules` | drop | drop | drop | drop |
| AGENTS.md | →`CLAUDE.md` (composite) | drop | →`GEMINI.md` (composite) | drop | drop |
| hooks | drop | drop | drop | drop | drop |

(T) = has a content Transform. `hooks` is dropped everywhere (documented gap,
asserted in e2e — not a bug).

### Global-scope root resolution (`--global`)

`--global` does NOT mean "$HOME-relative". Each agent CLI exposes its own
config-dir env var, and `ach-cli` honors it so installed content lands where
that tool actually reads it. One table drives both write paths:
`internal/cli/adapter/globalpath.go` (`globalScopes` + `RemapGlobalPath`).

| adapter | env var | shape | redirected prefix | NOT redirected |
|---|---|---|---|---|
| claude-code | `CLAUDE_CONFIG_DIR` | dir itself | `.claude/` | `.mcp.json`, `CLAUDE.md` |
| codex | `CODEX_HOME` | dir itself | `.codex/` | `.agents/skills/**` |
| gemini-cli | `GEMINI_CLI_HOME` | **parent** (`$VAR/.gemini/`) | `.gemini/` | `GEMINI.md` |
| opencode | `XDG_CONFIG_HOME` | **parent** (`$VAR/opencode/`) | `.opencode/` | — |
| pimono | `PI_CODING_AGENT_DIR` | dir itself | `.pi/agent/` | `.pi/mcp.json` |

Hard-won details — do NOT re-derive:

- **Gemini's var is `GEMINI_CLI_HOME`, and it is a PARENT.** `GEMINI_CONFIG_DIR`
  was proposed upstream (gemini-cli #2815) and never shipped. A `settings.json`
  written at `$GEMINI_CLI_HOME/settings.json` is silently ignored (#23622) — it
  must go to `$GEMINI_CLI_HOME/.gemini/settings.json`.
- **Pi's config dir defaults to `~/.pi/agent`, not `~/.pi`.** That is why the
  redirected prefix is `.pi/agent/` and why `.pi/mcp.json` is outside it.
  (`ackstorm/agent-profile` documents `~/.pi` — off by one level.)
- **Redirection is keyed on the PATH PREFIX, not the adapter.** `.agents/` is a
  cross-tool convention dir that `CODEX_HOME` does not own, so codex's skills
  stay under `$HOME/.agents/skills/`. One adapter can therefore write to two
  roots in a single run.
- **A relative env value is IGNORED** (falls back to `$HOME`). The real tools
  resolve it against their own cwd, which is not ach-cli's.
- **Anti-churn rule:** when an override resolves to the location the relative
  form already names (`XDG_CONFIG_HOME=$HOME/.config` — very common), the
  RELATIVE form is returned. Recorded destinations are what `Sync`'s
  set-difference compares, and Render (write) precedes Sync (delete), so
  flipping relative→absolute for the same file would make Sync delete what
  Render just wrote.
- **Recorded destinations MAY be absolute under `--global`.** Join them with
  `adapter.ResolveDest`, NEVER bare `filepath.Join`. Applies to
  `state.FileEntry.Target` and `store.FileRec.RelPath`.
- The remap MUST stay downstream of `route.Project` — its T-01-01 guard rejects
  absolute destinations and is what proves the path is `..`-free before a root
  is prepended.
- **Known ceilings:** `OPENCODE_CONFIG` (a file) and `OPENCODE_CONFIG_DIR` (a
  dir, additive per `paths.ts` but replacing per `global.ts` — verify against
  the real binary first) are NOT honored. Changing the var between runs without
  `env hydrate --sync` leaves the old files as orphans.
- `ach-cli` only READS these vars; it never sets them. If your shell does not
  export the var, `--global` behaves exactly as before.

### Transform provenance — WHO is the reference

These transforms are PORTS of two external sister projects. When changing a
transform, check it against its reference:

- **OpenPackage** — `github.com/enulus/openpackage`, TypeScript, **the
  authoritative + most mature reference** (40 targets, declarative
  `platforms.jsonc` + a `$`-operator DSL, hub-and-spoke through a *universal*
  format with `export`/`import` flows). Has: `claude`, `claude-plugin`, `codex`,
  `opencode`, `pimono` — **NOT gemini**. Our `codex.go` is a near-exact hand-port
  of its codex block (see `OPENPACKAGE-MAPPING` comments in `codex.go`).
- **ccplugin** — `~/workspace/local/ccplugin`, Python sister project, supports
  claude/opencode/**gemini**. `transforms.py` / `paths.py` / `mcp_config.py`.
  The ONLY reference for gemini (TOML commands, `Tool`-suffix tool names).

### Status vs the references (audited 2026-06)

**Faithful:** claude (no-op canonical), codex (field-lift + bearer/header
surgery match OpenPackage exactly).

**opencode MCP — CONVERTS per-entry shape (`opencodeMCPConvert`), NOT a bare
rename.** ⚠ Hard-won: OpenPackage's opencode export is just
`{mcpServers→mcp}`, and trusting it shipped invalid config — real opencode
(1.16.0) `ConfigInvalidError`: it validates each entry against a CLOSED
`local|remote` schema. **ccplugin's `transform_mcp_entry_to_opencode` is the
correct reference, not OpenPackage.** Conversion (schema
https://opencode.ai/docs/mcp-servers/, verified against the real binary):
stdio `{command,args,env}` → `{type:"local", command:[cmd,…args], enabled:true,
environment}`; remote `{url|type:streamable-http,…}` →
`{type:"remote", url, enabled:true, headers}`. Non-schema fields (description,
…) dropped; `type` normalized (streamable-http→remote); header/`environment`
values have env-var refs rewritten `${VAR}`→`{env:VAR}` (`convertEnvVarSyntax` —
opencode does NOT understand `${VAR}`, so a verbatim API-key ref would reach it
un-interpolated; ACH rewrites only the wrapper, never reads the secret).
**Lesson: validate adapter output against the real tool — OpenPackage ≠ ground
truth for opencode's strict schema.**

**Deliberate divergences (keep):**
- codex `codexAgentTOML` drops `mcp_servers` from agent frontmatter (OpenPackage
  lifts it) — security hardening WR-01 (no plugin-injected MCP registrations).
- opencode color→hex (`normalizeOpencodeColorField`) + command-frontmatter
  whitelist (`opencodeCommandFrontmatter`) — NOT in OpenPackage; added because
  opencode *strictly* validates `color` and chokes on claude's
  `argument-hint: [a] [b]` (invalid YAML). Verified empirically.
- containment: drop OpenPackage's `root/**/*` fallback.

**Closed gaps (hand-ported 2026-06-09 — user chose hand-port over full data-driven adoption):**
- ✅ **opencode agent tools** lowercase + rename map (`AskUserQuestion→question`,
  `NotebookEdit→notebook`, `ExitPlanMode→exitplan`) — `normalizeOpencodeToolName`
  in `opencode.go`. opencode's tool registry is lowercase; a PascalCase `Read`
  would not resolve. Verified live.
- ✅ **gemini commands → TOML**: `commands/**/*.md` → `.gemini/commands/**/*.toml`
  via `geminiCommandTOML` (description lifted, body→`prompt`, `$ARGUMENTS`→
  `{{args}}`); gemini-native `commands/*.toml` pass through. gemini-cli reads
  commands ONLY as TOML (https://geminicli.com/docs/cli/custom-commands/).

**Remaining GAPS (lower priority):**
- **gemini plugin MCP shape** — likely the SAME latent bug opencode had:
  `mcpDeepKeys` passes a plugin's `.mcp.json` entry verbatim into
  `.gemini/settings.json` `mcpServers`, but gemini-cli wants `httpUrl` for an
  HTTP server (a plain `url` = SSE). A streamable-http plugin MCP would project
  wrong. Not yet hit (no one installs gemini MCP); verify gemini-cli's schema
  before writing the transform. (RenderRuntime/governed-env already uses
  `httpUrl` correctly — only the local plugin-install path is suspect.)
- **gemini frontmatter strip** (`color`, `allowed-tools`) per ccplugin — gemini
  agents/skills still route verbatim; no strong reference, not clearly broken.

**Deliberately NOT doing** (fragile): OpenPackage/ccplugin body-regex rewrites
(`~/.claude/`→`~/.gemini/`, `Bash`→`BashTool` via word-boundary regex over
arbitrary markdown) — mutates prose, edge-case-prone.

## `internal/cli/localpkg/` — the local package manager

- `source/source.go` — `Parse(ref)` over `github:owner/repo[#ref]`,
  `git:https://…[#ref]`, local paths. Derives clone URL + default `AuthScheme`
  (GitLab host → `AuthBasicOAuth2`, else `AuthBearer`).
- `store/store.go` — local state under `~/.config/ach/local/`:
  `repos.json` (`hasToken:true` only — **token NEVER stored here**),
  `credentials.json` (mode `0600`, `{repo: token}`), `installed.json`
  (`{ref, repo, name, kind, target, resolvedSHA, files:[{relPath,hash}]}` — the
  files ledger drives clean uninstall; designed as a superset of an Environment
  CR for future GitOps export).
- `discover/discover.go` — repo capability detection (the 4 lenses:
  plugin-marketplace / skill-marketplace / direct-plugin / direct-skill),
  reusing `contentkit` parsers.
- `manager/manager.go` — install/uninstall/update orchestrator:
  `source → gitfetch → discover/contentkit verify → extract → route.Project →
  store`. `FetchCache` (`NewFetchCache`) memoizes git fetches per-invocation
  (keyed url+ref+subtree+scheme) — whole-repo cloned ONCE, then plugin/skill
  subtrees sliced from the cached tar (`contentkit.SliceSubtree`,
  `keepLeafDir=false` plugin / `true` skill). Measured 5.8× on the 17-plugin
  ackstorm install. `ResolveWithCache` is the cached entry; `Resolve` is the
  nil-cache wrapper.

## Build / test / gotchas

- Host has NO Go — `make build-cli`, `make test-unit`, `make qa-lint-changed`
  auto-route into the `ach-devtools` container. golangci is NOT on raw PATH; use
  `make qa-lint-changed` (don't call `golangci-lint` via `./scripts/dev.sh`).
- **Host `bin/ach-cli` reads host `~/.config/ach/local/`; the devtools
  container does NOT see host config.** Smoke-test the local package manager with
  the HOST binary, not inside the container.
- SPDX header on every new `*.go`: `// SPDX-License-Identifier: Apache-2.0`.
- Memory rule: smoke-test the built `ach-cli` before any release — the gates
  miss the localpkg path.

### Failure modes we have actually hit

- **`.mcp.json` not installing** ("doesn't install mcp"): root `.mcp.json`
  (Claude Code's standard MCP location, distinct from the `mcp/` dir) needs its
  own rule in every adapter → runtime-config deep-merge. For **claude** the
  target IS the project-root `.mcp.json` (the file claude actually reads — NOT
  `.claude/settings.json`); the local-installer containment filter
  (`manager.Project`) allows exactly that one root file. Fixed; pinned by
  `TestProject_RootMcpJson_Routed`.
- **`.toml` leaking into `.claude`/`.opencode` commands**: fixed by the
  `commands/**/*.md` glob (claude/opencode); gemini keeps `commands/**/*`.
- **`slice subtree "." empty`**: a whole-repo plugin entry (`source: "."`) must
  serve the cached repo tar directly, not slice — see the `path.Clean(Subtree)`
  switch in `resolvePluginMarketplace`.
- **Root-file overwrite**: containment drops project-root destinations; never
  overwrite the user's `CLAUDE.md`/`README.md`.

## Future: GitOps CR export (design note, not built)

`installed.json` is a superset of what an `Environment` CR needs (`repo`+
`resolvedSHA` = external-ref pin, `target` = adapter, `files` = manifest). A
future `ach-cli env export` can SERIALIZE local state → Environment/Plugin/Skill
CR YAML for gitops — this is a state serializer and does NOT require adopting
OpenPackage's universal-format import/export hub. The hub would only be needed to
*capture* arbitrary pre-existing tool configs (the `import` direction).
