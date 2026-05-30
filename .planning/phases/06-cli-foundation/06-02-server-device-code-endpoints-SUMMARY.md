---
phase: 06-cli-foundation
plan: 02
subsystem: auth
tags: [device-code, dex, redis, cli, oauth2-pkce, miniredis, chi]

# Dependency graph
requires:
  - phase: 03-hub-identity-platform-api
    provides: SSO LoginHandler + CallbackHandler (D-04), audit.NewLogger, render.JSON/Error, RequestID middleware, error envelope §15.5
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: Redis client in platformapi.Deps, single-binary cobra layout, SPDX header policy
provides:
  - POST /platform/auth/cli/init — anonymous device-code session mint (D-02)
  - POST /platform/auth/cli/token — Redis GETDEL one-shot pk_ exchange (D-02)
  - cli.{Put, Peek, Consume, NewSessionID} session helper over "ach:cli-session:<id>" namespace (D-19)
  - cli.Session{KeyID, Plaintext, OwnerEmail, CreatedAt} payload shape (D-19)
  - audit.ActionCliLogin = "platform.cli.login" closed-enum constant
  - sso.CallbackHandler D-20 extension — session_id-aware writeback + HTML close-window page
  - sso.LoginHandler ?session_id threading via OAuth2 state suffix ("<random_state>|<session_id>")
  - auth.Deps gains Redis *redis.Client field (only D-20 consumer today; transparent when nil)
affects: [06-03-ach-login-whoami-logout, 06-06-ach-hydrate, 06-09-e2e-demo-collapse]

# Tech tracking
tech-stack:
  added: []  # All deps already in go.mod (go-redis, miniredis, go-chi, oauth2, go-oidc, pgx).
  patterns:
    - "Pattern P7 mount placement — chi subtree outside Authn-gated r.Group for anonymous endpoints."
    - "Pattern P8 strict JSON decode — DisallowUnknownFields + render.Error 400 invalid_argument for both /init and /token."
    - "Pattern P9 Redis session storage — split into Peek (non-destructive) + Consume (GETDEL) to support pending-poll without burning the entry."
    - "Pattern S5 audit-safety — pk_ plaintext never enters deps.Logger or deps.Audit; only the resolved key_id pkid_… is emitted."
    - "OAuth2 state suffix packing — random_state|session_id round-trips through Dex unchanged; cookie carries only the random_state so CSRF check unchanged."

key-files:
  created:
    - "internal/platformapi/auth/cli/doc.go (package doc citing D-02/D-19/D-20)"
    - "internal/platformapi/auth/cli/session.go (Session, Put, Peek, Consume, NewSessionID)"
    - "internal/platformapi/auth/cli/session_test.go (10 miniredis-backed tests)"
    - "internal/platformapi/auth/cli/init.go (InitHandler + InitResponse)"
    - "internal/platformapi/auth/cli/init_test.go (5 httptest tests)"
    - "internal/platformapi/auth/cli/token.go (TokenHandler + Token{Request,Response,PendingResponse})"
    - "internal/platformapi/auth/cli/token_test.go (7 httptest + miniredis tests)"
    - "internal/platformapi/auth/cli/mount.go (chi.Router closure registering POST /init + /token)"
  modified:
    - "internal/audit/events.go (+ ActionCliLogin = platform.cli.login in the Action* block)"
    - "internal/platformapi/auth/sso.go (Deps gains Redis; LoginHandler packs state; CallbackHandler unpacks + writes Redis + renders HTML)"
    - "internal/platformapi/auth/sso_test.go (+4 D-20 invariant tests using miniredis + go-redis)"
    - "internal/platformapi/server.go (thread Redis into authDeps; mount /platform/auth/cli subtree)"

key-decisions:
  - "Pack session_id into OAuth2 state as '<random_state>|<session_id>' rather than a companion cookie. Cookie still carries only the random_state — CSRF equality check compares the URL state's prefix (before the separator) against the cookie. Avoids a second cookie + cookie-jar contract; survives Dex's opaque state round-trip."
  - "Split session.go into Peek + Consume (NOT a single GetAndDelete). Peek is non-destructive — pending polls preserve the entry across calls; Consume uses GETDEL for atomic one-shot retrieval once the pk_ is written. This deviates from Pattern P9's GetAndDelete shape but was explicitly authorized by the plan (Task 2 action block called out the refactor)."
  - "Sentinel/complete discriminator is Session.KeyID == '' — InitHandler writes an empty-KeyID Session, CallbackHandler writes a full one. TokenHandler branches purely on KeyID emptiness; no separate Redis key or extra field needed."
  - "D-02 410 session_expired aliased to 404 session_not_found at the wire — Redis cannot tell TTL-bust from never-existed via GETDEL (both surface as redis.Nil). Documented in 06-CONTEXT.md as planner-discretion; honored verbatim here."
  - "Pending poll re-Puts the sentinel with deps.SessionTTL on every call — refreshes the TTL across polls so a slow user does not lose the session mid-flight. Re-Put failure logs but returns 202 anyway (the previous TTL still bounds exposure)."
  - "Browser HTML response (D-20) carries NO pk_ plaintext — the close-window page is text/html only. The CLI receives the pk_ via the /platform/auth/cli/token poll, not via the browser. Defense-in-depth against accidental leak through browser caches, screenshots, screen readers."
  - "Absence-of-Redis OR absence-of-session_id preserves the pre-Phase-6 JSON callback response verbatim — test/e2e/phase3_invariants assertions continue to pass unchanged."

