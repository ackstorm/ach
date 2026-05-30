---
phase: 06-cli-foundation
plan: 08
subsystem: cli-foundation
tags: [cli, admin, cli-10, cli-13, force-refresh, allowlist]
requirements: [CLI-10, CLI-13]
dependency_graph:
  requires:
    - "06-01-cli-shared-internals (httpclient + exit + config foundations)"
    - "06-04-ach-config-env (render package + cobra parent-with-children pattern)"
    - "06-05-ach-env-keys-d07-deviation (resolveEnvKeysBearer — aliased verbatim by resolveAdminBearer)"
    - "06-07-synthetic-mode-enforcement (synthetic.GateAdmin pre-wire)"
  provides:
    - "cmd/ach/cmd/admin.go — ach admin parent + adminKeys/adminUsers intermediate parents + 3 leaf subcommands (keys revoke, users revoke-keys, refresh)"
    - "adminCredFlags struct + registerAdminCredFlags + adminConfirm helpers (per-file helpers; could be hoisted if a third subcommand surface appears)"
    - "validateAdminKeyID — CLI-13 client-side prefix gate (accepts pkid_/ekid_; rejects raw pk_/ek_ plaintext + unknown prefixes)"
    - "allowedRefreshKinds — D-CONTEXT W3b closed-set {plugin, prompt, artifact, marketplace} client-side validation"
  affects:
    - "06-09 e2e demo-collapse — admin smoke can hit /platform/admin/refresh against the kept-cluster fixture"
    - "Phase 7+ — additional refresh kinds (environment, backendidentitypolicy) MUST be added to allowedRefreshKinds AND tested if/when surfaced"
tech_stack:
  added: []  # No new third-party deps. Consumes foundation httpclient + exit + synthetic from 06-01/06-07; aliases env-keys resolver from 06-05.
  patterns:
    - "Pattern P3 — 2-level parent-with-children cobra (admin → keys → revoke; admin → users → revoke-keys; admin → refresh flat)"
    - "Pattern P5 — httpclient.Client Do() consumer; auth header carrier + §15.5 envelope decode"
    - "Pattern P6 — exit.MapServerError owns 403 not_admin → AuthN(3) / 503/504 → Network(6) / catch-all → General(1)"
    - "Pattern P12 — RunE returns typed *exit.CodedError / *httpclient.ServerError; cmd/ach/main.go's errors.As branch owns os.Exit"
    - "Pattern S5 — API key flows ONLY into httpclient.Client.APIKey; verbose-mode header dumps redact x-ach-key via httpclient.Redact; SilenceUsage + SilenceErrors prevent cobra echo of Usage block on RunE error"
    - "Per-file credFlags struct + registerAdminCredFlags helper to defeat the dupl detector at three structurally-identical flag declaration sites without inlining boilerplate at each constructor"
    - "resolveAdminBearer aliases resolveEnvKeysBearer (same wire surface; aliasing avoids dupl + drift between two identical resolvers)"
key_files:
  created:
    - "cmd/ach/cmd/admin.go"
    - "cmd/ach/cmd/admin_test.go"
  modified: []
