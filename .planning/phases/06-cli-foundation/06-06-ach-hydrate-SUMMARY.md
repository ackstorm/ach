---
phase: 06-cli-foundation
plan: 06
subsystem: cli-foundation
tags: [cli, hydrate, headline-demo, byte-for-byte, mutex-creds, pk-warning]
requirements: [CLI-03, CLI-05, CLI-06, CLI-09]
dependency_graph:
  requires:
    - "06-01-cli-shared-internals (httpclient.Client.DoRaw, ExtraHeaders, exit.Code, exit.CodedError, config.{Load,Save,ResolveActive,Mask})"
    - "06-03-ach-login-whoami-logout (newXCmd factory shape + whoamiTestEnv/seedConfig test helpers)"
  provides:
    - "ach hydrate cobra subcommand (newHydrateCmd factory; flags --environment, --no-warnings, --verbose, --api-key, --env-key, --deployment)"
    - "executeCommand test helper (cmd/ach/cmd/hydrate_test.go) — shared cobra → exit-code dispatcher mirroring cmd/ach/main.go's typed-error mapping"
  affects:
    - "W3 e2e (06-09): ach login + ach hydrate --environment demo > out.json | diff -q out.json examples/hydrate.json is the headline e2e anchor"
    - "W3 demo collapse (06-09): examples/hydrate-demo.sh deletion becomes safe once this command lands + W3 e2e proves byte parity"
tech_stack:
  added: []
  patterns:
    - "Pattern P2 — leaf cobra subcommand via newHydrateCmd() factory"
    - "Pattern P4 — closed-list mutex credential check (4 sources hardcoded; no flag-aliasing)"
    - "Pattern P5 — httpclient.Client consumer using DoRaw to stream byte-for-byte (NO json round-trip)"
    - "Pattern P12 — typed errors (*httpclient.ServerError + *exit.CodedError) flow to main.go for exit-code mapping"
    - "Package-level *http.Client seam (hydrateHTTPClient): nil → fresh stdlib client; tests swap to httptest.NewTLSServer().Client()"
key_files:
  created:
    - "cmd/ach/cmd/hydrate.go"
    - "cmd/ach/cmd/hydrate_test.go"
  modified: []
decisions:
  - "Implemented exactly as plan called for D-09 surface only: POST /platform/hydrate + io.Copy(stdout, resp.Body) on 2xx. NO state.json, NO adapter dispatch, NO concurrency lock — those are Phase 7 territory."
  - "§6.6 warning text taken VERBATIM from spec/ach_cli_spec_v20260515_FINALv4.md §6.6 (4 lines, mirrors Hub spec §15.3). Extracted as `const pkWarning` for source-greppability. The shortened 4-line text matches the spec's §6.6 changelog note (line 16: '§6.6 pk_ warning shortened to three lines' — the actual spec block at §6.6 ships 4 lines; the const matches the §6.6 block exactly)."
  - "io.Copy(stdout, resp.Body) via httpclient.Client.DoRaw (foundation API from 06-01) — NO inline httpclient extension required. The byte-for-byte stdout discipline is the W3-P3 e2e anchor: any future change MUST preserve the byte-equality vs the server's render.JSON output."
  - "Mutex credential check is a CLOSED LIST of 4 sources hardcoded into assertMutexCreds: --api-key, --env-key, ACH_API_KEY, ACH_ENV_KEY. No flag-aliasing, no env-prefix scan. Adding a new credential source is a one-file edit (T-06-06-01 mitigation)."
  - "Synthetic mode (ACH_BASE_URL + ACH_API_KEY) WORKS for pk_ runs. --env-key / ACH_ENV_KEY are REJECTED in synthetic mode because there is no config file to dereference the label against — surfaced via spec-mandated message ('--env-key requires a config-file-resolved deployment; not available with ACH_BASE_URL set')."
  - "ACH_ENVIRONMENT env-var satisfies the D-12 'pk_ requires --environment' gate alongside the --environment flag (effectiveEnv = environment || envEnvironment). Test TestHydrate_PK_EnvironmentFromEnv covers the env-only path."
  - "examples/hydrate.json was NOT regenerated — the golden artifact stays untouched (Phase 7 will evolve the wire format)."
  - "403 wrong_environment maps to General (1), NOT AuthN (3). The MapServerError closed switch reserves AuthN for the 403 codes `not_admin` and `unauthorized_team` only (06-01 foundation contract). wrong_environment falls through to General — documented in TestHydrate_EK_WrongEnvironment_403_Exit1."
  - "Extracted executeCommand test helper (cmd/ach/cmd/hydrate_test.go lines 28-46) as the shared typed-error → exit-code dispatcher. executeHydrate is now a 3-line delegation. The existing executeWhoami / executeLogout / executeLogin in 06-03 are NOT refactored — out of scope for this plan; future cleanup."
