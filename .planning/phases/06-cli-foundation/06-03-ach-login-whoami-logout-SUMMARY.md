---
phase: 06-cli-foundation
plan: 03
subsystem: cli-foundation
tags: [cli, login, whoami, logout, device-code, dex, oauth2-pkce, asymmetric-verify]
requirements: [CLI-01, CLI-04, CLI-11]
dependency_graph:
  requires:
    - "06-01-cli-shared-internals (config + httpclient + exit packages)"
    - "06-02-server-device-code-endpoints (POST /platform/auth/cli/{init,token})"
  provides:
    - "internal/cli/devicecode (Init, PollToken, ErrLoginTimeout, Opener seam, HTTPClient seam)"
    - "ach login cobra subcommand (newLoginCmd factory; flags --deployment, --base-url, --no-browser, --no-warnings)"
    - "ach whoami cobra subcommand (newWhoamiCmd factory; flags --verify, --verbose, --deployment, --api-key, --env-key)"
    - "ach logout cobra subcommand (newLogoutCmd factory; flag --deployment)"
  affects:
    - "W2 hydrate (06-06): consumes the same flag namespace + httpclient.Client.ExtraHeaders pattern; depends on whoami's asymmetric-verify proof-of-life for the demo path"
    - "W3 synthetic-mode enforcement (06-07): extends the inline ACH_BASE_URL + ACH_API_KEY check used by login/logout into a centralized synthetic.GuardCommand and adds full CLI-09 mutex enforcement"
    - "W3 e2e (06-09): demo-collapse path (`ach login` + `ach hydrate --environment demo`) becomes reachable once W2 hydrate ships"
tech_stack:
  added: []  # All deps already in go.mod (stdlib + cobra + foundation packages from 06-01).
  patterns:
    - "Pattern P2 — leaf cobra subcommand via newXCmd() factory (not package-level var) so tests construct a fresh tree per t.Run; rootCmd registration via init() that calls the factory once"
    - "Pattern P5 — httpclient.Client consumer with ExtraHeaders for the ek_ Accept-Encoding: gzip path (no inline httpclient extension)"
    - "Pattern P6 — exit codes flow through main.go's errors.As branch via *httpclient.ServerError → exit.MapServerError OR *exit.CodedError → cErr.Code"
    - "Pattern P12 — RunE returns typed errors; cmd/ach/main.go maps to exit code at entry point"
    - "Package-level *http.Client seam (HTTPClient / whoamiHTTPClient): nil → fresh stdlib client; tests target httptest.NewTLSServer with a TLS-trusting Client without bloating the package API with per-call hooks"
key_files:
  created:
    - "internal/cli/devicecode/doc.go"
    - "internal/cli/devicecode/client.go"
    - "internal/cli/devicecode/client_test.go"
    - "cmd/ach/cmd/login.go"
    - "cmd/ach/cmd/login_test.go"
    - "cmd/ach/cmd/whoami.go"
    - "cmd/ach/cmd/whoami_test.go"
    - "cmd/ach/cmd/logout.go"
    - "cmd/ach/cmd/logout_test.go"
  modified: []
decisions:
  - "devicecode.HTTPClient + whoami.whoamiHTTPClient are package-level *http.Client seams (not per-call args). Tests target httptest.NewTLSServer with .Client() (the TLS-trusting client). Keeps the strict https-only URL check in login while letting unit tests exercise the real wire path against a loopback HTTPS endpoint. Documented in source comments above each seam var."
  - "newXCmd() factory pattern (not package-level cobra.Command var) for login/whoami/logout. The init() function calls the factory once for rootCmd registration; tests call the factory per t.Run. Pre-empts global cobra state leaks (SetArgs, flag-mutation order) that the plan flagged as Phase 3 test-discipline concern."
  - "whoami consumes httpclient.Client.ExtraHeaders unconditionally for the ek_ Accept-Encoding: gzip path. NO inline httpclient extension — the foundation contract from 06-01 is sufficient verbatim."
  - "CLI-09 mutex (--api-key vs --env-key vs ACH_API_KEY vs ACH_ENV_KEY conflict rejection) is NOT enforced in W1. whoami ACCEPTS all four credential sources and pre-resolves to the first non-empty one in flag → flag → env → env → disk-pk order. Full mutex-rejection enforcement is deferred to W3-P1 (06-07) where synthetic.GuardCommand or its sibling owns the CLI-09 boundary. The plan's frontmatter requirements list intentionally omits CLI-09."
  - "Synthetic-mode short-circuit is inline in login + logout (both ACH_BASE_URL and ACH_API_KEY non-empty → exit 1). whoami transparently SUPPORTS synthetic mode (the bearer comes from ACH_API_KEY and the same asymmetric verify branches apply). The deployment-flag/env REJECTION in whoami's synthetic branch (`--deployment` not allowed when synthetic is active) is W1 spec-§3.3 compliance, NOT CLI-09 enforcement."
  - "Default poll cadence + total-timeout fallbacks in login (defaultLoginPollInterval=2s, defaultLoginExpiresIn=5min) match the server-canonical values from 06-02. Used ONLY when InitResponse.PollInterval=0 or ExpiresIn=0 — production servers always populate them."
  - "logout does NOT clear default: (D-06). Empty pk_ with non-empty url: is the explicit resumable-state shape (subsequent ach login --deployment <name> reuses the URL without re-prompting). Server-side, pk_ remains valid for sliding-window TTL per Hub §7.1."
  - "deviceName regex is DNS-1123 label (`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`). Stricter than yaml-key-safe; chosen because the deployment name doubles as a path component in some future surfaces (ach config show, env-keys label scope)."
