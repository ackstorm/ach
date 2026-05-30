---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W1-02
subsystem: cli
tags: [state, hydrate, schema-v2, atomic-write, sentinel-errors, stdlib-only, phase-7-foundation]

# Dependency graph
requires:
  - phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
    plan: 07-W1-01
    provides: "exit.SchemaMismatch (5) + exit.EnvironmentMismatch (4) — caller layer maps state.ErrSchemaMismatch / state.ErrEnvironmentGuard through *exit.CodedError."
  - package: "internal/cli/config (analog precedent)"
    provides: "File struct + Load + Save + Path + sentinel error idiom — directly mirrored for state.go; mode/encoding deltas documented in package doc."
  - package: "internal/cachefs (analog precedent for SweepTmp)"
    provides: "Idempotent absent-dir return + per-entry error swallow pattern; D-02 unconditional-sweep delta documented in sweep.go godoc."
provides:
  - "internal/cli/state.File / FileEntry / AdapterSection JSON schema v2 marshaler with Load + Save"
  - "internal/cli/state.WriteAtomic — STATE-07 four-step atomic write (tmp + fsync(fd) + rename + fsync(parent_dir))"
  - "internal/cli/state.ResolvePath — workspace vs global <ach-dir>/state.json resolver"
  - "internal/cli/state.SweepTmp — D-02 unconditional <achDir>/tmp/ sweep at hydrate start"
  - "internal/cli/state.GuardEnvironment — §8.3 same-<ach-dir>-different-Environment guard with --force escape hatch"
  - "internal/cli/state.ErrSchemaMismatch / ErrStateParse / ErrInvalidPath / ErrEnvironmentGuard sentinel errors"
affects: [07-W1-03, 07-W1-04, 07-W1-05, 07-W1-06, 07-W2-01, 07-W2-02, 07-W2-03, 07-W3-01, 07-W3-02, 07-W3-03, 07-W3-04, 07-W3-05, 07-W4-01, 07-W4-02]

# Tech tracking
tech-stack:
  added: []  # zero new go.mod entries; stdlib-only
  patterns:
    - "Stdlib-only file-I/O package mirrors internal/cli/config: sentinel errors + atomic publication via tmp+rename + 2-space-indent JSON; SetEscapeHTML(false) for human-diffable diffs."
    - "STATE-07 four-step atomic write extends config.Save's 3-step pattern (tmp+chmod+rename) with two fsync calls — fsync(fd) before rename, fsync(parent_dir) after. Parent-dir fsync runtime.GOOS-gated to skip Windows (NTFS does not honor fsync on directories)."
    - "Clean-break v2 per D-13: Load rejects schemaVersion != \"2\" with ErrSchemaMismatch (no v1 reader code ships). DisallowUnknownFields strictness catches both v1 leftovers (contentHashes) and forward-compat drift in one gate."
    - "GuardEnvironment is pure data: nil-existing + empty-Environment + same-Environment + force=true all return nil; the mismatch arm wraps ErrEnvironmentGuard with %q-quoted have/want detail for both errors.Is and string-contains assertions. Caller layer owns warning text."

key-files:
  created:
    - "internal/cli/state/doc.go (42 lines — SPDX header + package godoc citing §8.2 schema + §8.7 atomic-write contract + D-13 clean break)"
    - "internal/cli/state/state.go (141 lines — File/FileEntry/AdapterSection structs + Load/Save + ErrSchemaMismatch/ErrStateParse/ErrInvalidPath sentinels)"
    - "internal/cli/state/state_test.go (175 lines — 7 tests: AbsentFile_ReturnsNilNil, SchemaV1_ReturnsErrSchemaMismatch, SchemaV2_RoundTrip, UnknownField_ReturnsErrStateParse, CorruptJSON_ReturnsErrStateParse, NilFile_Errors, WritesValidJSON)"
    - "internal/cli/state/atomic.go (93 lines — WriteAtomic four-step STATE-07 contract with cleanup closure + runtime.GOOS gate)"
    - "internal/cli/state/atomic_test.go (135 lines — 5 tests: TargetExistsAfterWrite, NoTmpRemnant, NonexistentParentDir_TargetUntouched, OverwritesExistingTarget, FileMode 0644)"
    - "internal/cli/state/path.go (46 lines — ResolvePath workspace + global branches)"
    - "internal/cli/state/path_test.go (88 lines — 5 tests: Workspace, WorkspaceIgnoresEnvironment, WorkspaceEmptyCwd_Errors, Global, GlobalEmptyEnv_Errors)"
    - "internal/cli/state/sweep.go (59 lines — SweepTmp unconditional D-02 sweep)"
    - "internal/cli/state/sweep_test.go (146 lines — 4 tests: AbsentTmpDir_ReturnsNil, RemovesAllSubentries, PreservesNonTmpSiblings, RemoveFails_SwallowsError)"
    - "internal/cli/state/guard.go (60 lines — GuardEnvironment + ErrEnvironmentGuard sentinel)"
    - "internal/cli/state/guard_test.go (82 lines — 5 tests: FreshState, EmptyEnvironment, SameEnvironment, DifferentEnvironment_ReturnsErrEnvironmentGuard, DifferentEnvironment_WithForce)"
  modified: []

