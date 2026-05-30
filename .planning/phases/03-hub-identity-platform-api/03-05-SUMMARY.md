---
phase: 03-hub-identity-platform-api
plan: 05
subsystem: auth-middleware

tags: [keystore, redis-cache, singleflight, middleware, chi, authn, request-id, recover-panic, access-log, content-type-json, key-context, lookup-caller-teams, warn-06, blk-02, d-07, d-08, d-09, d-19, d-22, key-02]

# Dependency graph
requires:
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: "internal/credhash.Hash + ErrEmptyPepper (HMAC-SHA-256+pepper) used as the cache-key derivation primitive"
  - phase: 02-external-refs-marketplace-operator-reconciliation
    provides: "internal/audit.NewLogger + EmitAudit (audit=true slog handler) used by RecoverPanic + Authn internal_error emissions"
  - phase: 03-hub-identity-platform-api (wave 1)
    provides: "internal/db.PkCheckAndExtend + db.EkResolve (Plan 03-03) — keystore.dbResolver dispatches to these; internal/keys.ClassifyBearer + PrefixPk/PrefixEk + NewBearer (Plan 03-04) — keystore prefix dispatch; internal/litellm.Client.UserInfoByEmail (Plan 03-01) — teams.LookupCallerTeams calls this; internal/platformapi/render.Error (Plan 03-02) — Authn / RecoverPanic envelope writes"

provides:
  - "keystore.Resolver interface — single per-request auth contract for Phase 3 + 4 + 5 (D-08)"
  - "keystore.NewCachedResolver — Redis-backed read-through cache with singleflight dedup + 60s hard-TTL ceiling (D-07 / D-09)"
  - "keystore.NewDBResolver — Postgres-backed prefix dispatcher routing pk_ to PkCheckAndExtend and ek_ to EkResolve (D-08)"
  - "keystore.KeyInfo — normalized auth-row view consumed by middleware.WithKeyContext and downstream handlers"
  - "middleware.RequestID — outermost chi middleware (D-02 step 1); generates server-side req_<ulid>, sets X-Request-Id, stores in ctx; ALWAYS overrides caller-supplied X-Request-Id (T-03-05-06)"
  - "middleware.RecoverPanic — converts inner panics to 500 internal_error envelope + single audit emission; raw panic value never leaks to response body (T-03-05-07)"
  - "middleware.AccessLog — emits {method, path, status, latency_ms, request_id} exclusively; bearer plaintext NEVER logged (T-03-05-01 / FWD-11 invariant)"
  - "middleware.ContentTypeJSON — idempotent application/json content-type setter; preserves caller-set Content-Type (SSO 302 redirects)"
  - "middleware.Authn — reads x-ach-key, calls Resolver, on success DISCARDS plaintext from r.Header (D-19) and injects KeyContext into ctx with IsAdmin populated from the allowlist (BLK-02)"
  - "middleware.KeyContext + WithKeyContext + KeyContextFromCtx + WithRequestID + RequestIDFromCtx + ActorFromCtx — context-propagation primitives consumed by Plans 03-06..03-10"
  - "platformapi/teams.LookupCallerTeams — canonical team-membership lookup helper (WARN-06); Plans 03-08 + 03-09 import this verbatim. Phase 4 will replace with Redis-cached variant."
  - "platformapi/middleware/chi_compat.go — compile-time chi-signature anchor; pins go-chi/chi/v5 v5.2.0 in go.mod for Plan 03-06"

affects:
  - 03-06-server-router-mount (consumes Resolver + every middleware in chi.Mux constructor)
  - 03-07-sso-callback-handler (consumes middleware.KeyContextFromCtx for ctx access)
  - 03-08-env-keys-create-handler (consumes Resolver via middleware.Authn + teams.LookupCallerTeams for §8.2 step-4)
  - 03-09-hydrate-handler (consumes Resolver via middleware.Authn + teams.LookupCallerTeams for §15.1 team-intersection)
  - 03-10-admin-handlers (consumes KeyContext.IsAdmin set by middleware.Authn against the allowlist)
  - phase-04-forwarder (reuses keystore.Resolver verbatim; will swap teams.LookupCallerTeams to Redis-cached implementation)
  - phase-05-content-service (reuses keystore.Resolver verbatim for pk_ + ek_ auth)

