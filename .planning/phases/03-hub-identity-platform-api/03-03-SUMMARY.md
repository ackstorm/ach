---
phase: 03-hub-identity-platform-api
plan: 03
subsystem: database
tags: [pgx, postgres, sql, pk_, ek_, key-resolution, sliding-window, atomic-cte, orphan-cleanup]

# Dependency graph
requires:
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: personal_keys + environment_keys table schemas (migration 000001), pgxpool wrapper (internal/db.Open / db.Migrate), credhash.Hash discipline
  - phase: 02-external-refs-marketplace-operator-reconciliation
    provides: isTransientPgErr classifier (lifted in Task 1), litellm_user_id column (migration 000002), pgconn 08/57 transient-error convention, mustExec + setupPostgresForPhase2 testcontainers helper
  - phase: 02.2-phase-02-cleanup-gap-g1-fix-litellm-real-uat-path-invariant
    provides: litellm_token column (migration 000003) — the join key ListActiveACHKeyTokens now consumes
provides:
  - PkCheckAndExtend — Hub §7.1 atomic sliding-window check-and-extend SQL helper (literal BLK-04 CTE shape)
  - EkResolve — Hub §8.1 ek_ resolve with debounced last_used_at UPDATE that does NOT participate in the auth decision
  - InsertPersonalKey / GetPersonalKey / RevokePersonalKey / ListPersonalKeysByOwner — typed CRUD for personal_keys
  - InsertEnvironmentKey / GetEnvironmentKey / RevokeEnvironmentKey / ListEnvironmentKeysByOwner / ListEnvironmentKeysByOwnerWithFilter — typed CRUD for environment_keys (admin filter variant included)
  - ListActiveACHKeyTokens — replaces orphan-loop's ListActiveACHKeyIDs as the precise set-difference helper (Phase 02.2 D-02 closure)
  - PkKeyInfo / EkKeyInfo / PkInsertRow / EkInsertRow exported struct types for downstream handler + keystore code
  - shared isTransientPgErr classifier moved to internal/db/errors.go (package-private symbol still)
affects: [Plan 03-05 keystore (dbResolver consumes PkCheckAndExtend + EkResolve), Plan 03-07 SSO callback (consumes InsertPersonalKey), Plan 03-08 env-keys create (consumes InsertEnvironmentKey + LiteLLM compensation), Plan 03-09 admin handlers (consume RevokePersonalKey + RevokeEnvironmentKey + List variants), Plan 03-10 hydrate handler, Phase 4 Forwarder (reuses PkCheckAndExtend + EkResolve), Phase 5 Content Service (reuses PkCheckAndExtend + EkResolve), Phase 4+ orphan loop (swaps ListActiveACHKeyIDs → ListActiveACHKeyTokens)]

# Tech tracking
tech-stack:
  added: []  # zero new go.mod entries — reused pgx/v5 + pgconn already in scope
  patterns:
    - "Single-statement UPDATE … RETURNING with CASE-embedded debounce (PkCheckAndExtend / EkResolve)"
    - "Literal CTE shape WITH candidate AS (SELECT … FOR UPDATE) UPDATE … FROM candidate … RETURNING for atomic auth-decision + side-effect write (PkCheckAndExtend per BLK-04)"
    - "pgx.ErrNoRows → (nil, nil) — absent / revoked / expired indistinguishable to caller (KEY-04)"
    - "Opaque base64 cursor over (created_at, key_id) for paginated listers; limit clamped [1, 500] default 100"
    - "Admin variant via *string filter — nil = all rows, non-nil = narrow (ListEnvironmentKeysByOwnerWithFilter)"

key-files:
  created:
    - internal/db/errors.go (shared isTransientPgErr classifier)
    - internal/db/errors_test.go (8 unit cases, no Docker)
    - internal/db/types_keys.go (PkKeyInfo, EkKeyInfo, PkInsertRow, EkInsertRow)
    - internal/db/check_extend.go (PkCheckAndExtend)
    - internal/db/check_extend_test.go (6 testcontainers-go integration cases)
    - internal/db/ek_resolve.go (EkResolve)
    - internal/db/ek_resolve_test.go (5 integration cases)
    - internal/db/personal_keys.go (4 CRUD + pagination helpers)
    - internal/db/personal_keys_test.go (10 integration cases)
    - internal/db/environment_keys.go (5 CRUD + admin-filter helpers)
    - internal/db/environment_keys_test.go (9 integration cases)
  modified:
    - internal/db/external_refs.go (removed isTransientPgErr body + pgconn import — moved to errors.go)
    - internal/db/active_keys.go (appended ListActiveACHKeyTokens; ListActiveACHKeyIDs preserved)
    - internal/db/active_keys_test.go (appended 4 cases for ListActiveACHKeyTokens)

