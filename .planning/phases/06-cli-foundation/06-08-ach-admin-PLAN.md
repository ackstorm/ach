---
phase: 06-cli-foundation
plan: 08
type: execute
wave: 3
depends_on:
  - 06-01-cli-shared-internals
  - 06-04-ach-config-env
  - 06-05-ach-env-keys-d07-deviation
  - 06-07-synthetic-mode-enforcement
files_modified:
  - cmd/ach/cmd/admin.go
  - cmd/ach/cmd/admin_test.go
autonomous: true
requirements:
  - CLI-10
  - CLI-13

must_haves:
  truths:
    - "`ach admin keys revoke <key-id>` POSTs to /platform/admin/keys/revoke with {key_id} and exits 3 on 403 not_admin, 0 on 200 (CLI-10, CLI-13)"
    - "`ach admin keys revoke` accepts BOTH `pkid_…` AND `ekid_…` key-id prefixes; raw `pk_…`/`ek_…` plaintext is rejected client-side with a clear message and exit 1 (CLI-13)"
    - "`ach admin users revoke-keys <email>` POSTs to /platform/admin/users/{email}/revoke-keys (URL-escaped email), parses {revoked_count, errors} response, exits 3 on 403 not_admin (CLI-10)"
    - "`ach admin refresh <kind> <name>` POSTs to /platform/admin/refresh with {kind, name}; `kind` validated client-side against the closed set {plugin, prompt, artifact, marketplace} per spec §5.10 / D-CONTEXT W3b; exits 3 on 403 not_admin"
    - "Each admin subcommand exits 0 on 200 success, 3 on 401 invalid_key/403 not_admin/403 unauthorized_team, 6 on network/503, 1 on client-side validation failure or any other server error — via exit.MapServerError"
    - "`ach admin keys revoke` and `ach admin users revoke-keys` prompt for interactive y/n confirmation unless `--yes` is set; `ach admin refresh` does NOT prompt (idempotent, no destructive effect on the caller)"
    - "Synthetic mode: admin subcommands run normally when synthetic.IsActive is true (admin endpoints accept pk_ only — and a synthetic pk_ + allowlisted email works the same as a config-loaded pk_); no synthetic-mode rejection"
    - "Verbose-mode header dumps redact x-ach-key to <prefix>_*** via httpclient.Redact (CLI-04, Pattern S5)"
  artifacts:
    - path: "cmd/ach/cmd/admin.go"
      provides: "ach admin parent + adminKeys + adminUsers parents + leaf subcommands keys-revoke, users-revoke-keys, refresh"
      contains: "var adminCmd"
    - path: "cmd/ach/cmd/admin_test.go"
      provides: "httptest-backed unit tests covering exit-3, exit-0, exit-6 paths + plaintext-rejection + --yes + kind validation"
      contains: "TestAdminKeysRevoke"
  key_links:
    - from: "cmd/ach/cmd/admin.go"
      to: "internal/cli/httpclient/client.go"
      via: "httpclient.Client.Do — POST /platform/admin/{keys/revoke,users/{email}/revoke-keys,refresh}"
      pattern: "/platform/admin/"
    - from: "cmd/ach/cmd/admin.go"
      to: "internal/cli/exit/exit.go"
      via: "exit.MapServerError(*ServerError) — 403 not_admin → exit 3"
      pattern: "exit.MapServerError"
    - from: "cmd/ach/cmd/admin.go keys revoke"
      to: "internal/keys (PkidKeyIDPrefix, EkidKeyIDPrefix)"
      via: "client-side key-id prefix validation before HTTP call"
      pattern: "keys.PkidKeyIDPrefix\\|keys.EkidKeyIDPrefix"
---

<objective>
Ship `ach admin {keys revoke, users revoke-keys, refresh}` per CLI
spec §5.10 — the three admin subcommands that exit 3 on `403 not_admin`
(CLI-10) and surface the spec §16 wire contract for the closed `kind`
set on force-refresh (D-W3b CONTEXT.md). `keys revoke` accepts both
`pkid_…` and `ekid_…` key IDs and rejects raw `pk_…`/`ek_…` plaintext
client-side (CLI-13).

This is W3-P2 per the wave structure in `06-CONTEXT.md` `<domain>`.
Depends on:
- 06-01 — `internal/cli/{httpclient, exit, config}` (Wave 1 internals).
- 06-04 — `internal/cli/render/` text formatters (Wave 2 first plan)
  for the admin response rendering (revoked_count + errors list).
