---
phase: 02-external-refs-marketplace-operator-reconciliation
plan: 04
subsystem: infra
tags: [slog, log/slog, audit, cachefs, stdlib, OP-11, OP-15, D-17, D-18, Hub-10.3, Hub-18.4]

# Dependency graph
requires:
  - phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
    provides: internal/cachefs.EnsureLayout, SubDirs (.tmp), ErrCacheRootMissing, doc.go no-logger discipline reference from internal/credhash
provides:
  - internal/audit package with NewLogger(io.Writer) → *slog.Logger and top-level audit=true predicate attribute (D-17)
  - internal/cachefs.IsEmpty(root) (bool, error) — OP-11 empty-cache-root predicate that exempts .tmp/ from the emptiness determination
  - internal/cachefs.SweepTmp(root, maxAge) error — Hub §10.3 orphan staging-file sweep helper, race-safe against concurrent reconciler rename(2)
affects:
  - Plan 02-08 (orphan-cleanup Runnable — calls audit.NewLogger to emit Hub §18.4 revocation events per D-18)
  - Plan 02-09 (cmd/operator/main.go — calls cachefs.IsEmpty to drive ResetExternalRefRefreshOnEmptyCache + Reset…MarketplacePlugins…; may register a 1h SweepTmp Runnable per CONTEXT.md Claude's discretion bullet)

# Tech tracking
tech-stack:
  added: []  # stdlib-only by design — zero new go.mod entries
  patterns:
    - "Dedicated slog.Handler with a top-level predicate attribute (audit=true) for cheap downstream filtering without a second log destination"
    - "Stdlib-only package discipline: package docstring enumerates forbidden imports (mirrors internal/credhash)"
    - "Best-effort filesystem sweep with race tolerance: ignore ErrNotExist from per-entry Remove; never propagate partial failures from the sweep loop"

key-files:
  created:
    - internal/audit/handler.go
    - internal/audit/doc.go
    - internal/audit/handler_test.go
    - internal/cachefs/sweep.go
    - internal/cachefs/sweep_test.go
  modified: []

key-decisions:
  - "audit handler is stdlib-only (io + log/slog); doc.go enumerates the forbidden-import set (fmt, log, k8s.io, sigs.k8s.io) explicitly — discipline over scrubbing per [feedback_litellm_operator_no_redaction_filter]"
  - "audit.NewLogger pins level to slog.LevelInfo: audit is high-signal, Debug is dropped (D-17)"
  - "IsEmpty treats stray non-.tmp top-level files defensively as 'populated' (returns false on subdir ReadDir errors) — errs toward NOT triggering the OP-11 thundering-herd refresh (threat T-02-04-05)"
  - "SweepTmp returns nil when .tmp/ does not exist (benign — operator's hourly Runnable may sweep before EnsureLayout has run on a freshly-mounted PVC)"
  - "SweepTmp races benignly with reconciler rename(2): per-entry Remove ignores ErrNotExist, per-entry Info ignores file-disappeared errors, sweep loop never aborts on single-entry failure (threat T-02-04-03 mitigation)"
  - "handler_test.go and sweep_test.go use external _test package (audit_test, cachefs_test) for black-box testing; consistent with bootstrap_test.go's discipline"

patterns-established:
  - "Pattern 1: Predicate-attribute audit logger — single slog.Logger wrapping JSONHandler with .With(slog.Bool('audit', true)) so every record carries the discriminator; reusable across Phase 3 for pk_/ek_ lifecycle events without refactor"
  - "Pattern 2: Defensive 'populated' predicate — IsEmpty errs toward preserving reconciler data; never reports empty on uncertain input. Future cache-shape predicates should mirror this disposition"
  - "Pattern 3: Hourly best-effort sweep — SweepTmp's per-entry error-tolerance and 'nil if .tmp/ absent' shape is the template for any future periodic-cleanup helper on PVC paths"

requirements-completed:
  - OP-11
  - OP-15

# Metrics
duration: 4min
completed: 2026-05-17
---

# Phase 02 Plan 04: Audit Logger + Cache Sweep Helpers Summary

**Stdlib-only audit slog handler emitting JSON with a top-level audit=true predicate (D-17) plus two cachefs helpers (IsEmpty for OP-11 cache-loss recovery; SweepTmp for Hub §10.3 orphan staging-file cleanup) — both consumers (Plan 02-08, Plan 02-09) now have load-bearing infrastructure they can wire without further refactor.**

## Performance

- **Duration:** ~4 min (excluding initial Docker image build for ach-devtools:latest, which was a one-off cold start)
- **Started:** 2026-05-17T07:36:00Z (approx)
- **Completed:** 2026-05-17T07:40:22Z
- **Tasks:** 2
- **Files modified:** 5 created, 0 modified (Phase 1 bootstrap.go / bootstrap_test.go / doc.go untouched as required)

## Accomplishments

- Landed `internal/audit` package: net-new Phase 2 infrastructure with the dedicated stdout-JSON `slog.Handler` shape Phase 3 will reuse verbatim for pk_/ek_ events. Every record carries `audit=true` at the top level so log shippers (fluent-bit, Loki) can split via the predicate without a second log destination.
- Added `cachefs.IsEmpty` + `cachefs.SweepTmp` alongside Phase 1's `EnsureLayout` — the two helpers consumers in Plan 02-08 (orphan-cleanup Runnable) and Plan 02-09 (cmd/operator/main.go startup branch + optional sweep Runnable) need.
- 14 new unit tests added (5 audit + 9 cachefs/sweep), all pass; `go vet` clean; stdlib-only invariant preserved in both packages.

## Task Commits

Each task was committed atomically:

1. **Task 1: internal/audit handler + doc + tests (D-17)** — `c6f6302` (feat)
2. **Task 2: cachefs.IsEmpty + SweepTmp + tests (OP-11, §10.3)** — `7099886` (feat)

## Files Created/Modified

- `internal/audit/handler.go` — `NewLogger(io.Writer) *slog.Logger`; JSONHandler wired with a top-level `audit=true` attribute via `.With(slog.Bool("audit", true))`; level pinned to `slog.LevelInfo`.
- `internal/audit/doc.go` — Package docstring documenting D-17 contract, forbidden imports (fmt, log, k8s.io, sigs.k8s.io), Phase 3 reuse intention, audit-safety contract referencing `[feedback_litellm_operator_no_redaction_filter]`.
- `internal/audit/handler_test.go` — Five tests: `TestNewLogger_EmitsAuditTrue` (audit=true present as bool, not string), `TestNewLogger_PreservesUserAttrs` (D-18 event shape round-trips), `TestNewLogger_LevelFiltering` (Debug dropped, Info emitted), `TestNewLogger_MultipleEntries` (three Info calls → three newline-separated JSON objects), `TestNewLogger_AcceptsIoDiscard` (constructor accepts io.Discard without panic).
- `internal/cachefs/sweep.go` — Two exported helpers: `IsEmpty(root) (bool, error)` and `SweepTmp(root, maxAge) error`. Stdlib-only (`errors`, `io/fs`, `os`, `path/filepath`, `time`); defensive predicate on subdir ReadDir errors; benign-absent-.tmp/ for SweepTmp; per-entry race-tolerance for Remove and Info.
- `internal/cachefs/sweep_test.go` — Nine tests covering IsEmpty (fresh layout, single populated subdir, only-.tmp populated, missing root, root-is-file, empty-string root) and SweepTmp (removes old files / retains fresh, missing .tmp dir benign, concurrent-Remove race tolerance).

## Decisions Made

- **External `_test` packages for both new test files** (`audit_test`, `cachefs_test`). The plan's Task 1 `<action>` mentioned "internal test for access to NewLogger", but `NewLogger` is the only exported identifier and the test exercises it through the exported surface. External-test-package keeps the test posture identical to Phase 1's `bootstrap_test.go` (which uses `cachefs_test`), giving a single black-box convention across the repo. No tests required private access, so no in-package test file was needed.
- **`slog.LevelInfo` floor on the audit handler.** D-17 doesn't pin a level; the implementation picks Info because audit is intentionally high-signal (Hub §18.4 events) and Debug records would dilute the channel. Captured in the `TestNewLogger_LevelFiltering` test as a contract.
- **IsEmpty includes an `EmptyString` test case + guard** beyond the plan's enumerated edge cases — mirrors the bootstrap.go discipline (which also short-circuits empty root → ErrCacheRootMissing). Added test `TestIsEmpty_EmptyString` covers it. Counts as a Rule 2 (auto-add missing critical) addition: without the guard, IsEmpty("") would Stat("") which has platform-specific behavior (typically ENOENT, but not guaranteed). Rather than rely on Stat's error path, the explicit empty-string guard matches the bootstrap.go pattern and produces the documented sentinel.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Added explicit empty-string guard to IsEmpty**
- **Found during:** Task 2 (sweep.go implementation)
- **Issue:** Plan's `<action>` listed three edge cases for IsEmpty (missing root, root-is-file, .tmp/-only) but did not call out the empty-string root. `bootstrap.go`'s `EnsureLayout` has this guard explicitly (`if root == "" { return ErrCacheRootMissing }`); IsEmpty should match for consistency and to avoid platform-specific Stat("") behavior leaking through as a non-deterministic error.
- **Fix:** Added `if root == "" { return false, ErrCacheRootMissing }` as the first statement in IsEmpty; added matching `TestIsEmpty_EmptyString` test.
- **Files modified:** `internal/cachefs/sweep.go`, `internal/cachefs/sweep_test.go`
- **Verification:** `TestIsEmpty_EmptyString` passes; verifies `errors.Is(err, ErrCacheRootMissing)` and `empty == false`.
- **Committed in:** `7099886` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 missing critical)
**Impact on plan:** Adds one test case and a one-line guard; brings IsEmpty into surface-shape parity with EnsureLayout. No scope creep — the helper is still 2 functions in 1 file as planned.

