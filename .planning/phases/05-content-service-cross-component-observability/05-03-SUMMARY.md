---
phase: 05-content-service-cross-component-observability
plan: 03
subsystem: cache
tags: [redis, cache, singleflight, miniredis, envcache, go]

requires:
  - phase: 03-keys-resolver-cache
    provides: keystore.redisCachedTeamsResolver / keystore.redisCachedResolver patterns (singleflight + Redis read-through + 60s TTL + fall-through-on-malformed) — envcache mirrors them verbatim per D-07
  - phase: 05-content-service-cross-component-observability
    provides: 05-02 internal/db.EnvironmentRow (loader source)
provides:
  - internal/contentservice/envcache package — Redis-backed read-through cache for Environment projection rows (D-07)
  - Cache interface (Get(ctx, ns, name) -> (*EnvRow, error)) + EnvRow JSON payload (subset of db.EnvironmentRow)
  - NewCachedEnvCache constructor — singleflight + 60s TTL + best-effort writes + fall-through on malformed/Redis-down
  - 9 stdlib unit tests covering all D-07 invariants (hit, miss-hydrate, nil-row no-cache, loader-error, malformed cache fall-through, Redis-down fall-through, singleflight dedup, nil-loader/nil-redis constructor rejection)
affects: [05-05, 05-06]

tech-stack:
  added: []
  patterns:
    - "Per-package EnvRow payload type (subset of db row) — cache wire-format isolated from full schema changes"
    - "Singleflight + Redis read-through with cacheTTL=60s — mirrors keystore.redisCachedTeamsResolver per D-07"
    - "Fall-through (no DEL) on malformed cache entries — next successful Set overwrites; 60s TTL bounds worst case"

key-files:
  created:
    - internal/contentservice/envcache/cache.go
    - internal/contentservice/envcache/doc.go
    - internal/contentservice/envcache/cache_test.go

key-decisions:
  - "Local EnvRow payload type (not db.EnvironmentRow re-export) — protects Redis wire format from db package schema churn"
  - "(nil, nil) loader result NOT cached — a future reconcile can create the row immediately, no negative TTL"
  - "Loader error returns (nil, err) without caching — caller (05-05 pipeline) maps to 500/503"
  - "Malformed cache entries fall through to loader without DEL — minimizes Redis ops on cache poisoning; next successful Set overwrites"

patterns-established:
  - "envcache.Cache interface + redisCachedEnvCache implementation — Content Service pipeline types its dependency as the interface, never the concrete"
  - "miniredis + go-redis client in unit tests — TTL assertion via mr.TTL(key), close mr to simulate Redis-down"

requirements-completed: [CS-03, CS-04]

duration: ~35min
completed: 2026-05-27
---

# Phase 05: Plan 03 — Envcache Summary

**Redis-backed read-through cache (60s TTL + singleflight) for Environment projection rows; consumed per request by the Content Service pipeline**

## Performance

- **Duration:** ~35 min (executor Task 1 partial + orchestrator Task 1 commit + Task 2 + SUMMARY inline)
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments
- `internal/contentservice/envcache/cache.go` — Cache interface, EnvRow JSON payload, redisCachedEnvCache implementation, NewCachedEnvCache constructor; singleflight + 60s TTL per D-07
- `internal/contentservice/envcache/doc.go` — package-level documentation citing D-07 + 60s cache budget rationale + key shape
- `internal/contentservice/envcache/cache_test.go` — 9 stdlib unit tests; all PASS under `-race`

## Task Commits

Each task was committed atomically:

1. **Task 1: cache interface + Redis-backed implementation + constructor** — `8c5a411` (feat) [committed by orchestrator after executor 529]
2. **Task 2: unit tests** — `6f5ee11` (test) [orchestrator inline]

## Files Created/Modified
- `internal/contentservice/envcache/cache.go` (231 lines) — Cache + redisCachedEnvCache with Get flow
- `internal/contentservice/envcache/doc.go` (64 lines) — package doc
- `internal/contentservice/envcache/cache_test.go` (266 lines) — 9 test funcs

## Decisions Made
- None beyond plan — followed PATTERNS.md envcache section.

## Deviations from Plan

None — plan executed as written.

## Issues Encountered

- Executor terminated with `API Error: 529 Overloaded` after staging cache.go + doc.go but BEFORE committing them. Orchestrator committed Task 1 inline, then wrote and committed Task 2 (cache_test.go) inline. All 9 tests PASS under `-race`.

## Next Phase Readiness

- **05-05 CS handler** can now construct `NewCachedEnvCache(loaderClosure, rdb)` and inject `envcache.Cache` into the authz pipeline.
- **05-06 service wire-up** has the envcache constructor ready for the content-service Deps assembly.

---
*Phase: 05-content-service-cross-component-observability*
*Completed: 2026-05-27*
