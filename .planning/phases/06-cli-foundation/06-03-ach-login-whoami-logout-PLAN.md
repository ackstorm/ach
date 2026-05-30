---
phase: 06-cli-foundation
plan: 03
type: execute
wave: 1
depends_on:
  - 06-01-cli-shared-internals
  - 06-02-server-device-code-endpoints
# 06-03 starts AFTER 06-01 + 06-02 finish (sequenced within Wave 1 logical group;
# wave label is parallelization grouping per CONTEXT.md D-01, not strict topological
# level). 06-01 and 06-02 may run in parallel; 06-03 follows both.
files_modified:
  - internal/cli/devicecode/client.go
  - internal/cli/devicecode/client_test.go
  - internal/cli/devicecode/doc.go
  - cmd/ach/cmd/login.go
  - cmd/ach/cmd/login_test.go
  - cmd/ach/cmd/whoami.go
  - cmd/ach/cmd/whoami_test.go
  - cmd/ach/cmd/logout.go
  - cmd/ach/cmd/logout_test.go
autonomous: true
requirements:
  - CLI-01
  - CLI-04
  - CLI-11

must_haves:
  truths:
    - "`ach login` interactively prompts for deployment name + URL (https:// required), polls /platform/auth/cli/token until 200, writes deployments.<name>.pk to ~/.config/ach/config.yaml with 0600 mode"
    - "`ach login` sets default: when absent; overwrites prior pk: on existing deployment (prior server-side key expires per 7d sliding window per Hub §7.1)"
    - "`ach login` rejects non-HTTPS URL with exit 1 (CLI-02 via internal/cli/config)"
    - "`ach login` rejects synthetic mode with exit 1 (CLI-07; checked early via internal/cli/synthetic in W3 — for W1, --deployment + env conflict short-circuits at synthetic.IsActive)"
    - "`ach login --no-browser` prints verification_url and polls without opening a browser (D-03)"
    - "`ach login` prints SSO email + masked pk_ tail (pk_****WXYZ) per D-03"
    - "`ach whoami` no-net default prints identity block from on-disk config (deployment name + URL + masked pk_/ek_)"
    - "`ach whoami --verify` with pk_ calls GET /platform/environments?limit=1; with ek_ calls POST /platform/hydrate {}; exit 0 on 200, exit 3 on 401, exit 6 on network failure (CLI-11)"
    - "`ach logout` wipes pk: from active deployment, leaves url: so next ach login resumes (D-06)"
    - "CLI-04: pk_ plaintext printed exactly once at ach login completion; --verbose redacts x-ach-key to <prefix>_*** in any header dump"
  artifacts:
    - path: "internal/cli/devicecode/client.go"
      provides: "client.Init() + client.PollToken() against /platform/auth/cli/{init,token}"
      contains: "func PollToken"
    - path: "cmd/ach/cmd/login.go"
      provides: "ach login cobra subcommand"
      contains: "var loginCmd"
    - path: "cmd/ach/cmd/whoami.go"
      provides: "ach whoami cobra subcommand with --verify"
      contains: "var whoamiCmd"
    - path: "cmd/ach/cmd/logout.go"
      provides: "ach logout cobra subcommand"
      contains: "var logoutCmd"
  key_links:
    - from: "cmd/ach/cmd/login.go"
      to: "internal/cli/devicecode/client.go"
      via: "client.Init + client.PollToken"
      pattern: "devicecode.PollToken"
    - from: "cmd/ach/cmd/login.go"
      to: "internal/cli/config/config.go"
      via: "config.Save with deployments.<name>.pk = pk_..."
      pattern: "config.Save"
    - from: "cmd/ach/cmd/whoami.go"
      to: "internal/cli/httpclient/client.go"
      via: "Verify path: pk_→GET /platform/environments?limit=1, ek_→POST /platform/hydrate {}"
      pattern: "httpclient.Client"
---

<objective>
Ship the three leaf subcommands that complete the Wave-1 login round
trip: `ach login` (D-02/D-03 device-code client + interactive UX),
`ach whoami` (D-13/D-14 asymmetric verify), `ach logout` (D-06 pk_
wipe). Plus the shared device-code client under
`internal/cli/devicecode/`.