key-decisions:
  - "Flat top-level schema (Prompts/Plugins/Artifacts/RuntimeFiles as []FileEntry, plus Adapter AdapterSection{ID, Files}) per the plan's <must_haves.truths> + <behavior> — DIVERGES from the spec §8.2 example which shows a nested context.<kind>[*].files[] shape. The plan is the contract: D-13 deliberately simplifies the v2 shape for clean-break extraction work. Downstream plans (W1-05 manifest decoder, W1-06 commit-sequence skeleton) consume this flat shape; if a future plan needs the nested form it will add a second layer or repurpose AdapterSection's pattern."
  - "ErrStateParse declared in state.go alongside ErrSchemaMismatch (not in a sibling errors.go file) — keeps the data layer's sentinel surface co-located with the Load/Save functions that emit them. ErrEnvironmentGuard lives in guard.go for symmetric reasons (it pairs with GuardEnvironment). ErrInvalidPath lives in state.go even though it is emitted from path.go, because it belongs to the data-layer error vocabulary the caller layer maps to exit codes (it is the path-resolver's contribution to the same sentinel set)."
  - "Save renders JSON with `enc.SetIndent(\"\", \"  \")` + `enc.SetEscapeHTML(false)` — both deviate from the encoding/json defaults (no indent; html-escape on). Rationale: state.json is a CHECKED-INTO-`--diff`able artifact for `ach status` and forensic debugging; an indented, non-html-escaped form keeps `<`/`>`/`&` in target paths readable. The W4 e2e golden uses these defaults as the comparison fixture."
  - "WriteAtomic's parent-dir fsync is best-effort + runtime.GOOS-gated to skip Windows. NTFS does not honor fsync on directories, and the Linux/macOS branch uses os.Open(dir) + d.Sync() (the standard Go idiom). On Windows the rename's directory-entry durability falls back to the FS's own crash-consistency story — acceptable per the STATE-07 contract's silent best-effort clause for non-POSIX platforms."
  - "TDD discipline followed procedurally but RED+GREEN collapsed into single commits per task — same trade-off as 07-W1-01. The project's pre-commit hook (`make pre-commit`) runs `go vet` which rejects a failing-to-compile RED commit; CLAUDE.md explicitly forbids `--no-verify` (\"never `--no-verify` or otherwise bypass\"). RED was verified locally as build-fail (`undefined: state.File`, `undefined: state.WriteAtomic`, `undefined: state.ResolvePath`, `undefined: state.SweepTmp`, `undefined: state.GuardEnvironment`) before each impl write."

patterns-established:
  - "Phase 7 data-layer packages stay import-cycle-free from internal/cli/exit. Sentinel errors live at the data layer (state.ErrSchemaMismatch, state.ErrEnvironmentGuard); the caller layer (cmd/ach-cli/cmd/hydrate.go + the W1-06 commit orchestrator) does the *exit.CodedError wrapping. This separation is the threat-model guarantee against exit-code spoofing — even if a hostile server feeds a malformed state.json into a future caching path, the state package raises a typed error, not an exit code."
  - "STATE-07 four-step atomic write is the new precedent for any Phase 7 file-publication helper: tmp+fsync(fd)+rename+fsync(parent_dir). Other plans (W2 extract.Stage, W3 adapter writes) that need crash-safe publication should mirror this exact pattern rather than reverting to config.Save's 3-step (no fsync) shape."

