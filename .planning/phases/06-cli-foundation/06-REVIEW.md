---
phase: 06-cli-foundation
reviewed: 2026-05-28T00:00:00Z
depth: standard
files_reviewed: 51
files_reviewed_list:
  - cmd/ach/cmd/admin.go
  - cmd/ach/cmd/admin_test.go
  - cmd/ach/cmd/config.go
  - cmd/ach/cmd/config_test.go
  - cmd/ach/cmd/env.go
  - cmd/ach/cmd/env_keys.go
  - cmd/ach/cmd/env_keys_test.go
  - cmd/ach/cmd/env_test.go
  - cmd/ach/cmd/helpers_test.go
  - cmd/ach/cmd/hydrate.go
  - cmd/ach/cmd/hydrate_test.go
  - cmd/ach/cmd/login.go
  - cmd/ach/cmd/login_test.go
  - cmd/ach/cmd/logout.go
  - cmd/ach/cmd/logout_test.go
  - cmd/ach/cmd/synthetic_guard_test.go
  - cmd/ach/cmd/whoami.go
  - cmd/ach/cmd/whoami_test.go
  - cmd/ach/main.go
  - examples/README.md
  - internal/audit/events.go
  - internal/cli/config/config.go
  - internal/cli/config/config_test.go
  - internal/cli/devicecode/client.go
  - internal/cli/devicecode/client_test.go
  - internal/cli/devicecode/doc.go
  - internal/cli/doc.go
  - internal/cli/exit/exit.go
  - internal/cli/exit/exit_test.go
  - internal/cli/httpclient/client.go
  - internal/cli/httpclient/client_test.go
  - internal/cli/httpclient/redact.go
  - internal/cli/httpclient/redact_test.go
  - internal/cli/render/doc.go
  - internal/cli/render/ek.go
  - internal/cli/render/ek_test.go
  - internal/cli/render/render.go
  - internal/cli/render/render_test.go
  - internal/cli/synthetic/doc.go
  - internal/cli/synthetic/synthetic.go
  - internal/cli/synthetic/synthetic_test.go
  - internal/platformapi/auth/cli/doc.go
  - internal/platformapi/auth/cli/init.go
  - internal/platformapi/auth/cli/init_test.go
  - internal/platformapi/auth/cli/mount.go
  - internal/platformapi/auth/cli/session.go
  - internal/platformapi/auth/cli/session_test.go
  - internal/platformapi/auth/cli/token.go
  - internal/platformapi/auth/cli/token_test.go
  - internal/platformapi/auth/sso.go
  - internal/platformapi/auth/sso_test.go
  - internal/platformapi/server.go
  - test/e2e/README.md
  - test/e2e/cli_login_hydrate_test.go
  - test/e2e/phase6_helpers_test.go
findings:
  critical: 1
  warning: 10
  info: 4
  total: 15
status: issues_found
---

# Phase 6: Code Review Report

**Reviewed:** 2026-05-28T00:00:00Z
**Depth:** standard
**Files Reviewed:** 51 (full set per `<config>` file list)
**Status:** issues_found

## Summary

Phase 6 ships the `ach` CLI surface: `login`, `logout`, `whoami`,
`config`, `env-keys`, `env`, `hydrate`, `admin`, plus the server-side
device-code endpoints (`/platform/auth/cli/{init,token}`) and the
shared CLI internals (`internal/cli/{config,devicecode,httpclient,
exit,render,synthetic}`). The auth/security posture is generally
sound: plaintext PK/EK lifecycle is disciplined (single-emit on
success branches, masked elsewhere, redacted in `--verbose` header
dumps), synthetic-mode enforcement is centralized in
`internal/cli/synthetic`, the device-code flow uses Redis GETDEL
one-shot semantics, the SSO callback writes the freshly minted pk_
to Redis without leaking it through the browser HTML, and the typed
`*ServerError` → exit-code map at `cmd/ach/main.go` is a single
chokepoint.