metrics:
  duration_minutes: 24
  completed_date: 2026-05-28
  tasks: 1
  files_created: 2
  files_modified: 0
---

# Phase 6 Plan 06: `ach hydrate` Summary

The Phase 6 headline subcommand: `ach hydrate --environment <name>` POSTs `/platform/hydrate` and streams the response body byte-for-byte to stdout via `io.Copy`. This is the W2 surface-only deliverable that makes the W3 demo-collapse reachable — the single-line replacement for `examples/hydrate-demo.sh` (`ach login` + `ach hydrate --environment demo > hydrate.json` byte-equals `examples/hydrate.json`).

## What landed

### cmd/ach/cmd/hydrate.go (Task 1 — `fd69e22`)

- `newHydrateCmd()` factory + RunE driving the D-09 surface-only flow per CLI spec §5.7 / §6.1 / §6.6.
- Flag set (6 flags, no collisions with whoami/login/logout): `--environment`, `--no-warnings`, `--verbose`, `--api-key`, `--env-key`, `--deployment`.
- `const pkWarning` — verbatim §6.6 text:
  ```
  warning: hydrating with pk_; runtime spend is attributed to your
  user/Team budgets, not the Environment budget (Hub spec §8.6).
  For Environment-scoped workloads, use ek_:
      ach env-keys create <environment> --name <alias>
  ```
- `runHydrate` flow (low-cyclomatic; helpers split out):
  1. Snapshot inputs into `hydrateInputs` struct (read all env vars once).
  2. `assertMutexCreds` — D-11 closed-list check on the 4 sources. >1 set → exit 1 BEFORE any I/O (T-06-06-01 / T-06-06-05 mitigation: httptest counter MUST stay at 0).
  3. `assertSyntheticConstraints` — D-11 / spec §3.3: --deployment and --env-key rejected under ACH_BASE_URL.
  4. `resolveBearer` — synthetic mode reads (ACH_BASE_URL, env/flag --api-key); config-disk mode does `config.Load` + `config.ResolveActive` + `pickBearer`.
  5. `pickBearer` — bearer-source switch (mutex already asserted so AT MOST one path runs).
  6. `keys.ClassifyBearer(bearer)` to dispatch pk_ vs ek_; D-12 gate emits `--environment is required when using a pk_ key` BEFORE the HTTP call (test counter == 0).
  7. Emit `pkWarning` to stderr if pk_ + !noWarnings (D-10).
  8. `postAndStream`: compose body (`{environment:...}` if effectiveEnv non-empty, else `struct{}{}`), call `httpclient.Client.DoRaw`, `io.Copy(stdout, resp.Body)`. NO `json.Unmarshal`/`Marshal` round-trip (W3-P3 golden-diff anchor).