requirements-completed:
  - STATE-01  # ResolvePath workspace + global semantics
  - STATE-02  # schemaVersion gate via ErrSchemaMismatch
  - STATE-03  # GuardEnvironment + ErrEnvironmentGuard
  - STATE-07  # WriteAtomic four-step contract
  - STATE-09  # partial — schemaVersion strict gate (full STATE-09 also covered by W1-05 manifest decoder)

# Metrics
duration: ~25min
completed: 2026-05-29
---

# Phase 7 Plan 07-W1-02: internal/cli/state package — v2 marshaler + atomic write + sweep + Environment guard Summary

**The `internal/cli/state` package ships 11 files (5 source + 5 test + doc.go) under stdlib-only discipline, providing the §8.2 v2 state.json marshaler, STATE-07 four-step atomic-write helper, D-02 unconditional tmp/-sweep, and §8.3 same-`<ach-dir>`-different-Environment guard every Phase 7 plan downstream reads from.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-05-29T13:53:00Z (worktree spawn)
- **Completed:** 2026-05-29T14:18:14Z
- **Tasks:** 2 (both `auto`/`tdd=true`)
- **Files created:** 11 (all tracked, all under `internal/cli/state/`)
- **Tracked commits:** 2 (`2298760`, `43f993e`)
- **Tests added:** 26 (17 in Task 1 + 9 in Task 2), all passing under `-race`
- **Lines of code:** 1,067 total (442 source + 625 test) — test/source ratio ≈ 1.4:1

## Accomplishments

- `internal/cli/state/doc.go` exists with SPDX header and package godoc citing §8.2 schema verbatim + §8.7 atomic-write contract + D-13 clean-break disclaimer.
- `internal/cli/state/state.go` exports `File`, `FileEntry`, `AdapterSection` structs with exactly the JSON tags listed in the plan's `<behavior>`: `schemaVersion`, `environment`, `deployment`, `prompts`, `plugins`, `artifacts`, `runtimeFiles`, `adapter`. `omitempty` on every collection so a fresh File round-trips to a minimal JSON document.
- Sentinel errors exported: `ErrSchemaMismatch`, `ErrStateParse`, `ErrInvalidPath` (in state.go); `ErrEnvironmentGuard` (in guard.go).
- `Load(path)` returns `(nil, nil)` on `os.IsNotExist` (fresh workspace), wraps decode failures as `ErrStateParse`, enforces `DisallowUnknownFields` for strict §8.2 schema compliance, rejects `schemaVersion != "2"` with `ErrSchemaMismatch`.
- `Save(path, f)` rejects nil File defensively, dispatches to `WriteAtomic`, indents JSON 2-space, disables HTML escaping for human-diffable paths.
- `WriteAtomic(path, data)` implements the STATE-07 four-step contract: CreateTemp → Write → Sync(fd) → Chmod(0644) → Close → Rename → Sync(parent_dir). Parent-dir fsync is runtime.GOOS-gated (skip on Windows where NTFS does not honor it). Cleanup closure removes the tmp file on every error path.
- `ResolvePath(workspaceCwd, environment, global)` returns `<cwd>/.ach/state.json` for workspace and `$HOME/.ach/<env>/state.json` for global. Empty workspaceCwd or empty environment (in global scope) returns `ErrInvalidPath`.
- `SweepTmp(achDir)` removes every subentry under `<achDir>/tmp/` via `os.RemoveAll` per D-02 (no maxAge parameter). Returns nil for absent tmp/. Per-entry errors swallowed per spec §6.7 step 2.
- `GuardEnvironment(existing, requested, force)` returns nil for nil-existing / empty-Environment / matching-Environment / force=true. Otherwise wraps `ErrEnvironmentGuard` with `have=<x> want=<y>` detail.
- Stdlib-only discipline verified: `grep -E '^\s*"(log|log/slog|gopkg\.in/yaml)' internal/cli/state/*.go` returns 0 matches. Only stdlib imports: `bytes`, `encoding/json`, `errors`, `fmt`, `io/fs`, `os`, `path/filepath`, `runtime`.
- Zero new `go.mod` entries.

## Task Commits

Each task was committed atomically:

1. **Task 1: state.File + Load + Save + ErrSchemaMismatch + WriteAtomic + ResolvePath** — `2298760` (`feat`). 7 files / 720 insertions. RED+GREEN collapsed per the W1-01 precedent.
2. **Task 2: SweepTmp + GuardEnvironment** — `43f993e` (`feat`). 4 files / 347 insertions. RED+GREEN collapsed.