metrics:
  duration_minutes: 17
  completed_date: 2026-05-28
  tasks: 3
  files_created: 9
  files_modified: 0
---

# Phase 6 Plan 03: ach login + whoami + logout Summary

Wave 1's headline subcommand trio — `ach login` (D-02 device-code polling client + D-03 §5.1 UX), `ach whoami` (D-13 asymmetric verify against pk_ vs ek_), `ach logout` (D-06 pk_ wipe with url: preservation) — plus the shared `internal/cli/devicecode/` client. With this plan landed, a user can run the full Wave-1 round trip: `ach login` → `ach whoami --verify` → `ach logout`. The demo-collapse promise (`ach login` + `ach hydrate --environment demo` replaces `examples/hydrate-demo.sh`) becomes reachable once Wave 2 ships `ach hydrate`.

## What landed

### internal/cli/devicecode (Task 1 — `182e6a2`)

- `Init(ctx, baseURL)` POSTs `{}` to `/platform/auth/cli/init` and decodes the four-field `InitResponse{SessionID, VerificationURL, PollInterval, ExpiresIn}`. Reuses `*httpclient.ServerError` for envelope decode so cmd/ach/main.go's `errors.As` branch carries through unchanged.
- `PollToken(ctx, baseURL, sessionID, pollInterval, totalTimeout)` polls `/platform/auth/cli/token` with `{session_id}`. Four exit branches:
  - 200 → returns `*TokenResponse{KeyID, Plaintext, OwnerEmail}` (success).
  - non-pending 4xx (incl. 404 session_not_found, 410 session_expired aliased to 404 per 06-02 D-02 wire decision) → `*httpclient.ServerError` immediately, no retry.
  - persistent 5xx (>3 consecutive) → `*httpclient.ServerError`.
  - ctx cancelled → `ctx.Err()` (returned within one pollInterval).
  - totalTimeout elapsed → `ErrLoginTimeout`.
- `Opener` package-level `func(url string) error` seam: production = xdg-open / open / rundll32 dispatch by GOOS; tests override with a no-op. Best-effort — non-nil return → caller prints URL.
- `HTTPClient` package-level `*http.Client` seam: nil → fresh stdlib client with 30s timeout; tests override with `httptest.NewTLSServer().Client()`.
- Sentinel `ErrLoginTimeout` is distinct from `context.DeadlineExceeded` so the CLI can render a user-friendly "login timed out — please rerun `ach login`" message.
- 8 tests via httptest: init happy path + 5xx envelope decode, PollToken pending→200 with poll counter assertion, terminal 4xx (session_not_found) no-retry, ctx-cancel returns within one pollInterval, totalTimeout → ErrLoginTimeout, poll-interval cadence honored, Opener seam override.

### cmd/ach/cmd/login.go (Task 2 — `1419084`)

