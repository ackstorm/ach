---
phase: 06-cli-foundation
plan: 02
type: execute
wave: 1
depends_on: []
files_modified:
  - internal/platformapi/auth/cli/init.go
  - internal/platformapi/auth/cli/init_test.go
  - internal/platformapi/auth/cli/token.go
  - internal/platformapi/auth/cli/token_test.go
  - internal/platformapi/auth/cli/session.go
  - internal/platformapi/auth/cli/session_test.go
  - internal/platformapi/auth/cli/mount.go
  - internal/platformapi/auth/cli/doc.go
  - internal/platformapi/auth/sso.go
  - internal/platformapi/server.go
  - internal/audit/events.go
autonomous: true
requirements:
  - CLI-01

must_haves:
  truths:
    - "POST /platform/auth/cli/init returns 200 {session_id, verification_url, poll_interval, expires_in} with verification_url pointing at /platform/auth/login?session_id=<id>"
    - "POST /platform/auth/cli/token with body {session_id} returns 200 {key_id, plaintext, owner_email} once Dex callback completes"
    - "POST /platform/auth/cli/token returns 202 {status:'pending'} while session pending"
    - "POST /platform/auth/cli/token returns 404 session_not_found after TTL OR after first successful retrieval (one-shot via GETDEL)"
    - "POST /platform/auth/cli/token returns 410 session_expired when caller can distinguish TTL bust from never-existed (planner picks: GETDEL→nil-error → 404; planner MAY emit 410 only when session was observed pending and then expired — pragmatic: alias to 404)"
    - "/platform/auth/sso/callback writes the pk_ payload to ach:cli-session:<id> via cli.Put when ?session_id=<id> is present; returns a friendly browser-side HTML page (D-20)"
    - "/platform/auth/sso/callback preserves existing JSON-response behavior when ?session_id is absent (D-20 backward compat with phase3 e2e tests)"
    - "internal/audit/events.go exposes ActionCliLogin = 'platform.cli.login' as a closed-enum constant"
    - "platform.cli.login audit event emitted on successful token exchange; carries key.id (pkid_…) and owner_email; NEVER the pk_ plaintext (Hub §16.1, Pattern S5)"
    - "Redis key shape: 'ach:cli-session:<session_id>' with TTL ~5 minutes (D-19)"
  artifacts:
    - path: "internal/platformapi/auth/cli/init.go"
      provides: "POST /init handler — anonymously mints session_id + verification_url"
      contains: "func InitHandler(deps Deps) http.HandlerFunc"
    - path: "internal/platformapi/auth/cli/token.go"
      provides: "POST /token handler — polls session via GETDEL"
      contains: "func TokenHandler(deps Deps) http.HandlerFunc"
    - path: "internal/platformapi/auth/cli/session.go"
      provides: "Redis Put/GetAndDelete + ach:cli-session:<id> key shape"
      contains: "sessionKeyPrefix = \"ach:cli-session:\""
    - path: "internal/platformapi/auth/cli/mount.go"
      provides: "chi subtree mount"
      contains: "func Mount(deps Deps) func(r chi.Router)"
    - path: "internal/audit/events.go"
      provides: "ActionCliLogin closed-enum constant addition"
      contains: "ActionCliLogin"
  key_links:
    - from: "internal/platformapi/server.go"
      to: "internal/platformapi/auth/cli/mount.go"
      via: "r.Route(\"/platform/auth/cli\", authcli.Mount(...))"
      pattern: "/platform/auth/cli"
    - from: "internal/platformapi/auth/sso.go CallbackHandler"
      to: "internal/platformapi/auth/cli/session.go"
      via: "cli.Put(ctx, deps.Redis, sessionID, sess, ttl) when ?session_id is set"
      pattern: "session_id"
    - from: "internal/platformapi/auth/cli/init.go"
      to: "deps.BaseURL"
      via: "verification_url composed as BaseURL + /platform/auth/login?session_id=<id>"
      pattern: "/platform/auth/login\\?session_id="
---

<objective>
Ship the two new server-side endpoints that back `ach login`'s
device-code polling flow (D-02, D-19): `POST /platform/auth/cli/init`
and `POST /platform/auth/cli/token`. Both mount under a chi sub-router
OUTSIDE the Authn-gated group because init is anonymous (start of the
auth flow) and token gates by session_id alone (Redis one-shot
GETDEL).

