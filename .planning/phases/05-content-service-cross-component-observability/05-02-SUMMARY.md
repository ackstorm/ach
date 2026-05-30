---
phase: 05-content-service-cross-component-observability
plan: 02
subsystem: database
tags: [postgres, projection, migration, db, plugin-precedence]

requires:
  - phase: 02-external-refs-marketplace-operator-reconciliation
    provides: db/migrations/ baseline (000001-000003); internal/db helper patterns; testcontainers Phase 2 setup helper
provides:
  - db/migrations/000004_cs_projection.up/down.sql — environments, plugins, prompts, artifacts projection tables (spec v4 §5.2 reversal)
  - internal/db CRUD helpers for all 4 projection kinds (UpsertX, GetXByName, SoftDeleteX, DeleteX) — backing CS-01..CS-09
  - internal/db/plugins.go ResolvePluginByName CTE implementing §12.3 precedence (CRD-row wins; otherwise alphabetically-lowest marketplace_plugins.marketplace_name)
  - Integration test suite: 4 standard CRUD tests per kind (16) + 5 ResolvePluginByName precedence cases (CRDWins, MarketplaceFallback, AlphabeticallyLowestMarketplace, NoMatch_NilNil, SoftDeletedCRDFallsThrough) — testcontainers-gated under //go:build integration
affects: [05-03, 05-04, 05-05, 05-06]

tech-stack:
  added: []
  patterns:
    - "Per-kind projection table layered above CRD resources — Operator writes both K8s status AND Postgres row in same reconciler"
    - "SoftDelete keeps in-flight reads working (CS-09 grace window); hard Delete removes only after finalizer drain"
    - "Plugin resolution via single CTE — CRD branch joined with marketplace branch; ORDER BY plugin_source DESC, marketplace_name ASC; first row wins"

key-files:
  created:
    - db/migrations/000004_cs_projection.up.sql
    - db/migrations/000004_cs_projection.down.sql
    - internal/db/environments.go
    - internal/db/plugins.go
    - internal/db/prompts.go
    - internal/db/artifacts.go
    - internal/db/environments_test.go
    - internal/db/plugins_test.go
    - internal/db/prompts_test.go
    - internal/db/artifacts_test.go

key-decisions:
  - "Spec v4 §5.2 reversal: projection tables live in Postgres, not K8s status (CRDs remain authoritative source of truth, Postgres is denormalized read cache for CS)"
  - "ResolvePluginByName precedence in a single CTE — UNION ALL of CRD branch (filtered by ns+name+deletion_timestamp IS NULL) and marketplace branch (filtered by name only); resolution ORDER BY plugin_source DESC then marketplace_name ASC; LIMIT 1"
  - "SoftDelete sets deletion_timestamp but keeps the row; CS reads filter on deletion_timestamp IS NULL — guarantees CS-09 grace window during finalizer drain"
  - "isTransientPgErr helper centralizes pgconn class 08 (Connection Exception) / 57 (Operator Intervention) retry classification — shared across all CRUD helpers"

patterns-established:
  - "Per-kind XRow struct mirrors CRD spec subset; ResourceVersion + UpdatedAt fields round-trip on every Upsert; testcontainers integration tests gate via //go:build integration"
  - "Idempotent SoftDelete/Delete operations — re-calling on a soft-deleted or absent row succeeds without error"

requirements-completed: [CS-01, CS-02, CS-03, CS-04, CS-05, CS-06, CS-07, CS-08, CS-09]

duration: ~33min
completed: 2026-05-27
---

# Phase 05: Plan 02 — DB Projection Layer Summary

**Postgres projection layer (environments/plugins/prompts/artifacts) + ResolvePluginByName §12.3 precedence CTE backing the Content Service read path**

## Performance

- **Duration:** ~33 min executor wall time; +pre-commit retry by orchestrator
- **Tasks:** 4
- **Files modified:** 10

## Accomplishments
- `db/migrations/000004_cs_projection.up.sql` — 4 projection tables (environments, plugins, prompts, artifacts) with primary keys, foreign-key-free design (denormalized read cache), `deletion_timestamp` nullable for soft-delete
- `internal/db/{environments,plugins,prompts,artifacts}.go` — Upsert/Get/SoftDelete/Delete helpers per kind, with `isTransientPgErr` retry wrapping for transient pgconn classes
- `internal/db/plugins.go ResolvePluginByName` — single CTE implements §12.3 plugin precedence; CRD-namespace-keyed row wins; falls back to alphabetically-lowest marketplace_plugins row when no live CRD row matches
- 22 integration tests gated under `//go:build integration` — standard CRUD round-trip per kind (16 tests) + 5 ResolvePluginByName precedence cases + nil-check / boundary tests

## Task Commits

Each task was committed atomically:

1. **Task 1: migration 000004 up + down (4 projection tables)** — `2ef67bc` (feat)
2. **Task 2: CRUD helpers for environments/prompts/artifacts** — `647b427` (feat)
3. **Task 3: plugins CRUD + §12.3 ResolvePluginByName CTE** — `6bf866b` (feat) + `a8fb67a` (style: gofmt struct alignment fix)
4. **Task 4: testcontainers integration tests for all 5 helpers per kind + precedence cases** — `6f0fbf1` (test) [committed by orchestrator after executor 529]

## Files Created/Modified
- `db/migrations/000004_cs_projection.up.sql` — environments + plugins + prompts + artifacts projection tables
- `db/migrations/000004_cs_projection.down.sql` — inverse
- `internal/db/environments.go` — EnvironmentRow + UpsertEnvironment + GetEnvironmentByName + SoftDeleteEnvironment + DeleteEnvironment
- `internal/db/plugins.go` — PluginRow + 4 CRUD + ResolvePluginByName CTE (§12.3)
- `internal/db/prompts.go` — PromptRow + 4 CRUD
- `internal/db/artifacts.go` — ArtifactRow + 4 CRUD + scope CHECK constraint regression
- `internal/db/{environments,plugins,prompts,artifacts}_test.go` — 22 integration tests under `//go:build integration`

## Decisions Made

- Followed plan as written. Plugin precedence keyed via `plugin_source DESC` ordering rather than CASE expression — equivalent semantics with simpler EXPLAIN plan (single index scan per branch + sort).

## Deviations from Plan

None — plan executed as written.

## Issues Encountered

- Executor terminated mid-Task-4 with `API Error: 529 Overloaded` after creating the 4 test files but before committing them. Files were complete and well-formed (22 test funcs total); orchestrator staged + committed them inline after the agent return. Pre-commit hook caught a `go fmt` drift on first attempt — second commit attempt succeeded.

## Next Phase Readiness

- 05-03 envcache: ready to consume `db.GetEnvironmentByName` as its Loader closure
- 05-04 projection writes: Operator reconcilers now have their `UpsertX` / `SoftDeleteX` / `DeleteX` targets per kind
- 05-05 CS handler: `db.ResolvePluginByName` available for §12.3 plugin precedence on every plugin request

---
*Phase: 05-content-service-cross-component-observability*
*Completed: 2026-05-27*