- `newLoginCmd()` factory + RunE driving the device-code flow per D-02 / D-03 / D-04 / CLI spec §5.1.
- Flag set: `--deployment <name>`, `--base-url <url>`, `--no-browser`, `--no-warnings`.
- Synthetic-mode short-circuit: `ACH_BASE_URL + ACH_API_KEY` both set → `&exit.CodedError{Code: General, Msg: "ach login is not available in synthetic mode..."}`.
- DNS-1123 deployment-name validation; https-only URL validation; interactive `bufio.Scanner` prompts on missing flags.
- Config flow: `config.LoadWith(stderr warn sink)` → `devicecode.Init` → `Opener` (unless --no-browser) → always-print verification_url (helps copy-paste even on opener success) → `PollToken` (cadence + total-timeout sourced from InitResponse) → mutate `file.Deployments[name].PK` + preserve existing EK map + auto-set `file.Default` when previously absent → `config.Save` (mode 0600).
- **CLI-04 enforcement**: pk_ plaintext printed ONLY as `config.Mask(plaintext)` = `pk_****<last-4>` at success. Full plaintext flows from `tokenResp.Plaintext` → `file.Deployments[name].PK` → on-disk yaml only. Source-assertion: `grep -c '"pk_****"' login.go` proves no plaintext fmt.Print at any other site.

### cmd/ach/cmd/whoami.go (Task 3 — `56ea9ed`)

- `newWhoamiCmd()` factory. Flag set: `--verify`, `--verbose`, `--deployment <name>`, `--api-key <pk_…>`, `--env-key <label>`.
- No --verify: prints identity block from `config.Load` only; **zero HTTP calls** (asserted by httptest request counter in TestWhoami_NoNet_PrintsIdentityBlock).
- --verify (D-13): `keys.ClassifyBearer` dispatches:
  - `pk_` → `Client.Do(ctx, GET, "/platform/environments?limit=1", nil, nil)`.
  - `ek_` → set `c.ExtraHeaders = http.Header{"Accept-Encoding": {"gzip"}}` then `Client.DoRaw(ctx, POST, "/platform/hydrate", struct{}{})` and discard body (CLI-11).