This plan also performs the D-20 surgical extension to
`internal/platformapi/auth/sso.go CallbackHandler`: when called with
`?session_id=<id>`, write the pk_ payload to Redis under
`ach:cli-session:<id>` instead of (or in addition to) the existing
JSON response. The existing absence-of-session-id branch is preserved
verbatim so `test/e2e/phase3_invariants_test.go` browser-driven
assertions keep working.

Audit emission for `platform.cli.login` is wired through the existing
`internal/audit/handler.go` `*slog.Logger` per Phase 2 D-17 / Pattern
S5. The new action constant lands in `internal/audit/events.go` next
to the existing `ActionSSOLogin` block.

Purpose: Without these endpoints, `ach login` cannot resolve a pk_
plaintext via polling. This plan unlocks W1-P3 (`ach login` client) +
the whole demo-collapse path in W3.

Output: 1 new package (`internal/platformapi/auth/cli/`) with 4
source files + 3 test files + 1 doc.go, 3 modified files (`sso.go`,
`server.go`, `events.go`).
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
@internal/platformapi/auth/sso.go
@internal/platformapi/auth/sso_test.go
@internal/platformapi/auth/cookies.go
@internal/platformapi/server.go
@internal/platformapi/envkeys/handler.go
@internal/platformapi/envkeys/mount.go
@internal/platformapi/render/json.go
@internal/audit/events.go
@internal/audit/emit.go
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Author internal/platformapi/auth/cli session helper + audit constant</name>
  <files>
    internal/platformapi/auth/cli/doc.go
    internal/platformapi/auth/cli/session.go
    internal/platformapi/auth/cli/session_test.go
    internal/audit/events.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-19" (Redis schema + TTL) + §"D-20" (callback extension shape)
    - 06-PATTERNS.md §"Pattern P9" lines 449-509 (full session.go shape) + §"Modification Hotspots" line for events.go
    - internal/audit/events.go lines 30-115 (closed-enum block + file-level additivity-policy comment)
    - internal/audit/emit.go lines 44-119 (Event struct + EmitAudit shape — Pattern S5)
    - go.mod (confirm `github.com/redis/go-redis/v9` is direct dep)
  </read_first>
  <behavior>
    session tests (use github.com/alicebob/miniredis/v2 already in go.mod):
    - Test 1: Put(ctx, rdb, "abc", Session{...}, 5*time.Minute) writes JSON-marshaled payload at "ach:cli-session:abc" with TTL ~5 minutes (assert TTL > 4m via miniredis.TTL).
    - Test 2: GetAndDelete on a freshly-Put session returns the deserialized Session value, ok=true. A second GetAndDelete on the same id returns (nil, ErrNotFound) (one-shot semantics; uses Redis GETDEL).
    - Test 3: GetAndDelete on a never-existed id returns (nil, ErrNotFound).
    - Test 4: GetAndDelete on a key whose value is non-JSON garbage returns (nil, ErrCorruptSession) AND deletes the key (GETDEL semantics).
    - Test 5: NewSessionID returns a 32-character base64url-encoded string (24 bytes of entropy); two consecutive calls return distinct values.

    events constant test:
    - Test 6: `audit.ActionCliLogin == "platform.cli.login"` is the literal value (mirrors `ActionSSOLogin = "platform.sso.login"`).
  </behavior>
  <action>
    Append to `internal/audit/events.go` (existing file) immediately after `ActionSSOLogin = "platform.sso.login"` (around line 50):
    ```
    ActionCliLogin = "platform.cli.login"
    ```
    Preserve the alphabetical-by-platform.* ordering inside the const block; mirror the comment-block style of the existing constants. The file-level additivity-policy doc (lines 31-38) already authorizes additive extensions — no doc.go change needed.

    Author `internal/platformapi/auth/cli/doc.go` — package doc citing D-02, D-19, D-20, and the §15.4 trust-artifact contract.

    Author `internal/platformapi/auth/cli/session.go` mirroring Pattern P9 lines 449-505:
    - Package `cli` under `internal/platformapi/auth/cli/`.
    - Imports: `context`, `crypto/rand`, `encoding/base64`, `encoding/json`, `errors`, `time`, `github.com/redis/go-redis/v9`.
    - Const: `sessionKeyPrefix = "ach:cli-session:"`, `DefaultSessionTTL = 5 * time.Minute`, `DefaultPollInterval = 2 * time.Second`.
    - Type `Session{KeyID, Plaintext, OwnerEmail, CreatedAt string}` with JSON tags lower_snake.
    - Sentinel errors `ErrNotFound`, `ErrCorruptSession`.
    - Func `NewSessionID() (string, error)` — 24 random bytes (crypto/rand.Read) → base64.RawURLEncoding.EncodeToString.
    - Func `Put(ctx, rdb, id, session, ttl) error` — JSON-marshal + `rdb.Set(ctx, sessionKeyPrefix+id, json, ttl).Err()`.
    - Func `GetAndDelete(ctx, rdb, id) (*Session, error)` — `rdb.GetDel(ctx, sessionKeyPrefix+id).Result()`. Map `redis.Nil` → ErrNotFound. On non-nil string, json.Unmarshal; failure → ErrCorruptSession.

    Test file uses `github.com/alicebob/miniredis/v2` (already in go.mod) per the Phase 3 keystore test discipline. Stdlib `testing`, table-driven.

    SPDX header on every new file.
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./internal/platformapi/auth/cli/... ./internal/audit/...</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./internal/platformapi/auth/cli/... ./internal/audit/...` exits 0.
    - Source assertion: `grep -c "ActionCliLogin\s*=\s*\"platform.cli.login\"" internal/audit/events.go` returns 1.
    - Source assertion: `grep -c 'sessionKeyPrefix\s*=\s*"ach:cli-session:"' internal/platformapi/auth/cli/session.go` returns 1.
    - Source assertion: `grep -c "rdb.GetDel\|GetDel" internal/platformapi/auth/cli/session.go` returns ≥ 1 (atomic one-shot per D-19).
    - SPDX header line 1: `head -1 internal/platformapi/auth/cli/{doc.go,session.go,session_test.go}` all match `Apache-2.0`.
    - Behavior: miniredis test confirms GETDEL semantics — second read after first successful Get returns ErrNotFound.
  </acceptance_criteria>
  <done>
    session helper green; audit constant landed; closed-enum invariant preserved.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Author /platform/auth/cli/{init,token} handlers + mount</name>
  <files>
    internal/platformapi/auth/cli/init.go
    internal/platformapi/auth/cli/init_test.go
    internal/platformapi/auth/cli/token.go
    internal/platformapi/auth/cli/token_test.go
    internal/platformapi/auth/cli/mount.go
  </files>
  <read_first>
    - 06-CONTEXT.md §"D-02" (init/token wire shape verbatim) + §"D-19" (audit emission shape)
    - 06-PATTERNS.md §"Pattern P7" lines 365-422 (chi mount + mount placement) + §"Pattern P8" lines 425-447 (strict JSON decode + render) + §"Pattern S5" (no plaintext in audit)
    - internal/platformapi/envkeys/handler.go lines 148-179 (DisallowUnknownFields + decode + render.Error pattern)
    - internal/platformapi/envkeys/mount.go (whole file — Mount shape)
    - internal/platformapi/render/json.go lines 29-62 (render.JSON + render.Error + error envelope)
    - internal/platformapi/auth/sso.go lines 38-110 (Deps struct + RequestIDFromCtx usage)
    - internal/audit/emit.go lines 44-119 (EmitAudit signature; Event struct fields)
  </read_first>
  <behavior>
    init tests (httptest):
    - Test 1: POST /init with empty body returns 200 + JSON `{session_id, verification_url, poll_interval, expires_in}`. session_id is 32-char base64url; verification_url == `<BaseURL>/platform/auth/login?session_id=<id>`; poll_interval == 2; expires_in == 300 (seconds).
    - Test 2: POST /init writes a Redis `ach:cli-session:<id>` key with TTL ~5 minutes containing an EMPTY-Session sentinel (KeyID="", Plaintext="", OwnerEmail="", CreatedAt=RFC3339). This sentinel signals "pending" to TokenHandler. (Pragmatic choice: TokenHandler returns 202 when stored Session.KeyID == "".)
    - Test 3: POST /init with a non-empty body (unknown fields) returns 400 invalid_argument (DisallowUnknownFields).
    - Test 4: A `platform.cli.login` audit event with outcome "init" is NOT emitted on init (D-19: emit only on successful exchange in token handler).

    token tests (httptest + miniredis):
    - Test 5: POST /token with body `{"session_id":"<known-pending>"}` returns 202 `{"status":"pending"}` when stored Session.KeyID == "" (sentinel).
    - Test 6: POST /token with body `{"session_id":"<known-complete>"}` (where the stored Session has KeyID+Plaintext+OwnerEmail set) returns 200 `{"key_id":"pkid_...","plaintext":"pk_...","owner_email":"..."}`; the second call to /token with the same session_id returns 404 (GETDEL one-shot — the session has been consumed).
    - Test 7: POST /token with body `{"session_id":"<absent>"}` returns 404 `{"error":{"code":"session_not_found","message":"..."}}` (TTL bust or never existed; D-02 alias).
    - Test 8: POST /token with empty body OR missing `session_id` returns 400 invalid_argument.
    - Test 9: On 200 (successful exchange), a `platform.cli.login` audit event is emitted via deps.Audit with outcome="success", actor="<namespace>/<owner_email>", key.id=<pkid_…>, request_id=req_id_from_ctx. The plaintext is NEVER in the audit event.

    mount tests:
    - Test 10: Mount(deps) returns a func(r chi.Router) that, when applied to a chi.Mux subrouter, registers POST /init + POST /token.

    session refactor (per W1 warning — Task 1's GetAndDelete tests become partially obsolete once this task lands the Peek + Consume split):
    - Test 11: `internal/platformapi/auth/cli/session_test.go` Tests 2-4 from Task 1 are renamed/refactored to assert against the new Peek + Consume split. The original GetAndDelete func is REMOVED (no shim). New tests cover: (a) non-destructive Peek returns the value AND remaining TTL without deleting the Redis key, (b) destructive Consume returns the value AND deletes the key (GETDEL semantics), (c) Peek on a missing key returns ErrNotFound, (d) Peek on corrupted JSON returns ErrCorruptSession without deleting the key, (e) Consume on corrupted JSON returns ErrCorruptSession AND deletes the key.
  </behavior>
  <action>
    Author `internal/platformapi/auth/cli/mount.go` mirroring Pattern P7:
    - Package `cli` under `internal/platformapi/auth/cli/`.
    - `type Deps struct{ Redis *redis.Client; Audit *slog.Logger; Logger *slog.Logger; Namespace string; BaseURL string; SessionTTL time.Duration; PollInterval time.Duration }`. SessionTTL defaults to DefaultSessionTTL (5min) if zero; PollInterval defaults to DefaultPollInterval (2s) if zero.
    - `func Mount(deps Deps) func(r chi.Router)` returning a closure that `r.Post("/init", InitHandler(deps))` and `r.Post("/token", TokenHandler(deps))`. NO Authn middleware — both endpoints are anonymous.

    Author `internal/platformapi/auth/cli/init.go`:
    - `type InitResponse struct{ SessionID string json:"session_id"; VerificationURL string json:"verification_url"; PollInterval int json:"poll_interval"; ExpiresIn int json:"expires_in" }`.
    - `func InitHandler(deps Deps) http.HandlerFunc`:
      1. Get request_id via `middleware.RequestIDFromCtx(r.Context())`.
      2. Strict JSON decode of (empty) body — accept empty body and `{}`; reject extra fields. Use `json.NewDecoder(r.Body).DisallowUnknownFields()` and tolerate `io.EOF` as "no body sent".
      3. Generate session_id via `NewSessionID()`.
      4. Write a pending-sentinel Session{CreatedAt: time.Now().UTC().Format(time.RFC3339)} to Redis via `Put(ctx, deps.Redis, sessionID, sentinel, ttl)`. On failure → render.Error(500 internal_error).
      5. Compose verification_url = `deps.BaseURL + "/platform/auth/login?session_id=" + sessionID`.
      6. Render 200 InitResponse via `render.JSON(w, http.StatusOK, ...)`.
      7. NO audit emission on init.

    Author `internal/platformapi/auth/cli/token.go`:
    - `type TokenRequest struct{ SessionID string json:"session_id" }`.
    - `type TokenResponse struct{ KeyID string json:"key_id"; Plaintext string json:"plaintext"; OwnerEmail string json:"owner_email" }`.
    - `type TokenPendingResponse struct{ Status string json:"status" }` with `Status: "pending"`.
    - `func TokenHandler(deps Deps) http.HandlerFunc`:
      1. RequestIDFromCtx.
      2. Strict-decode body into TokenRequest; reject empty session_id with 400 invalid_argument.
      3. `sess, err := GetAndDelete(ctx, deps.Redis, req.SessionID)`. If `errors.Is(err, ErrNotFound)` → 404 `{error:{code:"session_not_found",...}}` (no audit emission). If err non-nil (corrupt, transient) → 500 internal_error.
      4. If `sess.KeyID == ""` (pending sentinel) → **re-PUT the same sentinel** (with the same remaining TTL — compute remainder via PTTL OR pragmatic: re-Put with full deps.SessionTTL since polling pace is bounded) → render 202 TokenPendingResponse.

         IMPORTANT: GETDEL is destructive. To preserve pending behavior, the implementation MUST first call `rdb.PTTL(ctx, sessionKeyPrefix+id)` and `rdb.Get(ctx, sessionKeyPrefix+id)` (peek), then conditionally `rdb.GetDel(...)` ONLY when KeyID != "". Refactor session.go to expose `Peek(ctx, rdb, id) (*Session, time.Duration, error)` returning the value + remaining TTL without deletion; expose `Consume(ctx, rdb, id) (*Session, error)` that does the destructive GETDEL. Use Peek for the pending branch and Consume only when KeyID is set. Update session_test.go accordingly.
      5. On `sess.KeyID != ""` (completed): emit audit event via `audit.EmitAudit(ctx, deps.Audit, audit.Event{Action: audit.ActionCliLogin, Outcome: "success", Actor: deps.Namespace + "/" + sess.OwnerEmail, RequestID: reqID, KeyID: sess.KeyID})`. Render 200 TokenResponse with `KeyID: sess.KeyID, Plaintext: sess.Plaintext, OwnerEmail: sess.OwnerEmail`.
      6. Audit-safety: NEVER log sess.Plaintext via deps.Logger; only the audit event with KeyID (pkid_…). Pattern S5.

    Mount placement in server.go is done in Task 3.

    SPDX header on every new file.

    UPDATE session.go from Task 1 to split into Peek + Consume, since pending-token polling cannot be one-shot. Adjust session_test.go and propagate. (This is the only deviation from Pattern P9 — call it out in the summary.)
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./internal/platformapi/auth/cli/...</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./internal/platformapi/auth/cli/...` exits 0.
    - Source assertion: `grep -c 'r.Post("/init"\|r.Post("/token"' internal/platformapi/auth/cli/mount.go` returns 2.
    - Source assertion: `grep -c 'DisallowUnknownFields' internal/platformapi/auth/cli/{init.go,token.go} | awk -F: '{s+=$2} END {print s}'` returns ≥ 2.
    - Source assertion: `grep -c 'audit.ActionCliLogin' internal/platformapi/auth/cli/token.go` returns ≥ 1.
    - Source assertion: `grep -E "Plaintext|plaintext" internal/platformapi/auth/cli/token.go | grep -E "Logger|slog|log\\." | wc -l` returns 0 (Pattern S5 — no plaintext through operational logs).
    - Source assertion (Peek + Consume split): `grep -cE 'func Peek\(|func Consume\(' internal/platformapi/auth/cli/session.go` returns 2 (Peek + Consume both declared).
    - Source assertion (no shim): `grep -cE 'func GetAndDelete\(' internal/platformapi/auth/cli/session.go` returns 0 (original API removed; no backward-compat shim).
    - Source assertion (Task 1 test file refactored): `grep -cE 'func Test.*Peek|func Test.*Consume' internal/platformapi/auth/cli/session_test.go` returns ≥ 2 (Tests 2-4 from Task 1 split into Peek + Consume tests).
    - Behavior: httptest + miniredis driver — init→pending-token→callback-write→token returns 200 with the stored pk_; subsequent token call returns 404.
    - Behavior: pending-poll (init → multiple token calls before callback) returns 202 each time; TTL preserved across polls (non-destructive Peek path).
    - Behavior: Peek on a session whose KeyID is set leaves the key intact in Redis; immediately following Consume retrieves the same value and removes it.
  </acceptance_criteria>
  <done>
    Both endpoints green; mount returns the chi closure; pending semantics non-destructive; plaintext never leaks to operational logs.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Wire mount + D-20 sso.CallbackHandler extension + audit emission</name>
  <files>
    internal/platformapi/server.go
    internal/platformapi/auth/sso.go
    internal/platformapi/auth/sso_test.go
  </files>
  <read_first>
    - 06-PATTERNS.md §"Modification Hotspots" rows for server.go (lines 136), sso.go (CallbackHandler 213-475 + Deps 38-90)
    - 06-CONTEXT.md §"D-20" (CallbackHandler extension semantics — must preserve absence-of-session-id branch)
    - internal/platformapi/server.go lines 33-86 (Deps struct — confirm Redis + BaseURL already threaded), lines 122-136 (unauth carve-out region — insertion point)
    - internal/platformapi/auth/sso.go lines 38-90 (auth.Deps struct), CallbackHandler 213-475 (step 7-8 — JSON render is the extension point)
    - internal/platformapi/auth/sso_test.go (httptest harness; phase3 e2e dependencies on the JSON branch)
    - test/e2e/phase3_invariants_test.go (top of file — confirm browser-driven flow still passes after the D-20 branch)
  </read_first>
  <behavior>
    server.go modification:
    - Mount the new cli subtree OUTSIDE the Authn-gated `r.Group` (alongside existing /platform/auth/login + /platform/auth/sso/callback) at the unauth carve-out region.

    sso.go modification:
    - LoginHandler accepts `?session_id=<id>` query param and threads it into the Dex state OR sets a transient cookie/value the CallbackHandler can read back. Recommended: thread session_id through OAuth2 `state` by concatenating `state_random|session_id` (the `state` field per RFC 6749 §4.1.1 is opaque to the OAuth2 server; we can pack extra data). Alternative: stash session_id in the `__Host-ach_sso` cookie next to the existing state+verifier. Planner picks; the test must show round-trip.
    - CallbackHandler at step 8 (final response render):
      - Decode session_id from the channel chosen above. If absent, preserve the existing `render.JSON(w, http.StatusOK, callbackResponse{...})` behavior verbatim (Phase 3 e2e flow).
      - If present, call `cli.Put(ctx, deps.Redis, sessionID, cli.Session{KeyID: row.KeyID, Plaintext: pkPlaintext, OwnerEmail: ownerEmail, CreatedAt: now.Format(time.RFC3339)}, sessionTTL)`. On Put failure: log via deps.Logger, but still render the browser-side HTML.
      - Render a browser-side HTML page `<html><body><h1>Login successful</h1><p>You may close this window.</p></body></html>` with `Content-Type: text/html; charset=utf-8`.
    - Update `auth.Deps` to add `Redis *redis.Client` field. Update `server.go` `authDeps` composition (lines 124-134) to pass `Redis: deps.Redis`.

    sso_test.go addition:
    - Test 1: CallbackHandler without ?session_id (and without packed state) → preserves existing JSON-response branch (assert response Content-Type is application/json + body is the existing JSON shape).
    - Test 2: CallbackHandler with session_id (via packed state OR cookie) → Redis is written with the pk_; response Content-Type is text/html; response body contains "You may close this window".
    - Test 3: The audit event emitted in either case is `platform.sso.login` (existing), NOT `platform.cli.login`. The `platform.cli.login` event happens at /token exchange (Task 2).
  </behavior>
  <action>
    Modify `internal/platformapi/server.go`:
    - After line 136 (`r.Get("/platform/auth/sso/callback", auth.CallbackHandler(authDeps))`), insert:
      ```go
      r.Route("/platform/auth/cli", authcli.Mount(authcli.Deps{
          Redis:     deps.Redis,
          Audit:     deps.Audit,
          Logger:    deps.Logger,
          Namespace: deps.Namespace,
          BaseURL:   deps.BaseURL,
      }))
      ```
    - Add the import alias: `authcli "github.com/ackstorm/ach/internal/platformapi/auth/cli"`.
    - In the existing `authDeps` composition (lines 124-134), add `Redis: deps.Redis,` field.

    Modify `internal/platformapi/auth/sso.go`:
    - Add `Redis *redis.Client` field to the `Deps` struct (after `Pool *pgxpool.Pool` for grouping); update existing Deps doc comments to mention "Phase 6 D-20: Redis used for CLI session writeback when ?session_id query param is set".
    - In LoginHandler: read `r.URL.Query().Get("session_id")` after the existing state/verifier generation. If non-empty, pack into the OAuth2 `state` value as `state_random + "|" + sessionID` (base64.RawURLEncoding-encoded segments). Update setSSOCookie call shape if needed — see decision below.
    - In CallbackHandler step 8 (right before the final `render.JSON(w, http.StatusOK, callbackResponse{...})` call at ~line 470):
      - Unpack the OAuth2 `state` param: split on `|`. If two parts, the second is sessionID. If one part, sessionID == "".
      - If sessionID != "" and deps.Redis != nil: build `cli.Session{KeyID: <row.KeyID from existing flow>, Plaintext: <pk_ plaintext>, OwnerEmail: <resolved owner>, CreatedAt: time.Now().UTC().Format(time.RFC3339)}`. Call `cli.Put(ctx, deps.Redis, sessionID, sess, 5*time.Minute)`. On error: log via deps.Logger ("cli session writeback failed", err) and continue.
      - If sessionID != "" : render HTML response `<html><body><h1>Login successful</h1><p>You may close this window.</p></body></html>` with `Content-Type: text/html; charset=utf-8`. Do NOT render the existing JSON.
      - If sessionID == "" : preserve the existing JSON render verbatim.

    Add the alias import in sso.go: `cli "github.com/ackstorm/ach/internal/platformapi/auth/cli"` (NOT `authcli` to avoid shadowing the package name in this file).

    Extend `internal/platformapi/auth/sso_test.go` (existing test file — append new t.Run subtests; DO NOT break existing assertions). The miniredis client is already used in similar phase3 tests; pass via the auth.Deps.Redis field.

    Verify the existing `internal/platformapi/auth/sso_test.go` continues to pass: every test that does NOT inject a Redis client OR session_id MUST get the JSON response (D-20 backward compat).

    Run `./scripts/dev.sh go vet ./...` to catch any unused-import or shadow issues.

    SPDX preserved on all files (existing headers untouched).
  </action>
  <verify>
    <automated>./scripts/dev.sh go test ./internal/platformapi/auth/... ./internal/platformapi/...</automated>
  </verify>
  <acceptance_criteria>
    - `./scripts/dev.sh go test ./internal/platformapi/auth/... ./internal/platformapi/...` exits 0.
    - Source assertion: `grep -c 'authcli.Mount\|r.Route("/platform/auth/cli"' internal/platformapi/server.go` returns ≥ 1.
    - Source assertion: `grep -c 'Redis:\s*deps.Redis' internal/platformapi/server.go` returns ≥ 2 (added to authDeps + new cli.Deps).
    - Source assertion: `grep -c 'Redis\s*\*redis.Client' internal/platformapi/auth/sso.go` returns 1 (Deps field added).
    - Source assertion: `grep -c 'cli.Put\|cli.Session{' internal/platformapi/auth/sso.go` returns ≥ 1 (D-20 writeback present).
    - Source assertion: `grep -c '"text/html"' internal/platformapi/auth/sso.go` returns ≥ 1 (browser-friendly HTML branch).
    - Behavior: sso_test.go original tests still pass without any code change in their httptest fixtures (D-20 backward compat).
    - Behavior: A new sso_test.go subtest that injects miniredis + packs session_id in state → asserts Redis key exists post-callback AND response Content-Type starts with "text/html".
    - Pre-push lint passes (`./scripts/dev.sh make lint`).
  </acceptance_criteria>
  <done>
    Cli subtree mounted; CallbackHandler extends gracefully; phase3 e2e flow preserved; new HTML branch present.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| CLI → Platform API (init) | Anonymous POST — untrusted client mints a session_id |