## Issues Encountered

- **First-run cold start:** the `./scripts/dev.sh` wrapper needed to build the `ach-devtools:latest` Docker image on first invocation (no Go toolchain on host per project convention). This added a one-time ~2 min image build but is now cached for subsequent runs. Not a plan issue — expected per `01-CONTEXT.md` "Containerized toolchain" pattern.

## Threat Model Coverage

All five threats from the plan's `<threat_model>` section have implementation hooks:

- **T-02-04-01** (audit info disclosure via caller-passed plaintext) — `accept`: handler emits raw per D-17; doc.go documents the caller-discipline requirement explicitly.
- **T-02-04-02** (audit JSON line truncation > 1 KiB) — `accept`: Phase 2 events are <300 bytes per D-18.
- **T-02-04-03** (SweepTmp race with reconciler rename) — `mitigate`: per-entry `Info`/`Remove` error tolerance + sweep loop never aborts on single-entry failure. `TestSweepTmp_RaceTolerance` asserts the property.
- **T-02-04-04** (IsEmpty triggering thundering-herd OP-11 reset) — `accept`: that's the intended OP-11 behavior; controller-runtime workqueue rate limiting bounds the actual upstream call rate.
- **T-02-04-05** (stray non-.tmp top-level entry confusing IsEmpty) — `mitigate`: defensive return-false on subdir ReadDir errors. `TestIsEmpty_OnePluginFile` asserts the positive case; the defensive subdir-ReadDir-fails branch is documented in `sweep.go`'s godoc and code comments.