# Tech tracking
tech-stack:
  added:
    - "github.com/redis/go-redis/v9 v9.7.0 — Redis client (D-09)"
    - "golang.org/x/sync v0.13.0 (promoted to direct) — singleflight subpackage (D-07)"
    - "github.com/alicebob/miniredis/v2 v2.33.0 — in-process Redis fake for keystore unit tests"
    - "github.com/go-chi/chi/v5 v5.2.0 — chi router; D-01 anchor for Plan 03-06"
  patterns:
    - "Read-through cache w/ singleflight: GET → on miss sf.Do → SET (best-effort) → return; nil KeyInfo NEVER cached"
    - "Cache key shape: 'ach:key:' + hex(HMAC-SHA-256(pepper, plaintext)); bearer plaintext never in key or value"
    - "60s hard-TTL ceiling — defaultTTL constant, no knob, no env override"
    - "Prefix dispatch via keys.ClassifyBearer: pk_ → db.PkCheckAndExtend; ek_ → db.EkResolve; invalid → (nil, nil) — treated as unknown"
    - "Chi-compatible middleware signature: every middleware satisfies func(http.Handler) http.Handler; closure middlewares return that shape from their constructors"
    - "Plaintext-discard discipline at D-19: literal r.Header.Del(\"x-ach-key\") inline for static-analysis grep gate visibility"
    - "Server-side request_id ALWAYS: req_<lowercase-ulid>; caller-supplied X-Request-Id ignored (T-03-05-06)"
    - "AccessLog discipline: 5 attributes only ({method, path, status, latency_ms, request_id}); statusCapturingWriter wraps but never inspects body"
    - "ContentTypeJSON idempotency: wrap ResponseWriter, set CT on first WriteHeader/Write only if caller hasn't set it"
    - "BLK-02 admin flag at Authn site: pk_ + OwnerEmail in allowlist → IsAdmin=true; ek_ → IsAdmin ALWAYS false"
    - "Compile-time canary for fake litellm.Client (lookup_test.go) — catches Client interface drift at build time"

key-files:
  created:
    - internal/keystore/doc.go
    - internal/keystore/keystore.go
    - internal/keystore/dbresolver.go
    - internal/keystore/keystore_test.go
    - internal/platformapi/middleware/doc.go
    - internal/platformapi/middleware/keyctx.go
    - internal/platformapi/middleware/middleware.go
    - internal/platformapi/middleware/chi_compat.go
    - internal/platformapi/middleware/middleware_test.go
    - internal/platformapi/teams/doc.go
    - internal/platformapi/teams/lookup.go
    - internal/platformapi/teams/lookup_test.go
  modified:
    - go.mod (added direct requires: redis/go-redis/v9, go-chi/chi/v5, alicebob/miniredis/v2, golang.org/x/sync promoted)
    - go.sum

key-decisions:
  - "Cache key shape literal 'ach:key:' + hex(HMAC-SHA-256(pepper, plaintext)) — bearer plaintext NEVER appears in key or value (T-03-05-03). Documented as defaultTTL = 60 * time.Second constant with no env-override knob (D-07 hard ceiling)."
  - "Nil KeyInfo is NOT cached. Caching a 'no such key' response would let a revoked credential survive the cache window past the explicit DEL barrier (KEY-07 / KEY-08). The next call simply re-misses and re-checks the DB."
  - "redisCachedResolver constructor refuses nil/empty pepper at construction time (ErrEmptyPepper). Refusing at construction is cleaner than failing at first Resolve — bad config surfaces at process startup."
  - "dbResolver dispatch via internal helper `dbLookupFn` callable type — production wraps db.PkCheckAndExtend + db.EkResolve; tests inject in-memory stubs via newDBResolverWith. Keeps dbresolver.Resolve branch-free per prefix and trivially unit-testable without a real pgx pool."
  - "ClassifyBearer ErrInvalidBearer → (nil, nil) at dbResolver — invalid plaintext is treated as unknown so the auth-layer renders 401 expired_or_revoked (KEY-04 / KEY-06 indistinguishability invariant). NOT propagated as an error to the caller."
  - "RequestID middleware ALWAYS generates server-side; caller-supplied X-Request-Id is IGNORED, not respected, not logged (T-03-05-06). The X-Request-Id response header reflects the server-generated id."
  - "AccessLog statusCapturingWriter wraps http.ResponseWriter but exposes only the status code field — body bytes are not captured, no header iteration occurs, no body inspection at all. The five logged attributes are hardcoded; FWD-11 / T-03-05-01 invariant enforced by code structure, not by allow-list filtering."
  - "ContentTypeJSON wraps the response writer and sets the application/json content-type only on first WriteHeader/Write call AND only if Content-Type has not been set yet. Preserves SSO 302 redirects (text/html), Plan 03-09 hydrate (already-set application/json), and any future content-type-aware handler."
  - "Authn populates KeyContext.IsAdmin uniformly at the middleware site (per BLK-02). Downstream admin handlers (Plan 03-10) read keyCtx.IsAdmin instead of re-deriving the allowlist lookup. ek_ callers ALWAYS get IsAdmin=false regardless of allowlist (admin endpoints reject ek_ upstream with 401 invalid_key_type)."
  - "Literal r.Header.Del(\"x-ach-key\") inline (NOT via the xAchKeyHeader constant) so the static-analysis grep gate that proves plaintext-discard discipline catches this site verbatim. The constant is still used for the GET side."
  - "Dual-branch 404 detection in teams.LookupCallerTeams: errors.Is(err, litellm.ErrNotFound) OR err.Error() contains '404'. Plan 03-01 D-25 design intentionally keeps UserInfoByEmail's 4xx wrapper at the type level (does NOT translate 404 → ErrNotFound) so the lookup helper covers both code paths defensively. Phase 4 may tighten this."
  - "platformapi/middleware/chi_compat.go ships a compile-time chi-signature anchor (`var _ chiMiddleware = RequestID`) plus a `var _ chi.Router = (chi.Router)(nil)` import pin. This (a) declares the chi-compatibility contract at the type level so future signature drift breaks the build, and (b) keeps chi v5.2.0 as a direct go.mod require ahead of Plan 03-06 (where the real chi.Mux constructor lives)."

