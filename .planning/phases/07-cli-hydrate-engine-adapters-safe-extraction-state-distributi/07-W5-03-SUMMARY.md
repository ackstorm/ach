---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W5-03
subsystem: cli-hydrate-autoclaim
tags:
  - cr-03
  - safe-04
  - autoclaim
  - re-hydration
  - path-normalization
  - state-schema
requirements:
  - SAFE-04
provides:
  - extract.Classify(finalPath, achDir, stateFile) — path-normalizing autoclaim classifier
  - extract.ErrTargetNotRelative — sentinel for malformed (absolute) state.FileEntry.Target
requires:
  - state.FileEntry.Target — workspace-relative per spec §8.2 (D-13 v2 clean break)
  - wiring.go:adapterDispatcherImpl.Render — achDir is already in scope at the Classify call site
affects:
  - internal/cli/hydrate/wiring.go:adapterDispatcherImpl.Render — Classify call site (one line)
  - 07-W6-01 phase closeout — runtime gate becomes reachable
tech-stack:
  added: []
  patterns:
    - "filepath.Join + filepath.Clean for workspace-relative→absolute normalization"
    - "filepath.Rel containment check (refuse to flag Owned on `..`-escaped Target)"
    - "Wrapped sentinel error (fmt.Errorf %w) for malformed-state surface"
key-files:
  created: []
  modified:
    - internal/cli/extract/autoclaim.go
    - internal/cli/extract/autoclaim_test.go
    - internal/cli/hydrate/wiring.go
decisions:
  - "Option (b) chosen over option (a): normalize entry.Target to absolute inside Classify rather than store absolute paths in state.json. Option (a) would require either a state schemaVersion bump (forbidden by D-13's clean-break posture) or silent on-disk incompatibility — both unacceptable."
  - "achDir is the second positional parameter (after finalPath) so the most-common arg (the file being classified) stays first. No struct field added to adapterDispatcherImpl — achDir is already a parameter on Render."
  - "Absolute entry.Target REJECTED with ErrTargetNotRelative sentinel rather than silently allowed to bypass normalization. Spec §8.2 mandates relative; an absolute value is malformed state.json and must surface."
  - "`..`-escaping Target values fail filepath.Rel-based containment check and are treated as non-matches (do NOT flag Owned). T-07-W5-03-02 mitigation: a tampered state.json cannot pivot the classification toward auto-claim."
  - "Tasks 1 + 2 folded into one atomic commit (Rule 3 deviation, same precedent as W5-02). Splitting Task 1's signature change from Task 2's call-site update would leave HEAD non-building, breaking git bisect. CLAUDE.md forbids --no-verify bypass."
metrics:
  duration: ~6min
  completed: "2026-05-29T19:14:27Z"
  tasks_completed: 2
  files_modified: 3
  lines_changed: ~210 (autoclaim.go +56, autoclaim_test.go +148, wiring.go +6)
---

# Phase 7 Plan 07-W5-03: Classify Path Normalization (CR-03 Fix) Summary

## One-liner

Re-hydration correctness fix — Classify normalizes workspace-relative `state.FileEntry.Target` against `achDir` so `CollisionOwnedByCurrent` is reachable for adapter-written files; rotated-credential re-runs no longer exit 7.

## What Changed

**Surface area:** 3 files, ~210 LOC net change (~56 / 148 / 6).

**`internal/cli/extract/autoclaim.go`** (Task 1):
- `Classify` signature extended from `(finalPath, stateFile)` to `(finalPath, achDir, stateFile)`. `achDir` is positional and second so the most-common arg stays first.
- Loop body replaces the broken `entry.Target == finalPath` equality check with:
  1. Reject `filepath.IsAbs(entry.Target)` with `ErrTargetNotRelative` (wrapped via `fmt.Errorf("%w: target=%q", ...)`).
  2. Normalize via `filepath.Join(achDirClean, entry.Target)` where `achDirClean := filepath.Clean(achDir)`.
  3. Containment check via `filepath.Rel(achDirClean, entryAbs)` — if `rel == ".."` or starts with `".."+separator`, continue the scan without flagging Owned.
  4. Compare `entryAbs == finalPath`.
- New sentinel: `var ErrTargetNotRelative = errors.New("autoclaim: state.FileEntry.Target is absolute (must be workspace-relative per spec §8.2)")`.
- Imports added: `path/filepath`, `strings`.
- Godoc documents the path-comparison contract, the achDir parameter, the absolute-Target rejection, and the `..`-containment check. References CR-03 from the Phase 7 verification report and T-07-W5-03-02 mitigation.