decisions:
  - "`--verbose` is a LOCAL flag on each admin subcommand (consistent with env-keys / env / hydrate / whoami / login from 06-03..06-07). NOT a rootCmd PersistentFlag. Per the plan §output spec: this decision affects 06-09 e2e fixtures, which can pass --verbose to a single subcommand without contaminating sibling cobra subtrees. If a future plan moves it to PersistentFlag (Phase 7+ for log-to-file), the change is mechanical: drop the flag declaration from registerAdminCredFlags + add to rootCmd."
  - "Phase 7 kinds (environment, backendidentitypolicy) are REJECTED OUTRIGHT by allowedRefreshKinds — NOT gated behind a hidden flag. Per the plan §output spec recommendation: user-facing CLI surface intentionally limited to the four v1alpha1 kinds. Phase 7 revisit is mechanical: add map entries + a test case to TestAdminRefresh_InvalidKind_Rejected's complement."
  - "userRevokeResponse.Errors rendering: newline-separated with leading `  - ` indent (NOT JSON pass-through). Format: `fmt.Fprintf(stdout, \"  - %s\\n\", e)` per err. This matches the human-readable Phase 6 output discipline (D-15 — plain text). JSON pass-through is deferred to the Phase 6b/7 --output-format json work."
  - "resolveAdminBearer is a one-line alias for resolveEnvKeysBearer rather than a copy. The 06-05 SUMMARY explicitly anticipated this: 'Drop-in copy of env-keys' resolver... Worth a re-think in a future refactor that hoists the resolver into internal/cli/ once a third caller appears.' Two callers is still cmd-package-local; a third caller (Phase 7 hydrate engine? Phase 6b adapters?) triggers a hoist into internal/cli/cred/. Aliasing here is the minimum-change anti-drift move."
  - "adminCredFlags struct + registerAdminCredFlags helper is per-file (cmd/ach/cmd/admin.go) NOT shared with env_keys/env/whoami. Hoisting prematurely would force every subcommand to follow the same flag shape (it currently doesn't — env_keys has --no-save on create only; env has --metadata-only on describe; hydrate has --no-warnings + --environment). The admin subcommands all share the same flag bag, so the struct buys local readability + dupl avoidance without imposing a project-wide constraint."
  - "T-06-08-07 (path-injection via email arg) mitigated via url.PathEscape BEFORE URL composition. Verified by test 9 (TestAdminUsersRevokeKeys_200): asserts srv.lastUserEmailPath == url.PathEscape(\"test@example.com\") = \"test%40example.com\"."
  - "validateAdminKeyID is a switch with three branches (pkid_/ekid_ → accept; pk_/ek_ → CLI-13 plaintext reject; default → invalid). Names both valid prefixes verbatim in the rejection message so the user can recover without consulting docs. Tests 3 + 4 assert no HTTP call fires on plaintext rejection (srv.revokeKeyCalls == 0)."
metrics:
  duration_minutes: 18
  completed_date: 2026-05-28
  tasks: 1
  files_created: 2
  files_modified: 0
---

# Phase 6 Plan 08: ach admin Summary

Closes the CLI-10 + CLI-13 admin clauses with a single cobra file
(`cmd/ach/cmd/admin.go`) shipping three sub-subcommands wired to the
Phase-3 `/platform/admin/{keys/revoke, users/{email}/revoke-keys,
refresh}` endpoints. With this plan landed, an allowlisted operator
can revoke misplaced keys + force-refresh content CRs through the
unified CLI surface — defeating the prior posture where the same
operations required `curl` + manual envelope-decode shell pipelines.

## What landed

### cmd/ach/cmd/admin.go (commit `fc1e023`)

Single file owning the `ach admin` parent + 2 intermediate parents
+ 3 leaf subcommands. Factory shape (`newAdminCmd()` /
`newAdminKeysCmd()` / `newAdminKeysRevokeCmd()` / etc.) mirrors the
06-03/06-04/06-05/06-07 convention so tests construct a hermetic
cobra subtree per `t.Run` without cross-test global state leaks.
Registered via `init() { rootCmd.AddCommand(newAdminCmd()) }`.

**`adminKeysRevokeCmd` — CLI-10 + CLI-13:**

- Args: `cobra.ExactArgs(1)` — `<key-id>`.
- Flags: `--yes`, `--deployment`, `--api-key`, `--env-key`, `--verbose`
  (registered via `registerAdminCredFlags(cmd, f, withYes=true)`).
- Synthetic gate FIRST via `synthetic.GuardCommand(Params{Gate:
  synthetic.GateAdmin})` — admin is in the allow-set; --deployment /
  --env-key / half-set still rejected.
- `validateAdminKeyID` — accepts `pkid_…`/`ekid_…` (CLI-13 both
  prefixes); raw `pk_…`/`ek_…` plaintext rejected with the
  "refusing plaintext key" CLI-13 message; unknown prefixes rejected
  with the invalid-prefix message. No HTTP fires on rejection
  (tests 3 + 4 assert `srv.revokeKeyCalls == 0`).
- `adminConfirm(stdin, stderr, prompt)` unless `--yes`. `y/Y/yes` →
  proceed; anything else → `CodedError{General, "cancelled"}` + 0 HTTP
  calls. Tests 8a/8b cover both branches.