| Browser → Platform API (Dex callback) | Dex-authenticated; D-20 extension writes pk_ plaintext to Redis under session_id |
| CLI → Platform API (token) | Pseudo-authenticated via session_id alone; one-shot consumption via GETDEL |
| Platform API → Redis | Internal LAN; session_id is the only auth |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-06-02-01 | Spoofing | /platform/auth/cli/token | mitigate | session_id is 24 bytes of crypto/rand entropy = 192 bits; brute force is computationally infeasible within 5-min TTL window. |
| T-06-02-02 | Tampering | session_id in OAuth2 state | mitigate | state is opaque to Dex; the existing CSRF state check (Phase 3) protects against state substitution; packing session_id inside state inherits that protection. |
| T-06-02-03 | Repudiation | platform.cli.login audit event | mitigate | audit.EmitAudit emits actor=namespace/owner_email + key.id=pkid_ + request_id on every successful exchange (Pattern S5). |
| T-06-02-04 | Information Disclosure | pk_ plaintext at rest in Redis | mitigate | Redis lives in the platform-api namespace with ServiceAccount-restricted access; TTL ≤ 5 minutes bounds exposure; GETDEL one-shot consumption deletes immediately on first retrieval. |
| T-06-02-05 | Information Disclosure | pk_ plaintext in operational logs | mitigate | Pattern S5 — sess.Plaintext never passes through deps.Logger; only deps.Audit (which carries key.id, not plaintext); enforced by source assertion `grep` gate. |
| T-06-02-06 | Denial of Service | /platform/auth/cli/init | accept | Anonymous endpoint can be flooded to fill Redis with pending sentinels. Mitigation: Phase 7 deployment-level rate-limit at ingress; v1alpha1 accepts the risk (single-user dev posture per PROJECT.md). |
| T-06-02-07 | Elevation of Privilege | /token returns 200 to caller without authn | mitigate | session_id is the bearer-equivalent; only the owner of the polling CLI session knows it. Once consumed (GETDEL), it cannot be replayed. |
| T-06-02-SC | Tampering | npm/pip/cargo installs | mitigate | No new package installs in this plan — only go-redis (already direct dep), miniredis (already direct dep), and stdlib. Existing govulncheck ack-list applies. |
</threat_model>