The 15 findings below cluster in three areas:

1. **Cross-package contract drift between CLI and server** —
   `render.RuntimeItem.Name` field never populated by the server's
   hydrate handler, `envKeysListResponse.NextCursor` is `string`
   while `envListResponse.NextCursor` is `*string` (pagination
   semantics diverge), `examples/README.md` apply list out of sync
   with the file inventory.
2. **Brittle error-envelope handling** — `DisallowUnknownFields` on
   the §15.5 envelope decoder will mis-classify any future
   server-side field addition as `ErrEnvelopeDecode`; the
   `unauthorized_team` graceful fallback in `env describe` then
   silently breaks AND the AuthN exit-code mapping for 403s collapses
   to General.
3. **Cobra/error-render UX inconsistencies** — `SilenceUsage` /
   `SilenceErrors` set on env-keys and admin but NOT on hydrate /
   login / whoami / logout / config / env, so the latter produce
   duplicate stderr lines on failure; `MarkFlagRequired` errors leak
   unwrapped; hand-rolled `errors.As` reimplementations are fragile.

No exploitable security defects found. The findings are correctness
and quality risks — most CRITICAL/WARNING items have small, contained
fixes.

## Critical Issues

### CR-01: `decodeServerError` uses `DisallowUnknownFields()` — any server-side envelope extension turns errors into `ErrEnvelopeDecode` and silently loses Code/Message

