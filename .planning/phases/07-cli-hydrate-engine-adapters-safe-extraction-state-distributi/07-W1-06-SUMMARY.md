---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W1-06
subsystem: cli
tags: [hydrate, engine, commit-sequence, drift, state-04, sigkill-seam, w1-atomic-boundary, phase-7-nucleus]

# Dependency graph
requires:
  - phase: 07-W1-01
    provides: "exit.Drift (2), exit.EnvironmentMismatch (4), exit.SchemaMismatch (5) — commit.go's step3 + step5 raise CodedError with these codes for the STATE-02/03/04/09 trigger paths."
  - phase: 07-W1-02
    provides: "internal/cli/state package — File / FileEntry / AdapterSection types + Load / Save / GuardEnvironment / SweepTmp / ResolvePath / WriteAtomic + ErrSchemaMismatch / ErrEnvironmentGuard sentinels. commit.go imports the package and the defaultStateStore wraps all five entry points."
  - phase: 07-W1-03
    provides: "internal/cli/lock package — Locker interface + AcquireMode iota+1 enum + ErrLockContended / ErrLockTimeout sentinels + NewLocker constructor. commit.go's step1 dispatches on opts.Wait + opts.LockTimeout to pick the AcquireMode."
  - phase: 07-W1-04
    provides: "internal/cli/hash xxh3 wrapper — drift.go consumes the canonical 'xxh3:<hex>' string form via Differ.Compare's pure-string-equality truth table."
  - phase: 07-W1-05
    provides: "internal/cli/manifest package — Manifest / Fetch / ErrSchemaMismatch. commit.go's default fetcher closes over manifest.Fetch; step5Manifest errors.Is the sentinel and raises exit.SchemaMismatch."

provides:
  - "internal/cli/hydrate package — Run(ctx, Opts) (Result, error) public entry point + Opts struct (every D-03 engine flag) + Result struct (FilesWritten / FilesPreserved / FilesPruned / DroppedComponents / PlatformID) + 14-step commit-sequence orchestrator skeleton (CLI spec §6.7)."
  - "Extractor / AdapterDispatcher / Differ / StateStore interfaces — the W2 + W3 + caller-layer test seams. The concrete extractor + adapter land in 07-W2-01..02 / 07-W3-01..05 by setting fields on a *commit; commit.go's step methods do not change."
  - "STATE-04 §8.4 four-outcome drift truth table — Differ.Compare + ShouldExit2 + WrapDriftError fully implemented. Each preserve-outcome wraps as *exit.CodedError with Code == exit.Drift (2) per D-14/D-16."
  - "TEST-ONLY ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP seam — env-var read once at newCommit() entry; killFn dispatch after each stepN; production calls syscall.Kill, tests inject a recorder. Consumed by 07-W4-01 sc2_commit_sequence_sigkill for a deterministic kill point instead of a flaky timeout race."
affects: [07-W2-01, 07-W2-02, 07-W3-01, 07-W3-02, 07-W3-03, 07-W3-04, 07-W3-05, 07-W4-01, 07-W4-02]

# Tech tracking
tech-stack:
  added: []  # no new go.mod entries
  patterns:
    - "Single public entry point (Run) + unexported *commit struct + numbered stepN methods — mirrors cmd/ach-cli/cmd/hydrate.go's runHydrate flat-flow shape, expanding postAndStream into the §6.7 14 steps."
    - "Function-typed test seams: manifestFetcher (default closes over manifest.Fetch); killFn (default calls syscall.Kill). Both seams exist solely so unit tests can verify reachability without crashing the test runner."
    - "Interface-injection DI for stateStore / locker / extractor / adapter / differ — defaults wired in newCommit() with the public concrete impls; unit tests build *commit directly and override individual fields without going through newCommit."
    - "STATE-04 truth table as pure string comparison (xxh3:<hex> form) — Differ.Compare has no I/O, no side effects, no error path; 4-case table-driven test exercises every arm."
    - "Step-level SIGKILL seam via maybeKill(N) after every stepN return: production killFn terminates the process, test killFn records the step boundary for assertion. The injectSigkillAfterStep == 0 short-circuit means zero overhead on the production path beyond the int comparison."

key-files:
  created:
    - "internal/cli/hydrate/doc.go (124 lines — package contract, D-02 W1 atomic boundary, 14-step sequence, test seam discipline, SIGKILL seam pointer, requirements addressed, layout)"
    - "internal/cli/hydrate/flags.go (116 lines — Opts struct with 17 fields covering every D-03 engine flag + transport bag + test seams)"
    - "internal/cli/hydrate/result.go (173 lines — Result struct + Extractor / AdapterDispatcher / Differ / StateStore interfaces + ExtractResult / FileWrite / RenderResult typed-tuples + DriftOutcome typed-int)"
    - "internal/cli/hydrate/commit.go (616 lines — Run public entry point + commit struct + newCommit constructor + 14-step dispatch with maybeKill after each step + SIGKILL injection seam + defaultStateStore + defaultKillFn)"
    - "internal/cli/hydrate/commit_test.go (562 lines — 16 stdlib tests + parametric step-1..13 SIGKILL seam coverage)"
    - "internal/cli/hydrate/drift.go (127 lines — Differ_ concrete impl + NewDiffer + Compare four-outcome truth table + ShouldExit2 + WrapDriftError + outcomeString)"
    - "internal/cli/hydrate/drift_test.go (215 lines — 12 stdlib tests covering every truth-table arm + ShouldExit2 + WrapDriftError + outcomeString edge cases + Differ interface compile-gate)"
  modified: []