key-decisions:
  - "Lifted isTransientPgErr to internal/db/errors.go (option a from Task 1) — single shared symbol, both call sites (Phase 2 external_refs + Phase 3 new files) resolve through package-level scope. The pgconn import was removed from external_refs.go since the moved function was its sole consumer there."
  - "PkCheckAndExtend body uses the literal BLK-04 CTE shape (`WITH candidate AS (SELECT … FOR UPDATE) UPDATE personal_keys … FROM candidate … RETURNING …`) per REQUIREMENTS.md KEY-04 line 41 verbatim wording. The candidate CTE is the authoritative auth-decision predicate; the UPDATE's CASE-embedded debounce is a side effect. FOR UPDATE inside the CTE serializes concurrent writers on the same row under READ COMMITTED."
  - "EkResolve uses a single-statement UPDATE … RETURNING (no CTE needed — environment_keys has no expiration column so the sliding-window logic from check_extend.go does not apply). status='active' is the authoritative predicate per KEY-06; the last_used_at debounce CASE is purely a write-side concern."
  - "Pagination cursor encodes (created_at as RFC3339Nano, key_id) joined by NUL, base64-URL encoded. Sub-millisecond timestamp resolution lets rows inserted within the same second still sort deterministically against the (created_at DESC, key_id DESC) tuple ORDER BY."
  - "ListPersonalKeysByOwner / ListEnvironmentKeysByOwner fetch limit+1 rows and trim the trailing 'peek' row before returning, avoiding a separate 'has more' round-trip."
  - "ListActiveACHKeyTokens preserves the existing ListActiveACHKeyIDs helper unchanged — Phase 2's orphan-cleanup Runnable continues to work; Phase 4+ may swap the consumer once every Phase 3+ row carries a litellm_token."
  - "credentialHashHex parameter names are uniform across PkCheckAndExtend + EkResolve; both helpers wrap errors with the function name only — credential_hash NEVER appears in any wrapped error, log statement, or audit field per Hub §16.1 / T-AUDIT-01."
  - "PkInsertRow / EkInsertRow are value-struct arguments instead of long positional argument lists — eight or more INSERT columns make positional calls error-prone, especially when mixing nullable *string with non-null string."

patterns-established:
  - "Auth-decision SQL idiom: single-statement UPDATE…WHERE…RETURNING returning (nil, nil) on pgx.ErrNoRows so revoked/expired/unknown are indistinguishable to the caller (KEY-04 + KEY-06 alignment)"
  - "Phase 3+ key-table CRUD: typed struct argument for INSERTs, *PkKeyInfo / *EkKeyInfo returns, (nil, nil) on 'absent' rather than wrapped pgx.ErrNoRows"
  - "Admin lister variant: WithFilter(*string) — nil returns all rows; non-nil narrows. Same pagination + clamping logic as user-visible lister"
  - "Test seam: integration tests use mustExec for SQL seeding, exercise the helper, then re-read the row via raw SQL to confirm side effects (advanced last_used_at, extended expires_at, flipped status)"

requirements-completed: [KEY-04, KEY-06]

# Metrics
duration: 17min
completed: 2026-05-20
---

# Phase 03 Plan 03: internal/db Key Helpers Summary

**Typed pgx helpers for personal_keys + environment_keys: §7.1 atomic-CTE sliding-window check-and-extend (PkCheckAndExtend), §8.1 debounced ek_ resolve (EkResolve), full CRUD + paginated listers, and ListActiveACHKeyTokens — the precise orphan-loop set-difference helper Phase 02.2 D-02 promised once `litellm_token` columns get written for the first time.**

## Performance

- **Duration:** ~17 min
- **Started:** 2026-05-20T20:39:25Z
- **Completed:** 2026-05-20T20:57:07Z
- **Tasks:** 3
- **Files created:** 11
- **Files modified:** 3
- **Total lines added:** ~2,157 (production + tests)

## Accomplishments

