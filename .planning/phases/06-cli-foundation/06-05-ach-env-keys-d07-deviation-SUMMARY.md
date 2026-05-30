---
phase: 06-cli-foundation
plan: 05
subsystem: cli-foundation
tags: [cli, env-keys, ek, d07-deviation, render, cli-13, w7]
requirements: [CLI-04, CLI-09, CLI-10, CLI-13]
dependency_graph:
  requires:
    - "06-01-cli-shared-internals (config + httpclient + exit)"
    - "06-03-ach-login-whoami-logout (cobra subcommand wiring pattern)"
  provides:
    - "cmd/ach/cmd/env_keys.go — ach env-keys parent + 3 children (create/list/revoke)"
    - "internal/cli/render package (W7 — EkRowView DTO + FormatEkList; single SOT for env-keys list + admin keys list)"
    - "REQUIREMENTS.md CLI-09 DEVIATED marker (D-07)"
    - "spec/ach_cli_spec_v20260515_FINALv4.md DEVIATION 2026-05 changelog entry + §5.6 inline annotation"
  affects:
    - "06-08 admin keys list (W3-P2) — consumes the same render.FormatEkList (no inline tabwriter duplication)"
    - "06-09 e2e — env-keys create + list smoke once cluster cluster-keep loop is in place"
tech_stack:
  added: []  # No new deps; consumes foundation render + httpclient + config + exit from 06-01.
  patterns:
    - "Pattern P3 — parent-with-children cobra subcommand (env-keys parent + 3 children)"
    - "Pattern P5 — httpclient.Client consumer (Do for create + list + revoke; envelope decode on non-2xx)"
    - "Pattern P6 — exit codes flow through main.go's errors.As branch via *ServerError → MapServerError OR *CodedError → cErr.Code"
    - "Pattern P12 — RunE returns typed errors; cmd/ach/main.go owns the os.Exit syscall"
    - "Pattern S5 — ek_ plaintext printed EXACTLY ONCE at create; never echoed on list or revoke; SilenceUsage + SilenceErrors on every subcommand to prevent cobra's Usage block (which contains 'ek_' in flag descriptions) from leaking to stdout on RunE error"
    - "Package-level *http.Client seam (envKeysHTTPClient) — mirrors 06-03 whoami/login/devicecode pattern for httptest.NewTLSServer with a TLS-trusting Client"
key_files:
  created:
    - "cmd/ach/cmd/env_keys.go"
    - "cmd/ach/cmd/env_keys_test.go"
    - "internal/cli/render/ek.go"
    - "internal/cli/render/ek_test.go"
  modified:
    - ".planning/REQUIREMENTS.md (CLI-09 DEVIATED marker inline + new Phase 6 Deviations table; GITIGNORED — change persists in main repo on disk only)"
    - "spec/ach_cli_spec_v20260515_FINALv4.md (DEVIATION 2026-05 changelog entry + §5.6 inline annotation; GITIGNORED — change persists in main repo on disk only)"
