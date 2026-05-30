---
phase: 06-cli-foundation
plan: 04
subsystem: cli-foundation
tags: [cli, config, env, render, tabwriter, pagination, hydrate, graceful-fallback]
requirements: [CLI-02, CLI-04, CLI-08, CLI-12]
dependency_graph:
  requires:
    - "06-01-cli-shared-internals (config + httpclient + exit packages)"
    - "06-03-ach-login-whoami-logout (resolveActiveBearer helper, whoamiHTTPClient seam pattern)"
  provides:
    - "internal/cli/render (FormatConfigList, FormatConfigShow, FormatEnvList, FormatEnvDescribe, FormatEkList, FormatIdentity + EnvView/EkRowView/HydrateView/RuntimeItem/ContextItem/BlockView DTOs)"
    - "cmd/ach/cmd/config.go — `ach config {list, show, use, remove, rename}` (5 children + masked-by-default --reveal opt-in)"
    - "cmd/ach/cmd/env.go — `ach env {list, describe}` (pagination + two-call describe + CLI-12 graceful 403)"
    - "cmd/ach/cmd/helpers_test.go (executeCommand shared test driver)"
    - "envHTTPClient package-level test seam (mirrors 06-03 whoamiHTTPClient pattern)"
  affects:
    - "06-05 env-keys list will import render.FormatEkList directly (W7 — single source of truth)"
    - "06-06 hydrate will reuse the render.HydrateView wire-shape decode (RuntimeItem.Endpoint + ContextItem.DownloadURL)"
    - "06-07 synthetic guard will subsume configSyntheticGuard inline check"
    - "06-08 admin keys list will import render.FormatEkList directly (W7)"
tech_stack:
  added: []  # All deps already in go.mod from 06-01.
  patterns:
    - "Pattern P3 — parent-with-children cobra subcommand (factory shape per 06-03 newXCmd convention)"
    - "Pattern P10 — render is a pure formatter package; NO log/slog/fmt.Print*; tests call functions, callers (cobra RunE) write to their own io.Writer"
    - "stdlib text/tabwriter for deterministic table spacing across config list / env list / env describe / FormatEkList"
    - "Lean local DTOs (HydrateView, RuntimeItem, ContextItem) match server wire tags verbatim so CLI binary does NOT import internal/platformapi (avoids k8s.io/* + chi pull-in)"
    - "Shared executeCommand test driver (helpers_test.go) so per-subcommand helpers stay tiny and dupl-clean"
key_files:
  created:
    - "internal/cli/render/doc.go"
    - "internal/cli/render/render.go"
    - "internal/cli/render/render_test.go"
    - "cmd/ach/cmd/config.go"
    - "cmd/ach/cmd/config_test.go"
    - "cmd/ach/cmd/env.go"
    - "cmd/ach/cmd/env_test.go"
    - "cmd/ach/cmd/helpers_test.go"
  modified: []
decisions:
  - "render.HydrateView is a LEAN LOCAL DTO matching the server's HydrateResponse wire tags (schemaVersion, environment, runtime, context, models, mcpServers, a2aAgents, prompts, plugins, artifacts, id, endpoint, name, downloadUrl). The CLI does NOT import internal/platformapi/hydrate — that would pull k8s.io/* + chi transitively. Field-by-field match was verified by reading internal/platformapi/hydrate/handler.go HydrateResponse + RuntimeItem (id, endpoint) + ContextItem (name, id, downloadUrl). render.RuntimeItem adds an optional Name field (server emits {id, endpoint} only — Name stays empty for server-decoded values; the field exists so future test fixtures + W3 contract assertions can populate it). The W2-P3 hydrate plan (06-06) consumes the same HydrateView via DoRaw + io.Copy, then decodes for golden-diff parity vs examples/hydrate.json."
  - "BlockView unifies runtime + context sub-blocks — single struct with omitempty tags on every slice. The runtime axis populates {Models, MCPServers, A2AAgents}; the context axis populates {Prompts, Plugins, Artifacts}. This kept the encoding/json round-trip trivial (one decode target per top-level key) without bifurcating into RuntimeBlockView + ContextBlockView (which would have duplicated 6 fields with the same shape)."
  - "FormatEkList ships in render NOW (per W7) even though no consumer lands until 06-05/06-08. Tested with its own table fixture in render_test.go. Hoisting at W2-P1 prevents 06-05 + 06-08 from independently growing inline tabwriter blocks and divergent column orderings."
  - "Synthetic-mode short-circuit is INLINE in config.go's configSyntheticGuard (mirrors login.go / logout.go). env.go has NO synthetic guard — env list + describe are read-only and synthetic-friendly per CLI-08 + the plan's explicit note. Centralization lands in W3-P1 (06-07) via internal/cli/synthetic.GuardCommand."
  - "Test driver consolidation: cmd/ach/cmd/helpers_test.go owns executeCommand (the shared dispatch logic of cobra exec + errors.As + exit.Code mapping). Per-subcommand executeFoo helpers (executeConfig, executeEnv, executeLogin, executeWhoami, executeLogout) shrink to 3 lines (Helper() + delegate). Pre-empts the golangci-lint dupl detector that fired at 5 structurally-identical 20-line bodies during Task 2's first commit attempt."
  - "Environment-not-found path is CLI-side (exit 1 via exit.CodedError{General}). The server's GET /platform/environments/{name} 404 path is the backstop; we don't call it because describe is name-driven against the list result. This avoids a redundant round-trip and surfaces the failure faster (no 404 envelope decode noise on stderr)."
  - "Pagination loop in env list is unbounded by design (the threat model accepts this — server-side ?limit cap is 500; a malicious server emitting infinite pages would OOM the accumulator, but the 60s httpclient.Client timeout bounds the per-call wall clock). Followed by findEnvironmentByName which uses the same unbounded loop scoped to a name lookup."