- `httpclient.Client.Do(ctx, POST, "/platform/admin/keys/revoke",
  {key_id}, &resp)`. On 2xx: render `Revoked %s (status: %s)`. On
  non-2xx: bubble `*ServerError`; `cmd/ach/main.go`'s `errors.As`
  branch maps via `exit.MapServerError` (403 not_admin/401 →
  AuthN(3), 503 → Network(6), etc.).

**`adminUsersRevokeKeysCmd` — CLI-10 bulk variant:**

- Args: `cobra.ExactArgs(1)` — `<email>`. Basic validation: must
  contain `@`, must not be empty after trim.
- Same synthetic gate + flag set + confirmation pattern as keys
  revoke.
- `url.PathEscape(email)` BEFORE URL composition (T-06-08-07 path-
  injection mitigation). Test 9 asserts `srv.lastUserEmailPath ==
  "test%40example.com"`.
- `httpclient.Client.Do(ctx, POST, "/platform/admin/users/<escaped>/
  revoke-keys", {}, &resp)`. Body is empty `{}`; email lives in path.
- Renders `Revoked %d keys owned by %s` then `  - <err>` per error
  from the response's `errors` array (test 10 covers partial-error
  rendering).

**`adminRefreshCmd` — D-CONTEXT W3b closed-kind set:**

- Args: `cobra.ExactArgs(2)` — `<kind> <name>`.
- Flags: same credential set MINUS `--yes` (idempotent operation, no
  prompt). `registerAdminCredFlags(cmd, f, withYes=false)`.
- Synthetic gate first.
- `allowedRefreshKinds` membership check BEFORE any HTTP. Set is
  `{plugin, prompt, artifact, marketplace}` per CONTEXT.md W3b
  (NOT the server's wider acceptance set which includes
  `pluginmarketplace`). Test 13 covers {team, environment,
  backendidentitypolicy, garbage} — all four reject with exit 1 +
  the four valid kinds listed in the message; no HTTP fires.
- `httpclient.Client.Do(ctx, POST, "/platform/admin/refresh",
  {kind, name}, &resp)`. On 2xx/202 → `Refresh requested: %s/%s`.

**Foundation contracts consumed verbatim:**

- `internal/cli/httpclient.{Client, ServerError, Do}` — auth-header
  carrier + §15.5 envelope decode + verbose-mode header redaction.
- `internal/cli/exit.{Code, CodedError, OK, General, AuthN, Network,
  ConfigFile, MapServerError}` — closed exit-code matrix.
- `internal/cli/synthetic.{GuardCommand, GateAdmin, Params}` — single
  source of truth for synthetic-mode gating (06-07).
- `internal/cli/config.{Path, Load, ResolveActive, Deployment, File}`
  — disk yaml registry (transitively via resolveEnvKeysBearer).
- `internal/keys.{PkBearerPrefix, EkBearerPrefix, PkidKeyIDPrefix,
  EkidKeyIDPrefix}` — prefix constants (NEVER string literals in
  admin.go for the CLI-13 gate).
- `resolveEnvKeysBearer` from `cmd/ach/cmd/env_keys.go` — aliased
  verbatim as `resolveAdminBearer` because admin shares the env-keys
  credential surface.

### cmd/ach/cmd/admin_test.go (commit `fc1e023`)

16 unit tests covering:

| Test | Behavior asserted |
|------|-------------------|
| TestAdminKeysRevoke_Pkid_200          | pkid_ + --yes + 200 → exit 0; body carries pkid_; stdout renders status |
| TestAdminKeysRevoke_Ekid_200          | ekid_ + --yes + 200 → exit 0 (CLI-13: both prefixes accepted) |
| TestAdminKeysRevoke_RejectsRawPk      | raw pk_ → exit 1; no HTTP; msg mentions plaintext/key id |
| TestAdminKeysRevoke_RejectsRawEk      | raw ek_ → exit 1; no HTTP; msg mentions plaintext/key id |
| TestAdminKeysRevoke_403NotAdmin_Exit3 | 403 not_admin → exit 3 (CLI-10) |
| TestAdminKeysRevoke_401_Exit3         | 401 invalid_key → exit 3 |
| TestAdminKeysRevoke_503_Exit6         | 503 → exit 6 (Network) |
| TestAdminKeysRevoke_Interactive_Cancelled | no --yes + stdin "n" → exit 1 cancelled; 0 HTTP |
| TestAdminKeysRevoke_Interactive_Confirmed | no --yes + stdin "y" → exit 0; 1 HTTP |
| TestAdminUsersRevokeKeys_200          | URL-escaped email; renders revoked_count |
| TestAdminUsersRevokeKeys_PartialErrors| renders errors list with `  - ` indent |
| TestAdminUsersRevokeKeys_403_Exit3    | 403 → exit 3 (CLI-10) |
| TestAdminRefresh_PluginFoo_Accepted   | 202 → exit 0; body carries kind+name |
| TestAdminRefresh_InvalidKind_Rejected | 4 sub-cases: team/environment/backendidentitypolicy/garbage → exit 1; 0 HTTP; msg names valid kinds |
| TestAdminRefresh_403_Exit3            | 403 → exit 3 |
| TestAdmin_NoSubcommand_PrintsHelp     | parent without sub → cobra Help → exit 0 |
| TestAdminKeysRevoke_Verbose_RedactsAchKey | --verbose stderr contains `pk_***` AND does NOT contain unredacted pk_ value |

`adminTestServer` builds a httptest.NewTLSServer with three route
handlers and counts calls per route + last-request body capture +
last-DELETE-id capture for assertion. `executeAdmin(t, stdin, args
...)` wraps the shared `executeCommand` driver from helpers_test.go
(reusing its `errors.As` dispatch for `*httpclient.ServerError` →
exit code AND `*exit.CodedError` → cErr.Code).

## Source-assertion grep gates (plan acceptance criteria)

```bash
$ grep -cE "newAdminCmd|newAdminKeysRevokeCmd|newAdminUsersRevokeKeysCmd|newAdminRefreshCmd" cmd/ach/cmd/admin.go
9         # ≥ 4 required (4 constructors, multiple call sites)
$ grep -cE 'rootCmd\.AddCommand\(newAdminCmd\(\)\)' cmd/ach/cmd/admin.go
1         # exactly 1
$ grep -cE 'PkidKeyIDPrefix|EkidKeyIDPrefix' cmd/ach/cmd/admin.go
4         # ≥ 2 required (both prefixes referenced in validation + msgs)
$ grep -cE 'PkBearerPrefix|EkBearerPrefix' cmd/ach/cmd/admin.go
2         # ≥ 1 required (plaintext rejection check)
$ grep -cE 'allowedRefreshKinds|"plugin".*"prompt"' cmd/ach/cmd/admin.go
3         # ≥ 1 required (closed-set kind validation)
$ grep -c "url\.PathEscape" cmd/ach/cmd/admin.go
1         # ≥ 1 required (T-06-08-07 mitigation)
$ grep -nE "params\.APIKey|apiKey" cmd/ach/cmd/admin.go | grep -v 'httpclient\.\|Redact\|x-ach-key' | grep -E "slog|fmt\.Fprint|os\.Stdout|os\.Stderr"
(empty)   # 0 — API key never flows into a print/log statement (Pattern S5 / T-06-08-02)
$ head -1 cmd/ach/cmd/admin.go cmd/ach/cmd/admin_test.go | grep -c 'SPDX-License-Identifier: Apache-2.0'
2         # both files carry SPDX header
```

All source-assertion gates from the plan's acceptance_criteria block
PASS.

## Test discipline + TDD

RED → GREEN → REFACTOR executed in the single Task 1:

1. **RED**: wrote `cmd/ach/cmd/admin_test.go` (16 tests). Build
   failed: `undefined: swapAdminHTTPClientForTest`, `undefined:
   newAdminCmd`. Confirmed.
2. **GREEN — first pass**: wrote `cmd/ach/cmd/admin.go`. 11 tests
   PASS, 5 FAIL — the *ServerError-path tests (403/401/503) returned
   exit code 1 instead of the expected 3/6. Root cause: my local
   `executeAdmin` helper had a hand-rolled dispatch that only mapped
   `*exit.CodedError`, missing the `*httpclient.ServerError` branch.