- 06-05 — establishes the `internal/keys` prefix-validation import
  pattern (the env-keys revoke command already rejects raw plaintext;
  admin keys-revoke mirrors that with both prefixes).
- 06-07 — `internal/cli/synthetic` (admin works in synthetic mode but
  consumes the same Params struct).

The server-side endpoints (`POST /platform/admin/keys/revoke`,
`POST /platform/admin/users/{email}/revoke-keys`,
`POST /platform/admin/refresh`) already shipped in Phase 3 plan
03-10 (`internal/platformapi/admin/handler.go`). This plan ships the
CLI client only — NO server-side changes.

Purpose: closes CLI-10 + CLI-13 admin clauses. The admin surface is
the operator-facing escape hatch for revoking misplaced keys and
forcing a content refresh outside the §10.3 hourly cycle. Without
this plan, an allowlisted operator must `curl` the endpoints directly
— defeating the value of the unified CLI.

Output: 1 new cobra subcommand file (`cmd/ach/cmd/admin.go`) + its
test file. ~250-300 LOC including tests; no new packages.
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
@spec/ach_hub_spec_v20260515_FINALv4.md
@CLAUDE.md
@cmd/ach/cmd/migrate.go
@internal/platformapi/admin/handler.go
@internal/platformapi/admin/mount.go
@internal/platformapi/admin/allowlist.go
@internal/keys/prefixes.go
@.planning/phases/06-cli-foundation/06-05-ach-env-keys-d07-deviation-PLAN.md
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Author cmd/ach/cmd/admin.go — parent + 3 leaf subcommands + kind validation</name>
  <files>
    cmd/ach/cmd/admin.go
    cmd/ach/cmd/admin_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"Wave 3 — Edges (3 plans)" — W3-P2 `ach admin` bullet (lines listing keys revoke + users revoke-keys + refresh + closed-kind set + exit-3 on 403 not_admin)
    - 06-CONTEXT.md `<decisions>` D-16 (exit-code matrix — 3 = AuthN, 6 = Network)
    - 06-PATTERNS.md §"Pattern P3" lines 168-209 (parent-with-children cobra; admin is a 2-level parent: admin → keys → revoke + admin → users → revoke-keys)
    - 06-PATTERNS.md §"Pattern P5" lines 246-291 (httpclient with x-ach-key + ServerError decode)
    - 06-PATTERNS.md §"Pattern P6" lines 295-361 (exit.MapServerError — 403 not_admin → exit 3)
    - 06-PATTERNS.md §"Pattern S5" lines 716-723 (no plaintext through logs)
    - cmd/ach/cmd/migrate.go (whole file — Pattern P2 leaf shape)
    - internal/platformapi/admin/handler.go lines 60-95 (revokeRequest, refreshRequest, revokeKeyResponse, userRevokeResponse wire shapes — used to decode response bodies)
    - internal/platformapi/admin/mount.go (route paths: POST /keys/revoke, POST /users/{email}/revoke-keys, POST /refresh)
    - internal/keys/ (run `grep -nE "PkidKeyIDPrefix|EkidKeyIDPrefix|PrefixPkid|PrefixEkid" internal/keys/*.go` to find the canonical prefix constants — the env-keys revoke command in 06-05 already uses these for its plaintext rejection check)
    - spec/ach_cli_spec_v20260515_FINALv4.md §5.10 (CLI surface verbatim — `ach admin keys revoke <key-id>`, `ach admin users revoke-keys <email>`, `ach admin refresh <kind> <name>` arg shapes)
    - spec/ach_hub_spec_v20260515_FINALv4.md §15.5 (force-refresh endpoint kind closed set {plugin, prompt, artifact, marketplace, environment, backendidentitypolicy}) — but per Phase 6 CONTEXT.md W3b only {plugin, prompt, artifact, marketplace} are user-facing; the planner-side validation rejects others with exit 1 + clear error
    - **EXACT foundation APIs Task 1 calls into (read these signature lines BEFORE coding — the action body below references them by exact name):**
      - 06-01-cli-shared-internals-PLAN.md Task 1 action (lines ~141-145) — `config.Load(path string) (*File, error)` AND `config.ResolveActive(f *File, flagDeployment, envDeployment string) (name string, dep *Deployment, err error)`. NOTE: there is NO `config.Resolve(cmd)` helper; the two-step `Load` then `ResolveActive` is the contract.
      - 06-01-cli-shared-internals-PLAN.md Task 2 action (lines ~246-247) — `type CodedError struct { Code Code; Msg string; Wrapped error }`. NOTE: there is NO `exit.NewCodedError` constructor and NO `exit.Wrap` helper; callers MUST use the struct literal `&exit.CodedError{Code: ..., Msg: ..., Wrapped: ...}` directly.
      - 06-07-synthetic-mode-enforcement-PLAN.md Task 1 action (line ~152) — Gate enumeration includes `GateAdmin`. NOTE: there is NO `GateAuto` constant.
      - 06-07-synthetic-mode-enforcement-PLAN.md Task 1 action (line ~159) — `func GuardCommand(p Params) error`. NOTE: `GuardCommand` takes a SINGLE arg (the `Params` struct, which carries the `Gate` field); do NOT call it with a separate gate argument.
  </read_first>
  <behavior>
    Test 1 — admin keys revoke with pkid_… key-id, server returns 200:
    - httptest server mounted at /platform/admin/keys/revoke; receives POST with body `{"key_id":"pkid_abc"}` and `x-ach-key: pk_…` header; responds 200 `{"key_id":"pkid_abc","status":"revoked"}`.
    - Drive via `RunE`-equivalent helper with `--yes` to bypass prompt; assert exit code 0, stdout includes "revoked" (or the response).

    Test 2 — admin keys revoke with ekid_… key-id, server returns 200:
    - Same as Test 1 but body `{"key_id":"ekid_xyz"}`; assert 200 path works (CLI-13: admin keys revoke accepts BOTH prefixes).

    Test 3 — admin keys revoke with raw pk_… plaintext is rejected client-side:
    - Call `ach admin keys revoke pk_abc123 --yes`. NO HTTP call is made (assert via httptest counter); exit code 1; stderr contains "plaintext key" or "must be a key id" verbatim.

    Test 4 — admin keys revoke with raw ek_… plaintext is rejected client-side:
    - Same as Test 3 but `ek_xyz789`; exit 1; no HTTP call.

    Test 5 — admin keys revoke 403 not_admin → exit 3:
    - httptest returns 403 + error envelope `{"error":{"code":"not_admin","message":"caller not in admin allowlist"},"request_id":"req_test"}`.
    - exit.MapServerError maps this to exit 3 (CLI-10). Assert exit code is exactly 3.

    Test 6 — admin keys revoke 401 invalid_key → exit 3:
    - httptest returns 401 invalid_key_type envelope; exit code 3.

    Test 7 — admin keys revoke 503 → exit 6:
    - httptest returns 503; exit code 6 (Network).

    Test 8 — admin keys revoke without --yes prompts for confirmation:
    - Stub stdin with "n\n" → exit 1 (declined); no HTTP call.
    - Stub stdin with "y\n" → 200 path executes; exit 0.

    Test 9 — admin users revoke-keys 200:
    - httptest at `/platform/admin/users/{email}/revoke-keys`; email is URL-escaped (assert path is `/platform/admin/users/test%40example.com/revoke-keys` for `test@example.com`).
    - Response body `{"revoked_count":3,"errors":[]}`; exit 0; stdout shows "Revoked 3 keys" (or equivalent) and renders the empty errors list.

    Test 10 — admin users revoke-keys 200 with errors:
    - Response `{"revoked_count":2,"errors":["litellm: timeout"]}`; exit 0 (server returned 200 — partial completion is still success); stdout shows both the count and the errors list.

    Test 11 — admin users revoke-keys 403 → exit 3 (CLI-10):
    - 403 not_admin envelope; exit 3.

    Test 12 — admin refresh plugin foo 200:
    - httptest at /platform/admin/refresh; body `{"kind":"plugin","name":"foo"}`; response 200/202 `{"status":"accepted"}` or empty body; exit 0.

    Test 13 — admin refresh with invalid kind (e.g. "environment", "team", or "garbage") rejected client-side:
    - `ach admin refresh team foo` → exit 1; NO HTTP call; stderr contains the closed-set message listing {plugin, prompt, artifact, marketplace}.

    Test 14 — admin refresh 403 not_admin → exit 3.

    Test 15 — admin parent without subcommand prints help + exit 0 (cobra default).

    Test 16 — verbose mode redacts x-ach-key in any stderr header dump:
    - Run with `--verbose`; assert stderr contains the literal substring `pk_***` AND does NOT contain the actual pk_ value (apart from the prefix).
  </behavior>
  <action>
    Author `cmd/ach/cmd/admin.go` per Pattern P2 + P3 (2-level parent-with-children):

    1. SPDX header (Pattern S1) — `// SPDX-License-Identifier: Apache-2.0`.

    2. File-level package comment describing what the command does and citing CLI-10 + CLI-13 + D-16 + CONTEXT.md W3-P2.

    3. `package cmd`.

    4. Imports: `bufio`, `context`, `encoding/json`, `errors`, `fmt`, `net/url`, `os`, `strings`, `github.com/spf13/cobra`, `github.com/ackstorm/ach/internal/cli/config`, `github.com/ackstorm/ach/internal/cli/exit`, `github.com/ackstorm/ach/internal/cli/httpclient`, `github.com/ackstorm/ach/internal/cli/synthetic`, `github.com/ackstorm/ach/internal/keys`.

    5. Subcommand tree:
       - `var adminCmd = &cobra.Command{Use:"admin", Short:"Admin operations (key revocation, force-refresh) — requires allowlisted pk_"}`. Long: enumerate the three children.
       - `var adminKeysCmd = &cobra.Command{Use:"keys", Short:"Admin key operations"}` (intermediate parent).
       - `var adminKeysRevokeCmd = &cobra.Command{Use:"revoke <key-id>", Short:"Revoke a key by ID (pkid_… or ekid_…)", Args:cobra.ExactArgs(1), RunE: runAdminKeysRevoke}`.
       - `var adminUsersCmd = &cobra.Command{Use:"users", Short:"Admin user operations"}` (intermediate parent).
       - `var adminUsersRevokeKeysCmd = &cobra.Command{Use:"revoke-keys <email>", Short:"Revoke all keys owned by <email>", Args:cobra.ExactArgs(1), RunE: runAdminUsersRevokeKeys}`.
       - `var adminRefreshCmd = &cobra.Command{Use:"refresh <kind> <name>", Short:"Force-refresh an external content resource (kind ∈ {plugin,prompt,artifact,marketplace})", Args:cobra.ExactArgs(2), RunE: runAdminRefresh}`.

    6. Per-command flags:
       - `--yes` (bool) on `adminKeysRevokeCmd` and `adminUsersRevokeKeysCmd` — bypasses interactive confirmation. NOT on refresh (idempotent / non-destructive to the caller).
       - `--deployment <name>` and `--api-key <pk_…>` flags inherited via PersistentFlags on `rootCmd` (already established in 06-01 / 06-03 / 06-05).

    7. `runAdminKeysRevoke(cmd, args)` body:
       a. Read config path via `config.Path()`; load via `cfg, err := config.Load(path)` (06-01 Task 1: NO `config.Resolve(cmd)` helper exists — Load returns `(*File, error)`). On err → return `&exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}`.
       b. Resolve the active deployment via `name, dep, err := config.ResolveActive(cfg, deploymentFlag, os.Getenv("ACH_DEPLOYMENT"))` (06-01 Task 1 signature: `ResolveActive(f *File, flagDeployment, envDeployment string)`). On err → return `&exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}`. `dep.URL` and `dep.PK` are the credentials carried forward.
       c. Call `synthetic.GuardCommand(synthetic.Params{Gate: synthetic.GateAdmin, APIKeyFlag: apiKeyFlag, EnvKeyFlag: envKeyFlag, DeploymentFlag: deploymentFlag})` per 06-07 Task 1 (line ~152 — `GateAdmin` is the admin entry in the Gate enumeration; `GateAuto` does NOT exist). On err → return err.
       d. `keyID := args[0]`. Validate via prefix check:
          - if `strings.HasPrefix(keyID, "pk_")` || `strings.HasPrefix(keyID, "ek_")` → return `&exit.CodedError{Code: exit.General, Msg: "refusing plaintext key — pass the key id (pkid_… or ekid_…) instead"}`.
          - if NOT (`strings.HasPrefix(keyID, keys.PkidKeyIDPrefix)` || `strings.HasPrefix(keyID, keys.EkidKeyIDPrefix)`) → return `&exit.CodedError{Code: exit.General, Msg: "key id must start with pkid_ or ekid_"}`.
       e. Prompt for confirmation unless `--yes`: write `"Revoke key " + keyID + " ? (y/N): "` to stderr; read one line from stdin via `bufio.NewReader(os.Stdin).ReadString('\n')`. If trimmed != "y"/"Y"/"yes" → return `&exit.CodedError{Code: exit.General, Msg: "cancelled"}`.
       f. Build the httpclient.Client with `dep.URL` + `dep.PK`; POST `/platform/admin/keys/revoke` body `{"key_id":<keyID>}`; on `*httpclient.ServerError` → return err (main maps via exit.MapServerError).
       g. On 200 → decode response into struct `{KeyID string `json:"key_id"`; Status string `json:"status"`}`. Render to stdout: `fmt.Fprintf(os.Stdout, "Revoked %s (status: %s)\n", resp.KeyID, resp.Status)`. Return nil (exit 0).

    8. `runAdminUsersRevokeKeys(cmd, args)` body:
       a. Same Load + ResolveActive + GuardCommand (with `Gate: synthetic.GateAdmin`) as steps 7a-c. NO `exit.NewCodedError` / `exit.Wrap` / `config.Resolve` / `synthetic.GateAuto` — all CodedError values are struct literals `&exit.CodedError{Code: ..., Msg: ..., Wrapped: ...}`.
       b. `email := args[0]`. Basic validation: must contain "@" and not be empty.
       c. Prompt for confirmation unless `--yes`: same shape as step 7e but message `"Revoke ALL keys owned by " + email + " ? (y/N): "`.
       d. URL-escape email via `url.PathEscape(email)`. POST `/platform/admin/users/<escaped>/revoke-keys` with empty `{}` body (the spec endpoint takes the email from the path, not the body).
       e. On 200 → decode `{RevokedCount int `json:"revoked_count"`; Errors []string `json:"errors"`}`. Render: `fmt.Fprintf(os.Stdout, "Revoked %d keys owned by %s\n", resp.RevokedCount, email)`. If `len(resp.Errors) > 0` → write each on its own line with prefix `"  - "`. Return nil.

    9. `runAdminRefresh(cmd, args)` body:
       a. Same Load + ResolveActive + GuardCommand (with `Gate: synthetic.GateAdmin`) as steps 7a-c. Same struct-literal CodedError discipline as step 8a.
       b. `kind := args[0]; name := args[1]`. Validate `kind` against the closed allow-list:
          `allowedKinds := map[string]struct{}{"plugin":{},"prompt":{},"artifact":{},"marketplace":{}}`.
          If `kind` not in set → return `&exit.CodedError{Code: exit.General, Msg: "kind must be one of: plugin, prompt, artifact, marketplace; got: "+kind}`.
       c. NO confirmation prompt (idempotent).
       d. POST `/platform/admin/refresh` body `{"kind":<kind>,"name":<name>}`.
       e. On 200 or 202 → write `fmt.Fprintf(os.Stdout, "Refresh requested: %s/%s\n", kind, name)`. Return nil.

    10. `init()`:
        ```
        adminKeysCmd.AddCommand(adminKeysRevokeCmd)
        adminUsersCmd.AddCommand(adminUsersRevokeKeysCmd)
        adminCmd.AddCommand(adminKeysCmd, adminUsersCmd, adminRefreshCmd)
        rootCmd.AddCommand(adminCmd)
        adminKeysRevokeCmd.Flags().Bool("yes", false, "skip interactive confirmation")
        adminUsersRevokeKeysCmd.Flags().Bool("yes", false, "skip interactive confirmation")
        ```

    Author `cmd/ach/cmd/admin_test.go`:
    - Stdlib `testing` + `net/http/httptest` (Pattern S3). No Ginkgo.
    - One helper `runAdminCmd(t, args []string, stdin io.Reader) (exitCode int, stdout, stderr string)` that captures the command's RunE return, maps via exit.MapServerError + exit.CodedError per the cmd/ach/main.go pattern, and uses cobra's SetIn/SetOut/SetErr for I/O capture.
    - Drive each subcommand via the helper, build an httptest server per test, set `ACH_BASE_URL=<httptest.URL>` + `ACH_API_KEY=pk_admintest` for synthetic-mode invocation OR build a temp config file via internal/cli/config helpers.
    - For Test 16 (verbose redaction), set the `--verbose` flag (or `ACH_VERBOSE=1`); assert stderr contains the literal substring `x-ach-key: pk_***` AND does NOT contain the unredacted `pk_admintest` value.

    Run `./scripts/dev.sh make lint-changed` after each commit; SPDX header on both files.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestAdmin</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestAdmin` exits 0.
    - Source assertion: `grep -c "var adminCmd\|var adminKeysRevokeCmd\|var adminUsersRevokeKeysCmd\|var adminRefreshCmd" cmd/ach/cmd/admin.go` returns 4.
    - Source assertion: `grep -cE 'rootCmd\.AddCommand\(adminCmd\)' cmd/ach/cmd/admin.go` returns 1.
    - Source assertion: `grep -cE 'PkidKeyIDPrefix|EkidKeyIDPrefix' cmd/ach/cmd/admin.go` returns ≥ 2 (both prefixes referenced).
    - Source assertion (plaintext rejection): `grep -cE 'strings\.HasPrefix\(.*"pk_"\)|strings\.HasPrefix\(.*"ek_"\)' cmd/ach/cmd/admin.go` returns ≥ 1.
    - Source assertion (kind closed set): `grep -cE '"plugin".*"prompt".*"artifact".*"marketplace"|allowedKinds' cmd/ach/cmd/admin.go` returns ≥ 1.
    - Source assertion (URL-escape email): `grep -c 'url\.PathEscape' cmd/ach/cmd/admin.go` returns ≥ 1.
    - Source assertion (no plaintext through operational logger — Pattern S5): `grep -nE "params\.APIKey|apiKey" cmd/ach/cmd/admin.go | grep -v 'httpclient\.\|Redact\|x-ach-key' | grep -E "slog|fmt\.Fprint|os\.Stdout|os\.Stderr"` returns 0 lines (the API key MUST flow only into httpclient.Client, never into a print or log statement).
    - Behavior: `pk_…` / `ek_…` plaintext arguments → exit 1 with no HTTP call.
    - Behavior: `403 not_admin` → exit 3; `503` → exit 6; `200` → exit 0.
    - Behavior: `--yes` skips prompt; absence of `--yes` + stdin "n" → exit 1 + no HTTP call.
    - Behavior: refresh with invalid kind → exit 1 + no HTTP call.
    - SPDX header: `head -1 cmd/ach/cmd/admin.go cmd/ach/cmd/admin_test.go` all match `// SPDX-License-Identifier: Apache-2.0`.
    - `./scripts/dev.sh make lint-changed` exits 0.
  </acceptance_criteria>
  <done>
    `ach admin` parent + 3 leaf subcommands green; CLI-10 (admin exit-3) + CLI-13 (admin keys revoke both prefixes + raw rejection) covered; kind closed-set client-side validation in place; no plaintext through operational stdout/stderr.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| Local CLI process → ~/.config/ach/config.yaml | Reads pk_ plaintext from on-disk trust artifact (Hub §15.4) |
