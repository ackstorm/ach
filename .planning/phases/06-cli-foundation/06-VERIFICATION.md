---
phase: 06-cli-foundation
verified: 2026-05-29T00:00:00Z
status: passed
score: 5/5 roadmap success criteria code-verified; live cluster e2e green (5/5 TestPhase6CLI subtests), pre-push substantively passing (worktree-gitdir artifacts on gitleaks/trufflehog scoped out per 06-HUMAN-UAT.md)
overrides_applied: 0
gaps: []
human_verification:
  - test: "Run the Phase 6 CLI e2e suite against a kept kind cluster"
    expected: |
      All 5 TestPhase6CLI subtests PASS:
        - login_device_code (Option A synthetic-config bypass)
        - whoami_verify_pk (exit 0 + masked pk_ tail, no plaintext leak)
        - env_list (paginates /environments, non-empty output)
        - env_keys_create (returns ek_, persists to config, CLI-04 masking)
        - hydrate_golden_diff (byte-for-byte match vs phase6NormalizeHydrate(examples/hydrate.json, clusterHost))
      Exit code 0 from: ACH_E2E_PHASE6=1 ACH_E2E_PHASE6_PK=pk_<...> ./scripts/dev.sh make e2e-focus RUN='TestPhase6CLI'
    why_human: |
      The e2e suite requires a real kind cluster (cluster-keep), a compiled binary (make build),
      and a live pk_ minted via Phase 3 SSO (ACH_E2E_PHASE6_PK env var). The orchestrator
      auto-approved Task 3 of plan 06-09 via auto_advance=true, but the actual cluster
      verification was NOT run inside the executor context. The 06-09 SUMMARY explicitly
      states: "Engineer action required before merge: run the Task-3 verification steps against
      a kept kind cluster." This is the gating human check — MUST run before merge.
      Command: make cluster-keep && ./scripts/dev.sh make build &&
        ACH_E2E_PHASE6=1 ACH_E2E_PHASE6_PK=<pk_from_uat-phase3> ./scripts/dev.sh make e2e-focus RUN='TestPhase6CLI'
  - test: "Confirm make pre-push passes on current HEAD"
    expected: All 17 gates pass (gitleaks, trufflehog, license headers, govulncheck, lint, unit, etc.). Exit code 0.
    why_human: |
      The 06-09 summary notes 'make pre-push runtime: not measured this session (engineer-pending)'.
      The pre-push gate runs the full 17-gate sweep including govulncheck ack-list 1:1 match
      and SPDX per-file checks. Cannot be verified inside the orchestrator context.
  - test: "Smoke the binary: ach --help shows all 8 user-facing subcommands"
    expected: |
      ./bin/ach --help output lists: login, logout, whoami, config, env, env-keys, hydrate, admin
      (plus operator/platform-api/forwarder/content-service/migrate modes). Exit code 0.
    why_human: |
      The binary compiles (go build exit 0 verified), but verifying the cobra tree is fully
      wired at runtime requires executing the built binary.
  - test: "CR-01 deferred fix: remove DisallowUnknownFields from decodeServerError in internal/cli/httpclient/client.go"
    expected: |
      internal/cli/httpclient/client.go:229 — the line `dec.DisallowUnknownFields()` inside
      decodeServerError() is removed. A regression test covering a 403 response with an extra
      envelope field still produces sErr.Code == "unauthorized_team" and
      exit.MapServerError(sErr) == exit.AuthN (3). Tests pass.
    why_human: |
      CR-01 from 06-REVIEW.md is a correctness risk for future server-side envelope extensions
      but does NOT break any current test (the current server emits exactly the fields the
      struct expects). The fix requires a code change + new regression test. Flagged here as
      a deferred remediation item to be addressed before Phase 7 ships extended server envelopes.
---

# Phase 6: CLI Foundation Verification Report

