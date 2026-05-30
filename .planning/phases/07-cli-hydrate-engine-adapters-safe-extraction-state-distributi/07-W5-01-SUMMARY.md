---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W5-01
subsystem: cli/hydrate
tags: [gap-closure, di-seams, orchestrator-wiring, state-05, adapt-03, adapt-04, adapt-05, adapt-06, adapt-07]
requires:
  - 07-W3-05 (NewWiring constructor + extractorImpl + adapterDispatcherImpl + Sync)
  - 07-W1-06 (orchestrator commit.go scaffold, interface fields, maybeKill seams)
provides:
  - hydrate.Opts.Extractor + hydrate.Opts.AdapterDispatcher (public DI seams)
  - commit.run() steps 7-11 wired to real impls (no more W1 stub fall-through in production)
  - syncFn package-level seam (test-only — production default = Sync)
affects:
  - cmd/ach-cli/cmd/hydrate.go (runHydrateEngine threads NewWiring's returns into Opts)
  - internal/cli/hydrate/{flags,commit,commit_test}.go
tech-stack:
  added: []
  patterns:
    - interface-typed DI seams on Opts (nil → W1 stub fall-through preserved)
    - package-level function variable as test seam (syncFn) for non-method-interface mocking
    - *exit.CodedError pass-through via errors.As to preserve exit code 7 (CollisionRefuse)
key-files:
  created: []
  modified:
    - internal/cli/hydrate/flags.go
    - internal/cli/hydrate/commit.go
    - internal/cli/hydrate/commit_test.go
    - cmd/ach-cli/cmd/hydrate.go
decisions:
  - "syncFn = Sync as a package-level seam (instead of adding Sync() to the AdapterDispatcher interface): the interface contract owns adapter dispatch; Sync is an orchestrator-level inverse-merge primitive that does not belong on the adapter surface. A package-level var swap is the lightest-weight unit-test seam that does not pollute the public interface."
  - "newFile arg to Sync is existingState until step12 composition lands: passing the composed next-state would activate STATE-05 inverse-merge for real, but composition belongs in step12WriteState (see existing comment at commit.go:548-552) which is out of scope for this plan. existingState as newFile produces an empty set-difference → Sync is a wired no-op, which is the correct posture pending the composition follow-up."
  - "PlatformID assigned at run() entry, not at step 12: ensures the field round-trips on early-exit paths (lock contention, schema mismatch, etc.) so Result.PlatformID is observable in --verbose even when the engine bails out before step 12."
metrics:
  duration: ~22min
  completed_date: 2026-05-29
---

# Phase 7 Plan W5-01: Wire Extractor + AdapterDispatcher + Sync into the hydrate orchestrator — Summary

CR-02 gap closure: connect 07-W3-05's NewWiring constructor (which built `extractorImpl` + `adapterDispatcherImpl` + `Sync` to production quality but had no path to the orchestrator) to commit.go's run() so steps 7-11 of the §6.7 14-step commit sequence actually execute real impls in production. ~58 LOC added across 4 files; 6 new unit tests; all gates green.

## What landed

**Task 1 — DI seams on Opts** (commit `6aa46da`):
- `internal/cli/hydrate/flags.go`: added `Extractor Extractor` + `AdapterDispatcher AdapterDispatcher` fields on `Opts` under a new `// --- DI seams (07-W5-01 gap closure) ---` group. Both use existing interface types declared in `result.go` so no new imports were needed.
- `internal/cli/hydrate/commit.go`: `newCommit()` now assigns `extractor: opts.Extractor` and `adapter: opts.AdapterDispatcher` from the Opts. Nil values preserve the W1 stub fall-through for unit tests that don't inject impls.
- `internal/cli/hydrate/commit_test.go`: added reusable `fakeExtractor` + `fakeAdapterDispatcher` types (with optional `*int` call counters for invocation tracking) and the `TestNewCommit_PopulatesExtractorAndAdapter` test asserting both the populate-on-set and preserve-nil-on-unset paths.

**Task 2 — Wire steps 7-11 in commit.run()** (commit `6bf56e9`):
- Deleted three stub lines: `_ = diffTargets`, `_ = c.extractor`, `_ = c.adapter`.
- Step 7: per-`diffTarget` loop calling `c.extractor.ExtractContent(ctx, dt.Ref, c.achDir)`. `ExtractResult.WrittenFiles` count accumulates into `Result.FilesWritten`. *exit.CodedError pass-through preserves CollisionRefuse exit code 7; non-coded transport failures get wrapped as `exit.General` with the message `extract content (%s): %v`.
- Step 10: single `c.adapter.Render(ctx, m, existingState, c.achDir)` call after the extraction loop. `RenderResult.WrittenFiles` count adds to `Result.FilesWritten`; `RenderResult.DroppedComponents` appends to `Result.DroppedComponents` (ADAPT-07 silent-drop surface). Same *exit.CodedError pass-through pattern.
- Step 11: `syncFn(existingState, existingState, c.achDir, SyncOptions{Force: c.opts.Force, Stderr: c.opts.Stderr})` gated on `c.opts.Sync && !c.opts.DryRun`. `SyncStats.Pruned` += `result.FilesPruned`, `SyncStats.Preserved` += `result.FilesPreserved`. The `maybeKill(11)` hook fires BEFORE syncFn so the sc2 SIGKILL boundary at step 11 still hits the line documented by the contract.
- `Result.PlatformID = c.opts.Platform` assigned at run() entry so early-exit paths still surface the value.
- Added `syncFn = Sync` package-level variable as the test seam for verifying step-11 wiring fires (or does not fire) without invoking the real 200+-LOC Sync handler.
- All three new call sites gated on `!c.opts.DryRun` per threat T-07-W5-01-03 (dry-run skip).

**Task 3 — Cobra wiring** (commit `d2bff1e`):
- `cmd/ach-cli/cmd/hydrate.go:runHydrateEngine`: replaced `_, _ = hydrate.NewWiring(...)` with `ext, ad := hydrate.NewWiring(...)`. Moved the call ABOVE the `opts := hydrate.Opts{...}` literal so the returns are in scope. Added `Extractor: ext` + `AdapterDispatcher: ad` to the Opts literal. Replaced the multi-line W1-stub comment block with a single-line description of the new contract.

**Task 4 — Test the step-11 Sync wiring** (commit `5fd3053`):
- Three new unit tests using the `syncFn` package seam swap pattern:
  - `TestRun_Step11Sync_InvokedWhenSyncOptSet`: assert syncFn called exactly once when `opts.Sync=true`, with `opts.Force` flowing through.
  - `TestRun_Step11Sync_NotInvokedWhenSyncOptUnset`: assert syncFn never called when `opts.Sync=false` (default).
  - `TestRun_Step11Sync_NotInvokedUnderDryRun`: assert syncFn never called when `opts.DryRun=true` even if `opts.Sync=true`.
- Each test restores `syncFn = Sync` via `t.Cleanup` so the package default is the production behavior outside the test scope.

## Verification

- `./scripts/dev.sh make unit-pkg PKG=./internal/cli/hydrate/...` → exits 0; 6 new tests pass; all 36 prior hydrate tests pass.
- `./scripts/dev.sh make unit-pkg PKG=./cmd/ach-cli/...` → exits 0; no regression in cobra hydrate tests.
- `./scripts/dev.sh make unit` → full tree exits 0.
- `./scripts/dev.sh make lint-changed` → exits 0; no unused-variable warnings from the stub removal.
- `./scripts/dev.sh go build ./...` → both ach + ach-cli binaries compile clean.
- `./scripts/dev.sh go vet ./...` → exits 0.

## Acceptance criteria — point-by-point

Task 1:
- `grep -n "Extractor[[:space:]]*Extractor" internal/cli/hydrate/flags.go` → match at line 124.
- `grep -n "AdapterDispatcher[[:space:]]*AdapterDispatcher" internal/cli/hydrate/flags.go` → match at line 132.
- `grep -n "extractor:[[:space:]]*opts.Extractor" internal/cli/hydrate/commit.go` → match in newCommit.
- `grep -n "adapter:[[:space:]]*opts.AdapterDispatcher" internal/cli/hydrate/commit.go` → match in newCommit.
- `grep -n "TestNewCommit_PopulatesExtractorAndAdapter" internal/cli/hydrate/commit_test.go` → match.
- `func Run(` signature unchanged: `func Run(ctx context.Context, opts Opts) (Result, error)`.

Task 2:
- `_ = c.extractor` / `_ = c.adapter` / `_ = diffTargets` → all three lines absent from commit.go.
- `c.extractor.ExtractContent` call site present.
- `c.adapter.Render` call site present.
- `result.PlatformID = c.opts.Platform` assignment present at run() entry.
- `result.DroppedComponents = append(...)` present after Render.
- `TestRun_InvokesExtractorPerDiffTarget` + `TestRun_DryRun_SkipsExtractorAndAdapter` both present + passing.
- All five `c.maybeKill(N)` calls for N=7,8,9,10,11 preserved on their own lines.

Task 3:
- `_, _ = hydrate.NewWiring` absent in cmd/ach-cli/cmd/hydrate.go.
- `ext, ad := hydrate.NewWiring` present (exactly one occurrence).
- `Extractor: ext,` + `AdapterDispatcher: ad,` both present in Opts literal.

Task 4:
- `syncFn(existingState` call site at commit.go:310.
- `if c.opts.Sync && !c.opts.DryRun` gate at commit.go:303.
- `result.FilesPruned += stats.Pruned` + `result.FilesPreserved += stats.Preserved` both present.
- All three `TestRun_Step11Sync_*` tests present + passing.
- `c.maybeKill(11)` appears exactly once.
- maybeKill(11) at line 302 is ABOVE syncFn call at line 310 (verified via awk gate).

## Deviations from Plan

None — plan executed exactly as written. The plan's "final test approach" section for Task 4 anticipated the syncFn package-level seam pattern (explicitly chosen over interface-method or build-tag alternatives); the implementation followed that recommendation verbatim. Plan was already revised on 2026-05-29 to add Task 4 (Sync wiring) as Option A — extending W5-01 rather than creating a new W5-07 — and that decision held throughout execution.

## Known Stubs

None introduced by this plan. The TODO comment at the step-11 Sync call site documents an EXISTING design constraint inherited from commit.go:548-552 (step12WriteState's "W2/W3 land their concrete Extractor + AdapterDispatcher, the FileEntries flow back...") — the `newFile` arg is passed as `existingState` so Sync's set-difference is empty until step12 composition lands in a follow-up plan. The Sync wiring itself is real; only the composed input is a placeholder. This is the load-bearing precondition for STATE-05's actual inverse-merge firing on a real `--sync` workspace, which the step12 composition follow-up will close.

## Threat Flags

No new threat surface introduced. All three call-site additions (extractor, adapter, Sync) reuse the `c.achDir` path validated at step 1 — no new path-traversal surface. Mitigations T-07-W5-01-01..03 from the plan's `<threat_model>` are implemented:
- T-01: `c.achDir` reused (not recomputed).
- T-02: `result.PlatformID = c.opts.Platform` accepted as residual log-spoofing risk on own-process stderr (documented).
- T-03: All three call sites gated on `!c.opts.DryRun`; `TestRun_DryRun_SkipsExtractorAndAdapter` + `TestRun_Step11Sync_NotInvokedUnderDryRun` are the regression gates.

## Commits

| Task | Commit  | Files                                       |
| ---- | ------- | ------------------------------------------- |
| 1    | 6aa46da | flags.go + commit.go + commit_test.go       |
| 2    | 6bf56e9 | commit.go + commit_test.go                  |
| 3    | d2bff1e | cmd/ach-cli/cmd/hydrate.go                  |
| 4    | 5fd3053 | commit_test.go                              |

## Self-Check: PASSED

- `internal/cli/hydrate/flags.go` — FOUND, contains Extractor + AdapterDispatcher fields.
- `internal/cli/hydrate/commit.go` — FOUND, contains c.extractor.ExtractContent + c.adapter.Render + syncFn(existingState).
- `internal/cli/hydrate/commit_test.go` — FOUND, contains all 6 new test functions.
- `cmd/ach-cli/cmd/hydrate.go` — FOUND, contains `ext, ad := hydrate.NewWiring` + `Extractor: ext` + `AdapterDispatcher: ad`.
- Commit `6aa46da` — FOUND in git log.
- Commit `6bf56e9` — FOUND in git log.
- Commit `d2bff1e` — FOUND in git log.
- Commit `5fd3053` — FOUND in git log.