**`internal/cli/extract/autoclaim_test.go`** (Task 1):
- 5 existing Classify tests updated to:
  - Pass `achDir` (typically the `t.TempDir()` already in scope).
  - Use workspace-relative `Target` values (`"foo.md"`, `".claude/.mcp.json"`) instead of pre-joined absolutes. The previous tests accidentally exercised the broken absolute-equality arm; they now exercise the fixed normalization arm.
- 4 new tests:
  - `TestClassifyRelativeTarget_NormalizesToAbsoluteAndReturnsOwned` — load-bearing positive case: state entry `.claude/.mcp.json` + `finalAbs := filepath.Join(achDir, ".claude/.mcp.json")` → `CollisionOwnedByCurrent`. Proves CR-03 is closed.
  - `TestClassifyRelativeTarget_NoMatch_ReturnsUnowned` — negative case: state entry `.codex/config.toml`, query `.claude/.mcp.json` → `CollisionExistsUnowned`. Catches regression where normalization is too loose.
  - `TestClassifyAbsoluteTarget_Rejected` — state entry `/etc/passwd` (absolute) → `errors.Is(err, ErrTargetNotRelative)`.
  - `TestClassifyDotDotTarget_DoesNotMatch` — state entry `../../etc/passwd` resolving outside `achDir` → must NOT return `CollisionOwnedByCurrent` even when `filepath.Join(achDir, target) == finalAbs` happens to be true.