**Plan metadata commit:** N/A — SUMMARY.md lives under `.planning/` (gitignored at repo level). Per the worktree-mode `<parallel_execution>` block, the SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem location.

## Files Created/Modified

| Path | Lines | Role |
|------|-------|------|
| `internal/cli/state/doc.go` | 42 | Package godoc + §8.2 schema + STATE-07 + D-13 |
| `internal/cli/state/state.go` | 141 | `File` / `FileEntry` / `AdapterSection` structs; `Load`; `Save`; `ErrSchemaMismatch` / `ErrStateParse` / `ErrInvalidPath` |
| `internal/cli/state/state_test.go` | 175 | 7 unit tests (Load + Save + struct round-trip) |
| `internal/cli/state/atomic.go` | 93 | `WriteAtomic` four-step STATE-07 contract |
| `internal/cli/state/atomic_test.go` | 135 | 5 unit tests (target/tmp/error/overwrite/mode invariants) |
| `internal/cli/state/path.go` | 46 | `ResolvePath` workspace + global |
| `internal/cli/state/path_test.go` | 88 | 5 unit tests (4 paths + 1 invariant) |
| `internal/cli/state/sweep.go` | 59 | `SweepTmp` unconditional D-02 sweep |
| `internal/cli/state/sweep_test.go` | 146 | 4 unit tests (absent/all/siblings/swallow) |
| `internal/cli/state/guard.go` | 60 | `GuardEnvironment` + `ErrEnvironmentGuard` |
| `internal/cli/state/guard_test.go` | 82 | 5 unit tests (fresh / empty / same / mismatch / force) |
| **Total** | **1,067** | **11 files** |

## Decisions Made

See `key-decisions` in frontmatter. Summary:

1. The plan's flat top-level schema (`Prompts []FileEntry`, `Plugins []FileEntry`, etc.) is honored verbatim — it DIVERGES from spec §8.2's nested `context.<kind>[*].files[]` shape, but the plan is the canonical contract for D-13 extraction work. Future plans can add a nested layer or repurpose `AdapterSection`'s pattern if needed.
2. `ErrStateParse` and `ErrInvalidPath` live in state.go (not a sibling errors.go) — keeps the data-layer sentinel surface co-located with the Load/Save/ResolvePath functions that emit them. `ErrEnvironmentGuard` lives in guard.go for symmetric reasons.
3. `Save` renders JSON with indent + `SetEscapeHTML(false)` for human-diffable forensics. The W4 e2e golden will use these as comparison defaults.
4. WriteAtomic's parent-dir fsync is best-effort + runtime.GOOS-gated (skip Windows). Per STATE-07's silent-best-effort clause for non-POSIX platforms.
5. RED+GREEN collapsed into single per-task commits — same trade-off as W1-01. Pre-commit `go vet` gate would block a failing-to-compile RED commit; CLAUDE.md forbids `--no-verify`. RED was verified locally as `undefined: state.X` build failures before each impl write.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Pre-existing flake in `internal/contentservice/envcache.TestGet_Singleflight_DedupesConcurrentMisses` blocked the first commit of Task 1**

- **Found during:** Task 1 commit (first attempt)
- **Issue:** The pre-commit hook (`make pre-commit`) runs `make unit` over the entire repo. On the first commit attempt, `TestGet_Singleflight_DedupesConcurrentMisses` in `internal/contentservice/envcache/` failed under `-race`. The failure is unrelated to anything in this plan's scope (state package introduces no concurrency or singleflight code).
- **Verification:** Ran the failing test in isolation three times via `./scripts/dev.sh go test -run "TestGet_Singleflight_DedupesConcurrentMisses" -count=3 ./internal/contentservice/envcache/` — passed 3-of-3. Confirms a pre-existing timing-sensitive flake, not a new regression.
- **Fix:** Per the SCOPE BOUNDARY rule ("only auto-fix issues DIRECTLY caused by the current task's changes; pre-existing failures in unrelated files are out of scope"), re-attempted the commit without modifications to the failing test or its source. Second attempt passed all gates including the previously-flaky test. The flake is logged here for future verifier visibility — fixing the flake itself is out of scope for this plan but should be tracked separately.
- **Files modified:** None (workflow retry, not a code/test change)
- **Committed in:** N/A — the retry produced `2298760` which is Task 1's atomic commit

**2. [Rule 3 - Workflow] TDD RED+GREEN collapsed into single commits per task**