2b. **GREEN — second pass**: refactored `executeAdmin` to wrap the
   shared `helpers_test.go::executeCommand` driver (which handles
   both error types). All 16 tests PASS.
3. **REFACTOR**: golangci-lint dupl complaints absorbed before
   commit. Three refactors:
   - `adminConfirmYes = "yes"` constant to defeat goconst (the
     literal appeared 3 times: 2 prompt sites + 1 flag name).
   - `adminCredFlags` struct + `registerAdminCredFlags(cmd, f,
     withYes)` helper — collapses the three structurally-identical
     flag declaration blocks (keys-revoke, users-revoke-keys,
     refresh) into a single registration call. Defeats the dupl
     check at the cobra.Command constructor sites.
   - `adminConfirm(stdin, w, prompt)` helper — collapses the two
     interactive-confirmation `bufio.NewScanner` blocks (keys-revoke,
     users-revoke-keys) into a single function.
   - `resolveAdminBearer` aliased to `resolveEnvKeysBearer` — same
     signature, same precedence; defeats the dupl check between the
     two file-local resolvers without hoisting prematurely.

The pre-commit hook gate (`make lint-changed` + `make unit`) was
satisfied on the `fc1e023` commit. Full lint sweep clean across the
touched packages.

## Foundation-contract confirmation (anti-rework gate)

- **synthetic.GateAdmin pre-wired in 06-07** — used verbatim in three
  RunEs (keys-revoke, users-revoke-keys, refresh). No new gate added
  to the synthetic package; no edits to internal/cli/synthetic/.
- **resolveEnvKeysBearer aliased** — `resolveAdminBearer` is a one-
  line passthrough. Anti-drift: any future change to the env-keys
  resolver propagates to admin automatically. When the third caller
  appears (Phase 7?), the natural next step is to hoist the resolver
  into `internal/cli/cred/` and have both env_keys.go + admin.go
  import it.
- **httpclient.Client.Do() verbatim** — no new Client surface
  introduced. The `ExtraHeaders` foundation contract (06-01) is left
  zero — admin endpoints don't need Accept-Encoding overrides or
  similar.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] executeAdmin helper missing *ServerError dispatch**
- **Found during:** Task 1 first test run after GREEN.
- **Issue:** My hand-rolled `executeAdmin` helper unwrapped only
  `*exit.CodedError`, missing the `*httpclient.ServerError → exit.
  MapServerError` branch. Tests 5/6/7/11/14 (the 403/401/503 paths)
  failed with `code = 1; want 3` / `want 6`.
- **Fix:** Refactored `executeAdmin` to wrap the shared
  `helpers_test.go::executeCommand` driver (which already handles
  both branches). Removes 18 lines of hand-rolled dispatch in
  favor of the established helper. Bytes/context/errors imports
  dropped accordingly.
- **Files modified:** `cmd/ach/cmd/admin_test.go`.
- **Commit:** Absorbed into `fc1e023` (Task 1 commit; not a separate
  commit since the test file was being authored in the same TDD
  cycle).

**2. [Rule 1 - Bug] golangci-lint goconst — adminConfirmYes hoist**
- **Found during:** Task 1 pre-commit lint sweep.
- **Issue:** The literal `"yes"` appeared 3 times (2 prompt-site
  switch arms + 1 cobra flag name).