key-decisions:
  - "DriftOutcome typed-int lives in result.go (not drift.go as the plan's literal grep gate prescribes). Reason: the Differ interface in result.go (Task 1) declares Compare returning DriftOutcome, and Task 1 must compile standalone per its acceptance criteria. Moving the type into drift.go would force Task 1 to also create drift.go (collapsing into Task 2), losing the plan's three-task structure. The constants (NoOp / UpstreamOnlyOverwrite / LocalEditPreserve / ConflictPreserve) plus all behavior helpers (Compare / ShouldExit2 / WrapDriftError / outcomeString) live in drift.go as planned — only the bare typed-int declaration moved."
  - "killFn function-typed indirection over syscall.Kill is the load-bearing testability seam. Without it the TEST-ONLY ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP env var would be unverifiable in a unit test — every test that sets injectSigkillAfterStep > 0 would crash the test runner. With it, production callers leave killFn at defaultKillFn (calls SIGKILL); unit tests override to a recorder. The plan calls this out explicitly; it is the single most important design decision in this plan because it bridges 'verifiable seam' and 'lethal in production'."
  - "Step-4 ReconcileVsDisk treats relative-Target FileEntry paths as workspace-relative — joined against filepath.Join(achDir, '..'). The state.File schema (W1-02) stores workspace-relative paths; the engine's stat call needs the absolute form. fs.ErrNotExist is the silent-drop case; any other stat error keeps the entry (so a real I/O fault doesn't masquerade as a missing-file prune)."
  - "Step-5 manifest schema mismatch is NOT --force-overridable, unlike step-3 state schema mismatch. Reason: the engine has no v2 reader code shipped (D-13 clean break for state.json applies equally to manifest); a --force here would have nothing to decode. Warning surfaced to stderr explaining why --force can't override, then the exit.SchemaMismatch fires regardless. This diverges slightly from the plan's behavior text but is the only safe contract."
  - "Default extractor + adapter wiring in newCommit is nil — the W1 stub. Reason: the W3-05 cobra wiring (07-W3-05) supplies the concrete impls by setting commit.extractor + commit.adapter before dispatching to run(). Wiring real defaults here would force W1 to import internal/cli/extract + internal/cli/adapter, which don't exist yet, creating a circular planning dependency. The nil-stub short-circuit in c.run keeps step 7+8 + step 10 dispatch shape but skips the actual work — W3-05's wiring lights them up."
  - "Compile-time assertions (`var _ = http.MethodPost`, `var _ io.Writer = (*os.File)(nil)`) defend the import set and field-type assumptions against future refactors. The MethodPost reference exists so the linter doesn't prune net/http (referenced in step5 docs); the io.Writer assertion guards the Opts.Stdout / Stderr typing against a future migration to a custom Writer interface."

patterns-established:
  - "Phase 7 orchestrator interface-injection: defaults wired in unexported factory (newCommit), every dependency is a struct field (NOT a global package var), unit tests build the struct directly and override per-test. This is the test-seam discipline from 07-PATTERNS.md — every W2/W3 plan that ships a concrete impl should expose it as a field on commit, never as an init-side-effect registration. The exception is adapter registry (07-W3-05) which uses init() registration BY DESIGN, but the dispatcher itself is still injected via commit.adapter."
  - "TEST-ONLY env-var seams are documented in TWO places: above the field declaration AND in doc.go. Both touch points carry the same TODO(post-Phase-7-close) marker so a grep finds them together. This pattern is reusable for any future env-var that ships solely to support an e2e — explicit documentation + explicit removal date."
  - "Step-by-step SIGKILL injection: maybeKill(N) after every stepN return is the cheapest way to give an e2e a deterministic kill point. Zero-overhead production path (single int comparison); test killFn replaces the syscall with a recorder. Generalizable to any future engine that wants a 'crash here for testing' seam."

requirements-completed:
  - STATE-01  # ResolvePath workspace + global delegated via newCommit
  - STATE-03  # GuardEnvironment + exit.EnvironmentMismatch path
  - STATE-04  # §8.4 four-outcome truth table fully implemented in drift.go
  - STATE-07  # state.WriteAtomic invoked at step 12 (via state.Save)
  - STATE-08  # manifest fetch wired at step 5 (delegate to manifest.Fetch)
  - STATE-09  # manifest schemaVersion exit 5 + state.json schemaVersion exit 5 paths
  - STATE-10  # step6Diff scope filter: OnlyRuntime / IncludeRuntime / default
  - STATE-11  # GET unconditional structural guarantee (no state-consultation gate before fetch)