- `hydrateHTTPClient` package-level test-only seam: production = nil (foundation httpclient default); tests swap to `httptest.NewTLSServer().Client()` for the lifetime of `t`.
- `mapHydrateError` — *ServerError flows through unchanged (cmd/ach/main.go's `errors.As` does the §9.3 mapping via `exit.MapServerError`); transport errors wrap as `Network` (exit 6).

### cmd/ach/cmd/hydrate_test.go (Task 1 — `fd69e22`)

15 tests via httptest.NewTLSServer + `swapHydrateHTTPClientForTest`:

| # | Test | Asserts |
|---|------|---------|
| 1 | `TestHydrate_PK_ByteForByte_Stdout` | bytes.Equal(stdout, canonicalHydrateJSON); x-ach-key carries pk_; body has environment |
| 2 | `TestHydrate_PK_EmitsWarning` | stderr contains §6.6 warning + ek_ hint; exit 0 |
| 3 | `TestHydrate_PK_NoWarnings_Suppresses` | stderr does NOT contain warning under --no-warnings |
| 4 | `TestHydrate_PK_MissingEnvironment_Exit1_NoHTTP` | exit 1; counter == 0 (client-side gate) |
| 5 | `TestHydrate_EK_NoEnvironmentRequired` | ek_ + no --environment → 200, exit 0; body OMITS environment field; NO pk_ warning |
| 6 | `TestHydrate_EK_WrongEnvironment_403_Exit1` | 403 wrong_environment → General (1) per MapServerError closed switch |
| 7 | `TestHydrate_MutexCreds_Exit1_NoHTTP` | --api-key + --env-key → conflict; counter == 0 |
| 7b | `TestHydrate_MutexCreds_EnvAndFlag_Exit1` | ACH_API_KEY + --env-key → conflict; counter == 0 |
| 8 | `TestHydrate_NoCredential_Exit1` | no flag/env/disk-pk → exit 1 with `ach login` hint |
| 9a | `TestHydrate_SyntheticMode_PK_Works` | ACH_BASE_URL + ACH_API_KEY → POST runs, byte-for-byte stdout |
| 9b | `TestHydrate_SyntheticMode_EnvKey_Exit1` | ACH_BASE_URL + --env-key → exit 1 (no config to dereference) |
| 10a | `TestHydrate_503_Exit6` | 503 → exit 6 (Network) |
| 10b | `TestHydrate_401_Exit3` | 401 → exit 3 (AuthN) |
| 10c | `TestHydrate_400_Exit1` | 400 missing_environment → exit 1 (General) |
| — | `TestHydrate_PK_EnvironmentFromEnv` | ACH_ENVIRONMENT satisfies D-12 (no --environment flag needed); body carries env |

Shared helpers (introduced here):
- `executeCommand(t, cmd, args...)` — cobra driver + typed-error → exit.Code dispatcher. Mirrors cmd/ach/main.go's production mapping (`errors.As` for `*httpclient.ServerError` then `*exit.CodedError`).
- `executeHydrate(t, args...)` — one-line delegation to `executeCommand(t, newHydrateCmd(), args...)`.
- `newHydrateMock(t, body)` — captures last x-ach-key, body, environment header.
- `newErrorServer(t, status, code, msg, reqID)` — §15.5 envelope mock.
- `runExitCodeMatrixCase(t, status, errCode, errMsg, reqID, wantExit, hydrateArgs...)` — one-call exit-code matrix runner (used by the 503/401/400/403 wrong_environment tests; collapses 4 near-identical tests into 3-line invocations and eliminates dupl lint flags).

## Foundation-contract confirmation (anti-rework gate)

The plan called out two foundation contracts to consume verbatim. Both honored:

1. **`httpclient.Client.DoRaw`** is consumed unchanged from 06-01. The 2xx branch returns the live `*http.Response` with Body unread; hydrate.go `io.Copy(cmd.OutOrStdout(), resp.Body)` preserves server bytes verbatim. NO inline httpclient extension.
2. **`exit.MapServerError` closed switch** maps 401 → AuthN, 503/504 → Network, 403 not_admin/unauthorized_team → AuthN (else General), default → General. 403 `wrong_environment` falls into General — documented in the test.

## §6.6 warning text (verbatim, for W3-P3 reference)

```
warning: hydrating with pk_; runtime spend is attributed to your
user/Team budgets, not the Environment budget (Hub spec §8.6).
For Environment-scoped workloads, use ek_:
    ach env-keys create <environment> --name <alias>
```

(End of warning. Trailing newline is part of the const for clean Fprint composition.)

## examples/hydrate.json status

UNCHANGED. The golden artifact stays at its pre-Phase-6 shape. Phase 7 will evolve the wire format if needed; Phase 6 only consumes the existing bytes.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Add `hydrateHTTPClient` package-level seam**
- **Found during:** Test design.
- **Issue:** Same root cause as the whoami test-discipline seam in 06-03. The hydrate command requires `https://` URLs (config.Load refuses non-https); `httptest.NewTLSServer` returns an `https://` URL but uses an ephemeral self-signed cert. The default `*http.Client` inside `httpclient.Client` rejects ephemeral certs. Need a way for tests to use `ts.Client()` (the TLS-trusting client) without per-call plumbing.
- **Fix:** Added `var hydrateHTTPClient *http.Client` package-level seam + `swapHydrateHTTPClientForTest(t, c)` helper in `cmd/ach/cmd/hydrate.go`. nil in production → `httpclient.Client.HTTPClient` zero value → foundation default. Mirrors the established whoami seam pattern documented in 06-03-SUMMARY.md (deviation #1).
- **Files modified:** `cmd/ach/cmd/hydrate.go`.

**2. [Rule 3 - Blocking] Refactor `runHydrate` to lower cyclomatic complexity (gocyclo)**
- **Found during:** Lint check after first GREEN pass.
- **Issue:** golangci-lint gocyclo flagged `runHydrate` at complexity 36 (gate is 30). The single function carried mutex check + synthetic detection + URL resolution + bearer resolution + classification + warning + body composition + io.Copy.
- **Fix:** Split into 5 helpers — `assertSyntheticConstraints`, `resolveBearer`, `pickBearer`, `postAndStream`, plus a `hydrateInputs` struct that snapshots flags+env once. `runHydrate` now drives the high-level flow at complexity well under 30.
- **Files modified:** `cmd/ach/cmd/hydrate.go`.

**3. [Rule 3 - Blocking] Extract `executeCommand` to dedupe `executeHydrate` vs `executeWhoami` (dupl)**
- **Found during:** Lint check.
- **Issue:** golangci-lint dupl flagged `executeHydrate` (hydrate_test.go) as a structural duplicate of `executeWhoami` (whoami_test.go from 06-03). Both functions ran identical cobra → typed-error → exit.Code logic.
- **Fix:** Extracted the dispatch logic into `executeCommand(t, *cobra.Command, args...) → (stdout, stderr, exit.Code, error)` in `hydrate_test.go`. `executeHydrate` is now a 3-line delegation. Did NOT refactor `executeWhoami` / `executeLogout` / `executeLogin` — those live in other plans' test files; out of scope for this plan to touch. dupl is satisfied because the new `executeCommand` body is unique (whoami_test's body is still a single block with no twin).
- **Files modified:** `cmd/ach/cmd/hydrate_test.go`.

**4. [Rule 3 - Blocking] Extract `runExitCodeMatrixCase` to dedupe HTTP-error matrix tests (dupl)**
- **Found during:** Lint check (second pass).
- **Issue:** golangci-lint dupl flagged the 503 and 401 tests as near-identical (server-mock → seed → execute → assert exit-code).
- **Fix:** Added `newErrorServer` + `runExitCodeMatrixCase` helpers in `hydrate_test.go`. Refactored TestHydrate_503_Exit6, TestHydrate_401_Exit3, TestHydrate_400_Exit1, and TestHydrate_EK_WrongEnvironment_403_Exit1 to use the shared runner. Each test is now 3-4 lines.
- **Files modified:** `cmd/ach/cmd/hydrate_test.go`.

**5. [Rule 1 - Bug] Long-line `canonicalHydrateJSON` constant (lll)**
- **Found during:** Lint check.
- **Issue:** The single-line constant exceeded the 120-char lll cap (187 chars).
- **Fix:** Split the JSON literal across three string-concat segments. Runtime value is unchanged (single uninterrupted JSON document + trailing newline) — verified by `bytes.Equal` in TestHydrate_PK_ByteForByte_Stdout.
- **Files modified:** `cmd/ach/cmd/hydrate_test.go`.

### Documented divergences from plan acceptance text

**6. Test 6 in plan: ek_ + --environment mismatch → exit 3**
- **Plan text:** "ach hydrate --env-key local-laptop --environment demo (ek_ + --environment) → both sent in request body; server-side mismatch yields 403 wrong_environment → exit 3 (per CLI exit-code map for 403)."
- **Actual mapping:** The 06-01 foundation's `exit.MapServerError` closed switch reserves AuthN (3) for the 403 codes `not_admin` and `unauthorized_team` only. `wrong_environment` falls through to `General` (1). This is intentional per the 06-01 threat-model T-06-01-07 mitigation (defends against exit-code spoofing).
- **Resolution:** Test `TestHydrate_EK_WrongEnvironment_403_Exit1` asserts `exit.General` (1), not `exit.AuthN` (3). The plan acceptance text was aspirational; the 06-01 closed-switch is authoritative. No code change in production hydrate.go required.

## Threat Surface Scan

| Threat ID | Coverage status |
|-----------|-----------------|
| T-06-06-01 | `assertMutexCreds` uses an EXPLICIT closed list of 4 sources: `--api-key`, `--env-key`, `ACH_API_KEY`, `ACH_ENV_KEY`. Tests `TestHydrate_MutexCreds_Exit1_NoHTTP` and `TestHydrate_MutexCreds_EnvAndFlag_Exit1` assert the gate fires BEFORE any HTTP (counter == 0). Source-assertion gate verifies 4 literal source names appear in hydrate.go. |
| T-06-06-02 | `io.Copy(stdout, resp.Body)` uses Go stdlib io. No cross-request buffer bleed possible (stdlib invariant). |
| T-06-06-03 | `--no-warnings` is the explicit user opt-out per spec §6.6. Mutex check + --environment requirement + synthetic enforcement are unaffected by --no-warnings (Test 3 + Test 4 confirm independence). |
| T-06-06-04 | `httpclient.Redact` (06-01) handles x-ach-key redaction in --verbose stderr dumps. Hydrate response body flows to stdout (intended); no plaintext stderr leak. |
| T-06-06-05 | Client-side `--environment is required when using a pk_ key` gate fires BEFORE the HTTP call. Test `TestHydrate_PK_MissingEnvironment_Exit1_NoHTTP` asserts counter == 0 on this path. |
| T-06-06-06 | TLS integrity provided by the Platform API endpoint. `io.Copy` is byte-faithful (stdlib). W3-P3 e2e golden-diff is the live verification gate. |
| T-06-06-07 | spec §6.1 permits `--api-key` to carry pk_ or ek_; `keys.ClassifyBearer` routes correctly. No info disclosed. |
| T-06-06-08 | Server-side hydrate handler (internal/platformapi/hydrate) emits the audit event with actor=key.id + request_id. CLI does not maintain its own audit log. |
| T-06-06-SC | No new deps. Only stdlib `net/http`, `io`, `fmt`, `os`, `strings` + foundation packages from 06-01 + cobra (already vendored) + internal/keys (already vendored). govulncheck ack-list unchanged. |

No new threat-flagged surface introduced beyond the plan's `<threat_model>` register.

## Self-Check: PASSED

Verified:
- `cmd/ach/cmd/hydrate.go` exists.
- `cmd/ach/cmd/hydrate_test.go` exists.
- Commit `fd69e22` in `git log` (`feat(06-06): add \`ach hydrate\` cobra subcommand`).
- `./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestHydrate"` exits 0 (15 tests pass).
- `./scripts/dev.sh go test ./cmd/ach/cmd/... ./internal/cli/...` exits 0 (no regressions).
- `./scripts/dev.sh go build ./...` exits 0.
- `./scripts/dev.sh sh -c '/workspace/bin/golangci-lint run ./cmd/ach/cmd/...'` exits 0 (clean).
- Pre-commit hook (lint-changed + unit) passed on commit `fd69e22`.
- SPDX header line 1 on both new files.
- Source-assertion gates from plan acceptance criteria all PASS:
  - `grep -c '"/platform/hydrate"' cmd/ach/cmd/hydrate.go` = 1 ✓
  - `grep -cE 'pkWarning|hydrating with pk_|§6\.6' cmd/ach/cmd/hydrate.go` = 8 ✓ (≥ 1)
  - `grep -cE '--no-warnings|noWarnings|NoWarnings' cmd/ach/cmd/hydrate.go` = 9 ✓ (≥ 1)
  - `grep -cE '--environment|"environment"|Environment\s+string|environment string|effectiveEnv' cmd/ach/cmd/hydrate.go` = 21 ✓ (≥ 2)
  - `grep -cE '"--api-key"|"--env-key"|"ACH_API_KEY"|"ACH_ENV_KEY"' cmd/ach/cmd/hydrate.go` = 6 ✓ (≥ 4)
  - `grep -cE 'io\.Copy\(' cmd/ach/cmd/hydrate.go` = 3 ✓ (≥ 1, raw stream-to-stdout; NO json round-trip)
- TestHydrate_PK_ByteForByte_Stdout uses `bytes.Equal` against `canonicalHydrateJSON` — byte-for-byte assertion passes.
- TestHydrate_PK_MissingEnvironment_Exit1_NoHTTP + TestHydrate_MutexCreds_*_NoHTTP confirm zero HTTP calls on client-side gate paths.
- TestHydrate_PK_NoWarnings_Suppresses confirms warning ABSENT under --no-warnings; TestHydrate_PK_EmitsWarning confirms PRESENT under default flags.

---
*Phase: 06-cli-foundation*
*Plan: 06*
*Completed: 2026-05-28*