- **PkCheckAndExtend** ships the Hub §7.1 atomic sliding-window check-and-extend in the literal BLK-04 CTE shape required by REQUIREMENTS.md KEY-04. The CTE holds the row lock; the UPDATE's CASE-embedded debounce is a side effect that does not participate in the auth decision. Revoked / expired / unknown all return (nil, nil) — indistinguishable by design.
- **EkResolve** ships the Hub §8.1 ek_ resolve as a single-statement UPDATE … RETURNING with a debounced last_used_at CASE. status='active' is the authoritative predicate (KEY-06).
- **Phase 02.2 D-02 prerequisite closed:** `ListActiveACHKeyTokens` returns a DISTINCT UNION of every active `litellm_token` across both key tables, replacing the Phase 2 `ListActiveACHKeyIDs` approximation (which never matched any LiteLLM-side `sk-...` token).
- **38 integration tests** under testcontainers-go Postgres + 8 unit tests for the shared classifier — every helper covered for happy + revoked + absent + debounce + pagination + admin-filter + null-column-mapping behaviors.
- **Zero new go.mod entries:** the plan reused existing pgx/v5, pgconn, and testcontainers-go dependencies.

## Task Commits

Each task was committed atomically:

1. **Task 1: Lift isTransientPgErr to errors.go + declare PkKeyInfo/EkKeyInfo** — `28c7372` (feat)
2. **Task 2: Implement PkCheckAndExtend + EkResolve** — `6a182b1` (feat)
3. **Task 3: CRUD helpers + ListActiveACHKeyTokens** — `9d54b8b` (feat)

## Files Created/Modified

### Created

- `internal/db/errors.go` — package-private `isTransientPgErr` classifier (pgconn class 08/57); 55 lines.
- `internal/db/errors_test.go` — 8 unit-test cases covering class 08/57 → true; class 23/42 → false; nil/non-pgconn → false; short Code → false; wrapped errors.As → true. No build tag, no Docker.
- `internal/db/types_keys.go` — `PkKeyInfo`, `EkKeyInfo`, `PkInsertRow`, `EkInsertRow` exported structs with column-shape doc comments.
- `internal/db/check_extend.go` — `PkCheckAndExtend(ctx, pool, credentialHashHex) (*PkKeyInfo, error)` with the verbatim BLK-04 CTE.
- `internal/db/check_extend_test.go` — 6 integration cases: active+stale (extends), active+fresh (debounce), revoked, expired, unknown hash, nullable LiteLLM columns map to nil pointer.
- `internal/db/ek_resolve.go` — `EkResolve(ctx, pool, credentialHashHex) (*EkKeyInfo, error)`.
- `internal/db/ek_resolve_test.go` — 5 integration cases: active happy, revoked, debounce, NULL litellm_token, unknown hash.
- `internal/db/personal_keys.go` — `InsertPersonalKey`, `GetPersonalKey`, `RevokePersonalKey`, `ListPersonalKeysByOwner`. Helpers `clampLimit`, `encodeCursor`, `decodeCursor` shared with environment_keys.go.
- `internal/db/personal_keys_test.go` — 10 integration cases: insert happy + unique-violation, get-absent, revoke happy + already-revoked + absent, list filters+orders, paginate 2 pages, limit clamping, malformed cursor → error.
- `internal/db/environment_keys.go` — `InsertEnvironmentKey`, `GetEnvironmentKey`, `RevokeEnvironmentKey`, `ListEnvironmentKeysByOwner`, `ListEnvironmentKeysByOwnerWithFilter` (admin variant).
- `internal/db/environment_keys_test.go` — 9 integration cases including the admin nil-filter returns-all-rows test.

### Modified

- `internal/db/external_refs.go` — removed the inlined `isTransientPgErr` body + the now-unused `pgconn` import; left a breadcrumb comment pointing at `errors.go`. Behavior unchanged (package-level visibility resolves the moved symbol).
- `internal/db/active_keys.go` — appended `ListActiveACHKeyTokens`. `ListActiveACHKeyIDs` preserved untouched.
- `internal/db/active_keys_test.go` — appended 4 cases for `ListActiveACHKeyTokens` (empty, distinct collapses shared token, excludes NULL, excludes revoked).

## Decisions Made