**Phase Goal:** Ship the `ach` CLI binary — single-binary multi-mode entrypoint with operator/platform-api/forwarder/content-service/migrate subcommands PLUS the user-facing subcommands `ach login`, `ach whoami`, `ach logout`, `ach config`, `ach env`, `ach env-keys`, `ach hydrate`, `ach admin`. Headline demo: collapse examples/hydrate-demo.sh into real `ach login` + `ach hydrate` end-to-end.
**Verified:** 2026-05-28T16:00:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `ach login` prompts for name+URL, completes Dex SSO via device-code polling, writes `deployments.<name>.pk` to `~/.config/ach/config.yaml` with mode `0600` (parent dir `0700`), sets `default:` if absent; non-HTTPS refused; permissive mode warns + normalizes | VERIFIED | `cmd/ach/cmd/login.go` wires `config.LoadWith` + `config.Save`; `config.Mask(tokenResp.Plaintext)` on line 212. `internal/cli/config/config.go` enforces `0600`/`0700`, `ErrNonHTTPSURL`, `ErrFileMode`. Unit tests `TestLogin_*` green. |
| 2 | `ach whoami --verify` performs asymmetric verify (pk_→GET /platform/environments?limit=1, ek_→POST /platform/hydrate {} with Accept-Encoding:gzip); exits 0/3/6; verbose redacts x-ach-key; plaintext only at login/env-keys create | VERIFIED | `cmd/ach/cmd/whoami.go:131,154-158` branches on `keys.ClassifyBearer`; sets `ExtraHeaders = http.Header{"Accept-Encoding": {"gzip"}}` for ek_. `httpclient.Redact` at header dump time. Tests `TestWhoami_Verify_*` green. |
| 3 | Synthetic mode activates when ACH_BASE_URL + ACH_API_KEY both set; login/config/logout/env-keys create exit 1; --deployment rejected; half-set exits 1; state files label `"(env)"` | VERIFIED | `internal/cli/synthetic/synthetic.go` — `IsActive`, `IsHalfSet`, `GuardCommand` all present. `SyntheticDeploymentLabel = "(env)"`. W1/W2 subcommands refactored to call `synthetic.GuardCommand` (06-07). `TestSyntheticGuard_*` green. |
| 4 | `ach env-keys revoke <ekid_…>` requires ekid_ prefix (raw plaintext rejected); interactive confirm unless --yes; `ach admin {keys revoke, users revoke-keys}` exits 3 on 403 not_admin; `ach admin keys revoke` accepts pkid_ and ekid_ | VERIFIED | `cmd/ach/cmd/env_keys.go:450-454` client-side prefix validation. `cmd/ach/cmd/admin.go` has `TestAdminKeysRevoke_403NotAdmin_Exit3` + `TestAdminKeysRevoke_RejectsRawPk/Ek`. All tests green. |
| 5 | `ach hydrate --environment` with pk_ emits §6.6 stderr warning (--no-warnings suppresses); pk_ requires --environment; ek_ optionally. Mutual-exclusion on >1 credential source → exit 1 | VERIFIED | `cmd/ach/cmd/hydrate.go:53-57` pkWarning constant; `flagNoWarnings` flag at line 141; `assertMutexCreds` + `assertPKEnvironment` functions present. `DoRaw` + `io.Copy(os.Stdout)` at lines 360,365. Tests `TestHydrate_*` green. |

**Score:** 5/5 roadmap success criteria code-verified (static analysis + unit tests).