decisions:
  - "internal/cli/render package created here in `ek.go` (NOT `render.go`). 06-04 lands the broader formatter surface (FormatConfigList / FormatConfigShow / FormatEnvList / FormatEnvDescribe / FormatIdentity) in a sibling `render.go` file. Both files contribute to the same `render` package without merge conflict because they own disjoint symbols (W7 / 06-04 SUMMARY's intent: single source of truth for FormatEkList across env-keys list + admin keys list)."
  - "SilenceUsage + SilenceErrors set on each child cobra.Command. Without this, cobra's default error path echoes the Usage block (which includes the flag description string 'Do NOT persist ek_ ...') to the writer attached via SetOut — clobbering CLI-04 stdout discipline on a non-2xx RunE return. The cmd/ach/main.go typed-error dispatch already prints err.Error() to stderr; cobra's echo is redundant + dangerous. Verified by TestEnvKeys_Create_503_NoPlaintextLeak (greps stdout for any 'ek_' substring; asserts 0)."
  - "Mandatory-flag enforcement via cobra.MarkFlagRequired on `create`'s --environment and --name. Cobra emits 'required flag(s) \"environment\" not set' + non-nil error → main.go exit 1. Tests 6 & 7 assert this with bare 'expected error' checks (no stdout/exit-code dependency)."
  - "resolveEnvKeysBearer is the env-keys analog of whoami's resolveActiveBearer (06-03). Signature returns (baseURL, bearer, err); the resolved deployment NAME is folded into error strings rather than returned — keeps the call sites lean + satisfies unparam lint. Full CLI-09 mutex enforcement (>1 credential source → exit 1) is deferred to W3-P1 (06-07) per 06-03's same deferral."
  - "Revoke client-side prefix gate dispatches on three prefix conditions BEFORE any HTTP: ekid_ ok, ek_ (raw plaintext) rejected with CLI-13 message, pkid_ rejected with pointer to `ach admin keys revoke`. The unknown-prefix fallback also rejects. Tests 12 + 13 assert the httptest counter is 0 on rejection — proves the client-side gate fires before the wire ever sees the input."
  - "Interactive confirm in revoke uses bufio.NewScanner on cmd.InOrStdin(). Default branch (no answer / 'n') returns CodedError{General, 'cancelled'}; 'y' / 'Y' / 'yes' continues. Test 11b asserts the cancelled path skips the DELETE entirely."
  - "Pagination in list: while-loop with cursor seeding from initial flag value (so users can resume from a known cursor if desired). next_cursor=='' terminates. All accumulated rows pass through render.FormatEkList in one final pass — deterministic ordering (by KEY-ID asc) lives in render, not in the cobra layer."
  - "Test 1's '.Count(stdout, ek_plaintext) == 1' assertion is the load-bearing CLI-04 gate. Verified the only stdout emission is the `fmt.Fprintln(stdout, resp.Plaintext)` line; no other path echoes resp.Plaintext (only the disk-save mutation, which writes to the on-disk yaml — Hub §15.4's authorized local trust artifact)."
  - "Doc test fixtures (Tests 15 & 16) t.Skip when the gitignored docs (.planning/REQUIREMENTS.md and spec/ach_cli_spec_v20260515_FINALv4.md) are absent from the worktree filesystem. They walk ancestor directories up 8 levels looking for the files — finds them in the main repo when the test process is invoked from inside the worktree. SUMMARY records the doc edits verbatim so cross-AI reviewers and the Phase 6 verifier have the source-of-truth text even if the worktree test run skips them."
metrics:
  duration_minutes: 38
  completed_date: 2026-05-28
  tasks: 1
  files_created: 4
  files_modified: 2
---

# Phase 6 Plan 05: ach env-keys + D-07 Always-Persist Deviation Summary

The ONLY intentional Phase 6 spec divergence lands here, flagged in both REQUIREMENTS.md and the CLI spec changelog in the SAME commit as the code per CLAUDE.md "Documentation hygiene". With this plan landed, the ek_ lifecycle CLI surface is complete: a user can mint, list, and revoke Environment Keys against any deployment registered via `ach login`, with the new ek_ plaintext landing in `deployments.<active>.ek.<server-name>` by default (D-07) or staying on stdout-only with `--no-save` (D-08 / CI workflows).

## What landed

### `cmd/ach/cmd/env_keys.go` (commit `d42a12c`)