This plan depends on:
- 06-01 — `internal/cli/{config,httpclient,exit}` (already landed by Wave 1 Plan 1).
- 06-02 — server `/platform/auth/cli/{init,token}` endpoints (already landed by Wave 1 Plan 2).

Once this plan ships, a user can:
1. Run `ach login` against a deployed Hub.
2. Confirm identity via `ach whoami --verify`.
3. Wipe their pk_ via `ach logout`.

The full demo-collapse promise (`ach login` + `ach hydrate
--environment demo` replaces `hydrate-demo.sh`) becomes reachable
after Wave 2 ships `ach hydrate`. W3 wires the e2e umbrella that
exercises the whole chain.

Purpose: This is the headline Wave-1 deliverable — the user-visible
authentication path. Without it the whole CLI surface stays
demo-locked behind shell scripts.

Output: 1 new package (`internal/cli/devicecode/`) with 2 source +
1 test + 1 doc; 6 new files under `cmd/ach/cmd/` (3 commands + 3 tests).
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
@cmd/ach/cmd/root.go
@internal/litellm/restclient.go
@.planning/phases/06-cli-foundation/06-01-SUMMARY.md
@.planning/phases/06-cli-foundation/06-02-SUMMARY.md
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Author internal/cli/devicecode client</name>
  <files>
    internal/cli/devicecode/doc.go
    internal/cli/devicecode/client.go
    internal/cli/devicecode/client_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-02" (device-code wire shape) + §"D-03" (UX semantics)
    - 06-PATTERNS.md §"Pattern P5" lines 246-294 (HTTP client shape — devicecode is a thin wrapper above httpclient)
    - 06-01-SUMMARY.md (final API of internal/cli/httpclient)
    - 06-02-SUMMARY.md (final wire shape of /platform/auth/cli/{init,token}, especially the 202 pending-vs-410-expired vs 404-not-found distinction)
    - spec/ach_cli_spec_v20260515_FINALv4.md §5.1 (ach login UX)
    - internal/litellm/restclient.go (analog for HTTP poll loop with context.Context cancellation)
  </read_first>
  <behavior>
    - Test 1: Init(ctx, baseURL) issues POST /platform/auth/cli/init against an httptest server; returns InitResponse{SessionID, VerificationURL, PollInterval, ExpiresIn}.
    - Test 2: PollToken(ctx, baseURL, sessionID, pollInterval, totalTimeout) polls /platform/auth/cli/token. When server returns 202 pending for first 3 polls then 200 with the pk_, PollToken returns the TokenResponse. The poll cadence honors pollInterval (test asserts ≥ 2 polls happened in N*pollInterval ± wallclock slack).
    - Test 3: When server returns 404 session_not_found OR 410 session_expired, PollToken returns *httpclient.ServerError immediately (does not retry).
    - Test 4: When ctx is cancelled mid-poll, PollToken returns ctx.Err() promptly (within one poll tick).
    - Test 5: When totalTimeout fires (e.g. 50ms with 10ms poll), PollToken returns devicecode.ErrLoginTimeout.
    - Test 6: Open(verificationURL) is a seam — exposes a package-level var `Opener = openInBrowser` that defaults to a no-op when `ACH_TEST_NO_BROWSER=1` env var is set; tests override the var with a no-op closure. Production code uses `os/exec` to invoke `xdg-open` on linux, `open` on darwin, `rundll32 url.dll,FileProtocolHandler` on windows.
  </behavior>
  <action>
    Author `internal/cli/devicecode/doc.go` — package doc citing D-02, D-03, RFC 8628 (OAuth 2.0 Device Authorization Grant family — pattern reference; ACH does NOT implement RFC 8628 strictly, only borrows the polling shape).

    Author `internal/cli/devicecode/client.go`:
    - Package `devicecode` under `internal/cli/devicecode/`.
    - Types mirror the server-side ones in `internal/platformapi/auth/cli/`:
      - `InitResponse{SessionID, VerificationURL string; PollInterval, ExpiresIn int}`.
      - `TokenResponse{KeyID, Plaintext, OwnerEmail string}`.
    - Sentinel errors: `ErrLoginTimeout`.
    - Package-level seam: `var Opener func(url string) error = openInBrowser` so unit tests can override.
    - Funcs:
      - `Init(ctx context.Context, baseURL string) (*InitResponse, error)` — uses a stdlib `http.Client{Timeout: 30 * time.Second}` (anonymous request; does NOT carry x-ach-key). POSTs empty body `{}` to `baseURL + "/platform/auth/cli/init"`. Decodes JSON or returns *httpclient.ServerError (reuse httpclient.ServerError type for envelope decode).
      - `PollToken(ctx context.Context, baseURL, sessionID string, pollInterval time.Duration, totalTimeout time.Duration) (*TokenResponse, error)`:
        1. Compose `deadline := time.After(totalTimeout)`.
        2. Loop:
           - POST `{session_id: <id>}` to `baseURL + "/platform/auth/cli/token"`.
           - On 200: decode TokenResponse, return.
           - On 202 (pending): sleep pollInterval (via `time.After` selected against ctx.Done()/deadline).
           - On 404/410/4xx other: return the *httpclient.ServerError immediately.
           - On 5xx: retry up to 3 consecutive 5xx before returning *httpclient.ServerError.
        3. ctx.Err() — return ctx.Err() promptly.
        4. deadline fires — return ErrLoginTimeout.

    Reuse `httpclient.ServerError` (do NOT redefine) — `internal/cli/httpclient` is a leaf dep so devicecode imports it cleanly. The `httpclient.Client` type itself can be used here too, but devicecode does NOT need an x-ach-key carrier (these endpoints are anonymous). Use a bare `http.Client` for simplicity.

    Tests use `httptest.NewServer` with a counter to simulate the 202→202→202→200 sequence. Use `internal/cli/httpclient` only to decode the error envelope on negative cases. Skip the `Opener` test on CI by gating with `ACH_TEST_NO_BROWSER=1`.

    SPDX header on every new file.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./internal/cli/devicecode/...</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./internal/cli/devicecode/...` exits 0.
    - Source assertion: `grep -E '^func (Init|PollToken)' internal/cli/devicecode/client.go | wc -l` returns 2.
    - Source assertion: `grep -c 'ErrLoginTimeout' internal/cli/devicecode/client.go` returns ≥ 1.
    - Source assertion: `grep -c 'select' internal/cli/devicecode/client.go` returns ≥ 1 (ctx cancellation honored via select).
    - Behavior: poll-loop test asserts at least 3 polls happened before 200 (httptest counter ≥ 3).
    - Behavior: ctx-cancel test returns within one pollInterval of cancel call.
    - SPDX header line 1: `head -1 internal/cli/devicecode/{doc.go,client.go,client_test.go}` all match `Apache-2.0`.
  </acceptance_criteria>
  <done>
    devicecode package green; non-blocking poll loop with context + deadline; opener seam testable.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Author `ach login` cobra subcommand</name>
  <files>
    cmd/ach/cmd/login.go
    cmd/ach/cmd/login_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-03" (login UX verbatim) + §"D-04" (config schema)
    - 06-PATTERNS.md §"Pattern P2" lines 110-165 (smallest cobra subcommand template) + §"Pattern P4" lines 213-244 (config validation + env-var bag)
    - spec/ach_cli_spec_v20260515_FINALv4.md §5.1 (ach login flow + prompts + masked pk_ tail)
    - cmd/ach/cmd/migrate.go (whole file — canonical Pattern P2 instance)
    - cmd/ach/cmd/root.go (rootCmd registration point)
    - 06-01-SUMMARY.md + 06-02-SUMMARY.md (final wire shapes; httpclient + config + devicecode APIs)
  </read_first>
  <behavior>
    - Test 1: ach login --deployment prod --base-url https://hub.example --no-browser against httptest server returning init→202→202→200 writes deployments.prod.{url,pk} to a `t.TempDir()` HOME/XDG_CONFIG_HOME-overridden config.yaml. Exit code 0.
    - Test 2: ach login --base-url http://insecure exits 1 with stderr "url must be https://" (refuse non-HTTPS).
    - Test 3: ach login on a config with no `default:` sets default to the new deployment name (D-04).
    - Test 4: Repeated ach login --deployment prod with a different mid-test pk_ payload overwrites the prior `pk:` (CLI AC1; per Hub §7.1 prior key expires server-side via sliding window).
    - Test 5: ach login when synthetic mode is active (ACH_BASE_URL + ACH_API_KEY set in test env) exits 1 with stderr "ach login is not available in synthetic mode" (per spec §3.3). Note: synthetic detection logic lives in W3 plan; for W1 the login command does a minimal inline check: if both ACH_BASE_URL and ACH_API_KEY are non-empty → exit 1.
    - Test 6: ach login --no-browser prints the verification_url to stdout for the user to copy/paste.
    - Test 7: stdout on success contains the SSO email + the masked pk_ tail `pk_****WXYZ` exactly once.
  </behavior>
  <action>
    Author `cmd/ach/cmd/login.go` mirroring Pattern P2:
    - File-level docstring above `package cmd` citing D-02 (device-code), D-03 (UX verbatim per §5.1), D-04 (config schema), D-20 (callback session_id branch).
    - Flags (cobra):
      - `--deployment <name>` (string) — skips deployment-name prompt; default "".
      - `--base-url <url>` (string) — skips URL prompt; default "".
      - `--no-browser` (bool) — print verification_url, do not open browser; default false.
      - `--no-warnings` (bool) — suppress info messages (kept for symmetry with hydrate; not yet load-bearing in login).
    - RunE flow:
      1. Synthetic-mode short-circuit: if `os.Getenv("ACH_BASE_URL") != "" && os.Getenv("ACH_API_KEY") != ""` → return `&exit.CodedError{Code: exit.General, Msg: "ach login is not available in synthetic mode"}`.
      2. Resolve deployment name: --deployment flag OR interactive prompt via `bufio.NewScanner(os.Stdin)`. Validate name: DNS-1123-style (`[a-z0-9-]+`). Empty/invalid → CodedError(General).
      3. Resolve URL: --base-url flag OR interactive prompt. Trim whitespace. Require `strings.HasPrefix(url, "https://")` — else CodedError(General, "url must be https://").
      4. Load existing config via `config.Load(configPath)`. (Tolerate ErrFileMode warnings; refuse other errors.)
      5. devicecode.Init(ctx, url) → InitResponse.
      6. If !--no-browser: call `devicecode.Opener(InitResponse.VerificationURL)` (best-effort; failure → fall back to printing URL).
      7. Print `Visit <verification_url> to complete login` to stdout/stderr.
      8. devicecode.PollToken(ctx, url, InitResponse.SessionID, time.Duration(InitResponse.PollInterval)*time.Second, time.Duration(InitResponse.ExpiresIn)*time.Second) → TokenResponse.
      9. On error: surface *httpclient.ServerError to main.go via direct return (main.go's errors.As branch handles exit-code mapping).
      10. Mutate the *config.File: ensure file != nil; set `file.Deployments[name] = &config.Deployment{URL: url, PK: tokenResp.Plaintext, EK: existing EK map preserved if deployment existed}`. If `file.Default == ""`, set `file.Default = name`.
      11. config.Save(configPath, file). On error → exit.ConfigFile (8).
      12. Print `Logged in as <owner_email>` + `pk_****<last-4>` to stdout (CLI-04 — plaintext shown exactly once at this final line; via `config.Mask`).
      13. Return nil → exit 0.

    Author `cmd/ach/cmd/login_test.go`:
    - Use httptest to mock /platform/auth/cli/init + /token. Override `devicecode.Opener` with a no-op closure (or set `ACH_TEST_NO_BROWSER=1`).
    - Use `t.TempDir() + setenv("XDG_CONFIG_HOME",...)` to redirect config writes.
    - Capture stdout via `os.Pipe()` swap (or use cobra's SetOut/SetErr — cobra exposes these).
    - Each test invokes the command by constructing a fresh cobra.Command tree and calling `rootCmd.SetArgs([]string{"login", "--deployment","prod","--base-url",ts.URL,"--no-browser"}); rootCmd.Execute()`. NOTE: the test cannot use the global rootCmd because state leaks; per Phase 3 test discipline, use a fresh command tree per test.

    Register with `init() { rootCmd.AddCommand(loginCmd) }`.

    SPDX header on every new file.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestLogin</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./cmd/ach/cmd/... -run TestLogin` exits 0.
    - Source assertion: `grep -c 'devicecode\.\(Init\|PollToken\)' cmd/ach/cmd/login.go` returns ≥ 2.
    - Source assertion: `grep -c 'config\.\(Save\|Load\|Mask\)' cmd/ach/cmd/login.go` returns ≥ 3.
    - Source assertion: `grep -c '"https://"' cmd/ach/cmd/login.go` returns ≥ 1 (URL validation).
    - Source assertion: `grep -c 'ACH_BASE_URL.*ACH_API_KEY\|ACH_API_KEY.*ACH_BASE_URL' cmd/ach/cmd/login.go` returns ≥ 1 (synthetic-mode short-circuit).
    - Behavior: stdout on success contains BOTH the owner email AND `pk_****` (4 stars, masked tail) — exactly one occurrence of either form of pk_.
    - Behavior: stdout does NOT contain the full pk_ plaintext anywhere except the masked form (CLI-04 — `grep -c 'pk_[A-Za-z0-9]\{8,\}' <captured-stdout>` returns 0 if pk_ tail is < 8 chars masked).
    - Behavior: a second `ach login --deployment prod ...` overwrites the prior `pk:` in config.yaml.
  </acceptance_criteria>
  <done>
    Login flow green; config persisted at 0600; synthetic-mode rejected; masked tail printed exactly once.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Author `ach whoami` + `ach logout` cobra subcommands</name>
  <files>
    cmd/ach/cmd/whoami.go
    cmd/ach/cmd/whoami_test.go
    cmd/ach/cmd/logout.go
    cmd/ach/cmd/logout_test.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-13" + §"D-14" (whoami asymmetric verify) + §"D-06" (logout semantics)
    - 06-PATTERNS.md §"Pattern P2" (leaf cobra subcommand) + §"Pattern P5" (httpclient usage) + §"Pattern P6" (exit codes)
    - spec/ach_cli_spec_v20260515_FINALv4.md §5.2 (ach logout) + §5.3 (ach whoami --verify)
    - 06-01-SUMMARY.md (config + httpclient API)
    - cmd/ach/cmd/login.go (just-shipped — for the synthetic-mode short-circuit pattern to reuse)
  </read_first>
  <behavior>
    whoami tests:
    - Test 1: ach whoami with NO --verify flag prints an identity block from ~/.config/ach/config.yaml: `Deployment: prod\nURL: https://x\nKey: pk_****WXYZ\n(no remote check)\n`. Exit 0. NO HTTP call.
    - Test 2: ach whoami --verify with pk_ stored → makes `GET /platform/environments?limit=1` carrying `x-ach-key: pk_...`. Server returns 200 + JSON → exit 0; stdout includes `Verified: yes`.
    - Test 3: ach whoami --verify with ek_ (resolved via --env-key flag OR ACH_ENV_KEY) → makes `POST /platform/hydrate {}` with `Accept-Encoding: gzip`. Server returns 200 → exit 0.
    - Test 4: ach whoami --verify when server returns 401 → exit 3.
    - Test 5: ach whoami --verify when server connection refused → exit 6 (network).
    - Test 6: ach whoami --verify in synthetic mode (ACH_BASE_URL + ACH_API_KEY set) reads pk_/ek_ from env via ClassifyBearer, performs the same asymmetric verify; --deployment flag rejected with exit 1 (synthetic-mode constraint).
    - Test 7: ach whoami when NO config file + NO synthetic env → exit 1 with "no deployment configured; run `ach login`" (CLI-08).
    - Test 8: --verbose mode prints a header dump with `x-ach-key: pk_***` (CLI-04 redaction) — grep on captured stderr.

    logout tests:
    - Test 9: ach logout against a config with `default: prod` + `deployments.prod.{url,pk}` → after run, config.yaml has `default: prod` + `deployments.prod.{url}` ONLY (pk: field removed; ek: map preserved if any).
    - Test 10: ach logout in synthetic mode → exit 1 (D-06).
    - Test 11: ach logout when no deployment resolvable → exit 1 with appropriate message.
  </behavior>
  <action>
    Author `cmd/ach/cmd/whoami.go`:
    - File-level docstring citing D-13 (asymmetric verify), D-14 (exit codes 0/3/6), CLI-11.
    - Flags:
      - `--verify` (bool) — opt-in remote check.
      - `--verbose` (bool) — header dump to stderr.
      - `--deployment <name>` (string) — override resolution.
      - `--api-key <pk_…>` / `--env-key <label>` — per spec §6.1 mutex resolution (D-11; full mutex enforcement lives in W2 hydrate or W3 synthetic — for whoami W1 implementation, accept these flags but apply the same mutex check inline since the resolution is identical).
    - RunE:
      1. Resolve config from disk (config.Load) and env vars. Apply CLI-08 precedence via `config.ResolveActive` (deployment name) AND `keys.ClassifyBearer` to determine pk_ vs ek_.
      2. NO --verify: print identity block to stdout `Deployment: <name>\nURL: <url>\nKey: <prefix>_****<last-4>\n(no remote check)\n`. Exit 0.
      3. --verify: construct `httpclient.Client{BaseURL: dep.URL, APIKey: resolvedKey, Verbose: verbose}`. Branch on `keys.ClassifyBearer(resolvedKey)`:
         - pk_ → `c.Do(ctx, "GET", "/platform/environments?limit=1", nil, nil)`.
         - ek_ → set `c.ExtraHeaders = http.Header{"Accept-Encoding": {"gzip"}}` then `c.Do(ctx, "POST", "/platform/hydrate", struct{}{}, nil)`. The body is discarded after status check per CLI-11.

         Per 06-01 SUMMARY, `httpclient.Client.ExtraHeaders` is part of the foundation API — NO conditional extension here. Assume it exists.
      4. On success: print identity block + `Verified: yes`. Exit 0.
      5. On *httpclient.ServerError 401 → return the error (main.go maps to exit 3).
      6. On network error → wrap in `&exit.CodedError{Code: exit.Network, Msg: err.Error(), Wrapped: err}`.

    Author `cmd/ach/cmd/logout.go`:
    - File-level docstring citing D-06.
    - Flags:
      - `--deployment <name>` (string) — override resolution.
    - RunE:
      1. Synthetic-mode short-circuit (same inline check as login) → exit 1.
      2. config.Load. If file == nil OR no deployments → exit 1.
      3. Resolve active deployment via `config.ResolveActive`.
      4. Set `file.Deployments[name].PK = ""` (preserve URL + EK map).
      5. config.Save → exit 0 or exit 8 on config-file error.
      6. Print `Logged out of <name>` to stdout.

    Tests use the same httptest + t.TempDir() XDG_CONFIG_HOME pattern as login_test.go. The pk_ classification mock can use the existing `internal/keys` ClassifyBearer (already shipped Phase 3).

    Register both commands via `init() { rootCmd.AddCommand(whoamiCmd, logoutCmd) }` — can be in one init in either file; convention: each file owns its own init.

    SPDX header on every new file.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestWhoami|TestLogout"</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./cmd/ach/cmd/... -run "TestWhoami|TestLogout"` exits 0.
    - Source assertion: `grep -c 'GET\s*"/platform/environments?limit=1"\|"/platform/environments?limit=1"' cmd/ach/cmd/whoami.go` returns ≥ 1.
    - Source assertion: `grep -c '"/platform/hydrate"' cmd/ach/cmd/whoami.go` returns ≥ 1.
    - Source assertion: `grep -c 'keys.ClassifyBearer\|ClassifyBearer' cmd/ach/cmd/whoami.go` returns ≥ 1.
    - Source assertion: `grep -c 'Accept-Encoding.*gzip\|"gzip"' cmd/ach/cmd/whoami.go` returns ≥ 1 (CLI-11 — discarded body for ek_).
    - Source assertion: `grep -c '"Verified: yes"\|"Verified: ok"' cmd/ach/cmd/whoami.go` returns ≥ 1.
    - Source assertion logout: `grep -c '\.PK = ""\|PK:\s*""' cmd/ach/cmd/logout.go` returns ≥ 1 (D-06 — wipe pk:, preserve url:).
    - Behavior: ach whoami without --verify makes 0 HTTP requests against the test server (assert via httptest request counter).
    - Behavior: ach whoami --verify exit codes match {0,3,6} across the three test cases (200/401/connection-refused).
    - Behavior: --verbose stderr contains `x-ach-key: pk_***` exactly once when verifying with a pk_.
    - Behavior (CLI-09 mutex deferral, per W6): whoami ACCEPTS `--api-key`/`--env-key`/`ACH_API_KEY`/`ACH_ENV_KEY` flags + envs but does NOT enforce full mutex-rejection in W1. Enforcement lands in W3-P1 (06-07) via `synthetic.GuardCommand` extension (or wherever CLI-07 lives). For W1 the implementation MAY pre-resolve credential to a single source if exactly one is set, OR exit 1 if zero are set. CLI-09 stays OUT of plan 06-03's frontmatter `requirements` — it is implemented in 06-06 + 06-07 where the mutex enforcement actually lands.
    - Behavior (ExtraHeaders contract): whoami's ek_ path sets `c.ExtraHeaders = http.Header{"Accept-Encoding": {"gzip"}}` and trusts the foundation API from 06-01; no conditional code path that extends httpclient inline.
  </acceptance_criteria>
  <done>
    whoami + logout green; asymmetric verify branches correctly; pk_ plaintext never leaks; synthetic-mode short-circuits where mandated.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| CLI process ↔ ~/.config/ach/config.yaml | Login writes pk_ plaintext into the on-disk trust artifact (Hub §15.4) at mode 0600. Logout wipes the pk: field but preserves url:. |
