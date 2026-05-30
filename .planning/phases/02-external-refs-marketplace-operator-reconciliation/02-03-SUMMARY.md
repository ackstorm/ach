---
phase: 02-external-refs-marketplace-operator-reconciliation
plan: 03
subsystem: database
tags: [postgres, pgx, sql, migration, golang-migrate, testcontainers, external-refs, marketplace, orphan-cleanup]

# Dependency graph
requires:
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: |
      internal/db.Open / Migrate + Phase 1 migration 000001 (the four base
      tables this plan ALTERs); db.go pgx/v5 + golang-migrate conventions;
      testcontainers-go integration-test scaffold (//go:build integration);
      Phase 1 environment_controller.go classifyDrainErr pgconn 08/57
      transient-classification idiom (mirrored as isTransientPgErr here).
provides:
  - "Migration 000002: six columns added across four tables (litellm_user_id, upstream_rev, force_refresh_requested_at)"
  - "db.ExternalRef struct + UpsertExternalRef, GetExternalRef, ResetExternalRefRefreshOnEmptyCache, DeleteExternalRef"
  - "db.MarketplacePlugin struct + UpsertMarketplacePlugin, ListMarketplacePlugins, DeleteMarketplacePlugin, ResetMarketplacePluginsRefreshOnEmptyCache"
  - "db.ListACHManagedLitellmUsers — DISTINCT union over active personal_keys + environment_keys litellm_user_id"
  - "db.isTransientPgErr — pgconn class 08/57 classifier (package-private, reused across helpers)"
  - "17 integration tests covering UPSERT idempotency, force-refresh clear semantic, NULL/empty/inactive filtering"
affects:
  - 02-05 (Plugin/Prompt/Artifact reconcilers) — calls UpsertExternalRef on rename(2) success; GetExternalRef for prior UpstreamRev; DeleteExternalRef on finalizer cleanup
  - 02-06 (PluginMarketplace reconciler) — calls UpsertMarketplacePlugin (Stage-2), ListMarketplacePlugins + DeleteMarketplacePlugin (Stage-3 sweep)
  - 02-08 (Orphan cleanup Runnable) — iterates ListACHManagedLitellmUsers
  - 02-09 (cmd/operator wire-up) — calls ResetExternalRefRefreshOnEmptyCache + ResetMarketplacePluginsRefreshOnEmptyCache on empty-PVC startup branch
  - 03-* (Platform API) — first writer of force_refresh_requested_at (D-07); first writer of personal_keys.litellm_user_id on SSO landing

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Parameterized SQL via $N binds — zero fmt.Sprintf into query strings (T-02-03-01)"
    - "pgconn class 08/57 → raw err return (transient); other errors wrap with non-secret CR identifiers (T-02-03-03)"
    - "ON CONFLICT DO UPDATE that force-clears force_refresh_requested_at = NULL in the same statement as last_successful_refresh write (D-07 forward-compat)"
    - "GetExternalRef returns (nil, nil) on pgx.ErrNoRows — absence is not an error; reconciler treats as 'first reconcile'"
    - "UNION + DISTINCT over status='active' rows with IS NOT NULL + <> '' filters for orphan-cleanup user enumeration"
    - "testcontainers-go shared helper (phase2_helpers_test.go) spawns a fresh postgres:16-alpine + applies migrations 000001+000002 per test for assertion isolation"

key-files:
  created:
    - db/migrations/000002_phase2.up.sql
    - db/migrations/000002_phase2.down.sql
    - internal/db/external_refs.go
    - internal/db/external_refs_test.go
    - internal/db/marketplace_plugins.go
    - internal/db/marketplace_plugins_test.go
    - internal/db/litellm_users.go
    - internal/db/litellm_users_test.go
    - internal/db/phase2_helpers_test.go
  modified: []

key-decisions:
  - "ON CONFLICT DO UPDATE binds force_refresh_requested_at = NULL via a literal in the SET clause (no $N) — Postgres treats this as a static value at every UPSERT, which is the D-07 semantic the reconcilers need (refresh-completed implies force-refresh-cleared)."
  - "GetExternalRef COALESCEs upstream_rev/last_successful_refresh/next_refresh_at into ''/epoch defaults so Scan into time.Time + string never NULL-fails; the absence semantic is communicated via (nil, nil) at the row level, not via field-level nullables."
  - "isTransientPgErr lifted from environment_controller.go (Phase 1 classifyDrainErr) as a package-private helper so all eight UPSERT/SELECT/UPDATE/DELETE call sites share one transient-classification policy."
  - "Shared phase2_helpers_test.go avoids touching Phase 1's db_test.go (which has its own inline TestOpenAndMigrate container) — keeps Phase 1's TestOpenAndMigrate atomic and lets Phase 2 tests own their setup independently."
  - "litellm_users.go SQL applies the IS NOT NULL filter inside each UNION arm AND the <> '' filter outside the UNION on the deduped column — two layers (Phase 2 schema invariant + pre-Phase-3 defense)."

patterns-established:
  - "Per-task atomic commits (feat with scope 02-03): one for migration, one for ExternalRef+MarketplacePlugin helpers, one for litellm_users — clean rollback per concern."
  - "Integration test file naming: <topic>_test.go with //go:build integration tag (Phase 1 carry-forward); shared setup helpers in phase2_helpers_test.go also tag-gated."
  - "Comment header on every new db helper file documenting (a) which reconciler calls it, (b) what column/PK it operates on, (c) the threat-mitigation discipline (T-02-03-01/02/03)."

requirements-completed:
  - OP-03
  - OP-11
  - OP-15

# Metrics
duration: ~12min
completed: 2026-05-17
---

# Phase 2 Plan 03: DB Layer for External-Refs + Marketplace Plugins + Orphan Cleanup Summary

**Six ALTER TABLE columns + eleven typed Go helpers backed by 17 testcontainers-go integration tests, all bound via parameterized $N SQL with zero fmt.Sprintf-into-query patterns.**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-05-17T07:34:00Z (approximate — agent spawned)
- **Completed:** 2026-05-17T07:46:00Z
- **Tasks:** 3 / 3
- **Files created:** 9 (2 migration + 3 helper sources + 3 integration test files + 1 shared test helper)
- **Files modified:** 0

## Accomplishments

- **Migration 000002** ALTERs four Phase-1 tables (personal_keys, environment_keys, external_refs, marketplace_plugins) with six new columns, all using `ADD COLUMN IF NOT EXISTS` for defense-in-depth re-apply safety. Reverse companion drops the six columns symmetrically.
- **internal/db/external_refs.go** (4 exported helpers + struct): UpsertExternalRef force-clears `force_refresh_requested_at` per D-07; GetExternalRef returns `(nil, nil)` on absence; ResetExternalRefRefreshOnEmptyCache wires OP-11 PVC-loss recovery; DeleteExternalRef keeps row counts aligned with live CRs.
- **internal/db/marketplace_plugins.go** (4 exported helpers + struct): parallel surface for the three-stage marketplace refresh (Plan 02-06).
- **internal/db/litellm_users.go** (1 exported helper): UNION + DISTINCT query over active rows in both key tables; defends against NULL + empty `litellm_user_id` (Phase 2 schema invariant + pre-Phase-3 defense).
- **17 integration tests** (5 ExternalRef + 6 MarketplacePlugin + 5 LiteLLM users + 1 implicit migration-apply via shared helper) pass under `make test-integration` in 34.4 seconds against postgres:16-alpine.

## Task Commits

Each task was committed atomically:

1. **Task 1: Migration 000002 (up + down)** — `2db9899` (feat)
   - 6 `ALTER TABLE … ADD COLUMN IF NOT EXISTS` statements (.up)
   - 6 matching `ALTER TABLE … DROP COLUMN IF EXISTS` statements (.down)
2. **Task 2: ExternalRef + MarketplacePlugin helpers + tests** — `79448c5` (feat)
   - external_refs.go (4 exported helpers + ExternalRef struct + isTransientPgErr)
   - marketplace_plugins.go (4 exported helpers + MarketplacePlugin struct)
   - phase2_helpers_test.go (shared setupPostgresForPhase2)
   - external_refs_test.go (6 tests), marketplace_plugins_test.go (6 tests)
3. **Task 3: ListACHManagedLitellmUsers + tests** — `99b898e` (feat)
   - litellm_users.go (UNION + DISTINCT query)
   - litellm_users_test.go (5 tests)

**Plan metadata commit (this SUMMARY):** appended after self-check.

## Files Created/Modified

### db/migrations/

- **`000002_phase2.up.sql`** — six `ALTER TABLE … ADD COLUMN IF NOT EXISTS` statements:
  - `personal_keys.litellm_user_id text` (OP-15 D-16; nullable in Phase 2)
  - `environment_keys.litellm_user_id text` (parity column)
  - `external_refs.upstream_rev text` (OP-03 — fetcher UpstreamRev for conditional-GET)
  - `marketplace_plugins.upstream_rev text`
  - `external_refs.force_refresh_requested_at timestamptz` (D-07 forward-compat)
  - `marketplace_plugins.force_refresh_requested_at timestamptz`
- **`000002_phase2.down.sql`** — six `DROP COLUMN IF EXISTS` mirroring the up file.

### internal/db/

- **`external_refs.go`** — `ExternalRef` struct + four free functions:
  - `UpsertExternalRef(ctx, pool, r ExternalRef) error`
  - `GetExternalRef(ctx, pool, kind, name) (*ExternalRef, error)`
  - `ResetExternalRefRefreshOnEmptyCache(ctx, pool) error`
  - `DeleteExternalRef(ctx, pool, kind, name) error`
  - Package-private `isTransientPgErr(err) bool` (shared with marketplace_plugins.go)
- **`marketplace_plugins.go`** — `MarketplacePlugin` struct + four free functions:
  - `UpsertMarketplacePlugin(ctx, pool, p MarketplacePlugin) error`
  - `ListMarketplacePlugins(ctx, pool, marketplaceName) ([]MarketplacePlugin, error)`
  - `DeleteMarketplacePlugin(ctx, pool, marketplaceName, name) error`
  - `ResetMarketplacePluginsRefreshOnEmptyCache(ctx, pool) error`
- **`litellm_users.go`** — one free function:
  - `ListACHManagedLitellmUsers(ctx, pool) ([]string, error)` (UNION + DISTINCT)
- **`phase2_helpers_test.go`** (`//go:build integration`) — `setupPostgresForPhase2(t, ctx)` spawns postgres:16-alpine via testcontainers-go, applies 000001 + 000002, returns `(*pgxpool.Pool, cleanup)`.
- **`external_refs_test.go`** (`//go:build integration`) — 6 tests:
  - TestUpsertExternalRef_Insert / Update / ClearsForceRefresh
  - TestGetExternalRef_Absent
  - TestResetExternalRefRefreshOnEmptyCache
  - TestDeleteExternalRef
- **`marketplace_plugins_test.go`** (`//go:build integration`) — 6 tests:
  - TestUpsertMarketplacePlugin_Insert / Update / ClearsForceRefresh
  - TestListMarketplacePlugins (multi-row with cross-marketplace exclusion + empty-marketplace nil-vs-empty check)
  - TestDeleteMarketplacePlugin
  - TestResetMarketplacePluginsRefreshOnEmptyCache
- **`litellm_users_test.go`** (`//go:build integration`) — 5 tests:
  - TestListACHManagedLitellmUsers_Empty
  - TestListACHManagedLitellmUsers_PersonalKeysOnly
  - TestListACHManagedLitellmUsers_Dedup (UNION dedupes shared user-id across tables)
  - TestListACHManagedLitellmUsers_ExcludesInactive (status revoked/expired filtered on both tables)
  - TestListACHManagedLitellmUsers_ExcludesNullAndEmpty (NULL + '' filtered)
  - Plus `mustExec` helper that respects Phase 1 CHECK constraints (`pkid_` / `ekid_` prefix, status enum, distinct credential_hash per row).

## Integration test fixture rows (PHASE-1 CHECK-constraint reference)

The Phase 1 schema enforces:
- `personal_keys.key_id LIKE 'pkid_%'` and `environment_keys.key_id LIKE 'ekid_%'` — every test INSERT uses these prefixes.
- `personal_keys.status IN ('active','revoked','expired')` and `environment_keys.status IN ('active','revoked')` — TestListACHManagedLitellmUsers_ExcludesInactive seeds both `revoked` (on each table) and `expired` (personal_keys only).
- `credential_hash UNIQUE` per table — each seed row uses a distinct `h_*` literal.
- `expires_at NOT NULL` on personal_keys — every insert sets `now() + interval '1 hour'`.

## Decisions Made

- **`force_refresh_requested_at = NULL` is a LITERAL in the ON CONFLICT SET clause**, not a $N bind. The D-07 contract is "every successful UPSERT clears the marker" — there is no caller-provided value to bind. This makes the column's clear-on-refresh semantic visible at SQL-read time and avoids an unused method parameter on the helper signatures.
- **`GetExternalRef` COALESCEs nullable columns at read time** rather than scanning into `sql.NullString`/`sql.NullTime`. The `(nil, nil)` row-level absence return obviates field-level nullability; callers that need to distinguish "row exists but never refreshed" from "row absent" check whether the returned `*ExternalRef` is nil.
- **`isTransientPgErr` is package-private**, not exported. Its sole consumers are the eight UPSERT/SELECT/UPDATE/DELETE call sites inside this package; exporting it would invite distant callers to make different transient-classification decisions and drift from the Phase 1 `classifyDrainErr` policy in environment_controller.go.
- **`phase2_helpers_test.go` does NOT extract a method from Phase 1's `db_test.go`.** Phase 1's `TestOpenAndMigrate` has its own inline container spin-up that doubles as a schema-invariant assertion (CHECK constraints, UNIQUE on credential_hash); refactoring it to use the shared helper would weaken those assertions. Phase 2 tests own a fresh helper.
- **One container per test** in Phase 2 (vs Phase 1's one-container-per-package). Trade-off: ~3-5s startup × 17 tests = bounded wall-clock cost (34.4s total). Benefit: every test is order-independent, can be `-run` filtered without considering prior side effects. Acceptable given Phase 2 test set size; revisit if test count grows past ~30 per package.

## Deviations from Plan

**None — plan executed exactly as written.**

The plan's `<action>` blocks were followed precisely:
- Migration uses the exact six ALTER statements specified, in the specified order, with the IF NOT EXISTS suffix per the plan's defense-in-depth note.
- Helper signatures match the `<must_haves>.artifacts.contains` list character-for-character.
- Test names match the plan's enumerated names (e.g., `TestUpsertExternalRef_ClearsForceRefresh`, `TestListACHManagedLitellmUsers_Dedup`).
- SQL discipline (T-02-03-01 / 02 / 03 in the threat model) is preserved: zero string concatenation of bind values; error wrappers carry only non-secret CR identifiers (Kind/Name/MarketplaceName); errors propagate via `%w` so callers' `errors.As`/`errors.Is` keep working.

A linter normalization (`go fmt` invoked transitively by `make test-integration`) updated two ASCII apostrophe characters in doc-comments to typographic apostrophes (`'` → `’`); this is cosmetic and was kept.

## Issues Encountered

- **`mustExec` in `litellm_users_test.go` initially declared a structurally-typed interface parameter referencing an undefined `pgconnCommandTag` type.** Caught immediately during pre-test review; fixed by importing `pgxpool` and accepting `*pgxpool.Pool` directly. No commit churn — fix landed before the Task 3 commit.

## User Setup Required

None — no external service configuration required. All work is internal Go package surface + DB migration.

## Next Phase Readiness

- **Plan 02-05 (Plugin/Prompt/Artifact reconcilers)** has the four helpers it needs: `UpsertExternalRef` after rename(2), `GetExternalRef` for prior `UpstreamRev`, `DeleteExternalRef` on finalizer cleanup, and (transitively via reconciler-author choice) `ResetExternalRefRefreshOnEmptyCache` is wired by Plan 02-09 startup branch — Plan 02-05 just consumes the read/write surface.
- **Plan 02-06 (PluginMarketplace reconciler)** has the four helpers it needs for the three-stage refresh: `UpsertMarketplacePlugin` in Stage-2, `ListMarketplacePlugins` + `DeleteMarketplacePlugin` in the Stage-3 vanish sweep, `ResetMarketplacePluginsRefreshOnEmptyCache` wired by Plan 02-09.
- **Plan 02-08 (Orphan cleanup Runnable)** has `ListACHManagedLitellmUsers` — note the Phase 2 invariant from the helper godoc: Phase 2 code never writes `litellm_user_id`, so the helper returns `[]` every tick until Phase 3 lands the SSO write path. The Plan 02-08 Runnable will exercise the empty-set steady-state, then naturally pick up real values when Phase 3 ships.
- **Plan 02-09 (cmd/operator/main.go wire-up)** has both reset helpers ready to call. The expected wiring sequence in 02-09 is:
  ```go
  empty, _ := cachefs.IsEmpty(cacheRoot)  // Plan 02-09 also adds IsEmpty
  if empty {
      if err := db.ResetExternalRefRefreshOnEmptyCache(ctx, dbPool); err != nil { os.Exit(1) }
      if err := db.ResetMarketplacePluginsRefreshOnEmptyCache(ctx, dbPool); err != nil { os.Exit(1) }
      setupLog.Info("PVC was empty on startup — last_successful_refresh reset on external_refs + marketplace_plugins")
  }
  ```
- **Plan 03-* (Platform API)** is the first writer of `personal_keys.litellm_user_id` (SSO landing) and `force_refresh_requested_at` (force-refresh annotation patch). Both columns exist now — Phase 3 ships without a second migration round-trip.

## Self-Check: PASSED

All 9 created files present at expected paths. All 3 task commits (`2db9899`, `79448c5`, `99b898e`) present on the worktree branch. `go build ./internal/db/...` clean. `go vet ./internal/db/...` clean (both with and without `-tags=integration`). Zero `fmt.Sprintf`-into-SQL patterns across all three new helper files. 17 integration tests pass under `make test-integration` (34.4s wall clock).

---
*Phase: 02-external-refs-marketplace-operator-reconciliation*
*Plan: 02-03*
*Completed: 2026-05-17*