| Local CLI process → Platform API | Sends pk_ in `x-ach-key`; receives `403 not_admin` when not allowlisted |
| Stdin → CLI (confirmation prompt) | User-supplied "y/n" |
| CLI → external email arg | User-supplied path-segment (URL-escaped before request) |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-06-08-01 | Spoofing | `ach admin keys revoke` accepting plaintext keys | mitigate | Client-side prefix check refuses `pk_…`/`ek_…` plaintext with exit 1 (CLI-13) — prevents the operator from accidentally pasting a freshly-minted plaintext key into a revoke arg, which would then appear in shell history and the audit event message. |
| T-06-08-02 | Tampering | `--api-key` flag on operational logs | mitigate | API key flows ONLY into `httpclient.Client.APIKey`; never into `fmt.Print*`, `slog`, or `os.Stdout`. Verbose mode redacts the `x-ach-key` header to `<prefix>_***` via `httpclient.Redact` (CLI-04, Pattern S5). Source-assertion gate enforces this. |
| T-06-08-03 | Repudiation | `platform.admin.*` audit events server-side | accept | The server-side `internal/platformapi/admin/handler.go` already emits `ActionAdminKeysRevoke` / `ActionAdminUsersRevokeKeys` / `ActionAdminRefresh` per Phase 3 OBS-02 — Phase 6 client does not need its own emission point. |
| T-06-08-04 | Information Disclosure | `ach admin users revoke-keys` rendering `errors` list | accept | Server's `userRevokeResponse.Errors` carries upstream LiteLLM error strings (e.g. "litellm: timeout"). These are operationally useful for the admin and do NOT contain user PII beyond the email already in the URL path. |
| T-06-08-05 | Denial of Service | `ach admin refresh` unbounded retry | accept | Single-shot POST; cobra surfaces non-zero exit. The server-side handler is the rate-limit point (Phase 3 D-22 allowlist gate). |
| T-06-08-06 | Elevation of Privilege | `ach admin keys revoke` invoked by non-allowlisted pk_ | mitigate | Server's `AdminOnly` middleware (`internal/platformapi/admin/mount.go`) returns `403 not_admin` BEFORE any side effect. CLI maps this to exit 3 (CLI-10), making the rejection unambiguous. |
| T-06-08-07 | Tampering | Path-injection via `<email>` arg | mitigate | `url.PathEscape(email)` before request URL composition prevents `/users/foo/../bar/revoke-keys` style injection. |
| T-06-08-08 | Spoofing | `ach admin refresh <kind>` with unsupported kind | mitigate | Client-side closed-set validation `{plugin,prompt,artifact,marketplace}` per CONTEXT.md W3b — rejects `environment`/`backendidentitypolicy`/garbage with exit 1 before HTTP call. Prevents an operator from accidentally requesting a refresh on a kind the spec does not yet surface on the user-facing CLI. |
| T-06-08-SC | Tampering | npm/pip/cargo installs | mitigate | No new package installs in this plan — only stdlib + already-pinned `github.com/spf13/cobra`. Existing govulncheck ack-list applies. |
</threat_model>