metrics:
  duration_minutes: 32
  completed_date: 2026-05-28
  tasks: 2
  files_created: 8
  files_modified: 0
---

# Phase 6 Plan 04: ach config + env Summary

Wave 2's first plan ships the local-mutate registry surface
(`ach config` 5 children) + the read-only environments surface
(`ach env list/describe`) plus the shared `internal/cli/render`
package. With this plan landed, a user can inspect their full ACH
state (deployments table, masked credentials, env list with
pagination, env describe with runtime + context manifest)
without touching keys — and the shared render package becomes the
single source of truth that 06-05 env-keys list, 06-06 hydrate,
and 06-08 admin keys list will all consume.

## What landed

### internal/cli/render (Task 1 — `b0f5135`)

- `doc.go` cites Pattern P10 (render is the text-output discipline
  owner for Phase 6; pure formatter, NO log/slog/fmt.Print*).
- DTOs defined locally so the CLI binary does NOT pull
  internal/platformapi (which transitively imports k8s.io/* + chi):
  - `EnvView{Name, Namespace, Status}` — per-row env shape.
  - `EkRowView{KeyID, OwnerEmail, Environment, Name, CreatedAt}` —
    per-row env-keys shape (W7 — single source of truth across
    06-05 env-keys list + 06-08 admin keys list).
  - `HydrateView{SchemaVersion, Environment, Runtime, Context}` +
    `BlockView` + `RuntimeItem{Name, ID, Endpoint}` +
    `ContextItem{Name, ID, DownloadURL}` — field tags match the
    server's HydrateResponse wire keys verbatim (`endpoint`,
    `downloadUrl`) per W3 contract.
- Functions:
  - `FormatConfigList(file)` — alphabetical row order; `(default)`
    suffix on the active row.
  - `FormatConfigShow(name, dep, reveal)` — masked-by-default;
    `--reveal` opt-in unmask scoped to ONE named deployment.
  - `FormatEnvList(envs)` — table with NAME, NAMESPACE, STATUS.
  - `FormatEnvDescribe(env, h, hydrateAvailable)` — when
    hydrateAvailable=true renders Runtime + Context sub-tables
    surfacing `endpoint` + `downloadUrl` per W3; when false prints
    `Runtime: (unavailable)` / `Context: (unavailable)` markers.
  - `FormatEkList(rows)` — W7 hoist; deterministic ordering by
    KeyID ascending.
  - `FormatIdentity(name, url, key)` — optional refactor target
    for 06-03 follow-up.
- stdlib text/tabwriter for deterministic spacing.

### cmd/ach/cmd/config.go (Task 1 — `b0f5135`)

- File-level docstring citing D-05 + spec §5.4.
- `newConfigCmd()` factory + 5 children: list, show, use, remove,
  rename. Each child is its own `newConfigXCmd()` factory.
- Every child applies the inline synthetic-mode guard
  (`configSyntheticGuard("<sub>")`) — exits 1 when ACH_BASE_URL +
  ACH_API_KEY are both set.
- D-05 contract honored: masked-by-default; `--reveal` opt-in for
  the named deployment only (`ach config show prod --reveal`).
- T-06-04-02 mitigation: `remove --force` clears `default:` when
  it was pointing at the removed name.
- T-06-04-03 mitigation: `rename` rejects target-name collision
  with exit 1 (never silently merges).
- 12 tests cover empty + populated registry, reveal vs masked,
  use happy + unknown-name, remove default-with-force vs without
  vs non-default, rename happy + collision, synthetic-mode exit 1
  for all 5 children.

### cmd/ach/cmd/env.go (Task 2 — `1d2fa57`)

- File-level docstring citing CLI-12 + spec §5.5.
- `newEnvCmd()` factory + 2 children: list, describe.
- `paginateEnvironments(ctx, hc, limit)` — issues N GET calls
  until next_cursor is null; accumulator preserves item order.
- `findEnvironmentByName(ctx, hc, name)` — same pagination shape
  scoped to a name lookup; exits 1 with "environment not found"
  on exhaustion.
- `callHydrate(ctx, hc, name)` — POST /platform/hydrate
  {environment:<name>} → decodes into render.HydrateView.
- CLI-12 graceful admin fallback: errors.As against
  *httpclient.ServerError with Status==403 && Code=="unauthorized_team"
  → render.FormatEnvDescribe(view, nil, false) → exit 0.
- `--metadata-only` skips /hydrate entirely (asserted by tests
  via httptest counter == 0).
- Test seam `envHTTPClient` mirrors `whoamiHTTPClient` from 06-03
  — package-level *http.Client overridable via
  `swapEnvHTTPClientForTest`.
- 10 tests cover single-page + multi-page list, --limit flag,
  401 → exit 3 mapping, describe happy + 403 graceful + paginated
  find + --metadata-only + not-found + unknown-flag rejection +
  synthetic-mode allowed (read-only friendly).

### cmd/ach/cmd/helpers_test.go (Task 2 — `1d2fa57`)

- `executeCommand(t, cmd, args...) (stdout, stderr, code, err)` —
  shared dispatch logic combining cobra exec + errors.As +
  exit.Code mapping. Used by config_test.go and env_test.go;
  pre-empts the golangci-lint dupl detector that fires at 5+
  structurally-identical 20-line bodies.

## Foundation-contract confirmation (anti-rework gate)

- **render.HydrateView field-by-field parity with server's
  HydrateResponse** — verified by reading
  `internal/platformapi/hydrate/handler.go` lines 56-106. Server
  emits: schemaVersion, environment, runtime{models, mcpServers,
  a2aAgents}, context{prompts, plugins, artifacts}; each runtime
  item has {id, endpoint}; each context item has {name, id,
  downloadUrl}. render.HydrateView's json tags match exactly.
  The `name` field on RuntimeItem is render-side optional (server
  emits {id, endpoint} only) — no decode failure; the field stays
  empty when round-tripped from server output. 06-06 hydrate can
  reuse this DTO directly OR decode into the server's
  HydrateResponse and re-marshal for stdout — both paths produce
  byte-for-byte identical wire output.
- **FormatEkList shape match** — server's
  `internal/platformapi/envkeys.EkRowView` is the wire source
  (KeyID, Environment, Name, OwnerEmail, Status, CreatedAt,
  LastUsedAt, RevokedAt). render.EkRowView covers the 5 columns
  the CLI table renders (KeyID, OwnerEmail, Environment, Name,
  CreatedAt). 06-05 env-keys list + 06-08 admin keys list will
  decode the server JSON into render.EkRowView and pass straight
  to FormatEkList — single source of truth, NO inline tabwriter
  duplication.

## Final flag set (for W2 hydrate + env-keys to avoid collisions)

| Command          | Flags                                                                                            |
| ---------------- | ------------------------------------------------------------------------------------------------ |
| config list      | (none)                                                                                           |
| config show      | `--reveal`                                                                                       |
| config use       | (positional only)                                                                                |
| config remove    | `--force`                                                                                        |
| config rename    | (positional only)                                                                                |
| env list         | `--limit`, `--verbose`, `--deployment`, `--api-key`, `--env-key`                                 |
| env describe     | `--metadata-only`, `--verbose`, `--deployment`, `--api-key`, `--env-key`                         |

06-06 hydrate planned flags: `--environment`, `--api-key`,
`--env-key`, `--no-warnings`. The `--api-key`/`--env-key`/
`--deployment`/`--verbose` set is shared with env list/describe
+ whoami (06-03) — consistent semantics already established.
06-05 env-keys planned flags: `--no-save`, `--yes`,
`--environment`, `--name`, `--deployment` — no collisions with
the env namespace.

## Test discipline + per-task TDD

Each task followed RED → GREEN with the test file written first:

- Task 1: 11 render tests + 12 config tests — RED via undefined
  FormatConfigList/Show/EnvList/EnvDescribe/EkList/Identity +
  EnvView/EkRowView/HydrateView types + undefined newConfigCmd;
  GREEN by implementing render.go + config.go.
- Task 2: 10 env tests + the shared executeCommand helper — RED
  via undefined newEnvCmd + envHTTPClient seam; GREEN by
  implementing env.go.

Pre-commit hook (`make lint-changed` + `make unit`) gated both
commits. golangci-lint clean across the affected packages on
every commit after auto-fixing two `lll` lines and one `dupl`
trip (see deviations below).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Helper test file extraction to defeat golangci-lint dupl**
- **Found during:** Task 2 pre-commit lint sweep.
- **Issue:** Adding `executeEnv` made it the 4th structurally-identical
  test helper (alongside `executeLogin`, `executeWhoami`, `executeLogout`,
  `executeConfig`). golangci-lint dupl detector flagged the env<>whoami
  pair (~20 identical lines of `bytes.Buffer + SetOut/Err/Args +
  ExecuteContext + errors.As against *ServerError + *CodedError`).
- **Fix:** Extracted `executeCommand(t, cmd, args...)` into a new
  `cmd/ach/cmd/helpers_test.go`. Refactored both `executeEnv` AND
  `executeConfig` (from Task 1) to delegate. Production code
  unchanged.
- **Files modified:** `cmd/ach/cmd/helpers_test.go` (new),
  `cmd/ach/cmd/env_test.go`, `cmd/ach/cmd/config_test.go`.
- **Commit:** Absorbed into `1d2fa57` (Task 2 commit).

**2. [Rule 1 - Bug] `lll` line-length violations in env.go**
- **Found during:** Task 2 pre-commit lint sweep.
- **Issue:** Two lines in env.go exceeded the 120-char golangci-lint
  cap — `c.Flags().BoolVar(&flagMetadataOnly, "metadata-only", ...)`
  was 124 chars; `func buildEnvHTTPClient(flagDeployment, flagAPIKey,
  flagEnvKey string, verbose bool, stderr io.Writer) (...)` was 132.
- **Fix:** Wrapped both — flag declaration across two lines,
  function signature across multiple lines.
- **Files modified:** `cmd/ach/cmd/env.go`.
- **Commit:** Absorbed into `1d2fa57`.

**3. [Rule 1 - Bug] `lll` line-length violation in config.go (Task 1)**
- **Found during:** Task 1 pre-commit lint sweep.
- **Issue:** The configSyntheticGuard error message was 130 chars.
- **Fix:** Wrapped the format string across two lines.
- **Files modified:** `cmd/ach/cmd/config.go`.
- **Commit:** Absorbed into `b0f5135` (Task 1 commit) — fixed before
  the commit landed.

### Documented divergences from plan acceptance text

**4. Plan acceptance text mis-numbered config tests (5-13)**
- **Found during:** Test authoring.
- **Issue:** Plan body lists render tests 1-5 (with FormatEkList as
  the 5th) and then config tests 5-13 (reusing 5). The two test
  groups live in different files (render_test.go vs config_test.go),
  so the numbering is just plan-body imprecision — both file's
  contents satisfy the actual test contracts. No code change.
- **Resolution:** authored tests are mapped to the plan's
  intent: 5 render tests for tabular formatters (incl. FormatEkList
  + FormatEnvDescribe per W3), 12 config tests for the 5 children
  (including unknown-name edge cases + synthetic-mode parametrized
  across all 5 children).

**5. Plan source assertion `grep -c 'configCmd.AddCommand' = ≥ 1`
  matches via `parent.AddCommand`**
- **Found during:** Task 1 acceptance check.
- **Issue:** Plan acceptance text uses the var-named pattern
  `configCmd.AddCommand`. The actual code uses the factory pattern
  (`newConfigCmd()` returns a local `parent` variable) — so
  `parent.AddCommand` is the call site. Semantic invariant is
  satisfied (the 5 children ARE registered on the config parent),
  but the literal regex would miss.
- **Resolution:** kept the factory pattern (per 06-03 Pattern P2 —
  newXCmd factories are the established convention for test
  hermeticity); documented the regex variance here. Acceptance
  intent satisfied: `grep -c 'parent.AddCommand' cmd/ach/cmd/config.go`
  returns 1 with 5 children listed in the call.

## Threat Surface Scan

| Threat ID    | Coverage status                                                                                                                                                                                       |
| ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| T-06-04-01   | `FormatConfigShow` masks pk_/ek_ unless `reveal=true`. Source-assertion: `grep -c 'config.Mask' internal/cli/render/render.go` returns 5 (used in FormatConfigShow + FormatIdentity, 2 sites each + 1 in unmask check). Test `TestFormatConfigShow_Masked` asserts no plaintext leak; `TestConfig_Show_Masked` asserts the same end-to-end via cobra. |
| T-06-04-02   | `remove --force` clears `default:` when it was pointing at the removed name. Source line `if f.Default == name { f.Default = "" }`. Test `TestConfig_Remove_DefaultWithForce` asserts `f.Default == ""` post-remove.                                                                                                                                  |
| T-06-04-03   | `rename` rejects target-name collision (`if _, exists := f.Deployments[newName]; exists`). Test `TestConfig_Rename_TargetExists` asserts exit 1 + the literal target-name in the error message.                                                                                                                                                       |
| T-06-04-04   | Accepted — `text/tabwriter` is a pure formatter (no %-verb interpretation); deployment names are constrained to DNS-1123 by login validation; no control chars reach the rendered output.                                                                                                                                                            |
| T-06-04-05   | Accepted — `downloadUrl` is surfaced verbatim per W3 phase-goal contract. Production posture (proxied URLs) is a server-side concern.                                                                                                                                                                                                                 |
| T-06-04-06   | Accepted — config mutations are local-only; on-disk yaml IS the audit trail. Subsequent `ach env list` against the new deployment exercises server-side `actor` audit.                                                                                                                                                                                |
| T-06-04-07   | Mitigated by per-call 60s httpclient.Client timeout (foundation default from 06-01) + server-side ?limit cap (500). The accumulator collects rows in memory; a malicious server emitting infinite pages would eventually OOM the CLI, but the timeout bounds the practical attack surface per the threat model's acceptance.                          |
| T-06-04-08   | `env describe` 403 unauthorized_team → render.FormatEnvDescribe(view, nil, false) which emits `Runtime: (unavailable)` / `Context: (unavailable)` markers. NO partial runtime/context rendered. Test `TestEnv_Describe_403_GracefulFallback` asserts exit 0 + `(unavailable)` substring.                                                              |
| T-06-04-SC   | No new third-party deps. Only stdlib (`text/tabwriter`, `sort`, `strings`, `fmt`) + foundation packages from 06-01 (config, exit, httpclient) + cobra (already vendored). Existing govulncheck ack-list applies.                                                                                                                                      |

No new threat-flagged surface introduced beyond the plan's
`<threat_model>` register.

## Self-Check: PASSED

Verified:
- `internal/cli/render/doc.go` exists.
- `internal/cli/render/render.go` exists.
- `internal/cli/render/render_test.go` exists.
- `cmd/ach/cmd/config.go` exists.
- `cmd/ach/cmd/config_test.go` exists.
- `cmd/ach/cmd/env.go` exists.
- `cmd/ach/cmd/env_test.go` exists.
- `cmd/ach/cmd/helpers_test.go` exists.
- Commits `b0f5135` (Task 1) + `1d2fa57` (Task 2) in `git log`.
- `./scripts/dev.sh go test ./internal/cli/render/... ./cmd/ach/cmd/...`
  exits 0 (all packages PASS).
- `./scripts/dev.sh go build ./cmd/ach/...` exits 0.
- `./scripts/dev.sh make lint-changed` exits 0 (clean after auto-fix
  of dupl + lll deviations).
- SPDX header line 1 on all 8 new files.
- Source-assertion gates from plan acceptance criteria all PASS:
  - Task 1: `tabwriter` count = 6, `config.Mask` count = 5,
    `--reveal` count = 6, `--force` count = 7, `FormatEkList` count
    = 1, `Endpoint string|DownloadURL string` count = 2,
    `endpoint|downloadUrl` count = 7, 5 child commands registered.
  - Task 2: `list|describe` Use count = 2, AddCommand count = 1
    (covers both children), `next_cursor|NextCursor` count = 10,
    `unauthorized_team` count = 1, `metadata-only|metadataOnly|
    MetadataOnly` count = 4, `/platform/environments|/platform/
    hydrate` references = 10.

---
*Phase: 06-cli-foundation*
*Plan: 04*
*Completed: 2026-05-28*