patterns-established:
  - "Per-request auth path: bearer plaintext -> credhash.Hash with pepper -> ach:key:<hex> cache lookup -> single-flighted DB on miss -> KeyInfo serialized as JSON in Redis with 60s TTL"
  - "Middleware chain (chi-compatible) outer→inner: RequestID -> RecoverPanic -> AccessLog -> ContentTypeJSON -> Authn -> handler (D-02 verbatim)"
  - "Test seams: keystore uses miniredis.RunT for full in-process Redis; dbResolver uses dbLookupFn injection for branch-coverage without a pgx pool; middleware uses fakeResolver function-adapter + httptest.NewRecorder; teams uses fakeLiteLLM stub with compile-time canary"
  - "Audit-safety: Authn emits audit ONLY on internal_error paths; 401 rejections are operational signals (no actor known) and never audit-logged"
  - "Closure middleware signature: constructors that need dependencies (RecoverPanic, AccessLog, Authn) return `func(http.Handler) http.Handler`; constructors with no deps (RequestID, ContentTypeJSON) take and return http.Handler directly — both shapes work with chi.Router.Use"

requirements-completed: [KEY-02]

# Metrics
duration: ~30min
completed: 2026-05-20
---

# Phase 3 Plan 05: Keystore + Middleware + Teams Summary

**Wire the per-request authentication path: a `Resolver` interface backed by Redis-cache→DB-on-miss with single-flight dedup (D-07/D-08/D-09), a chi-compatible middleware chain (RequestID/RecoverPanic/AccessLog/ContentTypeJSON/Authn) that funnels every authenticated handler's `r.Context()` to a populated `KeyContext` and discards the bearer plaintext per D-19, and the canonical `teams.LookupCallerTeams` helper Plans 03-08 + 03-09 consume per WARN-06.**

## Performance

- **Started:** 2026-05-20T(execution-window)
- **Tasks:** 3 of 3
- **Files created:** 12 (3 in internal/keystore/, 5 in internal/platformapi/middleware/, 3 in internal/platformapi/teams/, 1 chi compile anchor)
- **Files modified:** 2 (go.mod, go.sum)
- **Tests landed:** 37 (12 keystore + 19 middleware + 6 teams)
- **External deps added:** 4 (redis/go-redis/v9, go-chi/chi/v5, alicebob/miniredis/v2, golang.org/x/sync promoted to direct)

## Accomplishments