## User Setup Required

None — no external service configuration required. The audit handler writes to a caller-provided `io.Writer` (production: `os.Stdout`, wired by Plan 02-09 in `cmd/operator/main.go`); the cachefs helpers operate on a caller-provided root path (also Plan 02-09's concern).

## Next Plan Readiness

- **Plan 02-08 (orphan-cleanup Runnable)**: can now call `audit.NewLogger(os.Stdout)` directly in `cmd/operator/main.go` and pass the resulting `*slog.Logger` into `orphan.NewRunnable`. The D-18 event shape `audit.NewLogger(os.Stdout).Info("operator.orphan-cleanup", "target.kind", "litellm_key", "target.name", keyID, "outcome", "success")` round-trips through `TestNewLogger_PreservesUserAttrs`.
- **Plan 02-09 (operator main)**: can call `cachefs.IsEmpty(cacheRoot)` immediately after `cachefs.EnsureLayout` to drive the OP-11 reset branch. The forward-reference call shape in this plan's `<interfaces>` block compiles 1:1 against the helper signature. Optional 1h `SweepTmp` Runnable is implementable as a thin `manager.Runnable` wrapper.

## Self-Check

- **Created files exist:**
  - `internal/audit/handler.go` — FOUND
  - `internal/audit/doc.go` — FOUND
  - `internal/audit/handler_test.go` — FOUND
  - `internal/cachefs/sweep.go` — FOUND
  - `internal/cachefs/sweep_test.go` — FOUND
- **Commits exist on branch `worktree-agent-a87b744c399d01bf3`:**
  - `c6f6302` (Task 1 audit) — FOUND
  - `7099886` (Task 2 cachefs sweep) — FOUND
- **Tests pass:** 5/5 audit + 9/9 cachefs sweep (16/16 cachefs total including 7 pre-existing) = 14 new tests + 7 prior, all green.
- **`go vet` clean** on both packages.
- **Phase 1 untouched:** `git diff HEAD internal/cachefs/bootstrap.go internal/cachefs/bootstrap_test.go internal/cachefs/doc.go` returns no diff.

## Self-Check: PASSED

---
*Phase: 02-external-refs-marketplace-operator-reconciliation*
*Completed: 2026-05-17*