- Exit codes (D-14): 0 on 200; 3 on 401 (via *ServerError → main.go's `exit.MapServerError`); 6 on transport error (CodedError{Network}).
- Synthetic-mode TRANSPARENT support: bearer from `ACH_API_KEY` env, URL from `ACH_BASE_URL` env, `--deployment`/`ACH_DEPLOYMENT` REJECTED with exit 1 per CLI §3.3.
- `--verbose` stderr dump: `httpclient.HeaderDump` runs every `x-ach-key` value through `Redact` → `X-Ach-Key: pk_***` (3 stars). No plaintext leak (verified by TestWhoami_Verbose_RedactsKey: greps stderr for the full pk_ and asserts 0 hits).

### cmd/ach/cmd/logout.go (Task 3 — `56ea9ed`)

- `newLogoutCmd()` factory. Flag: `--deployment <name>`.
- Synthetic-mode short-circuit (D-06).
- D-06 semantics: `file.Deployments[name].PK = ""` only. URL preserved, EK map preserved, `default:` untouched. Server-side pk_ remains valid for its sliding-window TTL per Hub §7.1.
- Prints `Logged out of <name>` on success.

## Foundation-contract confirmation (anti-rework gate)

The plan called out two W1 contracts that the foundation (06-01) was expected to ship verbatim. Both are consumed without extension here:

1. **`httpclient.Client.ExtraHeaders` (top-level field)** — whoami's ek_ branch sets `c.ExtraHeaders = http.Header{"Accept-Encoding": {"gzip"}}` once on the Client and calls DoRaw without any per-call hook plumbing. NO inline conditional-extension code path.
2. **Server endpoints `/platform/auth/cli/{init,token}`** — devicecode.Init + PollToken consume the wire shape verbatim from 06-02-SUMMARY.md (four-field InitResponse, 200/202/404 contract on TokenResponse, session_id-only request body). NO retro changes to the server.

The W1-P2 session_id packing into the OAuth2 state (`<random_state>|<session_id>`) is server-side and CLI-transparent — devicecode never touches Dex state directly.

## Final flag set (for W2 hydrate to avoid collisions)

| Command  | Flags                                                                                          |
|----------|------------------------------------------------------------------------------------------------|
| login    | `--deployment`, `--base-url`, `--no-browser`, `--no-warnings`                                  |
| whoami   | `--verify`, `--verbose`, `--deployment`, `--api-key`, `--env-key`                              |
| logout   | `--deployment`                                                                                 |

W2 hydrate (06-06) plans to add: `--environment`, `--api-key`, `--env-key`, `--no-warnings`. The `--api-key`/`--env-key`/`--deployment`/`--no-warnings` set is shared with whoami; consistent semantics already established here.

## Test discipline + per-task TDD

Each task followed RED → GREEN with the test file written first:

- Task 1: 8 devicecode tests (httptest server with poll counter) — RED via undefined Init/PollToken/ErrLoginTimeout/Opener; GREEN by implementing client.go.
- Task 2: 7 login tests (httptest.NewTLSServer + devicecode.HTTPClient seam swap + XDG_CONFIG_HOME redirect) — RED via undefined newLoginCmd; GREEN by implementing login.go.
- Task 3: 8 whoami tests + 3 logout tests — RED via undefined newWhoamiCmd / newLogoutCmd / whoamiHTTPClient seam; GREEN by implementing both.

Pre-commit (`make lint-changed` + `make unit`) gated each of the 3 commits. golangci-lint clean across the affected packages on every commit.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Add `devicecode.HTTPClient` package-level seam**
- **Found during:** Task 2 test design.
- **Issue:** The plan's Test 1 calls `ach login --base-url <httptest.URL>` but the login command requires `https://` (per CLI-02 / D-04). `httptest.NewServer` returns an `http://127.0.0.1:PORT/` URL; `httptest.NewTLSServer` returns `https://` but uses an ephemeral self-signed cert that the default `*http.Client` will reject. The devicecode package was using a bare `http.Client{Timeout: ...}` with no injection seam, so tests couldn't reach the TLS server.
- **Fix:** Added a `var HTTPClient *http.Client` package-level seam in `internal/cli/devicecode/client.go`. nil → newHTTPClient builds a fresh stdlib client (production behavior preserved verbatim). Tests override with `httptest.NewTLSServer().Client()` (the TLS-trusting client) for the lifetime of the test via `t.Cleanup`. This is a single-purpose process-global seam (devicecode is only invoked once per process by `ach login`); adding a per-call argument would have bloated the API for no benefit.
- **Files modified:** `internal/cli/devicecode/client.go`. Documented in source comments above the var.
- **Commit:** Absorbed into `1419084` (Task 2 commit, since the seam was first consumed by the login tests).

**2. [Rule 3 - Blocking] Add `whoamiHTTPClient` package-level seam in whoami.go**
- **Found during:** Task 3 test design (same root cause as #1).
- **Issue:** whoami's `--verify` path goes through `httpclient.Client` (foundation, with its own injectable `HTTPClient` field). Setting `hc.HTTPClient = ts.Client()` per-test would require threading the test client through whoami's RunE — invasive. A package-level test-only seam mirrors the devicecode pattern and keeps the production code path clean.
- **Fix:** Added `var whoamiHTTPClient *http.Client` and `swapHTTPClientForTest(t, c)` helper in `cmd/ach/cmd/whoami.go`. nil in production → `httpclient.Client.HTTPClient` zero value → its own default. Tests swap via `t.Cleanup`.
- **Files modified:** `cmd/ach/cmd/whoami.go`. Documented in source comments above the var.
- **Commit:** Absorbed into `56ea9ed` (Task 3 commit).

### Documented divergences from plan acceptance text

**3. Test-fixture pk_/ek_ values changed from plan-suggested `pk_aaaaaaaaaaaaaaaaaaaaaaaaWXYZ` to `pk_aaaaaaaaaaaaaaaaaaaaaawxyz`**
- **Found during:** Task 3 first test run (TestWhoami_Verify_PK_Calls_Environments failed with "keys: invalid bearer plaintext; expected pk_<26-base32-lower>").
- **Issue:** The plan suggested test fixtures like `pk_aaaaaaaaaaaaaaaaaaaaaaaaWXYZ` (32 chars, mixed-case). `internal/keys.ClassifyBearer` (Phase 3) strictly enforces `pk_` + exactly 26 chars from `[a-z2-7]`. Uppercase + chars outside the base32-lower alphabet are rejected. whoami's --verify branch CALLS ClassifyBearer to dispatch pk_ vs ek_, so fixtures had to be valid 29-char base32-lower bearers.
- **Fix:** Changed all fixtures to valid 26-char base32-lower suffixes (e.g. `pk_aaaaaaaaaaaaaaaaaaaaaawxyz`, `pk_aaaaaaaaaaaaaaaaaaaaaabcde`, `ek_aaaaaaaaaaaaaaaaaaaaafghij`). The masked-tail assertion (`pk_****WXYZ` per plan acceptance text) updated to `pk_****wxyz` to match the lowercase reality.
- **Resolution:** Plan acceptance text was aspirational; actual key grammar pins fixture format. No behavior change in production code.
- **Files modified:** `cmd/ach/cmd/whoami_test.go`, `cmd/ach/cmd/logout_test.go`.

## Threat Surface Scan

| Threat ID | Coverage status |
|-----------|-----------------|
| T-06-03-01 | Honored by 06-02 server-side: session_id is 24 bytes crypto/rand, 5-min TTL, one-shot Consume. devicecode CLI consumes the wire shape verbatim — no key-management surface introduced. |
| T-06-03-02 | login.resolveBaseURL refuses non-https with exit 1 BEFORE any device-code call. config.Load also refuses non-https on read. Path injection via tampered config.url is blocked at load time. |
| T-06-03-03 | login interactive UX is the default (D-03). `--base-url` is a URL (not secret); `--api-key` is the explicit synthetic path; pk_ minted via login NEVER appears on the command line. logout takes no key on the CLI. |
| T-06-03-04 | devicecode.Opener is invoked only with InitResponse.VerificationURL composed server-side from `deps.BaseURL + "/platform/auth/login?session_id=<id>"`. login's URL validation refuses non-https config before any Opener call. Opener is best-effort (non-nil err → fall back to printing). |
| T-06-03-05 | login's success line uses `config.Mask(tokenResp.Plaintext)` → "pk_****<last-4>". Source verified: `grep -c 'tokenResp.Plaintext' login.go` shows 2 hits (one mutate file.Deployments[name].PK; one inside config.Mask call). No `fmt.Print*(tokenResp.Plaintext)` anywhere. |
| T-06-03-06 | 06-02 emits `platform.cli.login` audit event on the server's /token success branch. CLI does not maintain its own audit log; server is the source of truth. |
| T-06-03-07 | whoami's --verbose path uses httpclient.HeaderDump which runs every x-ach-key value through Redact (verified by TestWhoami_Verbose_RedactsKey: greps stderr for full pk_ and asserts 0 hits). |
| T-06-03-08 | Accepted — logout wipes pk: locally; server-side pk_ remains valid for sliding-window TTL. Documented in logout.go's package doc + the success line points to `ach admin keys revoke` for immediate revocation. |
| T-06-03-09 | PollToken's three exit conditions: success, terminal *ServerError, ctx.Done(), ErrLoginTimeout. totalTimeout is bounded by server's ExpiresIn (5min canonical per 06-02 DefaultSessionTTL). Default fallback in login.go (defaultLoginExpiresIn) is also 5min. |
| T-06-03-SC | No new third-party deps. Only stdlib + foundation packages from 06-01 + cobra (already vendored) + internal/keys (already vendored). Existing govulncheck ack-list applies. |

No new threat-flagged surface introduced beyond the plan's `<threat_model>` register.

## Self-Check: PASSED

Verified:
- `internal/cli/devicecode/doc.go` exists.
- `internal/cli/devicecode/client.go` exists.
- `internal/cli/devicecode/client_test.go` exists.
- `cmd/ach/cmd/login.go` exists.
- `cmd/ach/cmd/login_test.go` exists.
- `cmd/ach/cmd/whoami.go` exists.
- `cmd/ach/cmd/whoami_test.go` exists.
- `cmd/ach/cmd/logout.go` exists.
- `cmd/ach/cmd/logout_test.go` exists.
- Commits `182e6a2`, `1419084`, `56ea9ed` in `git log`.
- `./scripts/dev.sh go test ./internal/cli/... ./cmd/ach/cmd/...` exits 0 (all packages PASS).
- `./scripts/dev.sh go build ./cmd/ach/...` exits 0.
- `./scripts/dev.sh make lint-changed` exits 0 on every commit (pre-commit hook gated).
- SPDX header line 1 on all 9 new files (verified via `head -1 ... | grep Apache-2.0`).
- Source-assertion gates from plan acceptance criteria all PASS:
  - Task 1: `grep -E '^func (Init|PollToken)' client.go` = 2 ✓; ErrLoginTimeout count = 5 ✓; `select` count = 2 ✓.
  - Task 2: `grep -c 'devicecode.(Init|PollToken)' login.go` = 2 ✓; `grep -c 'config.(Save|Load|Mask|LoadWith)' login.go` = 5 ✓; `grep -c '"https://"' login.go` = 1 ✓; synthetic-check pattern hits = 4 ✓.
  - Task 3: environments path count = 1 ✓; hydrate path count = 1 ✓; ClassifyBearer hits = 3 ✓; gzip hits = 1 ✓; "Verified: yes" hits = 1 ✓; `.PK = ""` hits = 1 ✓.
- Build + run with `--help` exits 0 on all three new subcommands (cobra registration via init() succeeded).

---
*Phase: 06-cli-foundation*
*Plan: 03*
*Completed: 2026-05-28*