- Shipped `internal/keystore` as the single per-request auth path: `Resolver` interface, `redisCachedResolver` with singleflight + 60s hard-TTL ceiling, `dbResolver` prefix-dispatching to Wave 1's `db.PkCheckAndExtend` / `db.EkResolve`. Cache key shape derives from `credhash.Hash` only — bearer plaintext NEVER serialized to Redis.
- Shipped `internal/platformapi/middleware` as the chi-compatible auth chain: every middleware satisfies `func(http.Handler) http.Handler`. `Authn` is the load-bearing layer — it discards plaintext from `r.Header` (literal `r.Header.Del("x-ach-key")` for grep-gate visibility) BEFORE the inner handler runs, and populates `KeyContext.IsAdmin` against the allowlist parameter per BLK-02.
- Shipped `internal/platformapi/teams.LookupCallerTeams` — the canonical team-membership lookup helper Plans 03-08 (envkeys.CreateHandler) + 03-09 (hydrate.HydrateHandler) will import per WARN-06. Phase 3 implementation calls LiteLLM directly on every request (uncached by design); Phase 4 Forwarder will replace the implementation with a Redis-cached variant sharing the keystore 60s TTL.
- 37 unit tests pass under stdlib `testing` + `miniredis` + `httptest` — no testcontainers needed (testcontainers integration tests for the dbResolver path live in Wave 1's Plan 03-03 already).

## Task Commits

Each task was committed atomically with the `feat(03-05): ...` prefix:

1. **Task 1: internal/keystore Resolver + Redis-cached + dbResolver** — `c3939af`
2. **Task 2: internal/platformapi/middleware chain (RequestID, RecoverPanic, AccessLog, ContentTypeJSON, Authn)** — `26d0a2d`
3. **Task 3: internal/platformapi/teams LookupCallerTeams + chi compile anchor** — `3e05437`

## Files Created/Modified

### Created

- `internal/keystore/doc.go` — package doc (cache key shape, TTL ceiling, single-flight semantics, dispatch contract)
- `internal/keystore/keystore.go` — `KeyInfo` struct, `Resolver` interface, `redisCachedResolver` + `NewCachedResolver`, `defaultTTL` 60s constant, `cacheKeyPrefix` `"ach:key:"` constant, `ErrEmptyPepper`
- `internal/keystore/dbresolver.go` — `dbResolver` + `NewDBResolver`, `dbLookupFn` callable type + `pkLookupFor` / `ekLookupFor` closures, `newDBResolverWith` test-only constructor
- `internal/keystore/keystore_test.go` — 12 tests (miss / hit / single-flight / TTL-exact / empty-pepper / inner-err / nil-no-cache + dbResolver pk-happy / ek-happy / pk-invalid / malformed-bearer / error-wrapped)
- `internal/platformapi/middleware/doc.go` — package doc (chain order, contracts per layer, KeyContext propagation discipline)
- `internal/platformapi/middleware/keyctx.go` — `KeyContext` struct with `IsAdmin bool`, `WithKeyContext` / `KeyContextFromCtx`, `WithRequestID` / `RequestIDFromCtx`, `ActorFromCtx` composing `<namespace>/<email>` per Hub §18.3
- `internal/platformapi/middleware/middleware.go` — `RequestID` / `RecoverPanic` / `AccessLog` / `ContentTypeJSON` / `Authn`, `statusCapturingWriter` + `contentTypeJSONWriter` response wrappers, `xAchKeyHeader` constant + literal-inlined `r.Header.Del("x-ach-key")`, `newRequestID` ULID generator
- `internal/platformapi/middleware/chi_compat.go` — compile-time chi-signature anchor; pins chi v5.2.0 in go.mod via `var _ chi.Router = (chi.Router)(nil)`
- `internal/platformapi/middleware/middleware_test.go` — 19 tests (3 RequestID + 1 RecoverPanic + 2 AccessLog + 2 ContentTypeJSON + 2 Authn-happy + 1 missing + 1 invalid + 1 resolver-err + 1 discards-plaintext + 1 absent-on-raw-ctx + 3 allowlist + 1 ActorFromCtx)
- `internal/platformapi/teams/doc.go` — package doc (WARN-06 contract, Phase 3 uncached / Phase 4 cached migration plan)
- `internal/platformapi/teams/lookup.go` — `LookupCallerTeams(ctx, ll, email) ([]string, error)` with dual-branch 404 detection (errors.Is + string-contains)
- `internal/platformapi/teams/lookup_test.go` — 6 tests (happy / empty-teams / ErrNotFound-sentinel / 404-string-wrapped / transport-err / uncached-by-design) + compile-time `var _ litellm.Client = (*fakeLiteLLM)(nil)` canary

### Modified

- `go.mod` — direct requires added: `github.com/redis/go-redis/v9 v9.7.0`, `github.com/alicebob/miniredis/v2 v2.33.0`, `github.com/go-chi/chi/v5 v5.2.0`; `golang.org/x/sync v0.13.0` promoted from indirect to direct (singleflight subpackage)
- `go.sum` — hash entries for the new deps + transitive

## Pinned go.mod versions

| Package | Pinned Version | Rationale |
|---------|----------------|-----------|
| `github.com/redis/go-redis/v9` | v9.7.0 | D-09 — current major; canonical Redis client; ~25k GitHub stars |
| `golang.org/x/sync` | v0.13.0 | singleflight subpackage; pinned to repo-compatible version (later v0.20+ requires go 1.25+, repo is on go 1.23) |
| `github.com/alicebob/miniredis/v2` | v2.33.0 | Test-only; canonical Redis fake for Go tests; ~2k stars; spawns a per-test in-process Redis via miniredis.RunT |
| `github.com/go-chi/chi/v5` | v5.2.0 | D-01 — Platform API HTTP multiplexer; verified mainstream choice; ~17k stars. v5.2.5 is the current stable (tidy upgrades silently to it) but the plan calls for v5.2.0 so explicitly pinned |

## Verified middleware chain order (D-02)

Cross-checked against the test fixture: the outer-to-inner order shipped by this plan matches D-02 verbatim. The `RequestID -> RecoverPanic -> AccessLog -> ContentTypeJSON -> Authn -> handler` order is realized by chi's `r.Use()` stacking semantics — each `Use` call wraps the next. Plan 03-06's `server.go` will register these in this exact order via:

```go
r := chi.NewRouter()
r.Use(middleware.RequestID)
r.Use(middleware.RecoverPanic(opLog, auditLog))
r.Use(middleware.AccessLog(opLog))
r.Use(middleware.ContentTypeJSON)

r.Group(func(r chi.Router) {
    r.Use(middleware.Authn(resolver, allowlist, auditLog))
    // ... authenticated routes ...
})
```

The unauthenticated routes (D-02 carve-out: /healthz, /livez, /readyz, /platform/auth/login, /platform/auth/sso/callback) are intentionally mounted OUTSIDE the Authn-gated `chi.Group` — Plan 03-06 handles this.

## BLK-02 + WARN-06 contract verification

- **BLK-02 honored.** `KeyContext` exposes `IsAdmin bool` as a first-class field (line 42 of keyctx.go). `Authn` accepts the `allowlist map[string]struct{}` parameter (line 208 of middleware.go) and computes `isAdmin := false; if info.KeyType == keys.PrefixPk { _, isAdmin = allowlist[info.OwnerEmail] }` before calling `WithKeyContext(ctx, info, isAdmin)`. Three tests cover the contract verbatim (positive / negative / ek-never-admin).
- **WARN-06 honored.** `internal/platformapi/teams.LookupCallerTeams(ctx, ll litellm.Client, email string) ([]string, error)` ships as the single canonical helper. Plans 03-08 + 03-09 (which land in later waves) will import it via `import "github.com/ackstorm/ach/internal/platformapi/teams"` and call `teams.LookupCallerTeams(...)`. The verifier gate `grep -nE 'func lookupCallerTeams\(' internal/platformapi/envkeys/ internal/platformapi/hydrate/` currently returns ZERO matches (those packages do not exist yet); the gate will continue to return ZERO matches once those plans execute.

## Decisions Made

See `key-decisions` frontmatter array for the 11 load-bearing implementation decisions. Highlights:

- **Cache key derives from credhash only — bearer plaintext never reaches Redis** — code-structural enforcement of T-03-05-03 (Information Disclosure mitigation).
- **Nil KeyInfo is NOT cached** — protects the explicit DEL revocation barrier from being undermined by a positive "no such key" cache entry.
- **Literal "x-ach-key" inline at the discard site** — accommodates the plan's static-analysis grep gate for D-19 plaintext-discard discipline without sacrificing the constant elsewhere.
- **Dual-branch 404 detection in LookupCallerTeams** — covers Plan 03-01 D-25's intentional decision to keep UserInfoByEmail's 4xx wrapper at the type level (does NOT translate 404 → ErrNotFound).
- **chi compile anchor** — declares the chi-compatibility contract at the type level so future signature drift breaks the build, AND pins chi v5.2.0 as a direct go.mod require ahead of Plan 03-06.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Worktree base sync to 2834cf1 before execution**

- **Found during:** Initial worktree_base_verification step.
- **Issue:** The worktree spawned from commit `e975d28` (Phase 1 end-state), which predates the Wave 1 merges (`2834cf1`) that ship `internal/keys/`, `internal/audit/`, `internal/platformapi/render/`, `internal/db/check_extend.go`, etc. — every Wave 1 file this plan depends on.
- **Fix:** Per the worktree_base_verification block, ran `git reset --hard 2834cf1` to advance the worktree branch onto current main. The reset was strict-ancestor only (no divergent commits to lose), and the protected `main` ref was never touched (the worktree branch is `worktree-agent-a4e86d977e32747ec`).
- **Verification:** Post-reset, all six Wave 1 path tests (`test -d internal/keys`, `test -d internal/audit`, `test -f internal/db/check_extend.go`, etc.) returned 0; baseline `go build ./...` returned exit 0 before any Task 1 edits.
- **Commit:** N/A — reset-only, no new commits.

**2. [Rule 3 — Blocking] `go mod tidy` strips chi after `go get` because no Go code imports it**

- **Found during:** Task 2 acceptance gate verification (`grep -nE 'go-chi/chi' go.mod` returned nothing after `go mod tidy`).
- **Issue:** The plan's Sub-step 2a (`./scripts/dev.sh go get github.com/go-chi/chi/v5@v5.2.0`) adds chi as a `// indirect` require because no Phase 3 wave 2 file imports the chi package — the middleware constructors only need to SATISFY the chi-compatible signature, which is just `func(http.Handler) http.Handler` (a stdlib type). `go mod tidy` then removes the unused entry entirely.
- **Fix:** Added `internal/platformapi/middleware/chi_compat.go` — a small file that (a) declares `type chiMiddleware = func(http.Handler) http.Handler` for readability, (b) asserts `var _ chiMiddleware = RequestID` + `var _ chiMiddleware = ContentTypeJSON` as compile-time signature canaries, and (c) imports `github.com/go-chi/chi/v5` with `var _ chi.Router = (chi.Router)(nil)` to pin chi v5.2.0 as a DIRECT require in go.mod. This double-serves: future signature drift breaks the build, AND Plan 03-06's chi.Mux constructor inherits the dependency.
- **Verification:** `grep -nE 'go-chi/chi' go.mod` post-tidy returns `15: github.com/go-chi/chi/v5 v5.2.0`. All middleware tests still pass.
- **Committed in:** `3e05437` (Task 3 commit; chi_compat.go landed alongside the teams package).

**3. [Rule 3 — Blocking] `golang.org/x/sync` requires go 1.25+ on `@latest`**

- **Found during:** Task 1 `go get golang.org/x/sync@latest`.
- **Issue:** The latest x/sync (v0.20.0+) requires go 1.25+, but the ach repo is on go 1.23.0 (per go.mod line 3). The plan's instruction was "promote from indirect to direct" — no specific version pin.
- **Fix:** Pinned to `golang.org/x/sync v0.13.0` (the version already present as indirect via controller-runtime). This is the most recent x/sync version that supports go 1.23. The singleflight subpackage API has been stable since v0.x; no behavioral difference vs v0.20.
- **Committed in:** `c3939af` (Task 1 commit).

---

**Total deviations:** 3 auto-fixed (all Rule 3 — blocking environment / tooling adjustments). No scope creep, no behavior changes. The chi anchor (deviation 2) is the only deviation that landed Go code beyond the plan's literal file list — and it's a single-file compile-time anchor that the plan's `<verification>` block explicitly endorses ("go.mod gains: github.com/go-chi/chi/v5").

## Plan-level Verification

| Check | Result |
|-------|--------|
| `./scripts/dev.sh go build ./internal/keystore/... ./internal/platformapi/middleware/... ./internal/platformapi/teams/...` exits 0 | PASS |
| `./scripts/dev.sh go vet  ./internal/keystore/... ./internal/platformapi/middleware/... ./internal/platformapi/teams/...` exits 0 | PASS |
| `./scripts/dev.sh go test ./internal/keystore/... ./internal/platformapi/middleware/... ./internal/platformapi/teams/... -count=1` exits 0 | PASS (37 tests pass across the three packages) |
| `./scripts/dev.sh go build ./...` exits 0 | PASS (full-repo build clean — no downstream regressions) |
| `./scripts/dev.sh go test ./internal/... -count=1` exits 0 | PASS (full internal test sweep — every existing package still passes, no regressions) |

### Acceptance grep gates (per task)

**Task 1 — keystore:**
- `type Resolver interface` in keystore.go — PASS (1 match, line 83)
- `type KeyInfo struct` in keystore.go — PASS (1 match, line 57)
- `singleflight.Group` in keystore.go — PASS (line 100)
- `"ach:key:"` literal in keystore.go — PASS (line 41 + 146 via constant)
- `60.*time\.Second` in keystore.go — PASS (line 36)
- `keys.ClassifyBearer` in dbresolver.go — PASS (line 94)
- `db.PkCheckAndExtend` / `db.EkResolve` execute sites in dbresolver.go — PASS (lines 131, 155)
- 9 tests pass — PASS (12 tests actually shipped, exceeds the 9 floor)
- TTL ceiling exactness (Test 8 analog) — PASS (TestCachedResolverTTLExact asserts `ttl == 60*time.Second`)
- Single-flight dedup (Test 7 analog) — PASS (TestCachedResolverSingleFlight asserts exactly 1 inner call for 50 concurrent goroutines)

**Task 2 — middleware:**
- `^func RequestID\(` in middleware.go — PASS (line 59)
- `^func RecoverPanic\(` in middleware.go — PASS (line 75)
- `^func AccessLog\(` in middleware.go — PASS (line 133)
- `^func ContentTypeJSON\(` in middleware.go — PASS (line 187)
- `^func Authn\(` in middleware.go — PASS (line 208)
- `r\.Header\.Del\("x-ach-key"\)` in middleware.go — PASS (line 244, literal inline per AC)
- `type KeyContext struct` in keyctx.go — PASS (line 34)
- `IsAdmin\s+bool` in keyctx.go — PASS (line 42)
- `^func Authn\(resolver keystore\.Resolver, allowlist map\[string\]struct\{\}, auditLog \*slog\.Logger\)` — PASS (line 208)
- `"req_"` in middleware.go — PASS (line 41 constant + line 39 doc)
- 13 tests pass — PASS (19 tests actually shipped; covers Tests 14-16 IsAdmin propagation per BLK-02)
- Test 5 NO `pk_xxx_secret` in access log output — PASS (TestAccessLogRedactsAchKey asserts)
- Test 12 inner handler observes empty x-ach-key — PASS (TestAuthnDiscardsPlaintext asserts)

**Task 3 — teams:**
- `LookupCallerTeams` signature in lookup.go — PASS (line 50, exact signature match)
- `errors\.Is\(err, litellm\.ErrNotFound\)` in lookup.go — PASS (line 69)
- `info\.Teams` in lookup.go — PASS (lines 58, 61)
- 5 tests pass — PASS (6 tests actually shipped — adds the dual-branch 404 test for Phase 3 D-25 reality)
- Cross-package `grep -nE 'func lookupCallerTeams\(' internal/platformapi/envkeys/ internal/platformapi/hydrate/` returns ZERO — PASS (those packages do not exist yet in Wave 2)

## Threat-Model Coverage (from PLAN.md `<threat_model>`)

| Threat | Disposition | Mitigation Landed In Code |
|--------|-------------|---------------------------|
| T-03-05-01 (bearer plaintext in access log) | mitigate | `AccessLog` records only 5 hardcoded attributes; the `statusCapturingWriter` wrapper never iterates headers; the `TestAccessLogRedactsAchKey` test sets `X-Ach-Key: pk_supersecretplaintext1234567` and asserts NEITHER the plaintext NOR a `pk_***` masked form appears in the captured buffer. |
| T-03-05-02 (bearer plaintext in handler logs) | mitigate | `Authn` calls `r.Header.Del("x-ach-key")` BEFORE `next.ServeHTTP`. `TestAuthnDiscardsPlaintext` asserts the inner handler reads empty `r.Header.Get("X-Ach-Key")`. |
| T-03-05-03 (bearer plaintext in Redis) | mitigate | Cache key shape `"ach:key:" + hex(HMAC-SHA-256(pepper, plaintext))` enforced structurally in `keystore.Resolve`. KeyInfo struct contains no plaintext field; serialization is JSON-encoded KeyInfo only. `TestCachedResolverMiss` + `TestCachedResolverHit` verify the cache-key shape and round-trip. |
| T-03-05-04 (Redis cache poisoning) | accept | Phase 3 trusts the namespace-scoped Redis service. Cache entries are not signed. Deployment-layer NetworkPolicy + Redis AUTH are operational concerns. |
| T-03-05-05 (thundering herd on cache miss) | mitigate | `singleflight.Group` dedups concurrent misses on the same plaintext. `TestCachedResolverSingleFlight` asserts 50 concurrent goroutines collapse to exactly 1 inner call. |
| T-03-05-06 (client-supplied X-Request-Id) | mitigate | `RequestID` middleware ALWAYS calls `newRequestID()` server-side; never reads `r.Header.Get("X-Request-Id")`. `TestRequestIDOverridesCaller` asserts a caller-supplied "client-spoofed" header value is replaced with a server-generated `req_<ulid>`. |
| T-03-05-07 (panic-induced state leak) | mitigate | `RecoverPanic` emits audit `outcome=internal_error` (NOT the raw panic value) and writes a fixed 500 envelope via `render.Error`. The panic value goes to the operational logger only. `TestRecoverPanicWritesEnvelope` asserts the response envelope contains `internal_error` and the panic value `boom` does NOT appear in the response body. |
| T-03-05-SC (npm/pip/cargo installs) | mitigate | 4 go.mod entries: redis/go-redis/v9 (~25k stars, k8s ecosystem); go-chi/chi/v5 (~17k stars, mainstream router); alicebob/miniredis/v2 (test-only, ~2k stars, canonical Redis fake); golang.org/x/sync (stdlib-adjacent, owned by Go team). All `[VERIFIED]`, not `[SLOP]`. |
| T-03-05-08 (duplicated team-lookup helpers diverge) | mitigate | `internal/platformapi/teams.LookupCallerTeams` is the single canonical helper. Plans 03-08 + 03-09 (later waves) will import it; the cross-package grep gate `grep -nE 'func lookupCallerTeams\(' internal/platformapi/envkeys/ internal/platformapi/hydrate/` returns ZERO matches today and will continue to return ZERO. |

## Threat Flags

None. This plan introduces no new network endpoints, auth paths, file access patterns, or schema changes at trust boundaries beyond what the plan's `<threat_model>` already enumerates. The Redis client is a new transitive dependency on the Redis service, but the connection model (deployment-namespace-scoped, AUTH-required via `ACH_REDIS_PASSWORD`) is a Phase 1 / Phase 3 cmd/platform-api wiring concern handled in Plan 03-06.

## Next Phase Readiness

- **Plan 03-06 (server.go / chi.Mux constructor) READY** — chi v5.2.0 already in go.mod via the chi_compat.go anchor. The Deps struct will include `Resolver` (this plan's interface), `Allowlist` (map[string]struct{}), `Audit` (\*slog.Logger), and an operational `Logger`. The `r.Use(...)` order is documented above.
- **Plan 03-07 (SSO callback) READY** — `middleware.WithKeyContext` + `middleware.KeyContextFromCtx` are the ctx primitives the callback uses to read the freshly-minted pk_'s KeyContext for the `actor` audit field.
- **Plan 03-08 (env-keys create) READY** — consumes `teams.LookupCallerTeams(ctx, ll, keyCtx.OwnerEmail)` for §8.2 step 4. Consumes `middleware.KeyContextFromCtx(r.Context())` for caller authentication.
- **Plan 03-09 (hydrate) READY** — same imports as 03-08 (teams + KeyContext); §15.1 team-intersection logic implemented in the handler.
- **Plan 03-10 (admin) READY** — consumes `keyCtx.IsAdmin` (populated by `middleware.Authn` against the allowlist) instead of re-implementing the allowlist lookup inline.
- **Phase 4 Forwarder PARTIAL** — will import `keystore.Resolver` verbatim; the chi compatibility means the Forwarder's mux can adopt the same middleware chain (although the Forwarder's specific Authn flow may differ slightly). The 60s TTL ceiling on `keystore` is the SAME contract Phase 4 / Phase 5 share.
- **No blockers introduced.** The chi-anchor and the dual-branch 404 detection in `teams.LookupCallerTeams` are forward-compatible extensions, not breaking changes.

## Worktree Note

This plan was executed in a Claude Code worktree spawned from commit `e975d28` (Phase 1 end-state) and reset to `2834cf1` (Wave 1 merged) at startup per the worktree_base_verification block. The reset was strict-ancestor only (no divergent commits to lose); the protected `main` ref was never touched. All three Task commits (`c3939af`, `26d0a2d`, `3e05437`) live on the per-agent branch `worktree-agent-a4e86d977e32747ec` and will be merged back via the orchestrator's normal wave-2 merge pass.

## Self-Check: PASSED

- File `internal/keystore/doc.go` exists: FOUND
- File `internal/keystore/keystore.go` exists: FOUND
- File `internal/keystore/dbresolver.go` exists: FOUND
- File `internal/keystore/keystore_test.go` exists: FOUND
- File `internal/platformapi/middleware/doc.go` exists: FOUND
- File `internal/platformapi/middleware/keyctx.go` exists: FOUND
- File `internal/platformapi/middleware/middleware.go` exists: FOUND
- File `internal/platformapi/middleware/chi_compat.go` exists: FOUND (Rule 3 deviation 2)
- File `internal/platformapi/middleware/middleware_test.go` exists: FOUND
- File `internal/platformapi/teams/doc.go` exists: FOUND
- File `internal/platformapi/teams/lookup.go` exists: FOUND
- File `internal/platformapi/teams/lookup_test.go` exists: FOUND
- Commit `c3939af` (Task 1) present in `git log --oneline -5`: FOUND
- Commit `26d0a2d` (Task 2) present: FOUND
- Commit `3e05437` (Task 3) present: FOUND
- `./scripts/dev.sh go build ./...` exits 0
- `./scripts/dev.sh go test ./internal/keystore/... ./internal/platformapi/middleware/... ./internal/platformapi/teams/... -count=1` exits 0 (37 tests pass)
- `./scripts/dev.sh go test ./internal/... -count=1` exits 0 (full internal sweep, no regressions)
- Frontmatter `requirements-completed` lists every requirement from the plan's `requirements:` field ([KEY-02]) exactly.

---

*Phase: 03-hub-identity-platform-api*
*Plan: 03-05*
*Completed: 2026-05-20*