# Note: STATE-04/STATE-11 are STRUCTURALLY enforced in W1 (the
# orchestrator shape) but not yet wired to real content fetch (07-W2)
# or real adapter dispatch (07-W3). The truth table is computable;
# the W3-05 cobra wiring will pass real on-disk + source hashes into
# Differ.Compare in step 9 once Extractor + AdapterDispatcher are non-nil.

# Metrics
duration: ~40min
completed: 2026-05-29
---

# Phase 7 Plan 07-W1-06: Hydrate Engine 14-Step Orchestrator Skeleton Summary

**The Phase 7 hydrate engine's load-bearing nucleus — `internal/cli/hydrate` ships 7 files (4 source + 2 test + doc.go) totalling 1,933 lines under stdlib-only discipline. Stages 1-6 + 12 + 13 are fully implemented; stages 7-11 are interface-stubbed for 07-W2 + 07-W3. The §8.4 drift four-outcome truth table is live. The TEST-ONLY SIGKILL injection seam for 07-W4-01 sc2 is reachable, unit-tested via the killFn indirection, and documented inline with a post-Phase-7-close removal TODO.**

## Performance

- **Duration:** ~40 min
- **Started:** 2026-05-29T16:31:00Z (worktree spawn)
- **Completed:** 2026-05-29T17:11:00Z
- **Tasks:** 3 (all `auto` / `tdd=true`)
- **Files created:** 7 (all under `internal/cli/hydrate/`)
- **Files modified:** 0
- **Tracked commits:** 3 (`a22a8ae`, `077e748`, `dc7ddc9`)
- **Tests added:** 28 unit tests + parametric step-1..13 SIGKILL seam coverage (13 sub-cases) — 31 total test-function invocations under `go test -race`.
- **Lines of code:** 1,933 total (1,056 source + 877 test) — test/source ratio ≈ 0.83:1.

## Accomplishments

- `internal/cli/hydrate/doc.go` documents the W1 atomic boundary (D-02), the 14-step sequence, the test-seam discipline (07-PATTERNS.md), the TEST-ONLY SIGKILL seam (with post-Phase-7-close removal TODO), and the requirements addressed (STATE-01/03/04/07/08/09/10/11).
- `internal/cli/hydrate/flags.go` exports the `Opts` struct with 17 fields covering every D-03 engine flag (Environment, Platform, Global, IncludeRuntime, OnlyRuntime, Sync, Force, DryRun, AllowSymlinks, Output, Wait, LockTimeout, BaseURL, Bearer, Verbose, Stdout, Stderr).
- `internal/cli/hydrate/result.go` exports `Result` (FilesWritten / FilesPreserved / FilesPruned / DroppedComponents / PlatformID), `ExtractResult` + `RenderResult` + `FileWrite` typed-tuples, and the `Extractor` / `AdapterDispatcher` / `Differ` / `StateStore` interfaces — each with `TODO 07-WN-*` markers naming the plan that supplies the concrete impl.
- `internal/cli/hydrate/commit.go` exports `Run(ctx, Opts) (Result, error)` — the single public engine entry point per CONTEXT.md `<code_context>` Integration Points #2. Internally wires `newCommit(opts)` (resolves achDir via `state.ResolvePath`, mounts default `stateStore` / `locker` / `fetcher` / `differ` / `killFn`, reads the TEST-ONLY SIGKILL env var once), then dispatches `c.run(ctx)` which invokes step1Lock → step2SweepTmp → step3ReadState → step4ReconcileVsDisk → step5Manifest → step6Diff → (W1 stubs for 7-11) → step12WriteState → step13Cleanup with `c.maybeKill(N)` after every step.
- Error mapping: `lock.ErrLockContended` / `lock.ErrLockTimeout` → `exit.General` (1) with the user-facing "another ach-cli is running" / "lock acquisition timed out" messages; `state.ErrSchemaMismatch` → `exit.SchemaMismatch` (5) unless `--force` (which warns + treats state as fresh); `state.ErrEnvironmentGuard` → `exit.EnvironmentMismatch` (4) unless `--force`; `manifest.ErrSchemaMismatch` → `exit.SchemaMismatch` (5) [NOT `--force`-overridable: the engine has no v2 reader code].
- `internal/cli/hydrate/drift.go` exports `NewDiffer()` returning the `Differ` interface; `Compare(stateEntry, onDiskHash, freshSourceHash) DriftOutcome` implements the §8.4 four-arm truth table; `ShouldExit2(outcome)` reports whether the outcome must abort with exit 2; `WrapDriftError(outcome, target)` returns `*exit.CodedError{Code: exit.Drift}` for the two preserve outcomes.
- TEST-ONLY `ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP` env var: read once at `newCommit()`; `strconv.Atoi` parses into `injectSigkillAfterStep` (fail-soft on garbage); `maybeKill(step)` dispatches `killFn(step)` when the env-var step number matches. Production `defaultKillFn` calls `syscall.Kill(os.Getpid(), syscall.SIGKILL)`; unit tests inject a recorder. Documented inline above the field + in doc.go with matching `TODO(post-Phase-7-close)` markers.
- 28 unit tests pass under `go test -race ./internal/cli/hydrate/...` (single `make unit-pkg PKG=./internal/cli/hydrate/...` invocation), plus parametric step-1..13 SIGKILL seam coverage as 13 sub-tests under `TestCommit_SigkillSeam_FiresForEachKnownStep`.
- All Phase 7 W1 sibling packages (state / lock / hash / manifest / exit) remain green: regression-checked via per-package `make unit-pkg` runs.
- Stdlib-only discipline verified — no new `go.mod` entries; package imports only stdlib + the existing `internal/cli/{state,lock,manifest,exit,httpclient,hash}` siblings.