patterns-established:
  - "Anonymous-endpoint mount in the unauth carve-out region of server.go (alongside SSO LoginHandler/CallbackHandler). Reusable by any future Phase 6+ endpoint that authenticates via short-lived token rather than pk_/ek_ bearer."
  - "OAuth2 state suffix-packing for opaque per-flow data round-tripped through an unmodified IdP. The suffix is delimiter-safe because both halves are base64url-encoded (never contain '|')."
  - "Redis session helper API as Peek + Consume (rather than single GetAndDelete) when pending-state needs non-destructive observation. Pattern can be reused by future polling endpoints."

requirements-completed: [CLI-01]

# Metrics
duration: ~30min
completed: 2026-05-28
---

# Phase 06 Plan 02: Server Device-Code Endpoints Summary

**POST /platform/auth/cli/{init,token} device-code endpoints + D-20 sso.CallbackHandler writeback land — `ach login` can now drive Dex SSO via polling, with the pk_ plaintext flowing through a 5-minute Redis session (one-shot GETDEL) instead of a browser-visible URL or JSON body.**

## Performance

- **Duration:** ~30 min
- **Started:** 2026-05-28T12:09:00Z
- **Completed:** 2026-05-28T12:36:03Z
- **Tasks:** 3 (all `type="auto" tdd="true"`)
- **Files modified:** 12 (8 created + 4 modified)

## Accomplishments
- Two anonymous Platform API endpoints under `/platform/auth/cli/` mounted outside the Authn-gated chi.Group: `/init` mints a 32-char base64url session_id + verification_url + 5-minute Redis sentinel; `/token` polls via non-destructive Peek (re-Puts to refresh TTL on each poll) and atomically Consumes (GETDEL) once the SSO round-trip lands the pk_.
- D-20 surgical extension to `sso.CallbackHandler`: optional `?session_id=<id>` packed into the OAuth2 state as `<random_state>|<session_id>` round-trips through Dex; on the callback the random prefix is CSRF-checked against the cookie, the suffix is extracted, and on success the freshly minted pk_ payload is written to `ach:cli-session:<id>` via `cli.Put` before rendering a friendly browser-side HTML close-window page. Absence-of-session_id preserves the pre-Phase-6 JSON branch verbatim — phase3 e2e assertions remain valid.
- `audit.ActionCliLogin = "platform.cli.login"` lands in the closed-enum block per §18.5 additive policy; emitted exactly once per /token success carrying `actor=<ns>/<owner_email>` + `key.id=pkid_…` + `request_id` — NEVER the pk_ plaintext (Pattern S5 enforced by grep gate).
- Single new package `internal/platformapi/auth/cli/` (5 source + 3 test files) consolidates all device-code surface; reuses go-redis + miniredis already in go.mod.

## Task Commits

Each task was committed atomically:

1. **Task 1: cli session helper + ActionCliLogin** — `29dda87` (feat)
2. **Task 2: /init + /token handlers + mount** — `eeeb34b` (feat)
3. **Task 3: wire mount + D-20 sso callback extension** — `67519ea` (feat)

_Note: TDD discipline observed throughout — test files written before each implementation; lint and unit gates green on every commit._

## Files Created/Modified

**Created (8):**
- `internal/platformapi/auth/cli/doc.go` — package doc citing D-02/D-19/D-20 + Pattern S5
- `internal/platformapi/auth/cli/session.go` — `Session`, `Put`, `Peek`, `Consume`, `NewSessionID`, `ErrNotFound`, `ErrCorruptSession`, `DefaultSessionTTL`, `DefaultPollInterval`, `sessionKeyPrefix`
- `internal/platformapi/auth/cli/session_test.go` — 10 miniredis-backed tests (TTL, sentinel round-trip, Peek non-destructive, Consume one-shot, corrupt JSON discipline)
- `internal/platformapi/auth/cli/init.go` — `InitHandler` + `InitResponse{session_id, verification_url, poll_interval, expires_in}`
- `internal/platformapi/auth/cli/init_test.go` — 5 httptest tests (200 happy path, sentinel written, 400 on unknown fields, no audit emission)
- `internal/platformapi/auth/cli/token.go` — `TokenHandler` + `TokenRequest` + `TokenResponse` + `TokenPendingResponse`
- `internal/platformapi/auth/cli/token_test.go` — 7 httptest + miniredis tests (pending 202, completed 200 + audit + one-shot, absent 404, missing/empty body 400, TTL refresh on pending polls)
- `internal/platformapi/auth/cli/mount.go` — `Deps` struct + `Mount(deps Deps) func(chi.Router)` returning the closure that registers POST `/init` + `/token`