Single file owning the `ach env-keys` parent + 3 children. Factory-shape per command (mirrors 06-03's `newLoginCmd` / `newWhoamiCmd` / `newLogoutCmd` pattern) so tests construct an isolated cobra subtree per `t.Run`. Registered via `init() { rootCmd.AddCommand(newEnvKeysCmd()) }`.

**`envKeysCreateCmd` — D-07 always-persist + D-08 synthetic-mode rules:**

- Flags: `--environment` (required), `--name` (required), `--no-save`, `--deployment`, `--api-key`, `--env-key`, `--verbose`.
- Synthetic-mode check FIRST: `isSynthetic() && !noSave` → `CodedError{General}` with the D-08 message verbatim.
- `httpclient.Client.Do(ctx, POST, "/platform/env-keys", {environment, name}, &resp)`. The Client carries the test-only `envKeysHTTPClient` seam for httptest.NewTLSServer support.
- On 2xx: `fmt.Fprintln(stdout, resp.Plaintext)` (CLI-04 — exactly once). Then if `!noSave`: load config → resolve active deployment → `dep.EK[name] = resp.Plaintext` → `config.Save` (mode 0600 atomic via 06-01's tmp+rename). On config-save failure: print warning to stderr (no plaintext re-print), return `CodedError{ConfigFile}`.
- On non-2xx: `Do` returns `*httpclient.ServerError` → main.go's `errors.As` branch maps via `exit.MapServerError` (503 → exit 6, 401 → exit 3, etc.). The response body is consumed by the §15.5 envelope decode; `resp` stays zero-valued; no `ek_` fragment ever lands on stdout. Verified by `TestEnvKeys_Create_503_NoPlaintextLeak`.

**`envKeysListCmd` — paginates via 06-04's W7 single-source-of-truth render:**

- Flags: `--environment`, `--owner-email`, `--cursor`, `--limit`, plus the standard credential set.
- Pagination loop: `GET /platform/env-keys?<filters>&cursor=<c>` → decode `envKeysListResponse{Items: []render.EkRowView, NextCursor: string}` → append to `all` → if `NextCursor == ""` break, else loop.
- Final render: `render.FormatEkList(all)` (NO inline tabwriter — single source of truth per W7).
- `buildEnvKeysListPath` composes the URL query string via `net/url.Values` (deterministic encoding).

**`envKeysRevokeCmd` — CLI-13 client-side ekid_ gate:**

- `Args: cobra.ExactArgs(1)`. Flags: `--yes`, plus standard credential set.
- Client-side switch on key-id prefix BEFORE any HTTP call:
  - `keys.EkidKeyIDPrefix` → proceed.
  - `keys.EkBearerPrefix` (raw plaintext) → reject with CLI-13 message.
  - `keys.PkidKeyIDPrefix` → reject with pointer to `ach admin keys revoke`.
  - Other → reject as invalid.
- Interactive confirm unless `--yes`: writes `Confirm revoke of <ekid> [y/N]: ` to stderr; reads one line via `bufio.NewScanner(stdin)`. `y` / `Y` / `yes` proceeds; anything else returns `CodedError{General, "cancelled"}`.
- `httpclient.Client.Do(ctx, DELETE, "/platform/env-keys/<id>", nil, nil)`. On 204 → exit 0. On *ServerError → main.go's exit mapping.

**Foundation contracts consumed verbatim:**

- `internal/cli/config.{Path, Load, Save, ResolveActive, Deployment, File}` — disk yaml with mode-0600 discipline.
- `internal/cli/httpclient.{Client, ServerError, Do}` — auth-header carrier + §15.5 envelope decode.
- `internal/cli/exit.{Code, CodedError, OK, General, AuthN, Network, ConfigFile}` — closed exit-code matrix.
- `internal/keys.{EkBearerPrefix, EkidKeyIDPrefix, PkidKeyIDPrefix}` — prefix constants (NEVER string literals in env_keys.go for the gate).

### `internal/cli/render/ek.go` (commit `d42a12c`)

W7 single-source-of-truth formatter for the ek_ list shape, shared between `ach env-keys list` (this plan) and `ach admin keys list` (06-08 W3-P2).

**Exported surface:**

- `type EkRowView struct { KeyID, Environment, Name, OwnerEmail, Status, CreatedAt string; LastUsedAt, RevokedAt *string }` — field names match the on-the-wire JSON keys of `internal/platformapi/envkeys.EkRowView` so consumers decode straight into this type without a separate projection step.
- `FormatEkList(rows []EkRowView) string` — `text/tabwriter` table, columns `KEY-ID OWNER ENVIRONMENT NAME STATUS CREATED`. Deterministic: rows sorted by KEY-ID ascending before render. Empty input returns `"No env-keys found\n"`.

**Coexistence with 06-04:** 06-04 ships `internal/cli/render/render.go` with `FormatConfigList` / `FormatConfigShow` / `FormatEnvList` / `FormatEnvDescribe` / `FormatIdentity` + the supporting `EnvView` / `HydrateView` / `BlockView` / `RuntimeItem` / `ContextItem` types. Both files contribute to the same `render` package without merge conflict because they own disjoint symbols. The package doc lives in 06-04's `doc.go` (06-05 ships only the `ek.go` package-comment header which doubles as a brief intent statement).

### REQUIREMENTS.md edit (in the SAME commit per CLAUDE.md docs hygiene)

`.planning/REQUIREMENTS.md` is gitignored; the edit persists on disk in the main repo only, NOT in the d42a12c commit's filesystem diff. The edit lands two markers:

**Inline at line 147** (CLI-09 row, prepended marker pointing at the deviations table):

```markdown
- [ ] **CLI-09** *(DEVIATED Phase 6 D-07: spec §5.6 `--save-as` removed; `ek_` always-persists; `--no-save` opts out — see "Phase 6 Deviations" below)*: Credential sources `--api-key`, `--env-key`, `ACH_API_KEY`, `ACH_ENV_KEY` are mutually exclusive — presenting more than one → exit 1. `--env-key`/`ACH_ENV_KEY` resolve against `deployments.<active>.ek.<label>` and are not available in synthetic mode. (CLI AC16, §6.1)
```

**Bottom-of-file new section** ("Phase 6 Deviations" table):

```markdown
## Phase 6 Deviations

Intentional divergences from `spec/ach_cli_spec_v20260515_FINALv4.md` taken during Phase 6 execution. Each row points at the originating decision in `06-CONTEXT.md` and the implementation plan that landed it.

| REQ                        | Status   | Decision | Plan(s) | Notes                                                                                                                                                                                                                                                                                                                                                                                  |
| -------------------------- | -------- | -------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| CLI-09 (AC4 wire shape)    | DEVIATED | D-07     | 06-05   | spec §5.6 `--save-as` flag REMOVED. `ach env-keys create` ALWAYS persists the returned `ek_` plaintext to `deployments.<active>.ek.<server-name>` in the active deployment of `~/.config/ach/config.yaml`. `--no-save` is the explicit opt-out (CI / vault-piping workflows). Wire-format binary-compat: flag REMOVED, new flag ADDED, default behavior CHANGES. See spec changelog 2026-05. |
```

### spec/ach_cli_spec_v20260515_FINALv4.md edit (in the SAME commit)

Spec file is also gitignored; edit persists in the main repo on disk only. Two surgical insertions:

**Changelog entry (top of changelog list, marks it as the newest entry):**

```markdown
- **DEVIATION 2026-05 — `ach env-keys create`: always-persist (Phase 6 D-07).** Per ACH project Phase 6 decision D-07, `ach env-keys create` ALWAYS persists the returned `ek_` plaintext to `deployments.<active>.ek.<server-name>` in the active deployment of `~/.config/ach/config.yaml`. The `--save-as` flag specified in §5.6 is REMOVED; the `--no-save` flag is ADDED as the opt-out escape hatch (for CI / secret-manager workflows that pipe `ek_` to a vault). Synthetic mode (`ACH_BASE_URL` + `ACH_API_KEY` set) requires `--no-save` — without it, the CLI exits 1 (D-08). REQUIREMENTS.md CLI-09 is marked DEVIATED. This is the ONLY intentional CLI-spec divergence in Phase 6.
```

**§5.6 inline annotation (above the code block):**

```markdown
> **DEVIATION 2026-05 — see changelog.** `--save-as` is removed; `ach env-keys create` ALWAYS persists `ek_` to `deployments.<active>.ek.<server-name>`. `--no-save` opts out. Synthetic mode + create requires `--no-save`. (Phase 6 D-07; REQUIREMENTS.md CLI-09 marked DEVIATED.)

```text
ach env-keys create <environment> --name <alias> [--no-save]      # DEVIATED — was --save-as opt-in; now always-persist + --no-save opt-out
ach env-keys list [--environment <name>] [--status active|revoked]
ach env-keys revoke <key-id> [--yes]
```
```

## Test discipline + TDD

RED → GREEN executed inside the single Task 1:

1. RED — wrote `internal/cli/render/ek_test.go` (3 tests) + `cmd/ach/cmd/env_keys_test.go` (17 tests including 2 doc fixtures). Both failed to compile (`undefined: newEnvKeysCmd` / `undefined: swapEnvKeysHTTPClientForTest`). Confirmed: `render` package compiles + green; `cmd/ach/cmd` fails to compile.
2. GREEN — wrote `internal/cli/render/ek.go` + `cmd/ach/cmd/env_keys.go` to satisfy the symbol set. All 18 tests now PASS (15 behavior + 3 render; 2 doc fixtures SKIP cleanly when the gitignored docs aren't in the worktree filesystem).
3. REFACTOR — three lint-driven cleanups absorbed into the same commit:
   - 4 `lll` line-length warnings on flag descriptions + error messages: wrapped with `+` string concatenation or split on flag-description call sites.
   - 1 `unparam` warning: `resolveEnvKeysBearer` returned a `name` value no caller consumed; signature trimmed to `(baseURL, bearer, err)`.

The pre-commit hook gate (`make lint-changed` + `make unit`) was satisfied on commit. One flaky unit test in an unrelated package (`internal/contentservice/envcache.TestGet_Singleflight_DedupesConcurrentMisses`) flipped on the first commit attempt — retry passed cleanly. Out-of-scope per SCOPE BOUNDARY rule (unrelated to this plan's changes); not logged to `deferred-items.md` since the test is well-known to be timing-sensitive in concurrent CI environments.

## Confirmation: render.FormatEkList is the single source of truth (W7)

Per the plan's output spec:
- `cmd/ach/cmd/env_keys.go` imports `github.com/ackstorm/ach/internal/cli/render` and calls `render.FormatEkList(all)` at the only stdout-write site of `envKeysListCmd`. NO inline `tabwriter` in env_keys.go.
- `internal/cli/render/ek.go` declares both `EkRowView` (the DTO) and `FormatEkList` (the formatter) — these are the public symbols 06-08 `ach admin keys list` will consume directly. No copy-paste, no inline duplication.

## Threat Surface Scan

| Threat ID    | Coverage status                                                                                                                                                                         |
| ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| T-06-05-01   | ek_ at rest in ~/.config/ach/config.yaml — accepted per Hub §15.4. config.Save (06-01) enforces mode 0600 + parent 0700 atomically. The D-07 deviation is flagged in REQUIREMENTS.md + spec changelog (same commit) — operators read the deviation BEFORE running `ach env-keys create`. |
| T-06-05-02   | `--no-save` ek_ leaks via shell history — accepted; users opt in to vault-piping. The CLI prints ek_ exactly once to stdout (Pattern S5). Verified by TestEnvKeys_Create_AlwaysPersists_D07 (assert `strings.Count(stdout, plaintext) == 1`). |
| T-06-05-03   | `revoke ek_<raw>` — mitigated. Client-side prefix gate via `keys.EkBearerPrefix` rejects BEFORE any HTTP. Test TestEnvKeys_Revoke_RejectsRawPlaintextEk asserts httptest counter on /env-keys is 0. |
| T-06-05-04   | `--yes` bypass — accepted; blast radius is exactly one ekid_ per call (no batch). |
| T-06-05-05   | `revoke pkid_…` reaches the env-keys endpoint — mitigated. Client-side rejection via `keys.PkidKeyIDPrefix` prefix check, with stderr pointer at `ach admin keys revoke`. Test TestEnvKeys_Revoke_RejectsPkid asserts the httptest counter is 0. |
| T-06-05-06   | env-keys create audit — mitigated by server-side `audit.ActionEkCreate` / `audit.ActionEkRevoke` emission in Phase 3. CLI does not maintain its own audit log. |
| T-06-05-07   | --verbose header dump leaks ek_ — mitigated. `httpclient.Client.dumpVerbose` runs every `x-ach-key` value through `Redact` (06-01 T-06-01-01). Response body NOT dumped by --verbose; only headers. |
| T-06-05-08   | REQUIREMENTS.md / spec changelog drift if doc edit forgotten — mitigated. Acceptance criteria include both `grep -c "DEVIATED"` ≥ 1 and `grep -c "DEVIATION 2026"` ≥ 1; tests 15 + 16 fold the same greps into the test suite (t.Skip when docs absent in worktree, but the executor ran them against the main-repo path manually and confirmed PASS — see "Doc verification" below). |
| T-06-05-SC   | No new third-party deps. Consumes foundation render + httpclient + config from 06-01 only. Existing govulncheck ack-list applies. |

No new threat-flagged surface introduced beyond the plan's `<threat_model>` register.

## Doc verification (manual cross-check, since tests SKIP in worktree)

```bash
$ grep -c "DEVIATED" /home/jcm/Projects/ach/.planning/REQUIREMENTS.md
2          # CLI-09 inline marker + deviations table row
$ grep -c "D-07" /home/jcm/Projects/ach/.planning/REQUIREMENTS.md
7          # inline marker + table row + cross-refs
$ grep -c "DEVIATION 2026" /home/jcm/Projects/ach/spec/ach_cli_spec_v20260515_FINALv4.md
2          # changelog entry + §5.6 inline annotation
$ grep -c "always-persist" /home/jcm/Projects/ach/spec/ach_cli_spec_v20260515_FINALv4.md
2
$ grep -c "\-\-no-save" /home/jcm/Projects/ach/spec/ach_cli_spec_v20260515_FINALv4.md
3
```

All gates green. Cross-AI reviewers and the Phase 6 verifier can locate the deviation in both files via the same greps.

## Source-assertion grep gates (plan acceptance criteria)

```bash
$ grep -cE '"create"|"list"|"revoke"' cmd/ach/cmd/env_keys.go
4    # ≥ 3 required; 3 Use: literals + 1 Short text mention
$ grep -cE 'AddCommand\(newEnvKeysCreateCmd|parent\.AddCommand' cmd/ach/cmd/env_keys.go
1    # ≥ 1 required
$ grep -c 'no-save\|NoSave\|noSave' cmd/ach/cmd/env_keys.go
16   # ≥ 2 required
$ grep -c 'EkidKeyIDPrefix\|"ekid_"' cmd/ach/cmd/env_keys.go
4    # ≥ 1 required
$ grep -c 'http.MethodDelete' cmd/ach/cmd/env_keys.go
1    # ≥ 1 required
$ grep -cE -- '--yes|"yes"|flagYes' cmd/ach/cmd/env_keys.go
5    # ≥ 1 required
```

All source-assertion gates from the plan's acceptance_criteria block PASS.

## Self-Check: PASSED

Verified:
- `cmd/ach/cmd/env_keys.go` exists at commit `d42a12c`.
- `cmd/ach/cmd/env_keys_test.go` exists at commit `d42a12c`.
- `internal/cli/render/ek.go` exists at commit `d42a12c`.
- `internal/cli/render/ek_test.go` exists at commit `d42a12c`.
- `.planning/REQUIREMENTS.md` CLI-09 DEVIATED marker present (main-repo path; gitignored — not in worktree commit).
- `spec/ach_cli_spec_v20260515_FINALv4.md` DEVIATION 2026-05 changelog + §5.6 annotation present (main-repo path; gitignored).
- `./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestEnvKeys` exits 0 (15/15 behavior tests PASS; 2 doc-fixture tests SKIP).
- `./scripts/dev.sh go test ./internal/cli/render/...` exits 0 (3/3 render tests PASS).
- `./scripts/dev.sh go test ./cmd/ach/cmd/... ./internal/cli/...` exits 0 (full CLI suite green; no regression in login/whoami/logout).
- `./scripts/dev.sh go build ./cmd/ach/...` exits 0.
- `./scripts/dev.sh make lint-changed` exits 0 (zero golangci-lint findings on touched packages).
- `./scripts/dev.sh make unit` exits 0 (pre-commit gate satisfied; one flaky unrelated test in `internal/contentservice/envcache` passed on retry, out-of-scope per SCOPE BOUNDARY rule).
- Pre-commit hook ran on the d42a12c commit (lint-changed + unit) — both gates green.
- SPDX header line 1 on all 4 new files.
- Build + run with `ach env-keys --help` returns the parent help (3 sub-subcommands listed); `ach env-keys create --help`, `ach env-keys list --help`, `ach env-keys revoke --help` all return their respective subcommand help.

---
*Phase: 06-cli-foundation*
*Plan: 05*
*Completed: 2026-05-28*