- **Found during:** Task 1 and Task 2 RED steps
- **Issue:** The plan's `tdd="true"` attribute combined with the executor system prompt's `<tdd_execution>` step mandates separate RED (`test(...)`) and GREEN (`feat(...)`) commits. The project's `pre-commit` hook (`make pre-commit`) includes `go vet` over the full tree; a failing-to-compile RED commit (with test files referencing undefined `state.File`, `state.WriteAtomic`, etc.) trips the vet gate and the commit is rejected. CLAUDE.md explicitly forbids `--no-verify` (\"If a gate fails, fix the root cause — never `--no-verify` or otherwise bypass\").
- **Fix:** Same resolution as the W1-01 precedent (documented in `07-W1-01-SUMMARY.md` Decision #3 and Deviation #2). Collapsed RED + GREEN into one atomic commit per task. TDD discipline preserved procedurally: RED tests were written first, the build failure (`undefined: state.File`, `undefined: state.Load`, `undefined: state.Save`, etc. for Task 1; `undefined: state.SweepTmp`, `undefined: state.GuardEnvironment`, `undefined: state.ErrEnvironmentGuard` for Task 2) was confirmed locally before adding the impl files.
- **Files modified:** None (workflow trade-off, not a code/test change)
- **Verification:** Both tasks' GREEN test runs after impl show all sub-tests passing (`--- PASS: TestLoad_AbsentFile_ReturnsNilNil` through `--- PASS: TestGuard_DifferentEnvironment_WithForce_ReturnsNil`).
- **Committed in:** `2298760` (Task 1 combined) and `43f993e` (Task 2 combined)

---

**Total deviations:** 2 (both Rule 3 — workflow/tooling friction, no scope change)
**Impact on plan:** None on deliverables. All 5 source files + 5 test files + doc.go ship as the plan specified; all `<acceptance_criteria>` gates pass; all `<verification>` checks pass; all `<success_criteria>` bullets pass. The plan's intent — the foundation Phase 7 reads from — lands exactly as written.

## Threat Flags

None. The state package introduces no new network endpoints, no new auth paths, no new file-access patterns at trust boundaries, and no schema changes outside `<ach-dir>/state.json` (which the plan's threat model already anticipates). The package is purely local file I/O over a directory the calling process already owns. The STATE-07 fsync hardening REDUCES threat surface (mitigates partial-write attacks) rather than introducing new surface.

## Issues Encountered

- `internal/contentservice/envcache.TestGet_Singleflight_DedupesConcurrentMisses` is a pre-existing flake under `-race`. Reproduced once during the first pre-commit run of Task 1, then passed three runs in isolation and on the commit retry. Not in scope for this plan but tracked here for visibility. Likely root cause is goroutine-scheduling timing in the singleflight dedup assertion; a future plan touching `internal/contentservice/envcache/` should consider adding sync barriers or removing the flake.
- The `make lint-changed` target's BASE_REF-vs-HEAD diff strategy skips untracked-and-newly-added files (it only sees changes to FILES PRESENT IN THE BASE REF). Workaround: ran `./bin/golangci-lint run ./internal/cli/state/...` directly against the new package via `./scripts/dev.sh` — clean (LINT_OK). The pre-commit hook's full `make lint` sweep (which would also catch the new files) passed on both commits.
- `.planning/` is gitignored at the repo level, so this SUMMARY.md is not git-trackable. Per the worktree-mode `<parallel_execution>` block in the executor system prompt, the SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem path. No `docs(...)` follow-up commit is possible without `-f`-style force-stage (which is forbidden).

## User Setup Required

None. All changes are repo-internal Go code. No external services, no secrets, no schema migrations.

## Self-Check

```
# Tracked file existence (worktree)
[ -f internal/cli/state/doc.go ]            → FOUND
[ -f internal/cli/state/state.go ]          → FOUND
[ -f internal/cli/state/state_test.go ]     → FOUND
[ -f internal/cli/state/atomic.go ]         → FOUND
[ -f internal/cli/state/atomic_test.go ]    → FOUND
[ -f internal/cli/state/path.go ]           → FOUND
[ -f internal/cli/state/path_test.go ]      → FOUND
[ -f internal/cli/state/sweep.go ]          → FOUND
[ -f internal/cli/state/sweep_test.go ]     → FOUND
[ -f internal/cli/state/guard.go ]          → FOUND
[ -f internal/cli/state/guard_test.go ]     → FOUND

# Gitignored SUMMARY (main-repo .planning/)
[ -f /home/jcm/Projects/ach/.planning/phases/07-cli-hydrate-engine-adapters-safe-extraction-state-distributi/07-W1-02-SUMMARY.md ] → FOUND

# Commit existence
git log --oneline -5 | grep 2298760 → FOUND ("feat(07-W1-02): add internal/cli/state package …")
git log --oneline -5 | grep 43f993e → FOUND ("feat(07-W1-02): add SweepTmp + GuardEnvironment …")

# Plan-level acceptance gates (Task 1)
grep -q "type File struct" internal/cli/state/state.go                           → OK
grep -q "ErrSchemaMismatch" internal/cli/state/state.go                          → OK
grep -q "func WriteAtomic" internal/cli/state/atomic.go                          → OK
grep -q "tmp.Sync" internal/cli/state/atomic.go                                  → OK  (fsync(fd))
grep -qE "(parent|dir|d)\.Sync\(\)" internal/cli/state/atomic.go                 → OK  (fsync(parent_dir))
grep -q "func ResolvePath" internal/cli/state/path.go                            → OK
grep -q "DisallowUnknownFields" internal/cli/state/state.go                      → OK

# Plan-level acceptance gates (Task 2)
grep -q "func SweepTmp" internal/cli/state/sweep.go                              → OK
grep -q "os.RemoveAll" internal/cli/state/sweep.go                               → OK
grep -qE "func SweepTmp\(achDir string\) error" internal/cli/state/sweep.go      → OK  (no maxAge param)
grep -q "func GuardEnvironment" internal/cli/state/guard.go                      → OK
grep -q "var ErrEnvironmentGuard" internal/cli/state/guard.go                    → OK

# Plan-level verification gates
grep -vE '^#' internal/cli/state/*.go | grep -cE 'gopkg.in/yaml|"log"'           → 0  (stdlib-only)
./scripts/dev.sh make unit-pkg PKG=./internal/cli/state/...                      → exit 0  (26 tests pass)
./bin/golangci-lint run ./internal/cli/state/...                                 → LINT_OK
```

## Self-Check: PASSED

## Next Phase Readiness

- **07-W1-03 (lock):** can import `github.com/ackstorm/ach/internal/cli/state` for `state.ResolvePath` (lock path is `filepath.Join(filepath.Dir(stateJSONPath), "lock")`). No state-package change needed.
- **07-W1-04 (hash):** independent — does not consume state package directly but the W1-06 commit-sequence skeleton will wire xxh3 output into `FileEntry.Hash` / `FileEntry.SourceHash`.
- **07-W1-05 (manifest):** independent — the manifest decoder ships its own `schemaVersion: "v1alpha1"` check (manifest, not state). W1-06 wires the manifest into `File.Prompts` / `File.Plugins` / etc.
- **07-W1-06 (commit-sequence skeleton):** primary consumer — calls `state.SweepTmp` at step 2, `state.Load` + `state.GuardEnvironment` at step 3, `state.Save` at step 12. Will map `state.ErrSchemaMismatch` → `*exit.CodedError{Code: exit.SchemaMismatch}` and `state.ErrEnvironmentGuard` → `*exit.CodedError{Code: exit.EnvironmentMismatch}` at the cobra layer.
- **07-W2-01..03 (extract):** consumers — `extract.Stage` will mirror `state.WriteAtomic`'s STATE-07 pattern for per-file publication; new FileEntries flow back into `File.Prompts` / `File.Plugins` / `File.Artifacts` via the commit orchestrator.
- **07-W3-01..05 (adapters + manifest decoder):** consumers — adapter outputs populate `File.Adapter.Files` (`AdapterSection.ID` is the adapter id, `AdapterSection.Files` is the file entry list).
- **07-W4-01..02 (e2e + ROADMAP):** the SIGKILL crash-recovery test (D-20 SC#2) lives in W4; this plan's `TestWriteAtomic_NonexistentParentDir_TargetUntouched` covers the in-process invariant only. The W4 process-fork harness will round-trip a Save → SIGKILL → Load to assert the on-disk state survives a crash mid-Write.

No blockers. Package surface is the complete `{File, Load, Save, WriteAtomic, ResolvePath, SweepTmp, GuardEnvironment, ErrSchemaMismatch, ErrEnvironmentGuard, ErrStateParse, ErrInvalidPath}` listed in the plan's `<verification>` block.

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