- **Lifted isTransientPgErr to errors.go (Task 1 option a).** Both call sites (Phase 2 `external_refs.go` and the five new Phase 3 helpers) resolve through package-level scope. The `pgconn` import was removed from `external_refs.go` since the moved function was its sole consumer there. Alternative (duplicate the helper as package-private in each new file) was rejected: violates single-source-of-truth and creates drift risk when Phase 4+ Forwarder or Content Service adds new classifications.
- **Used literal BLK-04 CTE shape for PkCheckAndExtend.** REQUIREMENTS.md KEY-04 line 41 specifies "single atomic SQL CTE" verbatim; the body is `WITH candidate AS (SELECT … FOR UPDATE) UPDATE personal_keys … FROM candidate … RETURNING …`. The PATTERNS file showed a simpler single-statement UPDATE alternative — both shapes are functionally equivalent for the KEY-04 contract, but the CTE form matches the REQUIREMENTS wording verbatim, makes the candidate row-lock explicit (`FOR UPDATE` inside the SELECT), and is what the verifier's grep gates explicitly check for.
- **EkResolve does NOT use a CTE.** `environment_keys` has no `expires_at` column (revocation-only per migration 000001), so the sliding-window predicate that motivates the CTE in `check_extend.go` does not apply. A single-statement UPDATE with `status='active'` predicate + RETURNING is sufficient and more readable. KEY-06 invariant (debounce does not participate in auth) is preserved.
- **Pagination cursor encodes `(RFC3339Nano timestamp, key_id)` joined by NUL, base64-URL.** Sub-millisecond timestamp resolution lets rows inserted within the same second still sort deterministically against the `(created_at DESC, key_id DESC)` ORDER BY. The encoding is opaque to callers — its format may change across releases without breaking the API.
- **`limit+1` row fetch.** Both list helpers fetch one extra row and trim it before returning. This detects "more pages available" without a separate `SELECT COUNT(*)` round-trip; nextCursor is only computed when the +1 row materialized.
- **`PkInsertRow` / `EkInsertRow` value-struct arguments instead of positional.** Eight or more INSERT columns mixing nullable `*string` with non-null `string` makes positional calls error-prone (compile-passes-but-semantically-wrong on column reorder). Named struct fields catch the mistake at call time.
- **`ListActiveACHKeyIDs` preserved unchanged.** Phase 2's orphan-cleanup Runnable continues working without disruption; Phase 4+ may retire the older helper once every Phase 3+ row carries a `litellm_token`. The new `ListActiveACHKeyTokens` lives next to the existing helper in the same file.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Updated comment phrasing in `check_extend.go` + `ek_resolve.go` to satisfy literal-grep acceptance criteria**
- **Found during:** Task 2 verification step.
- **Issue:** The plan's acceptance criteria included two strict literal-grep gates:
  - `grep -cE "expires_at" internal/db/ek_resolve.go` must equal 0.
  - `grep -rnE "credentialHashHex" check_extend.go ek_resolve.go | grep -E "(log|slog|fmt\.Print)" | wc -l` must equal 0.
  I had written doc comments mentioning "no expires_at column" (literally containing `expires_at`) and "credentialHashHex is NEVER logged" (which matched the `log` substring). Both gates failed initially.
- **Fix:** Rephrased the comments. `expires_at` → "expiration column"; "NEVER logged" → "MUST NOT flow into any audit trail or structured event". Behavior unchanged; only doc-comment text adjusted.
- **Files modified:** `internal/db/check_extend.go`, `internal/db/ek_resolve.go`.
- **Verification:** Both gates now equal 0; full integration suite still passes.
- **Committed in:** `6a182b1` (Task 2 commit — change was made before the commit).

**2. [Rule 3 — Blocking] Updated comment phrasing in `active_keys.go` to satisfy strict-count grep**
- **Found during:** Task 3 verification step.
- **Issue:** `grep -cE 'litellm_token IS NOT NULL' internal/db/active_keys.go` must equal 2 (the two SQL clauses). My doc comment also contained the exact phrase, making the count 3.
- **Fix:** Reworded the comment to "Non-null litellm_token on both tables …" — preserves the meaning without the literal SQL phrase.
- **Files modified:** `internal/db/active_keys.go`.
- **Verification:** count now equals 2.
- **Committed in:** `9d54b8b` (Task 3 commit — change was made before the commit).

**3. [Rule 3 — Blocking] Fast-forwarded worktree branch from `e975d28` to `a4daf45` (current main) before executing**
- **Found during:** Initial environment setup (before Task 1).
- **Issue:** The worktree spawned from commit `e975d28` (end of Phase 1), which predates Phase 2 (commit-merged code: `internal/db/external_refs.go`, `internal/db/active_keys.go`, `internal/db/litellm_users.go`, `db/migrations/000002_phase2.up.sql`) and Phase 02.2 (`db/migrations/000003_litellm_token.up.sql`). The plan's `<context>` directive references these files, and Task 1 explicitly moves a symbol out of `external_refs.go` — none of which is present at `e975d28`.
- **Fix:** Verified the worktree HEAD is a strict ancestor of main (`git merge-base --is-ancestor HEAD main` succeeded), then ran `git merge --ff-only main` to advance the worktree branch. Zero merge conflicts; all 123 commits between `e975d28` and `a4daf45` applied cleanly. The fast-forward did NOT touch the protected `main` ref (the worktree branch is `worktree-agent-ae3dd7c4cac99b683`).
- **Verification:** `git log --oneline -3` post-merge showed the expected Phase 02.x head commits; `git ls-files internal/db/` listed all 11 Phase 1+2 files; baseline `go build ./internal/db/...` returned exit 0 BEFORE any Task 1 edits.
- **Committed in:** N/A — fast-forward only, no new commits introduced before Task 1.