**File:** `internal/cli/httpclient/client.go:228-233`
**Issue:**
The envelope decoder is strict-decoded:
```go
dec := json.NewDecoder(bytes.NewReader(raw))
dec.DisallowUnknownFields()
if err := dec.Decode(&envelope); err != nil {
    sErr.Underlying = fmt.Errorf("%w: %v", ErrEnvelopeDecode, err)
    return sErr
}
```
The server's `render.Error` (`internal/platformapi/render/json.go:55-61`)
emits exactly `{"error":{"code","message"},"request_id"}`. If any
handler ever adds a field (e.g. `outcome`, `retry_after`,
`error.details` — additive extensions are explicitly contemplated in
`internal/audit/events.go`'s file-level docstring under "Extension
policy (Hub §18.5)"), strict decode fails, `sErr.Code`/`sErr.Message`
come back empty, and `MapServerError` falls through to `General` for
any status that depended on the Code (notably 403). The asymmetry vs
the device-code client's own `decodeServerError`
(`internal/cli/devicecode/client.go:224-246`), which does NOT use
strict decode, confirms this was an inadvertent over-tightening.

Concrete downstream blast radius:
- `env describe` CLI-12 graceful fallback in `cmd/ach/cmd/env.go:190`
  reads `sErr.Code == "unauthorized_team"`. With empty Code, the
  fallback never triggers — the user gets exit 1 with a confusing
  generic error instead of the documented exit-0-with-`(unavailable)`
  behavior.
- `exit.MapServerError` 403 arm
  (`internal/cli/exit/exit.go:73-80`) reads
  `e.Code == "not_admin" || e.Code == "unauthorized_team"`. With
  empty Code on a 403, returns `General` (1) instead of `AuthN` (3),
  breaking the CLI-10 contract.
- Same blast radius applies to the 401 → AuthN arm if the server
  ever extends 401 envelopes.

**Fix:**
Drop the strict-decode (the §15.5 envelope is purposefully extensible):
```go
dec := json.NewDecoder(bytes.NewReader(raw))
// NOTE: do NOT DisallowUnknownFields — §15.5 is extensible per
// audit.go "Extension policy" docstring + Hub §18.5.
if err := dec.Decode(&envelope); err != nil {
    sErr.Underlying = fmt.Errorf("%w: %v", ErrEnvelopeDecode, err)
    return sErr
}
```
Add a regression test that fires a 403 with `{"error":
{"code":"unauthorized_team","message":"x"}, "request_id":"r",
"extra_field":"future"}` and asserts `sErr.Code == "unauthorized_team"`,
`exit.MapServerError(sErr) == exit.AuthN`.

## Warnings

### WR-01: `render.RuntimeItem.Name` field never populated by server — `env describe` table renders empty NAME column for every runtime row

**File:** `internal/cli/render/render.go:69-73`, `internal/cli/render/render.go:217-225`
**Issue:**
CLI's wire-shape DTO declares a `Name` field on `RuntimeItem`:
```go
type RuntimeItem struct {
    Name     string `json:"name,omitempty"`
    ID       string `json:"id"`
    Endpoint string `json:"endpoint"`
}
```
But the server's `internal/platformapi/hydrate/handler.go:64-67` emits
ONLY `{id, endpoint}`:
```go
type RuntimeItem struct {
    ID       string `json:"id"`
    Endpoint string `json:"endpoint"`
}
```
The decoder leaves `Name` zero. Then `FormatEnvDescribe` writes:
```go
_, _ = fmt.Fprintf(tw, "  model\t%s\t%s\t%s\n", m.Name, m.ID, m.Endpoint)
```
Every model/mcpServer/a2aAgent row appears in the table as
`model   <empty>  mdl_xxx  https://...` — the NAME column header
promises a value the data never carries, making the table mislabeled.
The unit test (`render_test.go:154-198`) hides this by manually
populating `{Name: "gpt-4", …}` and asserting on the endpoint string
only; the real wire decode never sets Name. ContextItem's `Name` field
IS populated by the server, so the bug is RuntimeItem-only.

**Fix:**
Either (a) drop the `Name` field from `RuntimeItem` and the matching
`%s` slot in the tabwriter format, OR (b) extend the server-side
hydrate handler to emit a `name` per runtime item (preferred for
operator-facing display — `id=mdl_gpt4 name=gpt-4` is more
informative than `id=mdl_gpt4` alone). Lock the choice with an
integration test that decodes a real `examples/hydrate.json` into
`render.HydrateView` and asserts `len(h.Runtime.Models[0].Name) > 0`
when the server fixture intends a non-empty name.

### WR-02: `envKeysListResponse.NextCursor` is `string` but `envListResponse.NextCursor` is `*string` — pagination loop in `env-keys list` cannot distinguish "empty string" from "absent"

**File:** `cmd/ach/cmd/env_keys.go:91-95`, `cmd/ach/cmd/env.go:72-75`
**Issue:**
Two pagination consumers use inconsistent JSON shapes for `next_cursor`:
- `envListResponse.NextCursor *string` (env.go:74) — `null` decodes
  to nil, loop exit is `resp.NextCursor == nil || *resp.NextCursor
  == ""`.
- `envKeysListResponse.NextCursor string` (env_keys.go:94) — `null`
  decodes silently to empty; loop exit is `if resp.NextCursor == ""`.

If the server ever emits `"next_cursor": "0"` (literal opaque cursor
value zero) or any future cursor encoding where the empty string is
a valid cursor token, the env-keys list loop terminates early. Use
the pointer form for both endpoints to make `null` vs `""`
distinguishable and to match the established `env.go` pattern.

**Fix:**
```go
type envKeysListResponse struct {
    Items      []render.EkRowView `json:"items"`
    NextCursor *string            `json:"next_cursor"`
}
// loop body:
if resp.NextCursor == nil || *resp.NextCursor == "" {
    break
}
currentCursor = *resp.NextCursor
```

### WR-03: `cmd/ach/main.go` double-renders errors when subcommands don't set `SilenceErrors: true`

**File:** `cmd/ach/main.go:35-55`, `cmd/ach/cmd/{hydrate,login,whoami,logout,config,env}.go`
**Issue:**
`cmd/ach/main.go` writes the error to `os.Stderr` directly after
`cmd.Execute()` returns non-nil:
```go
_, _ = fmt.Fprintln(os.Stderr, err)
os.Exit(int(exit.General))
```
But the cobra root command, when `SilenceErrors: false` (default),
ALSO writes `Error: <err>\n` to `cmd.ErrOrStderr()` before returning.
For subcommands like `hydrate`, `login`, `whoami`, `logout`, every
`config` child, and every `env` child — none of which set
`SilenceErrors: true` — the user sees the error message twice on
failure:
```
Error: --environment is required when using a pk_ key (CLI-06 / spec §5.7)
--environment is required when using a pk_ key (CLI-06 / spec §5.7)
```
`env_keys.go` (lines 154-155, 299-300, 412-413) and `admin.go`
correctly set `SilenceUsage: true, SilenceErrors: true`. The
inconsistency means the user UX differs by subcommand.

**Fix:**
Add `SilenceUsage: true, SilenceErrors: true` to every leaf
cobra.Command in
`cmd/ach/cmd/{hydrate,login,whoami,logout,config,env}.go`. Document
the convention in `06-PATTERNS.md` (Pattern P12). Add a guard test
that walks the cobra tree at construction time and fails when a leaf
has `SilenceErrors == false`.

### WR-04: `MarkFlagRequired` errors from cobra bypass the `*CodedError` exit-code mapping

**File:** `cmd/ach/cmd/env_keys.go:169-170`, `cmd/ach/main.go:47-54`
**Issue:**
`cmd.MarkFlagRequired("environment")` causes cobra to return a plain
error (`required flag(s) "environment" not set`) on missing flag.
That error is neither `*httpclient.ServerError` nor `*exit.CodedError`,
so it falls through to the catch-all in `cmd/ach/main.go:53` which
emits `os.Exit(int(exit.General))` — exit 1. That's the right code
by accident; the tests (`env_keys_test.go:341-369` —
TestEnvKeys_Create_RequiresEnvironment and
TestEnvKeys_Create_RequiresName) assert only `err != nil`, never the
exit code. Any future addition of new `MarkFlagRequired` calls
inherits the same behavior, but a refactor that changes cobra error
wrapping (e.g., to introduce a custom required-flag error) could
silently change the exit code.

**Fix:**
Tighten the affected tests to assert `code == exit.General`. For
defense-in-depth, add a small dispatcher in `main.go` that catches
the well-known cobra error sentinels (e.g.
`errors.Is(err, pflag.ErrHelp)` for `--help`) and explicitly maps
them to `exit.General` rather than relying on the fall-through.

### WR-05: `cmd/ach/cmd/env_keys.go::runEnvKeysRevoke` does not URL-escape `keyID` in the DELETE path

**File:** `cmd/ach/cmd/env_keys.go:502`
**Issue:**
```go
if doErr := hc.Do(ctx, http.MethodDelete, "/platform/env-keys/"+keyID, nil, nil); doErr != nil {
```
`keyID` is validated to start with `ekid_` but the SUFFIX character
class is not validated client-side — a user could pass
`ekid_../platform/something` and the resulting URL would traverse
the URL path. The server-side parser will reject most malformed
inputs, but the CLI silently emits a malformed request that may not
map cleanly to the intended endpoint. The sibling admin handler at
`cmd/ach/cmd/admin.go:407-408` (users revoke-keys) correctly uses
`url.PathEscape(email)`; the symmetry should hold here.

**Fix:**
```go
escaped := url.PathEscape(keyID)
path := "/platform/env-keys/" + escaped
if doErr := hc.Do(ctx, http.MethodDelete, path, nil, nil); doErr != nil {
    return doErr
}
```
Additionally, validate the suffix as `^[a-z2-7]{26}$` (the same
grammar `keys.NewKeyID` produces) at the top of `runEnvKeysRevoke`
so the malformed-but-prefix-correct case never reaches HTTP. Same
hardening applies to `cmd/ach/cmd/admin.go::runAdminKeysRevoke`,
which sends the key id through the JSON body — body injection is
safer than path interpolation but the principle (closed-grammar
validation client-side) holds.

### WR-06: `synthetic.GuardCommand` does NOT reject `--env-key` for `GateEnvKeysCreate` — silently dropped under synthetic mode

**File:** `internal/cli/synthetic/synthetic.go:127-135`, `cmd/ach/cmd/env_keys.go:174-279`
**Issue:**
`readOnlyGatesRejectingEnvKey` excludes `GateEnvKeysCreate`. A user
in synthetic mode (`ACH_BASE_URL` + `ACH_API_KEY` set) running:
```bash
ach env-keys create --environment demo --name x --no-save --env-key foo
```
gets neither a rejection from `GuardCommand` (env-keys create
passes the synthetic check because `--no-save` is set, but the
env-key check is skipped because `GateEnvKeysCreate` isn't in the
reject set) nor any error from `resolveEnvKeysBearer` (the synthetic
short-circuit at line 532 returns `(envBaseURL, envAPIKey, nil)`
without consulting `flagEnvKey`). The `--env-key` flag is silently
ignored. Confusing UX: the user thinks they set an ek_ override, but
the request goes out with the pk_ from `ACH_API_KEY`.

**Fix:**
Add `GateEnvKeysCreate` to `readOnlyGatesRejectingEnvKey` (rename
the map to `gatesRejectingEnvKeyInSynthetic` since create is not
read-only). The semantics — "ek_ labels require the config registry,
which synthetic has no access to" — apply equally to create.

### WR-07: `resolveEnvKeysBearer` synthetic short-circuit silently ignores `ACH_DEPLOYMENT` (defensive coverage gap)

**File:** `internal/cli/synthetic/synthetic.go:247-255`, `cmd/ach/cmd/env_keys.go:526-593`
**Issue:**
`synthetic.GuardCommand` rejects `--deployment` / `ACH_DEPLOYMENT`
under fully-synthetic mode. `resolveEnvKeysBearer`'s short-circuit at
lines 532-538 fires immediately on `envBaseURL != "" && envAPIKey
!= ""` without re-asserting that `ACH_DEPLOYMENT` was empty. The
test coverage in
`synthetic_guard_test.go::TestSyntheticGuard_DeploymentFlagRejected`
covers the `--deployment` flag case; the `ACH_DEPLOYMENT` env-var
case is covered only via the synthetic-test, not via the
resolver-direct path. If GuardCommand gets refactored, the disk-config
path could silently activate.

**Fix:**
Add a regression test in `env_keys_test.go` that sets
`ACH_BASE_URL + ACH_API_KEY + ACH_DEPLOYMENT=other` and confirms the
bearer comes from `ACH_API_KEY` (not the disk-config deployment
named `other`). Document the precedence rule explicitly in the
`resolveEnvKeysBearer` docstring (currently the synthetic clause is
buried in step 1 of the comment).

### WR-08: `mapHydrateError` and `mapVerifyError` reimplement `errors.As` manually — fragile against multi-error unwrapping

**File:** `cmd/ach/cmd/hydrate.go:406-437`, `cmd/ach/cmd/whoami.go:275-310`
**Issue:**
Both files define a hand-rolled `asHydrateErr` / `errorsAs` helper
that walks the `Unwrap() error` chain manually:
```go
for unwrap := err; unwrap != nil; {
    if t, ok := unwrap.(*httpclient.ServerError); ok {
        *target = t
        return true
    }
    type unwrapper interface{ Unwrap() error }
    u, ok := unwrap.(unwrapper)
    if !ok {
        return false
    }
    unwrap = u.Unwrap()
}
```
Stdlib `errors.As` already does this AND handles `Unwrap() []error`
(multi-error chains introduced in Go 1.20). The manual
reimplementation silently breaks if a caller ever wraps a server
error with `errors.Join` or a multi-error type. The file headers
reference the manual helper as "Mirrors the whoami errorsAs helper to
avoid cross-file coupling" — this couples each call site to a
fragile pattern instead of leveraging the stdlib.

**Fix:**
Replace both helpers with `errors.As(err, &sErr)` directly:
```go
func mapHydrateError(err error) error {
    var sErr *httpclient.ServerError
    if errors.As(err, &sErr) {
        return err
    }
    return &exit.CodedError{Code: exit.Network, Msg: err.Error(), Wrapped: err}
}
```
Delete `asHydrateErr`, `errorsAs`. The behavior is identical for
single-error chains AND robust against multi-error wrapping.

### WR-09: `examples/README.md` step 2 omits `examples/05b-pluginmarketplace-caveman.yaml` from the canonical apply list — doc inventory drift

**File:** `examples/README.md:37-41`
**Issue:**
The inventory table at line 14 includes
`05b-pluginmarketplace-caveman.yaml` (a single-plugin marketplace
canary), but the demo `kubectl apply -f ...` block at lines 37-41
does not include it. A user copy-pasting the demo block does not
exercise the marketplace fixture. If 05b is intentional, it should be
listed; if it is a redundant canary not needed for the demo, drop it
from the table to keep the surface area small.

**Fix:**
Either add `examples/05b-pluginmarketplace-caveman.yaml` to the demo
`kubectl apply` block, or annotate the inventory row with "(NOT part
of the default demo path; canary fixture for marketplace parser
tests)". Add a separate "Running the marketplace canary" sub-section
if the distinction is load-bearing.

