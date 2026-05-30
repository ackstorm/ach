---
phase: 06-cli-foundation
plan: 04
type: execute
wave: 2
depends_on:
  - 06-01-cli-shared-internals
  - 06-03-ach-login-whoami-logout
files_modified:
  - cmd/ach/cmd/config.go
  - cmd/ach/cmd/config_test.go
  - cmd/ach/cmd/env.go
  - cmd/ach/cmd/env_test.go
  - internal/cli/render/render.go
  - internal/cli/render/render_test.go
  - internal/cli/render/doc.go
autonomous: true
requirements:
  - CLI-02
  - CLI-04
  - CLI-08
  - CLI-12

must_haves:
  truths:
    - "`ach config list` prints deployments table (name, URL, has-pk, ek-count) — masks pk_/ek_ to '<prefix>_****<last-4>' (CLI-04)"
    - "`ach config show [deployment]` prints one deployment block; masks pk_/ek_ unless --reveal is set; --reveal unmasks only the named deployment (D-05)"
    - "`ach config use <name>` sets `default:` to <name> and persists"
    - "`ach config remove <name>` deletes entry; refuses to delete the active default unless --force (D-05)"
    - "`ach config rename <old> <new>` renames the map key; preserves pk + ek map; updates default: if it was pointing at <old>"
    - "`ach env list` paginates `next_cursor` automatically (CLI spec §5.5) — issues N HTTP calls until next_cursor==nil"
    - "`ach env describe <name>` is two-call: GET /environments paginated until row found, POST /platform/hydrate for runtime+context; --metadata-only skips the second call (CLI-12)"
    - "`ach env describe` on 403 unauthorized_team renders `Runtime: (unavailable)` / `Context: (unavailable)` and exits 0 (CLI-12 graceful admin fallback)"
    - "`ach env describe` runtime entries surface `endpoint` (runtime.{models,mcpServers,a2aAgents}[].endpoint); context entries surface `downloadUrl` (context.{prompts,plugins,artifacts}[].downloadUrl) — canonical hydrate wire shape per CONTEXT.md"
    - "internal/cli/render exports FormatEkList alongside FormatConfigList/FormatConfigShow/FormatEnvList/FormatEnvDescribe/FormatIdentity — single source of truth for tabular formatters consumed by 06-05 env-keys list + 06-08 admin keys list"
    - "Every config + env subcommand exits 1 in synthetic mode EXCEPT `ach env list` and `ach env describe` (which work with synthetic creds against the resolved /environments + /hydrate path)"
  artifacts:
    - path: "cmd/ach/cmd/config.go"
      provides: "5 sub-subcommands (list/show/use/remove/rename) under `ach config`"
      contains: "var configCmd"
    - path: "cmd/ach/cmd/env.go"
      provides: "2 sub-subcommands (list/describe) under `ach env`"
      contains: "var envCmd"
    - path: "internal/cli/render/render.go"
      provides: "shared text formatters for config show / env list / env describe / whoami"
      contains: "func FormatEnvList"
  key_links:
    - from: "cmd/ach/cmd/env.go (describe)"
      to: "internal/platformapi/hydrate/handler.go HydrateResponse"
      via: "POST /platform/hydrate → decode HydrateResponse"
      pattern: "HydrateResponse"
    - from: "cmd/ach/cmd/config.go (show)"
      to: "internal/cli/config/config.go"
      via: "config.Mask"
      pattern: "config.Mask"
    - from: "cmd/ach/cmd/env.go (list)"
      to: "internal/platformapi/environments/handler.go"
      via: "GET /platform/environments?limit=N&cursor=X repeated until next_cursor is null"
      pattern: "next_cursor"
---

<objective>
Ship the read-only + local-mutate parts of the Phase 6 command
surface: `ach config` (5 children per D-05) + `ach env` (list +
describe per spec §5.5). Plus a shared `internal/cli/render/`
package consolidating text formatters used by these subcommands AND
by W1 whoami (which can refactor to consume render in a tidy-up
commit if time permits — non-blocking).