---

**Total deviations:** 3 auto-fixed (3 Rule 3 — blocking).
**Impact on plan:** All three were mechanical adjustments that did not change any production behavior. Deviation #1 and #2 reworded doc comments to satisfy the verifier's literal-grep gates without touching any SQL or Go logic. Deviation #3 was a one-time environment sync to align the worktree with the reference state the plan assumed. No scope creep.

## Issues Encountered

- None. The plan's task ordering (Task 1 prepares shared types and the classifier; Task 2 builds the auth-path helpers on those types; Task 3 builds the CRUD helpers on the same types + adds the orphan helper) was a clean dependency DAG with no need to revisit earlier work.

## Self-Check

Verified:
- `[ -f internal/db/errors.go ]` → FOUND
- `[ -f internal/db/types_keys.go ]` → FOUND
- `[ -f internal/db/check_extend.go ]` → FOUND
- `[ -f internal/db/ek_resolve.go ]` → FOUND
- `[ -f internal/db/personal_keys.go ]` → FOUND
- `[ -f internal/db/environment_keys.go ]` → FOUND
- All four task-related test files exist with build-tag `//go:build integration`.
- Commit `28c7372` (Task 1) present in `git log --oneline --all`.
- Commit `6a182b1` (Task 2) present.
- Commit `9d54b8b` (Task 3) present.
- `./scripts/dev.sh go build ./internal/db/...` exits 0.
- `./scripts/dev.sh go build ./...` exits 0 (full repo build clean — no downstream regressions).
- `./scripts/dev.sh go vet ./internal/db/...` exits 0.
- `./scripts/dev.sh go test ./internal/db/... -count=1` exits 0 (8 unit cases).
- `./scripts/dev.sh go test ./internal/db/... -tags integration -count=1` exits 0 (38 cases including the regression check on the pre-existing `ListActiveACHKeyIDs` tests).
- Frontmatter `requirements-completed` lists every requirement from the plan's `requirements:` field ([KEY-04, KEY-06]) exactly.

## Self-Check: PASSED

## Next Phase Readiness

- **Plan 03-05 (keystore) READY:** `dbResolver` can branch on plaintext prefix and call `PkCheckAndExtend` / `EkResolve` directly. The exported `PkKeyInfo` / `EkKeyInfo` structs are the source types `keystore.KeyInfo` will be mapped FROM.
- **Plan 03-07 (SSO callback) READY:** `InsertPersonalKey` is the §7.1 row-write the callback runs after `litellm.KeyGenerate` returns. `RevokePersonalKey` is the DB-first revocation barrier the admin endpoint uses (KEY-07).
- **Plan 03-08 (env-keys create) READY:** `InsertEnvironmentKey` is the §8.2 step 7 write inside the create transaction. The handler in Plan 03-08 owns the LiteLLM compensation orchestration (revoke the LiteLLM-side key on INSERT failure per D-12 step 7).
- **Plan 03-09 (admin handlers) READY:** `ListPersonalKeysByOwner`, `ListEnvironmentKeysByOwnerWithFilter` (admin variant with nil filter), `RevokePersonalKey`, `RevokeEnvironmentKey` are all in place.
- **Plan 03-10 (hydrate handler):** No DB-side dependency from this plan; the hydrate response is built from the informer cache + Environment CR spec.
- **Phase 4 Forwarder:** `PkCheckAndExtend` + `EkResolve` are the SAME helpers the Forwarder will use on the runtime path; no Forwarder-specific resolution code needed.
- **Phase 5 Content Service:** Identical reuse — `PkCheckAndExtend` is the §15.6 auth predicate.
- **Phase 4+ orphan loop:** `ListActiveACHKeyTokens` is ready to be wired in once `litellm_token` populations land via Plan 03-07 + 03-08. The Phase 2 `ListActiveACHKeyIDs` continues to work in the meantime; the consumer swap is a one-line change in `internal/orphan/runnable.go`.
- **No blockers introduced.** The Phase 1 manifest gap (placeholder `ACH_DB_URL` pointing at `ach-postgres.system.svc.cluster.local` without a Postgres Deployment in `config/default/`) noted in STATE.md is orthogonal to this plan's pure-SQL-helper scope.

---

*Phase: 03-hub-identity-platform-api*
*Plan: 03-03*
*Completed: 2026-05-20*