| CLI ↔ Platform API (device-code flow) | Anonymous `/platform/auth/cli/init` mints session_id; `/platform/auth/cli/token` returns pk_ once Dex round-trip completes. CLI is the only consumer of the session_id. |
| CLI ↔ browser (verification_url) | Login opens or prints the verification_url; the user authenticates against Dex; pk_ never traverses the URL. |
| CLI ↔ network (whoami --verify) | Asymmetric verify probes either `/platform/environments?limit=1` (pk_) or `/platform/hydrate {}` (ek_) with `Accept-Encoding: gzip` discarded body. |
| Flag/env ↔ credential resolver | `--api-key`/`--env-key`/`ACH_API_KEY`/`ACH_ENV_KEY` flow into the bearer; W1 accepts them, full mutex enforcement lands in W3-P1 (per W6). |
| CLI ↔ stdout/stderr | Login prints pk_ ONCE in masked form (`pk_****WXYZ`). Stderr verbose dump runs every header through `httpclient.Redact`. |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-06-03-01 | Spoofing | device-code session_id stolen mid-poll | mitigate | session_id is 24 bytes of crypto/rand (192-bit entropy); 5-min TTL; one-shot consumption via Consume (06-02 Task 2). An attacker who intercepts the session_id has a ≤5-min window AND must race the legitimate CLI poll. |
| T-06-03-02 | Tampering | `--no-browser` URL printed to stdout — path injection | mitigate | The verification_url is composed server-side from `deps.BaseURL + "/platform/auth/login?session_id=<id>"` (06-02). CLI prints it as-is; any path-injection would have to originate from a tampered BaseURL in config — refused by `ErrNonHTTPSURL` on Load. |
| T-06-03-03 | Information Disclosure | pk_ disclosure via shell history when `--base-url` or `--api-key` passed on the command line | mitigate | Login's preferred UX is interactive prompts (D-03). `--base-url` is a URL (not a secret); `--api-key` is the explicit synthetic-mode path where the user already accepted that posture (spec §3.3). Logout never accepts a key on the CLI. The pk_ minted via login NEVER appears on the command line — it arrives via the token endpoint and is written directly to config. |
| T-06-03-04 | Tampering | Opener seam (xdg-open / open / rundll32) invoking attacker URL | mitigate | The `devicecode.Opener` seam receives the URL constructed by InitHandler from the server's BaseURL. A malicious config URL is the only attack vector; `config.Load` refuses non-HTTPS URLs (CLI-02 / 06-01 T-06-01-05) before login can use it. The Opener itself is best-effort — failure falls back to printing the URL to stdout (no privilege change). |
| T-06-03-05 | Information Disclosure | pk_ printed in plain form during login | mitigate | CLI-04: login's success message uses `config.Mask` to emit `<prefix>_****<last-4>`. Source-assertion gate verifies the masked form is the ONLY pk_ form on stdout. The full plaintext flows from `tokenResp.Plaintext` → `file.Deployments[name].PK` → `config.Save` — never through `fmt.Print*`. |
| T-06-03-06 | Repudiation | login completes without server-side audit | mitigate | 06-02 emits `platform.cli.login` audit event on the server's token endpoint with `actor=namespace/owner_email`, `key.id=<pkid_…>`, `request_id`. The CLI does not maintain its own audit log; the server is the source of truth. |
| T-06-03-07 | Information Disclosure | --verbose stderr dump leaks pk_ | mitigate | `httpclient.Redact` (06-01) rewrites every `^(pk|ek)_` header value to `<prefix>_***` before HeaderDump emits. Source-assertion gate in 06-01 Task 2 verifies the only path to stderr for header values is via Redact. |
| T-06-03-08 | Elevation of Privilege | logout leaves stale pk_ usable | accept | Logout wipes `pk:` from the local config file. Server-side, the pk_ remains valid for its sliding-window TTL (Hub §7.1) — by design, so a re-login on the same device resumes against an unexpired key. An operator who wants immediate revocation runs `ach admin keys revoke pkid_…` (06-08). |
| T-06-03-09 | Denial of Service | Login poll loop never terminates | mitigate | PollToken honors three exit conditions: success (200), terminal error (4xx/5xx-bursted), ctx cancellation, AND `ErrLoginTimeout` after `totalTimeout` (derived from server's `expires_in` field — bounded by 06-02's `DefaultSessionTTL = 5 * time.Minute`). |
| T-06-03-SC | Tampering | npm/pip/cargo installs | mitigate | No new third-party deps. Only stdlib + the foundation packages from 06-01. Existing govulncheck ack-list applies. |
</threat_model>