### WR-10: `runEnvKeysCreate` plaintext is on stdout when `config.Save` fails — exit 8 leaves operator with a printed-but-unsaved ek_

**File:** `cmd/ach/cmd/env_keys.go:233-277`
**Issue:**
```go
// CLI-04: print plaintext exactly once.
_, _ = fmt.Fprintln(stdout, resp.Plaintext)

if noSave {
    return nil
}
// ... config.Load, ResolveActive ...
if saveErr := config.Save(cfgPath, file); saveErr != nil {
    _, _ = fmt.Fprintf(stderr, "warning: failed to persist ek_ to config: %v\n", saveErr)
    return &exit.CodedError{Code: exit.ConfigFile, ...}
}
```
On the unhappy path (config.Save fails with EROFS, disk full, etc.),
the plaintext has ALREADY been printed to stdout. The CLI returns
exit 8. The operator now has an ek_ floating in their terminal
scrollback that the server thinks is alive but the disk doesn't
carry. If the operator misses the stderr warning and re-runs `ach
env-keys create`, they get a SECOND ek_ that successfully persists —
and the first ek_ is orphaned server-side until its TTL (or manual
revoke). This violates the implicit "save-or-don't-save" atomicity
the CLI-04 "exactly once" guarantee implies.

**Fix:**
Two options:
1. **Print AFTER save** (D-07 then becomes "always-persist, then
   echo"): move `_, _ = fmt.Fprintln(stdout, resp.Plaintext)` after
   `config.Save` succeeds. On save failure, also call the server-side
   revoke endpoint to clean up the orphan.
2. **Document the half-failure**: the current behavior is the safer
   of the two for CI users who pipe stdout (they get the ek_ even
   if the disk write fails; the warning on stderr is the operator's
   cue to revoke manually).

Option 2 matches the current implementation but the choice is buried.
Lock it in a SPEC anchor and add a test that asserts both `stdout`
contains the ek_ AND exit code is `exit.ConfigFile` AND stderr
contains "failed to persist".

## Info

### IN-01: `cmd/ach/cmd/env_keys.go` carries a dead `var _ = context.Background` defensive import

**File:** `cmd/ach/cmd/env_keys.go:597`
**Issue:**
```go
// Defensive: keep context import used even on platforms where the
// linker might trim the unused import.
var _ = context.Background
```
The `context` package IS used directly in the file via `ctx :=
cmd.Context()` and inside `runEnvKeysList` / `runEnvKeysRevoke`. The
defensive line is dead code. The Go compiler errors on unused imports
at compile time, not at link time — there is no platform variation
the linker would change. The comment misdiagnoses the issue.

**Fix:**
Delete the line and the comment.

### IN-02: Test files redefine `executeCommand`-style helpers per file despite the helpers_test.go extraction

**File:** `cmd/ach/cmd/login_test.go:99-118`, `cmd/ach/cmd/logout_test.go:16-34`, `cmd/ach/cmd/env_keys_test.go:136-153`, `cmd/ach/cmd/whoami_test.go:50-72`
**Issue:**
`cmd/ach/cmd/helpers_test.go` defines `executeCommand` and is consumed
by `executeConfig`, `executeEnv`, `executeAdmin`. But
`executeLogin`, `executeLogout`, `executeEnvKeys`, `executeWhoami`
each redefine a near-identical body (capture stdout/stderr, call
ExecuteContext, errors.As for `*ServerError` or `*CodedError`, etc.).
This is the exact duplication the `helpers_test.go` comment claimed to
eliminate. Refactor opportunity — every per-subcommand helper should
delegate to `executeCommand` (with optional stdin arg) to keep one
source of truth.

**Fix:**
Extend `executeCommand` to take an optional `stdin string` parameter
(or expose a sibling `executeCommandWithStdin`), then collapse the
four redefined helpers to one-line wrappers around the shared driver.
Removes ~80 lines of duplicate boilerplate.

### IN-03: `synthetic.go:272` carries a dead `_ = allowedInSyntheticGates` reference

**File:** `internal/cli/synthetic/synthetic.go:270-273`
**Issue:**
```go
// Allow-set silent fallthrough — the gate is permitted, all
// cross-gate checks passed.
_ = allowedInSyntheticGates // referenced for godoc; the actual gating logic above is sufficient.
return nil
```
The `_ = allowedInSyntheticGates` line is a no-op. Go does not require
this kind of "godoc referenced" silencer — godoc cross-references
work without a runtime statement. If the variable is genuinely unused
(as the comment below it suggests), it should be deleted along with
the variable; if it IS used elsewhere, the silencer is misleading.

**Fix:**
Decide: either inline the allow-set check (`if
!allowedInSyntheticGates[p.Gate] { return error }`) so the variable
participates in the gating logic, OR delete both the variable
declaration and the silencer line.

### IN-04: Five files declare near-identical `var <name>HTTPClient *http.Client` test seams — duplicative pattern

**File:** `cmd/ach/cmd/hydrate.go:69-81`, `cmd/ach/cmd/env_keys.go:65-77`, `cmd/ach/cmd/whoami.go:42-54`, `cmd/ach/cmd/env.go:46-58`, `cmd/ach/cmd/admin.go:123-135`
**Issue:**
Five files copy-paste a near-identical test seam:
```go
var xxxHTTPClient *http.Client
func swapXxxHTTPClientForTest(t interface { Helper(); Cleanup(func()) }, c *http.Client) {
    t.Helper()
    previous := xxxHTTPClient
    xxxHTTPClient = c
    t.Cleanup(func() { xxxHTTPClient = previous })
}
```
Each test file then constructs its own `httpclient.Client` with
`HTTPClient: xxxHTTPClient` (nil in production, swapped in tests).
The pattern works but adds five global vars + five helpers for an
injected dependency that could live on a single `internal/cli/httpclient`
package-level seam (or — cleaner — be passed as an argument from a
factory in `cmd/`). The duplication is benign today; it'll compound
as new subcommands ship.

**Fix:**
Lift the seam into `internal/cli/httpclient`:
```go
package httpclient

var TestClientOverride *http.Client // nil in production

func newDefaultClient() *http.Client {
    if TestClientOverride != nil {
        return TestClientOverride
    }
    return &http.Client{Timeout: defaultTimeout}
}
```
Then every subcommand uses `httpclient.Client{HTTPClient: nil}` and
tests set `httpclient.TestClientOverride = ts.Client()` once per test
via a small swap helper exported from the package. Removes 50+ lines
across 10 files.

---

_Reviewed: 2026-05-28T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