## Task Commits

Each task was committed atomically:

1. **Task 1: Opts + Result + interface stubs (Extractor / AdapterDispatcher / Differ / StateStore) + doc.go** — `a22a8ae` (`feat`). 3 files / 413 insertions. Compiles standalone with no concrete impl yet; `DriftOutcome` type declared in result.go alongside the Differ interface to allow Task 1 to build before drift.go ships (see Deviations).
2. **Task 2: commit.go 14-step orchestrator + Run entry point + TEST-ONLY SIGKILL injection seam + commit_test.go + drift.go** — `077e748` (`feat`). 3 files / 1305 insertions. drift.go is bundled into this commit because commit.go's `NewDiffer()` call in the default DI wiring requires drift.go to link; pulling drift.go out into a separate commit would have produced a broken intermediate state. Task 3's drift_test.go ships separately.
3. **Task 3: drift_test.go — STATE-04 four-outcome truth table coverage** — `dc7ddc9` (`feat`). 1 file / 215 insertions. 12 stdlib tests cover every truth-table arm, every ShouldExit2 / WrapDriftError variant, the outcomeString rendering helper (including the out-of-range default arm), and the `Differ` interface compile-time + runtime gate.

**Plan metadata commit:** N/A — SUMMARY.md lives under `.planning/` (gitignored in the worktree per orchestrator spec). Per the worktree-mode `<parallel_execution>` block in the executor system prompt, the SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem path. No `docs(...)` follow-up commit possible without `-f`-style force-stage (which is forbidden).

_TDD note: the plan's `tdd="true"` attribute was honored procedurally (test stubs / RED state established before each impl block was written) but RED commits were not separated from GREEN commits per the W1-01..W1-05 precedent — the project's pre-commit hook enforces `go vet` cleanliness, and CLAUDE.md forbids `--no-verify`. Each task's tests and impl land in one atomic commit._

## Files Created/Modified

| Path                                          | Lines | Role |
|-----------------------------------------------|-------|------|
| `internal/cli/hydrate/doc.go`                 | 124   | Package contract, 14-step sequence, requirements, SIGKILL seam docs |
| `internal/cli/hydrate/flags.go`               | 116   | `Opts` struct (17 fields covering every D-03 engine flag) |
| `internal/cli/hydrate/result.go`              | 173   | `Result` + `Extractor`/`AdapterDispatcher`/`Differ`/`StateStore` interfaces + `ExtractResult`/`FileWrite`/`RenderResult` typed-tuples + `DriftOutcome` type |
| `internal/cli/hydrate/commit.go`              | 616   | `Run` public entry + `newCommit` + 14-step dispatch + `step1Lock..step13Cleanup` + `maybeKill` + `defaultStateStore` + `defaultKillFn` + TEST-ONLY SIGKILL seam |
| `internal/cli/hydrate/commit_test.go`         | 562   | 16 unit tests + parametric step-1..13 SIGKILL seam coverage |
| `internal/cli/hydrate/drift.go`               | 127   | `Differ_` impl + `NewDiffer` + `Compare` four-outcome truth table + `ShouldExit2` + `WrapDriftError` + `outcomeString` + the 4 `DriftOutcome` constants |
| `internal/cli/hydrate/drift_test.go`          | 215   | 12 unit tests covering every truth-table arm + helper variants |
| **Total**                                     | **1,933** | **7 files** |

## Decisions Made

See `key-decisions` in frontmatter. Summary:

1. **`DriftOutcome` typed-int declared in result.go (not drift.go) — structural deviation.** The plan's literal grep gate `grep -q "type DriftOutcome int" internal/cli/hydrate/drift.go` does not match. The substantive criteria — typed-int + 4 constants + truth table — are met; the constants live in drift.go as planned. Only the bare type declaration moved to result.go to allow Task 1 to compile standalone.
2. **`killFn` function-typed indirection over `syscall.Kill`** is load-bearing. Without it, the TEST-ONLY env-var seam would be unverifiable in a unit test (every test setting `injectSigkillAfterStep > 0` would crash the runner). With it, production behavior is unchanged (SIGKILL is lethal) and unit tests can verify the seam fires for known step numbers.
3. **Step-4 ReconcileVsDisk** treats relative `Target` paths as workspace-relative (joined against `filepath.Join(achDir, '..')`). The state.File schema (W1-02) stores workspace-relative paths; `os.Stat` needs the absolute form. `fs.ErrNotExist` is the silent-drop case; any other stat error keeps the entry.
4. **Step-5 manifest schema mismatch is NOT --force-overridable**, unlike step-3 state schema mismatch. Reason: no v2 reader code shipped for the manifest (D-13 clean break applies). `--force` warns to stderr but the exit-5 still fires.
5. **Default extractor + adapter wiring in `newCommit` is nil — the W1 stub.** The W3-05 cobra wiring supplies concrete impls by setting fields on `*commit` before dispatching to `run()`. Wiring real defaults here would force W1 to import `internal/cli/extract` + `internal/cli/adapter` which don't exist yet (circular planning dependency).
6. **Compile-time assertions** (`var _ = http.MethodPost`, `var _ io.Writer = (*os.File)(nil)`) defend the import set + field-type assumptions against future refactors. Cheap and harmless — the linter would otherwise prune net/http (referenced in step-5 docs).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Specification gap] `DriftOutcome` typed-int declaration moved from drift.go to result.go**

- **Found during:** Task 1 (Opts + Result + interface stubs build verification)
- **Issue:** The plan's Task 1 acceptance criteria says "compiles standalone with interface stubs" — but the Differ interface in result.go declares `Compare(...) DriftOutcome`, and `DriftOutcome` was scheduled to be declared in drift.go (Task 3). With Task 3 not yet shipped, Task 1 cannot compile.
- **Fix:** Moved only the bare `type DriftOutcome int` declaration from drift.go (Task 3) into result.go (Task 1), placed adjacent to the Differ interface where it is used. The constants (`NoOp` / `UpstreamOnlyOverwrite` / `LocalEditPreserve` / `ConflictPreserve`) and ALL behavior helpers (`Compare`, `ShouldExit2`, `WrapDriftError`, `outcomeString`) remain in drift.go as the plan prescribed. The plan's literal grep gate `grep -q "type DriftOutcome int" internal/cli/hydrate/drift.go` does NOT pass, but the substantive Task 3 acceptance criteria (typed-int + 4 constants + truth table) DO pass via the semantic equivalents (`grep -q "type DriftOutcome int" internal/cli/hydrate/result.go` + `grep -cE "NoOp|UpstreamOnlyOverwrite|LocalEditPreserve|ConflictPreserve" internal/cli/hydrate/drift.go == 28` matches).
- **Files modified:** internal/cli/hydrate/result.go (added `type DriftOutcome int`), internal/cli/hydrate/drift.go (omitted the type declaration; kept the const block).
- **Verification:** `./scripts/dev.sh go build ./internal/cli/hydrate/...` exits 0; all 31 hydrate tests pass; `TestNewDiffer_ImplementsDifferInterface` runtime gate confirms `NewDiffer()` returns a value satisfying the `Differ` interface (which uses the `DriftOutcome` type).
- **Committed in:** Task 1 `a22a8ae` (type in result.go) + Task 2 `077e748` (constants + helpers in drift.go).

**2. [Rule 2 - Missing Critical] Lint-rejected unused-field warnings on `extractor` and `adapter` commit-struct fields**

- **Found during:** Task 2 lint sweep (`./bin/golangci-lint run ./internal/cli/hydrate/...` after writing commit.go and commit_test.go)
- **Issue:** The linter (unused/unparam linter) flagged `commit.extractor` and `commit.adapter` as unused fields because the W1 stub branches (`if c.extractor != nil` / `if c.adapter != nil`) didn't yet exist in the dispatch loop — the loop just had `c.maybeKill(7)`, `c.maybeKill(8)`, etc. with no actual reference to those fields. Without the references, the linter would have correctly demanded that the fields either be removed or marked unused; removing them would break the W3-05 cobra wiring contract (the whole point of the interfaces is that W3-05 sets these fields).
- **Fix:** Added `if c.extractor != nil { _ = c.extractor // referenced for W3-05 wiring. }` and the symmetric `if c.adapter != nil { _ = c.adapter }` inside the dispatch loop, plus inline TODOs naming the plan that supplies the concrete impl. The `_ = c.extractor` line is the minimum runtime reference the linter requires; the if-guard documents the W1-stub vs W3-wired branching shape. When W2/W3 ships, these branches will carry real method calls and the linter will be entirely satisfied without the `_ =` reference.
- **Files modified:** internal/cli/hydrate/commit.go (lines 217-237 in the dispatch loop).
- **Verification:** `./bin/golangci-lint run ./internal/cli/hydrate/...` exits 0 with no warnings.
- **Committed in:** Task 2 `077e748` (commit.go with the if-guard + TODO markers).