<verification>
After all 3 tasks complete:

```bash
./scripts/dev.sh go test ./internal/platformapi/auth/... ./internal/platformapi/... ./internal/audit/...
./scripts/dev.sh go vet ./...
./scripts/dev.sh make lint
```

Confirm wire shape via a one-off integration test (optional — Task 2's miniredis test covers this):
- `POST /platform/auth/cli/init` returns 200 + `{session_id, verification_url, poll_interval, expires_in}`.
- `POST /platform/auth/cli/token {session_id:<id>}` returns 202 pending until callback completes; 200 once.

Confirm phase3 e2e still green when run against a kept kind cluster:
```bash
./scripts/dev.sh make e2e-focus FOCUS=TestPhase3
```
(Engineer-pending until cluster available; the unit-level sso_test.go covers the D-20 invariant for now.)
</verification>

<success_criteria>
- `POST /platform/auth/cli/init` and `POST /platform/auth/cli/token` live in `internal/platformapi/auth/cli/`.
- Redis schema `ach:cli-session:<id>` with TTL 5 min.
- D-20 extension: `CallbackHandler` writes session under `?session_id`, returns HTML; preserves JSON branch when `?session_id` absent.
- `ActionCliLogin = "platform.cli.login"` added to `internal/audit/events.go`.
- Audit event emitted on `/token` 200; never carries pk_ plaintext.
- Unit tests via miniredis + httptest are green.
</success_criteria>

<output>
Create `.planning/phases/06-cli-foundation/06-02-SUMMARY.md` when done. The summary MUST record:
- Final shape of `auth.Deps` (added `Redis *redis.Client` field).
- Whether session_id was packed into OAuth2 state OR cookie (decision affects W3 e2e test fixtures).
- Whether session.go was split into Peek + Consume (pending-poll non-destructive contract).
- Any deviations from Pattern P7/P8/P9 with rationale.
</output>