- `strings` import added (used by the dot-dot test's HasPrefix invariant assertion).

**`internal/cli/hydrate/wiring.go`** (Task 2):
- Line 213: `class, err := extract.Classify(finalAbs, achDir, s)` (was `extract.Classify(finalAbs, s)`). `achDir` was already a parameter of the enclosing `Render(ctx, m, s, achDir)` method, in scope at this line — no new struct field needed.
- Godoc on `adapterDispatcherImpl.Render` step 3b updated to mention the achDir argument and reference CR-03 / 07-W5-03.

## Verification

- `./scripts/dev.sh make unit-pkg PKG=./internal/cli/extract/...` exits 0 — all 9 Classify subtests pass (5 existing + 4 new) plus the full extract suite (StageAndPublish + tar safety + bomb caps).
- `./scripts/dev.sh make unit-pkg PKG=./internal/cli/hydrate/...` exits 0 — adapter dispatcher + collision cascade + Sync inverse-merge tests all green.
- `./scripts/dev.sh go build ./...` exits 0 — whole-tree compile-clean, confirms no missed call sites in `cmd/` or `internal/`.
- `./scripts/dev.sh go vet ./...` exits 0.
- `./scripts/dev.sh make lint-changed` exits 0.
- Pre-commit hook (`make pre-commit` = `lint-changed` + `unit`) passed on commit `b5a9ef5`.
- Full-tree audit `grep -rn "extract.Classify(" --include="*.go" .` shows every call site (1 production in `wiring.go:213` + 9 test calls in `autoclaim_test.go`) passes the achDir argument.

## Acceptance Criteria

- [x] `extract.Classify` signature is `func Classify(finalPath string, achDir string, stateFile *state.File) (CollisionClass, error)`.
- [x] Relative `entry.Target` values normalized via `filepath.Join(achDir, entry.Target)` before comparison.
- [x] Absolute Target values rejected with `ErrTargetNotRelative` sentinel.
- [x] `..`-escaped Target values fail containment check and are treated as non-matches.
- [x] Single production call site in `internal/cli/hydrate/wiring.go` passes achDir.
- [x] 4 new unit tests covering (a) positive normalization, (b) negative no-match, (c) absolute rejection, (d) `..`-containment.
- [x] All existing `internal/cli/extract/...` and `internal/cli/hydrate/...` tests pass.
- [x] state.json wire format unchanged — `FileEntry.Target` remains workspace-relative per spec §8.2. No schemaVersion bump.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Tasks 1 + 2 folded into one atomic commit**

- **Found during:** Task 1 commit attempt
- **Issue:** Task 1's signature change (`Classify(finalPath, stateFile)` → `Classify(finalPath, achDir, stateFile)`) makes the tree non-building until Task 2's call-site update in `wiring.go:213` lands. Splitting into two commits would leave HEAD non-building between commits, breaking `git bisect` for any future regression hunt. CLAUDE.md forbids `--no-verify` bypass of the pre-commit lint/unit hook (which requires the tree to build).
- **Fix:** Folded Task 1 (autoclaim.go + autoclaim_test.go) + Task 2 (wiring.go) into a single atomic commit `fix(07-W5-03): normalize FileEntry.Target in Classify (CR-03)`. Same precedent set by Phase 07-W5-02 (per STATE.md decision log: "Tasks 1+2 folded into one atomic commit (Rule 3 deviation) because Task 1's signature change breaks the tree until Task 2 updates the 4 wiring.go callers").
- **Files modified:** internal/cli/extract/autoclaim.go, internal/cli/extract/autoclaim_test.go, internal/cli/hydrate/wiring.go
- **Commit:** b5a9ef5

## Stub Tracking

None. The Classify fix lands as production code; the 4 new tests exercise both the positive normalization path and the three defensive arms (absolute-rejection, `..`-containment, no-match).

## Threat Coverage

The plan's `<threat_model>` enumerated three threats:

- **T-07-W5-03-01 (symlink races)** — accepted. Classify inspects only the in-memory `*state.File`; the actual on-disk file inspection happens later in stage.go which has its own SAFE-01/SAFE-02 surface. The fix did not introduce new stat racing.
- **T-07-W5-03-02 (`..` escape in entry.Target)** — mitigated. Implemented as `filepath.Rel(achDirClean, entryAbs)` containment check; if the relative path starts with `..`, the entry is treated as a non-match. `TestClassifyDotDotTarget_DoesNotMatch` exercises this arm.
- **T-07-W5-03-03 (absolute-Target bypass)** — mitigated. Implemented as `filepath.IsAbs(entry.Target)` early-return with `ErrTargetNotRelative` sentinel. `TestClassifyAbsoluteTarget_Rejected` exercises this arm.

No new threat surface introduced beyond what the plan anticipated.

## Commits

- `b5a9ef5` — `fix(07-W5-03): normalize FileEntry.Target in Classify (CR-03)`

## Runtime Gate (HUMAN_VERIFICATION — Phase 7 closeout)

This plan is the third and final CR fix (CR-03) before Phase 7 closeout. The runtime gate fires AFTER all three CRs (CR-01 / W5-02, CR-02 / W5-01, CR-03 / this plan) have landed and BEFORE the W6-01 checkpoint plan dispatches. On the dev host:

```
make cluster-keep
ACH_E2E_PHASE7=1 \
  ACH_E2E_PHASE7_PK=<a-fresh-pk_> \
  ACH_E2E_PHASE7_BASE_URL=http://localhost:8080 \
  ./scripts/dev.sh make e2e-focus FOCUS=TestPhase7CLIEngine
```

Expected: all 14 subtests in `TestPhase7CLIEngine` pass. The 8 `sc1_*` platform×credential subtests are the load-bearing proof — they produce actual workspace files for all four adapters (`.claude/.mcp.json`, `.codex/config.toml`, `.gemini/settings.json`, `.opencode/opencode.json`) at mode `0o600` (CR-01 / W5-02) with valid bearer headers. The sc2 SIGKILL recovery proves the §6.7 commit sequence is crash-safe end-to-end. The sc3 drift four-outcome subtest fires exit codes 0/0/2/2. The sc4 safe-extract subtests reject malicious archives and bomb tarballs with non-zero exit. **A re-hydrate of any sc1 subtest with rotated credentials returns exit 0 (NOT 7) — the CR-03 fix is load-bearing here.**

The cluster is not available in this execution context (sequential executor on main worktree; no `make cluster-up` invoked). The runtime gate is deferred to the human verifier / W6-01 checkpoint per the plan's `must_haves.truths` entry HUMAN_VERIFICATION.

## Self-Check: PASSED

- File `internal/cli/extract/autoclaim.go` exists — FOUND
- File `internal/cli/extract/autoclaim_test.go` exists — FOUND
- File `internal/cli/hydrate/wiring.go` exists — FOUND
- Commit `b5a9ef5` exists in git log — FOUND
- Signature `func Classify(finalPath string, achDir string, stateFile *state.File)` present — FOUND
- Sentinel `ErrTargetNotRelative` present — FOUND
- `entry.Target == finalPath` broken comparison absent — CONFIRMED REMOVED
- `extract.Classify(finalAbs, achDir, s)` call site in wiring.go — FOUND
- All 4 new tests present (TestClassifyRelativeTarget_NormalizesToAbsoluteAndReturnsOwned, TestClassifyRelativeTarget_NoMatch_ReturnsUnowned, TestClassifyAbsoluteTarget_Rejected, TestClassifyDotDotTarget_DoesNotMatch) — FOUND
- `./scripts/dev.sh make unit-pkg PKG=./internal/cli/extract/...` — PASSED
- `./scripts/dev.sh make unit-pkg PKG=./internal/cli/hydrate/...` — PASSED
- `./scripts/dev.sh go build ./...` — PASSED
- `./scripts/dev.sh go vet ./...` — PASSED
- `./scripts/dev.sh make lint-changed` — PASSED