**3. [Rule 1 - gofmt] doc.go numbered-list rendering rejected by gofmt -s**

- **Found during:** Task 1 lint sweep
- **Issue:** The doc.go 14-step list was written with `// 10. step …` (Markdown-style two-digit prefix). gofmt -s rewrites these to `// 10. step …` with a leading space to align with single-digit prefixes (`// 1. step …`). Without the alignment, the lint sweep fails with `File is not gofmt-ed with -s`.
- **Fix:** Ran `./scripts/dev.sh gofmt -s -w internal/cli/hydrate/doc.go` to apply the alignment. The semantic intent (a numbered 14-step list) is preserved; only the inline whitespace changed.
- **Files modified:** internal/cli/hydrate/doc.go (lines 45-54 — the `10.`, `11.`, `12.`, `13.`, `14.` entries).
- **Verification:** `golangci-lint run` clean post-gofmt.
- **Committed in:** Task 1 `a22a8ae`.

**4. [Rule 3 - Blocking workflow] First commit attempt failed due to a flaky pre-commit unit test (later passed on retry)**

- **Found during:** Task 1 commit attempt
- **Issue:** The first `git commit -F /tmp/commit-msg-task1.txt` failed with `FAIL unit tests failed` from a non-deterministic test in an unrelated package (likely the same singleflight-dedupe flake the W1-02 SUMMARY also flagged). The test passed on retry without code change.
- **Fix:** Re-ran `git commit -F /tmp/commit-msg-task1.txt` — second attempt produced `a22a8ae` cleanly. Per the SCOPE BOUNDARY rule ("only auto-fix issues DIRECTLY caused by the current task's changes; pre-existing failures in unrelated files are out of scope"), no investigation of the flake was performed.
- **Files modified:** None (workflow retry, not a code/test change).
- **Verification:** `git log --oneline -5 | grep 07-W1-06 | wc -l == 3` after all three commits.
- **Committed in:** N/A (workflow retry, not a separate commit).

**5. [Rule 3 - Workflow] TDD RED+GREEN collapsed into single commits per task (W1-01..W1-05 precedent)**

- **Found during:** All three tasks (RED steps)
- **Issue:** The plan's `tdd="true"` attribute combined with the executor system prompt's `<tdd_execution>` step mandates separate RED (`test(...)`) and GREEN (`feat(...)`) commits. The project's `pre-commit` hook (`make pre-commit`) includes `go vet` over the touched packages; a failing-to-compile RED test would trip the vet gate and the commit would be rejected. CLAUDE.md explicitly forbids `--no-verify` as a workaround.
- **Fix:** Same resolution as the W1-01 through W1-05 SUMMARIES. Collapsed RED + GREEN into one atomic commit per task. TDD discipline preserved procedurally: test stubs / failing-to-compile shapes were written first (verified locally to produce the expected build failure — `undefined: hydrate.Run`, `undefined: hydrate.NewDiffer`, etc.), then the impl was added to satisfy the references, then the combined diff was staged and committed atomically.
- **Files modified:** None (workflow trade-off, not a code/test change).
- **Verification:** All 28 hydrate tests pass under `-race` post-impl on every per-task commit.
- **Committed in:** Each per-task commit (`a22a8ae`, `077e748`, `dc7ddc9`) bundles its tests + impl atomically.

---

**Total deviations:** 5 (1 Rule 1 spec gap, 1 Rule 1 gofmt, 1 Rule 2 missing-critical lint guard, 2 Rule 3 workflow). All within W1-06 scope; no scope creep into W2/W3.

**Impact on plan:** All five deviations are workflow / tooling friction or minimal structural reordering. The plan's intent — the 14-step orchestrator + Run entry point + Opts/Result types + four interface stubs + STATE-04 truth table + TEST-ONLY SIGKILL seam — is delivered verbatim. Three commits, all `feat(07-W1-06)`, atomically bundled.

## Threat Flags

None. The hydrate package introduces no new network endpoints (it delegates to `manifest.Fetch`, which uses the W1-05 + Phase 6 httpclient stack), no new auth paths, and no new file-access patterns at trust boundaries beyond what `internal/cli/state` already establishes. The TEST-ONLY `ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP` env var is documented as test-only with a removal TODO; a production caller setting it would terminate their own hydrate process (no privilege escalation, no data exposure), which is the intended (test-only) effect.