<verification>
After the task completes:

```bash
./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestAdmin
./scripts/dev.sh go build ./cmd/ach/...
./scripts/dev.sh make lint
```

Manual smoke matrix (run against the kept kind cluster — `make cluster-keep`):

```bash
# Build the binary
./scripts/dev.sh make build  # produces ./bin/ach

# As an allowlisted admin pk_ (set ACH_BASE_URL + login first)
./bin/ach admin keys revoke pkid_abc --yes               # exit 0 on 200
./bin/ach admin keys revoke ekid_xyz --yes               # exit 0 on 200
./bin/ach admin keys revoke pk_oops --yes                # exit 1 (plaintext)
./bin/ach admin users revoke-keys alice@example.com --yes # exit 0 on 200
./bin/ach admin refresh plugin caveman                   # exit 0 on 200
./bin/ach admin refresh team something                   # exit 1 (kind not in closed set)

# As a non-allowlisted pk_
./bin/ach admin keys revoke pkid_abc --yes               # exit 3 (403 not_admin)

# Network failure (point ACH_BASE_URL at an unreachable host)
ACH_BASE_URL=https://unreachable.test ./bin/ach admin refresh plugin foo  # exit 6
```
</verification>

<success_criteria>
- `cmd/ach/cmd/admin.go` exists with parent + 3 children (`keys revoke`, `users revoke-keys`, `refresh`) wired into rootCmd.
- All 16 admin_test.go subtests green.
- Both `pkid_…` and `ekid_…` accepted on `keys revoke`; raw `pk_…`/`ek_…` rejected with exit 1.
- 403 not_admin → exit 3 (CLI-10) on all three subcommands.
- Closed `kind` set on `refresh`: {plugin, prompt, artifact, marketplace}.
- `--yes` flag bypasses interactive confirmation on `keys revoke` + `users revoke-keys`.
- SPDX header on every new file; lint clean; full Phase 6 unit suite passes.
</success_criteria>

<output>
Create `.planning/phases/06-cli-foundation/06-08-SUMMARY.md` when done. Record:
- Whether the `--verbose` flag was set as a PersistentFlag on rootCmd or a local flag on each command (decision affects W3-P3 e2e fixtures).
- Whether the refresh command supports the additional Phase 7 kinds (`environment`, `backendidentitypolicy`) gated behind a hidden flag or rejected outright (recommendation: rejected; revisit in Phase 7 if needed).
- The exact wire shape of the `userRevokeResponse.Errors` rendering — newline-separated vs JSON-pass-through — chosen.
- Any deviations from Pattern P3 (2-level parent-with-children) or P5 (httpclient header carrier) with rationale.
</output>