<verification>
After all 3 tasks complete:

```bash
./scripts/dev.sh go test ./internal/cli/... ./cmd/ach/cmd/...
./scripts/dev.sh go build ./cmd/ach/...
./scripts/dev.sh make lint
```

Smoke against a hypothetical Hub URL (engineer-pending until W3 e2e):
```bash
ACH_TEST_NO_BROWSER=1 ./bin/ach login --base-url https://hub.test --deployment prod --no-browser
./bin/ach whoami
./bin/ach whoami --verify
./bin/ach logout
```

Confirm SPDX headers via:
```bash
for f in internal/cli/devicecode/{doc,client,client_test}.go \
         cmd/ach/cmd/{login,login_test,whoami,whoami_test,logout,logout_test}.go; do
  head -1 "$f" | grep -q "Apache-2.0" || { echo "MISSING SPDX: $f"; exit 1; }
done
```
</verification>

<success_criteria>
- `ach login` device-code polling round trip works end-to-end against the W1-P2 server endpoints.
- `ach whoami` no-net + --verify both work; --verify branches on pk_ vs ek_ correctly.
- `ach logout` wipes pk_ but preserves URL.
- All three commands honor synthetic-mode short-circuit (full enforcement in W3-P1).
- pk_ plaintext printed only at login completion (masked tail); never echoed elsewhere.
- Unit tests via httptest are green.
</success_criteria>

<output>
Create `.planning/phases/06-cli-foundation/06-03-SUMMARY.md` when done. The summary MUST record:
- Confirmation that whoami consumes `httpclient.Client.ExtraHeaders` (landed in 06-01) for the ek_ `Accept-Encoding: gzip` header — no inline httpclient extension here.
- Final flag set for login/whoami/logout (so W2 hydrate flags don't collide). Note: whoami accepts `--api-key`/`--env-key` flags but full mutex enforcement is deferred to W3-P1 (06-07) per W6.
- Any deviations from Pattern P2/P5 with rationale.
</output>