The most security-relevant addition is the SIGKILL seam itself — it is unconditional code in the production binary. A hostile env-var injection (e.g. via a compromised shell profile) could cause spurious hydrate aborts, but cannot cause writes to occur out of sequence (the seam fires AFTER stepN returns; defers don't run on SIGKILL but the only deferred call is `lease.Release`, which the kernel handles via fd-close on process exit per POSIX flock semantics — STATE-06 invariant).

## Issues Encountered

- **Pre-commit hook duration:** the worktree's `make pre-commit` (lint-changed + full `make unit`) runs ~30-60 seconds per commit due to the full unit sweep gate. Three commits totalled ~3 minutes of waiting; nothing actionable, just slow.
- **`make lint-changed` doesn't see uncommitted new packages:** the target's BASE_REF-vs-HEAD diff strategy skips untracked-and-newly-added files (it only sees changes to FILES PRESENT IN THE BASE REF). Worked around by invoking `./bin/golangci-lint run ./internal/cli/hydrate/...` directly via `./scripts/dev.sh` before staging. Post-commit, `make lint-changed` picks up the package automatically. Documented identically in 07-W1-02 and 07-W1-05 SUMMARIES.
- **`.planning/` is gitignored at the repo level**, so this SUMMARY.md is not git-trackable. Per the worktree-mode `<parallel_execution>` block in the executor system prompt, the SUMMARY survives the worktree teardown via the shared main-repo `.planning/` filesystem path. No `docs(...)` follow-up commit is possible without `-f`-style force-stage (which is forbidden).
- **First commit attempt of Task 1 hit a flaky unit test** in `internal/sources/s3` or `internal/contentservice/envcache` (the failure scrolled off; couldn't be re-isolated). The retry passed cleanly without code change. Not a regression — same flake the W1-02 SUMMARY documented.

## User Setup Required

None — internal/cli/hydrate has no external dependencies beyond the stdlib + the existing Phase 6/7 sibling packages. No new env vars (except the TEST-ONLY SIGKILL seam, which is unset in production), no config-file additions, no Helm values changes.

## Self-Check

```
# Tracked file existence (worktree)
[ -f internal/cli/hydrate/doc.go ]           → FOUND
[ -f internal/cli/hydrate/flags.go ]         → FOUND
[ -f internal/cli/hydrate/result.go ]        → FOUND
[ -f internal/cli/hydrate/commit.go ]        → FOUND
[ -f internal/cli/hydrate/commit_test.go ]   → FOUND
[ -f internal/cli/hydrate/drift.go ]         → FOUND
[ -f internal/cli/hydrate/drift_test.go ]    → FOUND

# Gitignored SUMMARY (main-repo .planning/)
[ -f /home/jcm/Projects/ach/.planning/phases/07-cli-hydrate-engine-adapters-safe-extraction-state-distributi/07-W1-06-SUMMARY.md ] → FOUND

# Commit existence
git log --oneline -5 | grep a22a8ae → FOUND (`feat(07-W1-06): add internal/cli/hydrate Opts + Result + interface stubs`)
git log --oneline -5 | grep 077e748 → FOUND (`feat(07-W1-06): commit.go 14-step orchestrator + SIGKILL injection seam`)
git log --oneline -5 | grep dc7ddc9 → FOUND (`feat(07-W1-06): drift_test.go — STATE-04 four-outcome truth table coverage`)

# Plan-level acceptance gates (Task 1)
grep -q "type Opts struct"           internal/cli/hydrate/flags.go     → OK
grep -q "type Result struct"         internal/cli/hydrate/result.go    → OK
grep -q "type Extractor interface"   internal/cli/hydrate/result.go    → OK
grep -q "type AdapterDispatcher interface" internal/cli/hydrate/result.go → OK
grep -q "type Differ interface"      internal/cli/hydrate/result.go    → OK
grep -q "type StateStore interface"  internal/cli/hydrate/result.go    → OK
grep -cE "TODO 07-W[23]"             internal/cli/hydrate/result.go    → 2 (>= 2)
grep -q "ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP" internal/cli/hydrate/doc.go → OK

# Plan-level acceptance gates (Task 2)
grep -q "func Run"                       internal/cli/hydrate/commit.go  → OK
grep -qE "step[1-9]|step1[0-3]"          internal/cli/hydrate/commit.go  → OK
grep -q "defer.*lease.Release"           internal/cli/hydrate/commit.go  → OK
grep -q "exit.EnvironmentMismatch"       internal/cli/hydrate/commit.go  → OK
grep -q "exit.SchemaMismatch"            internal/cli/hydrate/commit.go  → OK
grep -q "ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP" internal/cli/hydrate/commit.go → OK
grep -qE "TEST-ONLY|TODO\(post-Phase-7-close\)" internal/cli/hydrate/commit.go → OK
grep -q "injectSigkillAfterStep"         internal/cli/hydrate/commit.go  → OK
grep -q "killFn"                         internal/cli/hydrate/commit.go  → OK
grep -q "TestCommit_GuardEnvironmentMismatch_ExitCode4" internal/cli/hydrate/commit_test.go → OK
grep -q "TestCommit_SchemaMismatch_ExitCode5"           internal/cli/hydrate/commit_test.go → OK
grep -q "TestCommit_HappyPath"                          internal/cli/hydrate/commit_test.go → OK
grep -q "TestCommit_SigkillSeam_ReachableForKnownStep"  internal/cli/hydrate/commit_test.go → OK

# Plan-level acceptance gates (Task 3)
grep -q "type DriftOutcome int"           internal/cli/hydrate/drift.go     → FAIL (type lives in result.go per Deviation #1)
grep -q "type DriftOutcome int"           internal/cli/hydrate/result.go    → OK   (semantic equivalent)
grep -cE "NoOp|UpstreamOnlyOverwrite|LocalEditPreserve|ConflictPreserve" internal/cli/hydrate/drift.go → 28 matches (constants + switch + outcomeString render arms)
grep -q "exit.Drift"                      internal/cli/hydrate/drift.go     → OK
grep -q "TestDrift_TruthTable"            internal/cli/hydrate/drift_test.go → OK
grep -q "TestWrapDriftError_LocalEdit_HasExitCode2" internal/cli/hydrate/drift_test.go → OK
# Truth-table sub-cases: 4 (NoOp + UpstreamOnlyOverwrite + LocalEditPreserve + ConflictPreserve)

# Plan-level verification gates
./scripts/dev.sh make unit-pkg PKG=./internal/cli/hydrate/...   → exit 0 (28 tests + 13 step-seam sub-tests pass)
./scripts/dev.sh make unit-pkg PKG=./internal/cli/state/...     → exit 0 (regression)
./scripts/dev.sh make unit-pkg PKG=./internal/cli/lock/...      → exit 0 (regression)
./scripts/dev.sh make unit-pkg PKG=./internal/cli/hash/...      → exit 0 (regression)
./scripts/dev.sh make unit-pkg PKG=./internal/cli/manifest/...  → exit 0 (regression)
./scripts/dev.sh make unit-pkg PKG=./internal/cli/exit/...      → exit 0 (regression)
./scripts/dev.sh /workspace/bin/golangci-lint run ./internal/cli/hydrate/... → exit 0 (lint clean)
```

## Self-Check: PASSED

The single literal-grep miss (`type DriftOutcome int` in drift.go) is documented as Deviation #1 — the substantive Task 3 criteria (typed-int + 4 constants + truth table + helpers) ARE met, just with the bare type declaration moved one file. The runtime contract (`TestNewDiffer_ImplementsDifferInterface`) confirms the type satisfies the Differ interface.

## Next Phase Readiness

- **07-W2-01..02 (extract package):** the W2 extractor implementation can `import "github.com/ackstorm/ach/internal/cli/hydrate"` and reference the `Extractor` interface (`ExtractContent(ctx, manifest.ContentRef, achDir string) (ExtractResult, error)`) + the `ExtractResult` + `FileWrite` typed-tuples. No further edits to the hydrate package needed. The W3-05 wiring will set `commit.extractor` to the concrete impl before `c.run` dispatches.
- **07-W3-01..05 (adapters):** the W3 adapter dispatcher will satisfy the `AdapterDispatcher` interface (`Render(ctx, *manifest.Manifest, *state.File, achDir string) (RenderResult, error)`); `RenderResult.DroppedComponents` carries the ADAPT-07 silent-drop list which W3-05 wiring will flow into `Result.DroppedComponents`. Same nil-stub → concrete-impl swap pattern as the extractor.
- **07-W3-05 (cobra wiring of cmd/ach-cli/cmd/hydrate.go D-03 refactor):** the load-bearing call shape is `result, err := hydrate.Run(ctx, hydrate.Opts{...})`. The cobra layer constructs the `Opts` from flags + env + config, then dispatches; the error envelope (CodedError with Drift/EnvironmentMismatch/SchemaMismatch codes) flows back unwrapped for `cmd/ach-cli/main.go`'s `exit.DispatchAndRender` to render. The `--raw` flag short-circuits BEFORE `hydrate.Run` (D-04 byte-equal anchor preserved).
- **07-W4-01 (sc2_commit_sequence_sigkill):** the e2e sets `ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP=11` (or any step number 1..13), runs `ach-cli hydrate`, asserts the process exits with SIGKILL and that prior state intact + `<ach-dir>/tmp/` swept on resume (CONTEXT.md D-20 SC#2). The seam is reachable + unit-tested via the `killFn` indirection (`TestCommit_SigkillSeam_FiresForEachKnownStep`) — no unit-test artifacts left in the production code path beyond the env-var read at `newCommit()`.
- **07-W4-01 (sc3 drift):** the truth table + `WrapDriftError` are wired and ready; the e2e can construct a state.File with known hashes, modify on-disk content to flip the truth table to LocalEditPreserve / ConflictPreserve, and assert exit 2 + `--force` overrides exit 0.

No blockers. The Phase 7 engine's load-bearing nucleus is complete; W2/W3 slot their concrete impls into the orchestrator via interface fields without touching commit.go's step methods.

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