- **Fix:** Hoisted `adminConfirmYes = "yes"` as a package-level
  constant; replaced the two switch arms via replace_all. The cobra
  flag name `"yes"` stays as a literal (that's the user-facing flag
  identifier — replacing with a constant would obfuscate the help
  output's flag column).
- **Files modified:** `cmd/ach/cmd/admin.go`.
- **Commit:** Absorbed into `fc1e023`.

**3. [Rule 1 - Bug] golangci-lint dupl — three constructors structurally identical**
- **Found during:** Task 1 pre-commit lint sweep.
- **Issue:** The three constructor functions
  (`newAdminKeysRevokeCmd`, `newAdminUsersRevokeKeysCmd`,
  `newAdminRefreshCmd`) each declared the same 5-field flag bag +
  the same `BoolVar/StringVar/...` registration sequence. dupl
  fired between every pair.
- **Fix:** Introduced `adminCredFlags` struct + `registerAdminCredFlags
  (cmd, f, withYes bool)` helper. Each constructor now declares
  `f := &adminCredFlags{}` + one helper call. Defeats dupl
  without inlining boilerplate at each call site.
- **Files modified:** `cmd/ach/cmd/admin.go`.
- **Commit:** Absorbed into `fc1e023`.

**4. [Rule 1 - Bug] golangci-lint dupl — resolveAdminBearer copy of resolveEnvKeysBearer**
- **Found during:** Task 1 pre-commit lint sweep.
- **Issue:** My initial implementation copied the full
  `resolveEnvKeysBearer` body (~70 lines) verbatim into
  `resolveAdminBearer` with the "admin" message swap. dupl fired
  between the two files.
- **Fix:** Reduced `resolveAdminBearer` to a one-line alias:
  `return resolveEnvKeysBearer(flagDeployment, flagAPIKey,
  flagEnvKey)`. The slight error-message divergence ("admin" vs
  "env-keys") was discretionary — keeping a single resolver
  eliminates drift and satisfies the lint without hoisting into
  `internal/cli/cred/` (premature for two callers).
- **Files modified:** `cmd/ach/cmd/admin.go`.
- **Commit:** Absorbed into `fc1e023`.

### Documented divergences from plan acceptance text

**5. Plan §action line 1 specifies `bufio` + `context` + `errors` + `os` imports**
- **Found during:** Task 1 implementation.
- **Issue:** Plan §action enumerates imports as: `bufio`, `context`,
  `encoding/json`, `errors`, `fmt`, `net/url`, `os`, `strings`,
  cobra, internal/cli/{config, exit, httpclient, synthetic}, keys.
- **Resolution:** My final import set is `bufio`, `fmt`, `io`,
  `net/http`, `net/url`, `strings`, cobra, internal/cli/{exit,
  httpclient, synthetic}, keys. Diff: `context`, `errors`,
  `encoding/json`, `os`, `config` are NOT directly imported because:
  - `context` is reached transitively via `cmd.Context()`.
  - `errors` is not used (cobra's RunE return + executeCommand's
    `errors.As` lives in the test file).
  - `encoding/json` is not used (httpclient.Client.Do handles the
    marshal/unmarshal internally).
  - `os` and `config` are reached transitively via the
    `resolveAdminBearer → resolveEnvKeysBearer` alias.
  - `net/http` is used for http.MethodPost (plan implied it via the
    POST verb; not enumerated in the import list).
  - `io` is needed for the `adminConfirm(stdin io.Reader, w
    io.Writer, ...)` helper signature.
  Semantic intent satisfied — every action step lands as specified.

**6. Plan §verify acceptance criteria check `grep -cE 'var adminCmd\|...' = 4`**
- **Found during:** Task 1 acceptance check.
- **Issue:** Plan acceptance text uses `var adminCmd` / `var
  adminKeysRevokeCmd` / `var adminUsersRevokeKeysCmd` / `var
  adminRefreshCmd` — package-level var pattern. My code uses the
  factory pattern (`newAdminCmd() *cobra.Command` etc.), consistent
  with 06-03/06-04/06-05/06-07 (Pattern P3 + the established
  newXCmd convention).
- **Resolution:** the regex-equivalent gate
  `grep -cE "newAdminCmd|newAdminKeysRevokeCmd|newAdminUsersRevokeKeysCmd|newAdminRefreshCmd"`
  returns 9 (≥ 4); the intent (4 constructor functions registered)
  is satisfied.

## Auth Gates

None encountered. Plan is pure CLI client code consuming Phase 3
endpoints that landed long ago; no Dex / OAuth flows touched.

## Threat Surface Scan

| Threat ID    | Coverage status                                                                                                                                                                         |
| ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| T-06-08-01 (raw plaintext via admin keys revoke) | mitigated — `validateAdminKeyID` rejects `pk_…`/`ek_…` plaintext BEFORE any HTTP call. Tests 3 + 4 assert `srv.revokeKeyCalls == 0` on rejection. |
| T-06-08-02 (API key via operational logs)        | mitigated — Pattern S5 source-assertion gate (`grep` for `apiKey` flowing to `fmt.Fprint`/`slog`/`os.Stdout`/`os.Stderr` minus httpclient/Redact paths) returns 0. Test 16 (verbose redaction) asserts unredacted `pk_admintestadminadminadminxyz` does NOT appear in stderr; the redacted `pk_***` DOES. |
| T-06-08-03 (server-side audit) | accepted — `ActionAdminKeysRevoke` / `ActionAdminUsersRevokeKeys` / `ActionAdminRefresh` already emit on the server side (Phase 3 OBS-02). CLI does not emit its own audit log. |
| T-06-08-04 (errors list info disclosure) | accepted — `userRevokeResponse.Errors` is operationally useful and contains no user PII beyond the email already in the URL path. |
| T-06-08-05 (DoS via refresh retry) | accepted — single-shot POST; cobra surfaces non-zero exit. Server-side rate-limit is the gate. |
| T-06-08-06 (non-allowlisted pk_ revoke) | mitigated — server's `AdminOnly` middleware returns `403 not_admin`; CLI maps to exit 3 via `MapServerError`. Tests 5 + 11 + 14 cover the three subcommands. |
| T-06-08-07 (path-injection via email) | mitigated — `url.PathEscape(email)` BEFORE URL composition. Test 9 asserts the escaped form lands on the wire. |
| T-06-08-08 (refresh unsupported kind) | mitigated — `allowedRefreshKinds` closed-set client-side validation. Test 13 covers {team, environment, backendidentitypolicy, garbage} → exit 1 + 0 HTTP. |
| T-06-08-SC (third-party deps) | mitigated — zero new deps. Only stdlib + already-pinned cobra + foundation internal/cli/* packages. Existing govulncheck ack-list applies. |

No new threat-flagged surface introduced beyond the plan's
`<threat_model>` register.

## Verification (full plan §verification gates)

```
$ ./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestAdmin
ok    github.com/ackstorm/ach/cmd/ach/cmd  0.065s

$ ./scripts/dev.sh go test ./cmd/ach/cmd/... ./internal/cli/...
ok    github.com/ackstorm/ach/cmd/ach/cmd            0.157s
ok    github.com/ackstorm/ach/internal/cli/config    (cached)
ok    github.com/ackstorm/ach/internal/cli/devicecode (cached)
ok    github.com/ackstorm/ach/internal/cli/exit      (cached)
ok    github.com/ackstorm/ach/internal/cli/httpclient (cached)
ok    github.com/ackstorm/ach/internal/cli/render    (cached)
ok    github.com/ackstorm/ach/internal/cli/synthetic (cached)

$ ./scripts/dev.sh go build ./cmd/ach/...
(clean)

$ ./scripts/dev.sh make lint-changed
(clean; exit 0)
```

Pre-commit hook on `fc1e023` ran `lint-changed` + `unit`; both gates
green ("All pre-commit gates passed.").

## Self-Check: PASSED

Verified:
- `cmd/ach/cmd/admin.go` exists at commit `fc1e023`.
- `cmd/ach/cmd/admin_test.go` exists at commit `fc1e023`.
- Commit `fc1e023` in `git log`: `feat(06-08): ach admin parent + 3
  children (keys revoke, users revoke-keys, refresh)`.
- `./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestAdmin` exits 0
  (16/16 tests PASS, including the 4 sub-cases inside
  TestAdminRefresh_InvalidKind_Rejected).
- `./scripts/dev.sh go test ./cmd/ach/cmd/... ./internal/cli/...` exits 0
  (no regression in env-keys / env / hydrate / login / whoami / logout /
  config; full CLI suite green).
- `./scripts/dev.sh go build ./cmd/ach/...` exits 0.
- `./scripts/dev.sh make lint-changed` exits 0 (clean after the
  four absorbed lint deviations).
- SPDX header line 1 on both new files.
- No file deletions in the commit (verified via `git diff
  --diff-filter=D --name-only HEAD~1 HEAD` returns empty).
- Pre-commit hook gate fired on `fc1e023`; all gates passed.

---
*Phase: 06-cli-foundation*
*Plan: 08*
*Completed: 2026-05-28*