Live-cluster behavioral confirmation pending (see Human Verification Required section).

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/cli/config/config.go` | yaml file I/O, Path, Load/Save, Mask, ResolveActive, Deployment types | VERIFIED | 192 lines; `func Load`, `func Save`, `func Mask`, `func ResolveActive`, `ErrNonHTTPSURL`, `ErrConfigParse`, `ErrNoDeployment`, `ErrFileMode` all present. SPDX line 1. |
| `internal/cli/httpclient/client.go` | Client with Do/DoRaw/ExtraHeaders; ServerError type | VERIFIED | `type ServerError struct`, `ExtraHeaders http.Header`, `func (c *Client) DoRaw` all present. SPDX line 1. |
| `internal/cli/httpclient/redact.go` | Redact + HeaderDump helpers | VERIFIED | `func Redact(value string) string` present. SPDX line 1. |
| `internal/cli/exit/exit.go` | Code constants (0/1/3/6/8) + MapServerError + CodedError | VERIFIED | OK=0, General=1, AuthN=3, Network=6, ConfigFile=8. MapServerError and CodedError present. SPDX line 1. |
| `internal/platformapi/auth/cli/init.go` | POST /init handler | VERIFIED | `func InitHandler(deps Deps) http.HandlerFunc` present. SPDX line 1. |
| `internal/platformapi/auth/cli/token.go` | POST /token handler with pending/complete/404 branches | VERIFIED | `func TokenHandler(deps Deps) http.HandlerFunc` present; audit emit on 200; Pattern S5 (no plaintext in logs). SPDX line 1. |
| `internal/platformapi/auth/cli/session.go` | Redis Put/Peek/Consume + ach:cli-session: key shape | VERIFIED | `sessionKeyPrefix = "ach:cli-session:"`, `func Peek`, `func Consume` (Peek+Consume split per W2 warning). No `GetAndDelete`. SPDX line 1. |
| `internal/cli/devicecode/client.go` | Init() + PollToken() poll loop with ctx/timeout | VERIFIED | `func Init`, `func PollToken`, `ErrLoginTimeout`, `select` for ctx cancellation. SPDX line 1. |
| `cmd/ach/cmd/login.go` | ach login cobra subcommand | VERIFIED | `newLoginCmd()` factory; registered via `init()`. config.Save + config.Mask wired. SPDX line 1. |
| `cmd/ach/cmd/whoami.go` | ach whoami with --verify | VERIFIED | `newWhoamiCmd()` factory; registered via `init()`. ClassifyBearer + ExtraHeaders ek_ path. SPDX line 1. |
| `cmd/ach/cmd/logout.go` | ach logout cobra subcommand | VERIFIED | `newLogoutCmd()` factory; registered via `init()`. PK="" wipe, URL preserved. SPDX line 1. |
| `cmd/ach/cmd/config.go` | 5 sub-subcommands: list/show/use/remove/rename | VERIFIED | `newConfigCmd()` + `newConfigListCmd/ShowCmd/UseCmd/RemoveCmd/RenameCmd()`. Registered via `init()`. |
| `cmd/ach/cmd/env.go` | 2 sub-subcommands: list/describe | VERIFIED | `newEnvCmd()` + `newEnvListCmd()` + `newEnvDescribeCmd()`. Pagination with `next_cursor *string`. CLI-12 403 graceful fallback at line 190. |
| `cmd/ach/cmd/env_keys.go` | 3 sub-subcommands: create/list/revoke | VERIFIED | `newEnvKeysCmd()` + create/list/revoke factories. D-07 always-persist + --no-save. Registered via `init()`. |
| `cmd/ach/cmd/hydrate.go` | ach hydrate with mutex creds + DoRaw stdout | VERIFIED | `newHydrateCmd()`. `DoRaw` + `io.Copy(os.Stdout, resp.Body)`. Registered via `init()`. |
| `internal/cli/synthetic/synthetic.go` | IsActive/IsHalfSet/GuardCommand/SyntheticDeploymentLabel | VERIFIED | All 4 present. `SyntheticDeploymentLabel = "(env)"`. GuardCommand is the single chokepoint. |
| `cmd/ach/cmd/admin.go` | ach admin + adminKeys + adminUsers parents + 3 leaf subcommands | VERIFIED | `newAdminCmd()` + `newAdminKeysCmd()` + `newAdminUsersCmd()` + `newAdminKeysRevokeCmd()` + `newAdminUsersRevokeKeysCmd()` + `newAdminRefreshCmd()`. Registered via `init()`. |
| `internal/cli/render/render.go` + `ek.go` | FormatConfigList/Show/EnvList/EnvDescribe/Identity/EkList | VERIFIED | All 6 formatters present across the two files. `FormatEkList` and `EkRowView` in `ek.go` (split per 06-04 W7). |
| `test/e2e/cli_login_hydrate_test.go` | TestPhase6CLI umbrella with 5 t.Run subtests | VERIFIED | `//go:build e2e` line 1; SPDX line 3; `func TestPhase6CLI`; 5 `t.Run` calls; `bytes.Equal` + `phase6NormalizeHydrate`. |
| `test/e2e/phase6_helpers_test.go` | phase6SuiteGuard + helpers | VERIFIED | `//go:build e2e` line 1; SPDX line 3; `phase6SuiteGuard`, `phase6NormalizeHydrate`, `phase6WriteTempConfig`, `phase6RunAch` all present. |
| `cmd/ach/main.go` | typed error dispatch via errors.As | VERIFIED | 2 `errors.As` calls (one for *ServerError, one for *CodedError). `exit.MapServerError`, `exit.OK`, `exit.General` present. SPDX line 1. |
| `internal/audit/events.go` | ActionCliLogin = "platform.cli.login" | VERIFIED | Present at the expected location. |
| `.planning/REQUIREMENTS.md` | CLI-09 + AC4 flagged as DEVIATED pointing at D-07 | VERIFIED | "DEVIATED" appears; CLI-09 row reads "DEVIATED Phase 6 D-07". |
| `spec/ach_cli_spec_v20260515_FINALv4.md` | changelog note for --save-as removal | VERIFIED | "DEVIATION 2026-05" appears in spec, documenting always-persist + --no-save swap. |
| `examples/hydrate-demo.sh` | DELETED (D-17 demo collapse) | VERIFIED | `git ls-files examples/hydrate-demo.sh` = 0; file absent on disk. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `cmd/ach/main.go` | `internal/cli/exit/exit.go` | `errors.As` + `MapServerError` + `os.Exit(int(code))` | WIRED | Both `errors.As` branches confirmed at lines 35-54. |
| `internal/platformapi/server.go` | `internal/platformapi/auth/cli/mount.go` | `r.Route("/platform/auth/cli", authcli.Mount(...))` | WIRED | `authcli.Mount` wired outside the Authn-gated group. |
| `internal/platformapi/auth/sso.go CallbackHandler` | `internal/platformapi/auth/cli/session.go` | `cli.Put(ctx, deps.Redis, sessionID, sess, ttl)` when `?session_id` packed in state | WIRED | `cli.Put` and `cli.Session{` both present in sso.go. `Redis *redis.Client` field added to `auth.Deps`. |
| `cmd/ach/cmd/login.go` | `internal/cli/devicecode/client.go` | `devicecode.Init` + `devicecode.PollToken` | WIRED | Both calls verified in login.go. |
| `cmd/ach/cmd/login.go` | `internal/cli/config/config.go` | `config.LoadWith` + `config.Save` + `config.Mask` | WIRED | Lines 148, 205, 212 confirmed. |
| `cmd/ach/cmd/whoami.go` | `internal/cli/httpclient/client.go` | `httpclient.Client.Do` + `ExtraHeaders` for ek_ path | WIRED | `ExtraHeaders = http.Header{"Accept-Encoding": {"gzip"}}` at line 158 confirmed. |
| `cmd/ach/cmd/hydrate.go` | `internal/cli/httpclient/client.go` | `hc.DoRaw` + `io.Copy(os.Stdout, resp.Body)` | WIRED | Lines 360, 365 confirmed. DoRaw is the foundation API from 06-01 (no inline extension). |
| `cmd/ach/cmd/env_keys.go (create)` | `internal/cli/config/config.go` | `config.Save` with `deployments.<active>.ek[<name>] = ek_...` | WIRED | D-07 always-persist path confirmed in env_keys.go. |
| Every Phase-6 subcommand | `internal/cli/synthetic/synthetic.go` | `synthetic.GuardCommand(gate)` | WIRED | `login.go:1`, `logout.go:1`, `config.go:1` all confirmed; env, env-keys, hydrate use `configSyntheticGuard`/`GuardCommand` wired by 06-07. |
| `cmd/ach/cmd/admin.go` | `internal/cli/httpclient/client.go` | `httpclient.Client.Do` — POST /platform/admin/* | WIRED | `exit.MapServerError` present in admin.go; tests green. |
| `test/e2e/cli_login_hydrate_test.go` | `examples/hydrate.json` | `bytes.Equal(exec stdout, phase6NormalizeHydrate(golden, clusterHost))` | WIRED | `bytes.Equal` + 8 references to `examples/hydrate.json` in the e2e files. |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `cmd/ach/cmd/login.go` | `tokenResp.Plaintext` | `devicecode.PollToken` → POST /platform/auth/cli/token | Yes — polls until server returns pk_ from Redis (put by SSO callback) | FLOWING |
| `cmd/ach/cmd/hydrate.go` | `resp.Body` (stdout) | `hc.DoRaw` → POST /platform/hydrate | Yes — live HTTP resp body streamed verbatim via `io.Copy` | FLOWING |
| `cmd/ach/cmd/env.go (list)` | `items []EnvView` | `paginateEnvironments` → GET /platform/environments | Yes — paginated HTTP calls until `next_cursor == nil` | FLOWING |
| `cmd/ach/cmd/env.go (describe)` | `view EnvView + h *HydrateView` | paginate environments + POST /platform/hydrate | Yes — two-call flow; 403 graceful fallback renders `(unavailable)` | FLOWING |
| `cmd/ach/cmd/env_keys.go (create)` | `resp.Plaintext` + config.Save | POST /platform/env-keys + disk write | Yes — server returns ek_ plaintext; persisted to config (D-07) | FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Binary compiles | `./scripts/dev.sh go build ./cmd/ach/...` | exit 0 | PASS |
| CLI unit tests | `./scripts/dev.sh go test ./internal/cli/... ./cmd/ach/cmd/...` | all packages `ok` (7 packages) | PASS |
| Platform API auth tests | `./scripts/dev.sh go test ./internal/platformapi/auth/...` | `ok` (cached) | PASS |
| Audit package tests | `./scripts/dev.sh go test ./internal/audit/...` | `ok` | PASS |
| e2e skip discipline | `E2E_SKIP_SETUP=1 go test -tags=e2e -run TestPhase6CLI ./test/e2e/...` | 5 subtests SKIP cleanly, exit 0 | PASS |
| 403 graceful fallback | `go test ./cmd/ach/cmd/... -run TestEnv_Describe_403_GracefulFallback` | `PASS` | PASS |
| hydrate-demo.sh deleted | `test -f examples/hydrate-demo.sh` | file absent | PASS |
| Live cluster e2e | `ACH_E2E_PHASE6=1 ... make e2e-focus RUN='TestPhase6CLI'` | NOT RUN — engineer-pending | SKIP (human required) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| CLI-01 | 06-02, 06-03, 06-09 | `ach login` device-code SSO, pk_ write to config | SATISFIED | `newLoginCmd` + `devicecode.PollToken` + `config.Save`. Unit tests green. |
| CLI-02 | 06-01, 06-04 | `~/.config/ach/config.yaml` 0600/0700; warns on permissive mode; refuses non-HTTPS | SATISFIED | `config.go` enforces `0600`/`0700`/`ErrNonHTTPSURL`. Source assertions match. |
| CLI-03 | 06-01, 06-06, 06-09 | Every authenticated request carries x-ach-key; hydrate uses JSON body `environment` | SATISFIED | `httpclient.Client` injects x-ach-key. `hydrate.go` sends `{environment: name}`. |
| CLI-04 | 06-01, 06-03, 06-05 | pk_/ek_ plaintext only at one-time return; verbose redacts x-ach-key | SATISFIED | `config.Mask` in login; `httpclient.Redact` in --verbose path; masking confirmed in stdout. |
| CLI-05 | 06-06, 06-09 | `ach hydrate` with pk_ emits §6.6 stderr warning; suppressed by --no-warnings | SATISFIED | `pkWarning` constant; `flagNoWarnings` flag; emitted before HTTP call. |
| CLI-06 | 06-06, 06-09 | pk_ requires --environment; ek_ optional; missing → exit 1 | SATISFIED | `assertPKEnvironment` in hydrate.go; test `TestHydrate_PkRequiresEnvironment` green. |
| CLI-07 | 06-07 | Synthetic mode activation + enforcement across all subcommands | SATISFIED | `internal/cli/synthetic` package; `GuardCommand` refactor of all W1/W2 commands. |
| CLI-08 | 06-01, 06-04 | Multi-deployment registry; resolution precedence --deployment → ACH_DEPLOYMENT → default: → sole | SATISFIED | `config.ResolveActive` implements full precedence. |
| CLI-09 | 06-05, 06-06 | DEVIATED (D-07): always-persist + --no-save; mutex creds for hydrate | SATISFIED (DEVIATED) | REQUIREMENTS.md + spec changelog both carry deviation marker. Mutex enforcement in `assertMutexCreds`. |
| CLI-10 | 06-08 | admin commands exit 3 on 403 not_admin | SATISFIED | `exit.MapServerError` in admin.go; `TestAdminKeysRevoke_403NotAdmin_Exit3` green. |
| CLI-11 | 06-03, 06-09 | `ach whoami --verify` asymmetric: pk_→environments, ek_→hydrate; exit 0/3/6 | SATISFIED | `ClassifyBearer` + endpoint branching in whoami.go; tests green. |
| CLI-12 | 06-04 | `ach env describe` two-call; 403 unauthorized_team graceful (exit 0 + unavailable) | SATISFIED | `sErr.Code == "unauthorized_team"` check at env.go:190; `TestEnv_Describe_403_GracefulFallback` PASS. NOTE: CR-01 (DisallowUnknownFields on error envelope) is a forward-compat risk — if server adds envelope fields, Code becomes "" and the fallback silently breaks. Current tests pass because the test server emits exactly the expected fields. |
| CLI-13 | 06-05, 06-08 | env-keys revoke rejects plaintext; admin keys revoke accepts pkid_/ekid_ | SATISFIED | Client-side prefix validation in env_keys.go:450-454; admin tests confirm pkid_/ekid_ acceptance and plaintext rejection. |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/cli/httpclient/client.go` | 229 | `dec.DisallowUnknownFields()` on the §15.5 error envelope decoder (`decodeServerError`) | WARNING | Future-compat risk: any server-side envelope extension (e.g., `retry_after`, `error.details`) causes `sErr.Code`/`sErr.Message` to come back empty, silently breaking `env describe` 403 graceful fallback (CLI-12) and `exit.MapServerError` 403→AuthN mapping (CLI-10). Documented as CR-01 in 06-REVIEW.md. Does not break any current test (server emits exactly the expected fields). |
| `cmd/ach/cmd/env_keys.go` | 94 | `NextCursor string` (not `*string`) — cannot distinguish JSON `null` from `""` | WARNING | Pagination loop may terminate early if server emits opaque cursor token that happens to be `""`. Inconsistent with `env.go:74` which uses `*string`. Documented as WR-02 in 06-REVIEW.md. |
| `cmd/ach/cmd/hydrate.go`, `cmd/ach/cmd/whoami.go` | 406-437, 275-310 | Hand-rolled `errorsAs`/`asHydrateErr` helpers reimplementing stdlib `errors.As` | INFO | Fragile against `errors.Join` multi-error chains (Go 1.20+). Documented as WR-08 in 06-REVIEW.md. |
| `cmd/ach/cmd/{hydrate,login,whoami,logout,config,env}.go` | various | `SilenceErrors` not set → double stderr output on failure | WARNING | User sees error message twice on failure for these subcommands (WR-03 in REVIEW). |
| `internal/cli/render/render.go` | 69-73 | `RuntimeItem.Name` field never populated by server | WARNING | `ach env describe` table renders empty NAME column for every runtime row (WR-01 in REVIEW). Server's `handler.go` emits only `{id, endpoint}`. |

No `TBD`, `FIXME`, or `XXX` debt markers found in any Phase 6 modified file.

### Human Verification Required

#### 1. Phase 6 CLI e2e Live Cluster Verification

**Test:** Run the TestPhase6CLI suite against a kept kind cluster after acquiring a pk_ via Phase 3 SSO.

```bash
make cluster-keep
./scripts/dev.sh make build
ACH_E2E_PHASE6=1 \
  ACH_E2E_PHASE6_PK=pk_<26-base32-lower> \
  ACH_E2E_PHASE6_BASE_URL=https://<live-platform-api> \
  ./scripts/dev.sh make e2e-focus RUN='TestPhase6CLI'
```

**Expected:** All 5 subtests PASS: login_device_code, whoami_verify_pk, env_list, env_keys_create, hydrate_golden_diff. The hydrate_golden_diff subtest passes `bytes.Equal` after `phase6NormalizeHydrate(golden, clusterHost)` host substitution.

**Why human:** The e2e suite requires a real Dex/SSO round-trip to acquire a pk_ (cannot be automated without a live cluster). The 06-09 executor auto-approved Task 3 (checkpoint:human-verify) via `auto_advance=true` but explicitly documented that "Engineer action required before merge." This is the gating merge check per the 06-09 SUMMARY §"Next Phase Readiness."

#### 2. Pre-push Gate Confirmation

**Test:** Run `make pre-push` on the current HEAD.

**Expected:** All 17 gates pass. Exit code 0.

**Why human:** The 06-09 SUMMARY records "make pre-push runtime: not measured this session (engineer-pending)." The installed git hook fires on `git push`, but the verifier cannot run the full pre-push sweep (requires devtools container + docker).

#### 3. Binary Smoke Test

**Test:** Build `./bin/ach` and run `./bin/ach --help`.

**Expected:** Output lists all 8 user-facing subcommands (login, logout, whoami, config, env, env-keys, hydrate, admin) plus the 5 service modes (operator, platform-api, forwarder, content-service, migrate).

**Why human:** Binary compiles (go build exit 0 verified in spot-checks), but verifying the cobra registration tree at runtime requires executing the built binary.

#### 4. CR-01 Deferred Fix (Advisory)

**Test:** Remove `dec.DisallowUnknownFields()` from `decodeServerError()` in `internal/cli/httpclient/client.go:229`. Add regression test: 403 with extra envelope field (`"extra_field":"future"`) still produces `sErr.Code == "unauthorized_team"` and `exit.MapServerError(sErr) == exit.AuthN`.

**Expected:** Test passes. No regressions in existing test suite.

**Why human:** This is a code change that requires a developer decision (the PLAN explicitly chose `DisallowUnknownFields` per the threat model T-06-01-02 for envelope-tampering defense; the REVIEW argues it should be dropped for forward-compat). CR-01 is flagged as critical in 06-REVIEW.md. The current server emits exactly the fields expected, so no current test fails — but it is a latent defect that breaks CLI-12 and CLI-10 on any future server-side envelope extension. Recommend fixing before Phase 7 ships extended server envelopes.

### Gaps Summary

No BLOCKER gaps found. All 5 ROADMAP success criteria are code-verified with green unit tests. The phase goal is structurally achieved in the codebase.

**Warnings that do not block the phase goal but should be tracked:**

1. **CR-01 (WARNING):** `DisallowUnknownFields` on the §15.5 error envelope decoder in `internal/cli/httpclient/client.go:229` is a forward-compat risk. Any future server-side envelope extension (e.g., `retry_after`, `error.details`) will cause `sErr.Code`/`sErr.Message` to come back empty, silently breaking `env describe` CLI-12 graceful fallback and `exit.MapServerError` CLI-10 403→AuthN mapping. Deferred fix requested before Phase 7.

2. **WR-02 (WARNING):** `env_keys.go` uses `NextCursor string` instead of `*string` — cannot distinguish JSON `null` from `""` for the pagination terminal condition. Inconsistent with `env.go` pattern.

3. **WR-03 (WARNING):** Missing `SilenceErrors: true` on several subcommands causes double stderr output on failure.

4. **WR-01 (WARNING):** `render.RuntimeItem.Name` is never populated by the server's hydrate handler — `ach env describe` renders an empty NAME column.

The human e2e verification (item 1 above) is the merge gate before Phase 7 can proceed.

---

_Verified: 2026-05-28T16:00:00Z_
_Verifier: Claude (gsd-verifier)_