`ach config` is local-mutate only — no HTTP. `ach env` is read-only
HTTP (GET /platform/environments paginated + POST /platform/hydrate
for describe). The asymmetric admin-403 fallback in `ach env
describe` is the load-bearing CLI-12 behavior.

Purpose: This plan ships the surface that lets a user inspect their
ACH state without touching keys. It unblocks W2-P2 (env-keys, which
edits the ek: map) and W2-P3 (hydrate, which consumes the same
endpoints with a different presentation).

Output: 4 new files under `cmd/ach/cmd/` (2 commands + 2 tests) +
3 new files under `internal/cli/render/`.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/phases/06-cli-foundation/06-CONTEXT.md
@.planning/phases/06-cli-foundation/06-PATTERNS.md
@spec/ach_cli_spec_v20260515_FINALv4.md
@CLAUDE.md
@cmd/ach/cmd/migrate.go
@cmd/ach/cmd/root.go
@cmd/ach/cmd/login.go
@cmd/ach/cmd/whoami.go
@internal/platformapi/environments/handler.go
@internal/platformapi/hydrate/handler.go
@.planning/phases/06-cli-foundation/06-01-SUMMARY.md
@.planning/phases/06-cli-foundation/06-03-SUMMARY.md
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Author internal/cli/render package + `ach config` cobra parent-with-children</name>
  <files>
    internal/cli/render/doc.go
    internal/cli/render/render.go
    internal/cli/render/render_test.go
    cmd/ach/cmd/config.go
    cmd/ach/cmd/config_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-05" (5-child config + --reveal semantics)
    - 06-PATTERNS.md §"Pattern P3" lines 168-209 (parent-with-children cobra) + §"Pattern P10" lines 511-583 (config.Mask reference)
    - spec/ach_cli_spec_v20260515_FINALv4.md §5.4 (ach config 5 subcommands)
    - 06-01-SUMMARY.md (final config.File + config.Deployment + config.Mask + config.ResolveActive APIs)
    - 06-03-SUMMARY.md (whoami's identity-block formatting — render can lift this if convenient)
    - cmd/ach/cmd/root.go (rootCmd shape)
  </read_first>
  <behavior>
    render tests:
    - Test 1: FormatConfigList(file) returns a deterministic multi-line table with one row per deployment: `NAME\tURL\tPK\tEK\nprod\thttps://x\tyes\t2\n...`. Masks the pk_/ek_ presence as a boolean ("yes"/"no"). Order: alphabetical by name; `default` deployment marked with " (default)" suffix.
    - Test 2: FormatConfigShow(name, dep, reveal bool) returns a block: `Deployment: <name>\nURL: <url>\nPK: <masked or full>\nEK:\n  <label1>: <masked or full>\n  ...`. When reveal=false, pk_/ek_ values pass through `config.Mask`. When reveal=true, values are emitted in full.
    - Test 3: FormatEnvList([]EnvView) returns a table `NAME\tNAMESPACE\tSTATUS\n` — uses the wire shape from `internal/platformapi/environments/handler.go` (we'll define an EnvView struct in render.go matching the server's per-row shape).
    - Test 4: FormatEnvDescribe(env EnvView, hydrate *HydrateView, available bool) renders the full describe block — when available=false, prints `Runtime: (unavailable)\nContext: (unavailable)`; when available=true, prints two sub-tables. CRITICAL (W3): the rendered Runtime block surfaces each item's `endpoint`; the rendered Context block surfaces each item's `downloadUrl`. Test fixtures use `RuntimeItem{Name:"gpt-4", ID:"mdl_...", Endpoint:"https://hub.example/v1"}` and `ContextItem{Name:"caveman", ID:"plg_...", DownloadURL:"https://hub.example/content/plugin/caveman"}`; both substring values MUST appear in the rendered string.
    - Test 5 (FormatEkList, per W7): FormatEkList(rows []EkRowView) returns a table `KEY-ID\tOWNER\tENVIRONMENT\tNAME\tCREATED\n` deterministic by KEY-ID ascending. Both 06-05 env-keys list AND 06-08 admin keys list consume this — single source of truth.

    config tests:
    - Test 5: ach config list with a fresh empty config exits 0 + prints "No deployments configured" to stdout.
    - Test 6: ach config list with two deployments {prod (default), stg} prints the table with "prod (default)" suffix on the default row.
    - Test 7: ach config show prod (no --reveal) prints masked pk_/ek_; output does NOT contain the literal full pk_ plaintext from the file.
    - Test 8: ach config show prod --reveal prints the full pk_ plaintext.
    - Test 9: ach config use stg sets default: stg and persists; subsequent ach config list shows "stg (default)".
    - Test 10: ach config remove prod (where prod is default) WITHOUT --force exits 1 with stderr "cannot remove active default; use --force".
    - Test 11: ach config remove prod --force succeeds and clears `default:` if it was pointing at prod.
    - Test 12: ach config rename prod prod-v2 renames the map key, preserves pk + ek map, AND updates default if it was pointing at prod.
    - Test 13: Every ach config <sub> in synthetic mode exits 1 (synthetic detection — same inline check as login).
  </behavior>
  <action>
    Author `internal/cli/render/doc.go` — package doc citing Pattern P10 (render is the text-output discipline owner for Phase 6; replaces ad-hoc fmt.Printf scattered across subcommands).

    Author `internal/cli/render/render.go`:
    - Package `render` under `internal/cli/render/`.
    - Types (use minimal wire-shape DTOs; do NOT import the platformapi server packages — instead, define lean copies):
      - `EnvView struct { Name, Namespace, Status string }` — matches the per-row shape of `internal/platformapi/environments` handler list response.
      - `EkRowView struct { KeyID, OwnerEmail, Environment, Name, CreatedAt string }` — matches the per-row shape of `internal/platformapi/envkeys` list response. Consumed by 06-05 env-keys list AND 06-08 admin keys list (per W7 — both call FormatEkList in render).
      - The full `HydrateResponse` shape lives in `internal/platformapi/hydrate/handler.go` lines 101-108 — but `render` should NOT import that package (avoid pulling K8s/chi dependencies into the CLI binary). Instead, define a lean local `HydrateView` carrying the subset render needs:
        - `HydrateView struct { SchemaVersion, Environment string; Runtime, Context BlockView }`
        - `BlockView struct { Models, MCPServers, A2AAgents []RuntimeItem; Prompts, Plugins, Artifacts []ContextItem }`
        - `RuntimeItem struct { Name, ID, Endpoint string }` — `Endpoint` surfaces the per-runtime `endpoint` key for models/mcpServers/a2aAgents (per W3 — phase-goal sentence quotes `endpoint` verbatim).
        - `ContextItem struct { Name, ID, DownloadURL string }` — `DownloadURL` surfaces the per-context-entry `downloadUrl` key for prompts/plugins/artifacts (per W3 — canonical hydrate wire-format from CONTEXT.md).
      - The W2-P3 hydrate plan + the env describe handler here both decode into HydrateView locally. Field names match the on-the-wire keys (`endpoint`, `downloadUrl`) for trivial decode.
    - Funcs:
      - `FormatConfigList(file *config.File) string` — alphabetical row order; "default" suffix.
      - `FormatConfigShow(name string, dep *config.Deployment, reveal bool) string`.
      - `FormatEnvList(envs []EnvView) string`.
      - `FormatEnvDescribe(env EnvView, h *HydrateView, hydrateAvailable bool) string` — renderer iterates `h.Runtime.{Models,MCPServers,A2AAgents}` and emits each row's `endpoint`; iterates `h.Context.{Prompts,Plugins,Artifacts}` and emits each row's `downloadUrl`. Both surface in the rendered text body (per W3).
      - `FormatEkList(rows []EkRowView) string` — table with KEY-ID / OWNER / ENVIRONMENT / NAME / CREATED columns; used by 06-05 env-keys list AND 06-08 admin keys list (hoisted here per W7 to avoid inline duplication).
      - `FormatIdentity(name, url string, key string) string` — for whoami no-net path (optional refactor target for 06-03 follow-up).
    - Use stdlib `text/tabwriter` for the table output (canonical Go pattern, deterministic spacing).
    - NO `log`, NO `slog`, NO `fmt.Print*` — pure formatters returning strings. Caller writes to its own stdout/stderr.

    Author `cmd/ach/cmd/config.go` mirroring Pattern P3:
    - File-level docstring citing D-05 + spec §5.4.
    - `var configCmd = &cobra.Command{Use:"config", Short:"...", RunE: cmd.Help()}` parent.
    - 5 child commands:
      - `configListCmd` — no args; loads config; writes `render.FormatConfigList(file)` to stdout. Exit 0 on empty config (with stdout "No deployments configured").
      - `configShowCmd` — `Use: "show [deployment]"`, `cobra.MaximumNArgs(1)`. Flag `--reveal` (bool). Resolves deployment via `config.ResolveActive` (or args[0] if provided). Writes `render.FormatConfigShow(name, dep, reveal)`.
      - `configUseCmd` — `Use: "use <name>"`, `cobra.ExactArgs(1)`. Sets `file.Default = args[0]` if the name exists in `file.Deployments`. Save → exit 0; missing-name → exit 1.
      - `configRemoveCmd` — `Use: "remove <name>"`, `cobra.ExactArgs(1)`. Flag `--force` (bool). Refuses to delete the active default unless --force; on success, clears `file.Default` if it was pointing at the removed name.
      - `configRenameCmd` — `Use: "rename <old> <new>"`, `cobra.ExactArgs(2)`. Preserves PK + EK map; updates default if pointing at <old>; rejects rename to an existing name (exit 1).
    - Every child RunE: synthetic-mode short-circuit at the top → exit 1.
    - `init() { configCmd.AddCommand(configListCmd, configShowCmd, configUseCmd, configRemoveCmd, configRenameCmd); rootCmd.AddCommand(configCmd) }`.

    Tests: same XDG_CONFIG_HOME redirect pattern as login_test.go.

    SPDX header on every new file.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./internal/cli/render/... ./cmd/ach/cmd/... -run "TestConfig"</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./internal/cli/render/... ./cmd/ach/cmd/... -run "TestConfig"` exits 0.
    - Source assertion: `grep -c '"list"\|"show"\|"use"\|"remove"\|"rename"' cmd/ach/cmd/config.go` returns 5.
    - Source assertion: `grep -c 'configCmd.AddCommand' cmd/ach/cmd/config.go` returns ≥ 1 with 5 children listed.
    - Source assertion: `grep -c '\-\-reveal\|"--reveal"\|reveal' cmd/ach/cmd/config.go` returns ≥ 2 (flag declaration + check).
    - Source assertion: `grep -c '\-\-force\|"--force"\|force' cmd/ach/cmd/config.go` returns ≥ 2.
    - Source assertion: `grep -c 'config.Mask' internal/cli/render/render.go` returns ≥ 1 (render uses Mask).
    - Source assertion: `grep -c 'tabwriter' internal/cli/render/render.go` returns ≥ 1.
    - Source assertion (per W3): `grep -cE 'Endpoint\s+string|DownloadURL\s+string' internal/cli/render/render.go` returns ≥ 2 (RuntimeItem.Endpoint + ContextItem.DownloadURL).
    - Source assertion (per W3): `grep -cE 'endpoint|downloadUrl|downloadURL' internal/cli/render/render.go` returns ≥ 2 (renderer references the wire keys).
    - Source assertion (per W7): `grep -c 'func FormatEkList' internal/cli/render/render.go` returns 1 (hoisted from inline).
    - Behavior: FormatEnvDescribe with a RuntimeItem{Endpoint:"https://hub.example/v1"} renders a line containing the literal substring "https://hub.example/v1".
    - Behavior: FormatEnvDescribe with a ContextItem{DownloadURL:"https://hub.example/content/plugin/caveman"} renders a line containing the literal substring "https://hub.example/content/plugin/caveman".
    - Behavior: ach config show without --reveal on a pk_ "pk_abc...wxyz" emits "pk_****wxyz" only; full plaintext is absent from stdout.
    - Behavior: ach config remove <default> without --force exits 1; with --force succeeds + clears default.
    - SPDX header line 1: `head -1 internal/cli/render/{doc,render,render_test}.go cmd/ach/cmd/{config,config_test}.go` all match `Apache-2.0`.
  </acceptance_criteria>
  <done>
    Config commands green; render package consolidates formatters; --reveal opt-in unmask works per D-05; synthetic-mode short-circuit consistent.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Author `ach env {list, describe}` cobra parent-with-children</name>
  <files>
    cmd/ach/cmd/env.go
    cmd/ach/cmd/env_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-?? Wave 2 — ach env" + reference §"D-12" (env semantics; describe two-call)
    - 06-PATTERNS.md §"Pattern P3" (parent-with-children) + §"Pattern P5" (httpclient consumer)
    - spec/ach_cli_spec_v20260515_FINALv4.md §5.5 (ach env list + describe with admin-403 graceful)
    - internal/platformapi/environments/handler.go lines 60-200 (list handler — wire shape: `{items:[<EnvironmentView>], next_cursor: <string or nil>}`, pagination via `?limit=N&cursor=X`)
    - internal/platformapi/hydrate/handler.go lines 56-108 (HydrateRequest{Environment string}, HydrateResponse{SchemaVersion, Environment, Runtime, Context})
    - 06-01-SUMMARY.md (httpclient.Client + ServerError APIs)
    - 06-04 Task 1 SUMMARY shape (FormatEnvList + FormatEnvDescribe + EnvView + HydrateView)
  </read_first>
  <behavior>
    list tests:
    - Test 1: ach env list against an httptest server returning {items:[{name:"a"},{name:"b"}], next_cursor:null} prints both rows; exit 0.
    - Test 2: ach env list against a server returning {items:[{name:"a"}], next_cursor:"c1"} then {items:[{name:"b"}], next_cursor:null} on the cursor=c1 call → calls the server twice, prints both rows in order. Asserts ≥ 2 HTTP requests via httptest counter.
    - Test 3: ach env list with --limit 10 sends `?limit=10` on the first request.
    - Test 4: ach env list with a 401 response → exit 3.

    describe tests:
    - Test 5: ach env describe demo finds row in the first list page, then POSTs /platform/hydrate {environment:"demo"} → prints describe block; exit 0.
    - Test 6: ach env describe demo when /environments has the row but /hydrate returns 403 unauthorized_team → exit 0 with `Runtime: (unavailable)\nContext: (unavailable)` in stdout (CLI-12 graceful fallback).
    - Test 7: ach env describe demo when /environments paginates and the row is on page 3 → calls /environments 3 times before /hydrate.
    - Test 8: ach env describe demo with --metadata-only skips the /hydrate call entirely (assert httptest counter on /hydrate is 0).
    - Test 9: ach env describe nonexistent → exit 1 with "environment not found".
    - Test 10: ach env describe demo with --output-format json → NOT supported in Phase 6 (deferred per CONTEXT §"Phase 6 explicitly excludes" `--output-format json`). The flag is intentionally absent; cobra rejects unknown flag with exit 1.
  </behavior>
  <action>
    Author `cmd/ach/cmd/env.go`:
    - File-level docstring citing CLI-12 + spec §5.5.
    - `var envCmd = &cobra.Command{Use:"env", Short:"...", RunE: cmd.Help()}` parent.
    - 2 children:
      - `envListCmd`:
        - Flags: `--limit <int>` (default 100), `--verbose` (bool), `--deployment <name>` (string), `--api-key`/`--env-key`/etc. mutex from D-11.
        - RunE:
          1. Resolve config + credential (pk_ or ek_); compose httpclient.Client.
          2. Loop: `GET /platform/environments?limit=<limit>&cursor=<cursor>`. Decode `{items:[…], next_cursor:<*string>}`. Accumulate items. Loop while next_cursor != nil.
          3. Write `render.FormatEnvList(accumulated)` to stdout.
          4. *httpclient.ServerError surfaces to main.go.
      - `envDescribeCmd`:
        - `Args: cobra.ExactArgs(1)` — environment name.
        - Flags: `--metadata-only` (bool), `--verbose`, `--deployment`, credential mutex flags.
        - RunE:
          1. Resolve credential.
          2. Paginate /platform/environments looking for the named row. If found, capture the EnvView. Not found → exit 1.
          3. If --metadata-only: skip /hydrate, write `render.FormatEnvDescribe(view, nil, false)`. Return.
          4. POST /platform/hydrate {environment: name} → decode HydrateView.
             - On 200: write `render.FormatEnvDescribe(view, &hydrate, true)`. Exit 0.
             - On *ServerError with Status==403 && Code=="unauthorized_team": write `render.FormatEnvDescribe(view, nil, false)`. Exit 0 (CLI-12 graceful).
             - Other ServerError: return to main.go.

    Register with `init() { envCmd.AddCommand(envListCmd, envDescribeCmd); rootCmd.AddCommand(envCmd) }`.

    Tests use httptest with a mux that handles GET /platform/environments + POST /platform/hydrate; assert request counters per scenario.

    NOTE: env list + describe should WORK in synthetic mode (CLI-08: deployment resolution via flag → env → default → sole entry; ACH_BASE_URL + ACH_API_KEY synthetic resolves the URL + key directly). The synthetic-mode short-circuit (exit 1) applies to config-mutating commands like config/login/logout/env-keys-create — env list/describe are read-only and synthetic-friendly. Confirm this distinction by NOT inserting a synthetic short-circuit in env.go.

    SPDX header on every new file.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestEnv"</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestEnv"` exits 0.
    - Source assertion: `grep -c '"list"\|"describe"' cmd/ach/cmd/env.go` returns 2.
    - Source assertion: `grep -c 'envCmd.AddCommand' cmd/ach/cmd/env.go` returns ≥ 1.
    - Source assertion: `grep -c 'next_cursor\|NextCursor' cmd/ach/cmd/env.go` returns ≥ 1 (pagination).
    - Source assertion: `grep -c '"unauthorized_team"' cmd/ach/cmd/env.go` returns ≥ 1 (CLI-12 graceful).
    - Source assertion: `grep -c '\-\-metadata-only\|metadataOnly\|MetadataOnly' cmd/ach/cmd/env.go` returns ≥ 1.
    - Source assertion: `grep -c '"/platform/environments"\|"/platform/hydrate"' cmd/ach/cmd/env.go` returns ≥ 2.
    - Behavior: pagination test asserts ≥ 2 HTTP GETs against /environments when next_cursor is non-null on page 1.
    - Behavior: --metadata-only test asserts 0 POSTs to /hydrate.
    - Behavior: 403 unauthorized_team test exits 0 with stdout containing "(unavailable)".
  </acceptance_criteria>
  <done>
    env list paginates; describe is two-call with graceful admin-403 fallback; --metadata-only skips the second call.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| CLI process ↔ ~/.config/ach/config.yaml | All 5 `ach config` subcommands read; use/remove/rename/--reveal write back. Mode discipline owned by 06-01 config.Save (0600). |
| CLI ↔ network (env list/describe) | Authenticated GET /platform/environments (paginated) + POST /platform/hydrate (describe). Two-call describe with admin-403 graceful fallback (CLI-12). |
| `--reveal` flag ↔ stdout | `--reveal` is the ONLY path that emits unmasked pk_/ek_ on stdout; restricted to ONE named deployment per invocation (D-05). |
| Flag/env ↔ destructive config-mutate | `remove` and `rename` mutate the config registry; `remove` of the active default needs `--force`. |
| Network ↔ rendered describe block | Server-side `endpoint` + `downloadUrl` strings flow through FormatEnvDescribe to stdout; user inspects them to validate runtime/context wiring. |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-06-04-01 | Information Disclosure | `--reveal` accidentally emits secrets to a terminal recording | mitigate | `--reveal` is scoped to ONE named deployment per invocation (D-05). The flag is opt-in (default mask) — a careless `ach config show` without the flag emits `<prefix>_****<last-4>` only. Source-assertion gate verifies the unmask path runs ONLY when `--reveal` is set. |
| T-06-04-02 | Tampering | `--force` removes the active default deployment | mitigate | `remove` of the active default requires `--force`; on success, `default:` is CLEARED (not left dangling). Subsequent `ach env list` sees `ErrNoDeployment` and exits 1 with a clear message — never silently routes traffic to an unintended deployment. |
| T-06-04-03 | Tampering | `rename` clobbers an existing deployment | mitigate | Rename to an existing target name is REJECTED with exit 1 + a "target exists" stderr message; no silent merge. The user must `remove` the target first. |
| T-06-04-04 | Spoofing | Tabwriter format-string injection via deployment NAME | accept | `text/tabwriter` is a pure formatter — it does not interpret %-verbs in its input. Deployment names are also constrained to `[a-z0-9-]+` via login validation; arbitrary control characters never reach the rendered output. |
| T-06-04-05 | Information Disclosure | Hydrate response `downloadUrl` may expose internal cluster hosts | accept | The `downloadUrl` is server-emitted; the CLI surfaces it verbatim per W3 because the user explicitly asked for `describe` to inspect content routing. Production posture (proxied URLs) is a server-side concern, not a CLI redaction concern. |
| T-06-04-06 | Repudiation | No client-side audit of `config use`/`remove`/`rename` | accept | Config mutations are local-only (no server round-trip). The on-disk yaml IS the audit trail; subsequent `ach env list` against the new deployment exercises the server-side `actor` audit. |
| T-06-04-07 | Denial of Service | env list pagination loop unbounded | mitigate | Each `GET /platform/environments?cursor=…` issues a single HTTP request with a 60s timeout (httpclient.Client default). The accumulator collects rows in memory; a malicious server emitting infinite pages would eventually OOM the CLI, but the timeout AND `--limit` flag (server-side cap) bound the practical attack surface. |
| T-06-04-08 | Elevation of Privilege | `ach env describe` with admin-403 fallback emits half-data | mitigate | When `/hydrate` returns 403 unauthorized_team, the renderer prints `Runtime: (unavailable)` / `Context: (unavailable)` markers — explicit signal to the user that data was suppressed. No partial runtime/context rendered; no silent omission. |
| T-06-04-SC | Tampering | npm/pip/cargo installs | mitigate | No new third-party deps; render uses stdlib `text/tabwriter` only. Existing govulncheck ack-list applies. |
</threat_model>

<verification>
After all 2 tasks complete:

```bash
./scripts/dev.sh go test ./internal/cli/render/... ./cmd/ach/cmd/... -run "TestConfig|TestEnv"
./scripts/dev.sh go build ./cmd/ach/...
./scripts/dev.sh make lint
```

Smoke against a Wave-1-deployed Hub (engineer-pending; W3 e2e covers this):
```bash
./bin/ach config list
./bin/ach config show prod
./bin/ach config use stg
./bin/ach env list
./bin/ach env describe demo
./bin/ach env describe demo --metadata-only
```
</verification>

<success_criteria>
- All 5 config subcommands (list/show/use/remove/rename) work with the masked-by-default discipline.
- --reveal opt-in unmask works for a named deployment only.
- --force gates active-default deletion.
- env list paginates; env describe is two-call; --metadata-only skips the second call.
- 403 unauthorized_team yields graceful exit 0 with `(unavailable)` markers.
- All unit tests via httptest + t.TempDir() are green.
</success_criteria>

<output>
Create `.planning/phases/06-cli-foundation/06-04-SUMMARY.md` when done. Record:
- render.HydrateView shape (must include RuntimeItem.Endpoint + ContextItem.DownloadURL per W3); confirm field-by-field match with the server-side HydrateResponse for W2-P3 hydrate JSON round-trip.
- Confirmation that FormatEkList ships in render (per W7) — 06-05 env-keys list and 06-08 admin keys list will consume it directly, NOT inline.
- Final flag set across config + env (W2-P2 env-keys + W2-P3 hydrate need to avoid name collisions).
- Any deviations from Pattern P3.
</output>