**Modified (4):**
- `internal/audit/events.go` — `ActionCliLogin` constant added inside the Action* block (additive extension per §18.5)
- `internal/platformapi/auth/sso.go` — Deps gains `Redis *redis.Client`; LoginHandler packs optional `?session_id` into the OAuth2 state via the new `stateSessionSeparator` ('|'); CallbackHandler splits the URL state to extract the suffix and validates the prefix against the cookie; success branch writes Redis + renders HTML when session_id is present (new `renderCallbackHTML` helper + `callbackHTMLPage` constant)
- `internal/platformapi/auth/sso_test.go` — 4 new tests covering D-20 state packing and CallbackHandler's two response shapes; new miniredis + go-redis imports
- `internal/platformapi/server.go` — `authcli` import alias; threads `Redis: deps.Redis` into `authDeps`; mounts `/platform/auth/cli` subtree in the unauth carve-out region alongside the existing SSO routes

## Decisions Made

See `key-decisions` in frontmatter — 7 substantive decisions captured. Most load-bearing for downstream plans:

- **OAuth2 state suffix-packing** for session_id round-trip (no second cookie; cookie still stores only random_state; CSRF check intact) — affects W3 e2e test fixtures.
- **session.go Peek + Consume split** (NOT single GetAndDelete) so pending polls don't burn the entry — pending-poll non-destructive contract documented; existing Task 1 GetAndDelete API was never shipped (replaced inline before /token landed).
- **D-02 410 aliased to 404** (Redis GETDEL cannot tell TTL-bust from never-existed) — planner-discretion honored.

## Deviations from Plan

None — plan executed exactly as written, including the explicitly-authorized session.go refactor from Task 2 (Pattern P9 GetAndDelete → Peek + Consume split; the plan flagged this as the only deviation from Pattern P9 with full rationale, and called it out in `<action>` step "UPDATE session.go from Task 1 to split into Peek + Consume").

Minor lint adjustment: golangci-lint's `errcheck` flagged `defer r.Body.Close()` in init.go + token.go on first compile; fixed in-place with `defer func() { _ = r.Body.Close() }()` (idiomatic; matches existing project conventions). Caught by pre-commit hook before Task 2 commit landed; no Rule-1/2/3 deviation since the issue was a linter-flagged style adjustment, not a behavior change.

## Issues Encountered

None — pre-commit hooks (lint-changed + unit) gated every commit; full platformapi + audit test suites green on each iteration; `go vet ./...` clean.

## User Setup Required

None — no external service configuration required. The endpoints are wired into the existing `platform-api` Deployment and pick up the new routes automatically once the image rolls. No Helm value changes, no new env vars, no new RBAC. The existing Redis instance already shared between keystore + envkeys + admin handlers picks up the `ach:cli-session:` namespace without conflict.

## Next Phase Readiness

**W1-P3 (`ach login` + `whoami` + `logout`)** can now proceed:
- Client must POST `/platform/auth/cli/init` (empty body OK), parse `{session_id, verification_url, poll_interval, expires_in}`, open `verification_url` in the browser, then poll `/platform/auth/cli/token` with `{session_id}` at `poll_interval` cadence until 200 (parse `{key_id, plaintext, owner_email}`) or 404 (session expired / unknown).
- Pending state surfaces as 202 with `{status:"pending"}`; client should NOT treat 202 as terminal.
- Error envelope is `{error:{code,message}, request_id}` per §15.5 — `code` is `invalid_argument` (400), `session_not_found` (404), or `internal_error` (500).

**Demo collapse path (W3-P9)** is unblocked: the device-code flow replaces `examples/hydrate-demo.sh`'s browser-form posting once the CLI client lands.

## Self-Check: PASSED

Created files exist (8/8):
- `internal/platformapi/auth/cli/{doc,session,session_test,init,init_test,token,token_test,mount}.go` — all FOUND.

Modified files contain expected anchors:
- `internal/audit/events.go` — `ActionCliLogin = "platform.cli.login"` FOUND (1 hit).
- `internal/platformapi/auth/sso.go` — `Redis *redis.Client` field FOUND; `cli.Put` + `cli.Session{...}` FOUND (3 hits); `text/html` Content-Type FOUND.
- `internal/platformapi/server.go` — `authcli.Mount` mount FOUND; `Redis: deps.Redis` FOUND in 3 dep blocks (authDeps + cli.Deps + pre-existing adminDeps).

Commits exist on branch:
- `29dda87` Task 1 — FOUND in `git log`.
- `eeeb34b` Task 2 — FOUND in `git log`.
- `67519ea` Task 3 — FOUND in `git log`.

Full verification suite green:
- `./scripts/dev.sh go test ./internal/platformapi/auth/... ./internal/platformapi/... ./internal/audit/...` — exit 0, all packages PASS.
- `./scripts/dev.sh go vet ./...` — clean.
- Per-task pre-commit hook (`lint-changed` + `unit`) — green on all 3 commits.

---
*Phase: 06-cli-foundation*
*Completed: 2026-05-28*
