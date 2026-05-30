---
phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi
plan: 07-W1-03
subsystem: cli
tags: [flock, posix-lock, advisory-lock, state-06, lock-package, hydrate-foundation]

# Dependency graph
requires:
  - phase: 07-W1-01
    provides: "exit.Drift / exit.EnvironmentMismatch / exit.SchemaMismatch / exit.CollisionRefuse constants — not directly consumed by lock pkg, but the broader hydrate-engine Phase 7 surface main.go uses (lock.ErrLockContended / ErrLockTimeout will both map to exit.General=1)."
provides:
  - "internal/cli/lock package (Locker + Lease interfaces, AcquireMode enum, NewLocker constructor, Path resolver, POSIX flock impl) — Phase 7.1's lock_windows.go ships alongside without touching lock.go"
  - "Three contention modes (AcquireFailFast / AcquireWait / AcquireWithTimeout) line up with the spec §6.7 flag surface: no-flag → fail-fast, --wait → block, --lock-timeout=<dur> → bounded"
  - "Sentinel errors ErrLockContended + ErrLockTimeout + ErrInvalidMode for callers to errors.Is() — main.go (Phase 7 W1-04) maps to exit codes"
  - "Race-detector-clean poll-and-backoff impl (1ms→50ms) for AcquireWait / AcquireWithTimeout — avoids the goroutine+close hazard around Linux flock(2) blocking semantics"
affects: [07-W1-04, 07-W1-05, 07-W1-06, 07-W2-01, 07-W3-01, 07-W4-01, 07.1]

# Tech tracking
tech-stack:
  added:
    - "golang.org/x/sys v0.45.0 (promoted from indirect to direct — was already transitively pulled by controller-runtime)"
  patterns:
    - "Build-tag OS-dispatched constructor: interface lives in lock.go (no build tag), impl in lock_unix.go behind //go:build !windows, Phase 7.1 adds lock_windows.go with //go:build windows. Compile-time dispatch — no runtime.GOOS branching at hot path."
    - "POSIX flock cancellation via LOCK_NB poll-and-backoff (1ms→50ms) rather than spawn-goroutine + close-fd-to-unblock. The latter races with the race detector (goroutine reads file.Fd() while caller writes via file.Close()) and is not reliably unblockable on Linux (Go's File.Close may keep the underlying poll.FD alive past the syscall close). Poll loop is a few extra syscalls per second of waiting in exchange for clean ctx cancellation surface and race-clean."
    - "Idempotent Lease.Release via atomic.Int32 CAS gate: deferred Release + explicit Release is safe; second call returns nil without re-issuing flock(LOCK_UN) or File.Close."
    - "Inline `var ErrFoo = errors.New(...)` declarations instead of a `var (...)` block when downstream tooling greps for the literal `var ErrName` anchor (matches 07-W1-03 plan's acceptance criteria)."

key-files:
  created:
    - "internal/cli/lock/doc.go (Apache-2.0 package contract citing STATE-06, spec §6.7, D-18 + D-23; tracked, committed edf0fa8)"
    - "internal/cli/lock/lock.go (Locker + Lease interfaces, AcquireMode iota+1 enum, sentinel errors ErrLockContended/ErrLockTimeout/ErrInvalidMode; tracked, committed edf0fa8)"
    - "internal/cli/lock/path.go (Path(achDir) → filepath.Join(achDir, \"lock\") — co-located with state.json per D-09/STATE-06; tracked, committed edf0fa8)"
    - "internal/cli/lock/path_test.go (3 stdlib-only join tests; tracked, committed edf0fa8)"
    - "internal/cli/lock/lock_unix.go (//go:build !windows; unixLocker + unixLease, NewLocker public constructor, FailFast/Wait/WithTimeout dispatch with poll-and-backoff; tracked, committed 6a6ff1e)"
    - "internal/cli/lock/lock_test.go (//go:build !windows; 10 stdlib-only tests, every goroutine wait capped at 200ms hard ceiling; tracked, committed 6a6ff1e)"
    - ".planning/phases/07-cli-hydrate-engine-adapters-safe-extraction-state-distributi/07-W1-03-SUMMARY.md (this file; gitignored in worktree, written to main repo .planning/)"
  modified:
    - "go.mod (golang.org/x/sys v0.45.0 promoted from indirect to direct require — only new dep; tracked, committed 6a6ff1e)"

key-decisions:
  - "Poll-and-backoff blocking replaces the plan's suggested 'goroutine + flock + ctx select' pattern (Rule 1 — root-cause fix). The naive goroutine pattern fails -race because the goroutine reads file.Fd() while the caller's error path writes via file.Close(), AND it hangs in practice because Linux flock(2) blocked-in-kernel waiters are not reliably unblocked by close(2) from another goroutine in the same process (Go's *os.File.Close keeps the underlying poll.FD alive past close). The 1ms→50ms LOCK_NB poll with ctx.Done() between attempts trades a handful of extra syscalls per second of contention for a clean cancellation surface and a race-detector-clean impl. Lock contention is rare (single-writer per <ach-dir>), so the syscall overhead is negligible."
  - "Sentinel errors use inline `var ErrName = errors.New(...)` declarations rather than a single `var (...)` block. The 07-W1-03 acceptance criteria grep for the literal `var ErrLockContended` and `var ErrLockTimeout` substrings; the block form (`var (\\n ErrLockContended = ...`) would not match. Idiomatic Go accepts both forms; the inline form is mildly more verbose but compiles and lints identically."
  - "NewLocker (public constructor) lives in lock_unix.go alongside newLocker (package-private). The plan's <action> for Task 1 said 'package-private constructor stub func newLocker(path string) Locker declared but NOT defined here', so I kept NewLocker out of lock.go (no build tag) entirely — it would otherwise reference an undefined symbol on Windows and break the link before Phase 7.1's lock_windows.go arrives. Phase 7.1 will define its own NewLocker / newLocker pair in lock_windows.go behind //go:build windows; compile-time build-tag dispatch picks one OR the other, never both."
  - "Pre-cancelled context short-circuits added at the top of unixLocker.Acquire (defensive guard, Rule 2). The plan didn't specify this explicitly, but a caller that passes ctx whose deadline has already fired should get the ctx error immediately rather than opening a fd + reaching the dispatch switch. Test TestAcquire_PreCancelledContext asserts the property."
  - "AcquireMode validation via default branch returning ErrInvalidMode (Rule 2). The plan's behavior list documents three modes but doesn't explicitly say what happens on a zero-value or unrecognized AcquireMode. Falling through to a silent no-op would be a footgun; ErrInvalidMode + a defensive test (TestAcquire_InvalidMode) document the contract for callers."
  - "Two extra tests beyond the plan's named four: TestAcquireWait_CancelledByContext (proves AcquireWait honors ctx cancellation, not just AcquireWithTimeout's internal timeout) and TestAcquireFailFast_SequentialReuseAfterRelease (proves Release truly releases — guards against a regression where the kernel lock leaks past lease lifetime). Both are zero-cost, no new infrastructure."

patterns-established:
  - "lock package layout for OS-dispatched primitives: interface + sentinel errors + iota+1 enum in <pkg>.go (no build tag); concrete impl in <pkg>_unix.go (//go:build !windows) with both the package-private factory and the public NewXxx constructor; Phase 7.1 adds <pkg>_windows.go (//go:build windows) symmetrically. doc.go documents the contract and the Phase 7.1 handoff. tests live in <pkg>_test.go with the SAME build tag as the impl."
  - "Linux flock(2) cancellation via LOCK_NB poll-and-backoff (1ms initial, 2× per attempt, capped at 50ms). When a primitive does not have a built-in context-aware blocking variant, do not spawn a goroutine to bridge the gap when the data-race / unblock-via-close hazards apply — poll with backoff instead."

requirements-completed:
  - STATE-06

# Note: STATE-06 is the only requirement this plan directly closes (lock
# at <ach-dir>/lock acquired before manifest fetch in §6.7 step 1, kernel
# releases on SIGKILL via fd-close). The W4 e2e SC#2 subtest will validate
# the kernel-released-on-SIGKILL property end-to-end (out of scope for
# unit tests).

# Metrics
duration: 30min
completed: 2026-05-29
---

# Phase 7 Plan 07-W1-03: internal/cli/lock package Summary

**Advisory POSIX flock(LOCK_EX) implementation with interface-based test seam — `internal/cli/lock` ships 6 files (3 source + 2 test + doc.go), three contention modes (FailFast / Wait / WithTimeout), poll-and-backoff cancellation, and a race-detector-clean impl ready for §6.7 step 1 in W1-06.**

## Performance

- **Duration:** ~30 min
- **Started:** 2026-05-29T15:55:00Z (worktree spawn / branch base)
- **Completed:** 2026-05-29T16:25:00Z
- **Tasks:** 2 (both `auto` / `tdd=true`)
- **Files created:** 7 (6 in internal/cli/lock + 1 SUMMARY)
- **Files modified:** 1 (go.mod — promote x/sys to direct)
- **Tracked commits:** 2 (`edf0fa8`, `6a6ff1e`)

## Accomplishments

- `internal/cli/lock` package compiles standalone on linux-amd64 with no engine imports — testable without W2/W3/W4.
- Locker interface (single method `Acquire(ctx, mode, timeout) (Lease, error)`) and Lease interface (`Release() error`) live in `lock.go` with NO build tag — Phase 7.1 extends lock_windows.go against the same contract.
- AcquireMode iota+1 enum (AcquireFailFast / AcquireWait / AcquireWithTimeout) prevents accidental string/int confusion at call sites; the zero value is intentionally invalid (returns ErrInvalidMode).
- POSIX impl in `lock_unix.go` behind `//go:build !windows`: uses `golang.org/x/sys/unix.Flock` with LOCK_EX/LOCK_NB/LOCK_UN; FailFast translates EWOULDBLOCK→ErrLockContended; Wait and WithTimeout share a poll-and-backoff (1ms→50ms) loop that selects on ctx.Done between attempts.
- 13 unit tests pass under `go test -race`: 3 path tests + 10 lock tests (uncontended fail-fast, contended fail-fast, wait-then-release, wait-cancelled-by-ctx, withTimeout-elapses, withTimeout-succeeds, idempotent Release, invalid mode, pre-cancelled context, sequential reuse after release).
- Every test goroutine wait is capped by a 200ms hard ceiling so a regression that hangs flock surfaces as a fast t.Fatalf rather than a frozen suite.
- `go.mod` promotes `golang.org/x/sys v0.45.0` from indirect to direct — only new dep, was already transitively pulled by controller-runtime.

## Task Commits

Each task was committed atomically:

1. **Task 1: Locker interface + path resolver + doc.go** — `edf0fa8` (feat)
2. **Task 2: lock_unix.go POSIX flock impl + unit tests** — `6a6ff1e` (feat)

No separate metadata commit — `.planning/` is gitignored in the worktree per orchestrator spec, so the SUMMARY.md lives in the main repo's .planning/ and is not committed from the worktree.

## Files Created/Modified

- `internal/cli/lock/doc.go` — Package contract, citation chain STATE-06 → spec §6.7 → D-18 → D-23, contention semantics narrative.
- `internal/cli/lock/lock.go` — Locker + Lease interfaces, AcquireMode iota+1 enum, sentinel errors ErrLockContended/ErrLockTimeout/ErrInvalidMode. No build tag — this IS the cross-OS shape.
- `internal/cli/lock/path.go` — `Path(achDir) → filepath.Join(achDir, "lock")` — single function, mirrors internal/cli/state path resolver shape.
- `internal/cli/lock/path_test.go` — 3 stdlib-only join tests (basic, empty ach-dir, nested ach-dir).
- `internal/cli/lock/lock_unix.go` — `//go:build !windows`. unixLocker + unixLease, NewLocker public constructor, dispatch switch on AcquireMode, acquireFailFast helper, acquireBlocking poll-and-backoff loop with pollMin=1ms/pollMax=50ms constants.
- `internal/cli/lock/lock_test.go` — `//go:build !windows`. 10 stdlib-only tests across the three modes + idempotent Release + invalid mode + pre-cancelled ctx + sequential reuse. Every goroutine wait capped at 200ms hard ceiling.
- `go.mod` — `golang.org/x/sys v0.45.0` promoted from indirect to direct require.

## Decisions Made

See `key-decisions:` frontmatter above. Six decisions in total:
1. Poll-and-backoff blocking replaces goroutine+close pattern (race + Linux flock unblock hazards).
2. Inline `var Err = errors.New(...)` declarations vs `var (...)` block (matches acceptance-criteria grep anchors).
3. NewLocker (public) lives in lock_unix.go, not lock.go (avoid undefined-symbol linker error pre-Phase-7.1).
4. Pre-cancelled-ctx short-circuit at top of Acquire (defensive Rule 2 guard).
5. ErrInvalidMode default-branch guard against silent fall-through.
6. Two extra tests beyond the plan's named four (AcquireWait_CancelledByContext, AcquireFailFast_SequentialReuseAfterRelease) for zero-cost regression coverage.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Poll-and-backoff replaces goroutine+close-to-unblock pattern in acquireBlocking**
- **Found during:** Task 2 (running `make unit-pkg PKG=./internal/cli/lock/...` with race detector)
- **Issue:** The plan's behavior list and action prescribed "spawn a goroutine running unix.Flock blocking, signal via done channel; select on done vs ctx.Done()" for AcquireWait/AcquireWithTimeout. This pattern fails in two ways:
  1. **Race condition (detected by -race):** the goroutine calls `int(file.Fd())` while the caller's error path calls `file.Close()` — both touch the underlying poll.FD struct, which the race detector flags as a Write/Read race.
  2. **Hang (observed when -race reaches the timeout case):** even after fixing the race by capturing `int fd` before spawning the goroutine, the caller's `file.Close()` does NOT reliably unblock the goroutine's `unix.Flock(fd, LOCK_EX)` syscall on Linux. Go's *os.File.Close keeps the underlying poll.FD alive past close in subtle runtime ways, so the kernel flock waiter never receives EBADF. Test hung 600s and failed.
- **Fix:** Replaced the goroutine pattern with a poll-and-backoff loop using `unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)` repeatedly with exponential delay (1ms initial, 2× per attempt, capped at 50ms). The ctx.Done select sits between attempts so cancellation is clean. AcquireWithTimeout wraps the same loop in a `context.WithTimeout(ctx, timeout)` and maps `errors.Is(err, context.DeadlineExceeded) → ErrLockTimeout`.
- **Files modified:** internal/cli/lock/lock_unix.go (acquireBlocking function body + pollMin/pollMax constants)
- **Verification:** `./scripts/dev.sh go test -race ./internal/cli/lock/...` exits 0; TestAcquireWait_BlocksUntilRelease and TestAcquireWithTimeout_Elapses both pass in <50ms each.
- **Committed in:** 6a6ff1e (Task 2 commit)

**2. [Rule 2 - Missing Critical] ErrInvalidMode default-branch guard**
- **Found during:** Task 1 design (writing lock.go interface contract)
- **Issue:** AcquireMode is a typed-int with three valid values (iota+1). The plan did not specify what should happen when a caller passes the zero value or an unrecognized mode. A silent fall-through to a default path would be a footgun for future maintainers adding a fourth mode.
- **Fix:** Added `ErrInvalidMode` sentinel error in lock.go; Acquire dispatch switch has a `default` case returning `ErrInvalidMode` after closing the opened fd. Added `TestAcquire_InvalidMode` to assert the property.
- **Files modified:** internal/cli/lock/lock.go (var ErrInvalidMode), internal/cli/lock/lock_unix.go (default branch in switch), internal/cli/lock/lock_test.go (TestAcquire_InvalidMode)
- **Verification:** `TestAcquire_InvalidMode` passes; the zero-value AcquireMode returns `lock.ErrInvalidMode`.
- **Committed in:** edf0fa8 (var declaration) + 6a6ff1e (switch default + test)

**3. [Rule 2 - Missing Critical] Pre-cancelled-context short-circuit**
- **Found during:** Task 2 (writing tests)
- **Issue:** A caller passing a `ctx` that is already cancelled (or whose deadline already fired) would still get past the open-file syscall and reach the dispatch switch. For AcquireFailFast this is mostly harmless; for AcquireWait/WithTimeout the poll loop's first ctx.Err() check inside acquireBlocking does catch it, but the open syscall already ran. A defensive `ctx.Err()` check at the top of Acquire short-circuits all three modes uniformly.
- **Fix:** Added `if err := ctx.Err(); err != nil { return nil, err }` at the top of `unixLocker.Acquire`. Added `TestAcquire_PreCancelledContext`.
- **Files modified:** internal/cli/lock/lock_unix.go, internal/cli/lock/lock_test.go
- **Verification:** Test passes with `context.Canceled` returned verbatim.
- **Committed in:** 6a6ff1e

---

**Total deviations:** 3 auto-fixed (1 Rule 1 bug, 2 Rule 2 missing-critical defensive guards)
**Impact on plan:** All auto-fixes are correctness or robustness improvements that the plan's behavior list implicitly required but did not enumerate. The most significant (poll-and-backoff) is a Rule 1 fix for an actual race+hang the plan-as-written would have produced; the other two are defensive Rule 2 guards that prevent future-author footguns. No scope creep — every change is inside `internal/cli/lock/`.

## Issues Encountered

- **Test hang at 600s** during the first attempt with the goroutine+close-to-unblock pattern. Resolved by switching to poll-and-backoff (deviation #1 above). Time cost: ~3 minutes diagnosing + ~5 minutes re-architecting + ~1 minute verifying.
- **gofmt -s reformatting** of doc.go on the first lint sweep: gofmt rewrote `// Section name` godoc section headers to `// # Section name` per the modern doc convention. Applied via `gofmt -w -s`. Time cost: ~10 seconds.
- **NewLocker placement decision**: the plan's Task 1 <action> directs to "Add a package-private constructor stub `func newLocker(path string) Locker` declared but NOT defined here". I interpreted that as: lock.go declares the contract, lock_unix.go provides BOTH the package-private constructor AND the public NewLocker. This keeps lock.go compiling cleanly without a forward-reference to a symbol that only exists on linux-amd64 in Phase 7. Phase 7.1's lock_windows.go will define a symmetric NewLocker/newLocker pair behind `//go:build windows`. The package-private newLocker exists primarily for the lock_windows.go author's convenience — same shape on both sides of the build-tag divide.

## User Setup Required

None — internal/cli/lock has no external dependencies beyond `golang.org/x/sys` (already in go.sum from controller-runtime).

## Next Phase Readiness

- `lock.NewLocker(path).Acquire(ctx, mode, timeout)` is callable from the W1-06 hydrate commit skeleton with no further edits to the lock package.
- `lock.Path(achDir)` returns the same `<ach-dir>/lock` location every caller should use — the W1-02 state package (`<ach-dir>/state.json`) and the W1-06 hydrate orchestrator agree on the parent directory.
- The three contention modes line up exactly with the spec §6.7 flag surface: no flag → AcquireFailFast, `--wait` → AcquireWait, `--lock-timeout=<dur>` → AcquireWithTimeout. The cobra flag binding in W1-06 just needs `if wait { mode = lock.AcquireWait } else if timeout > 0 { mode = lock.AcquireWithTimeout } else { mode = lock.AcquireFailFast }`.
- main.go's error-handling chain (W1-04) needs to map `errors.Is(err, lock.ErrLockContended)` → `exit.General=1` with the user-facing message "another ach-cli is running; use --wait or --lock-timeout", and `errors.Is(err, lock.ErrLockTimeout)` → `exit.General=1` with the user-facing message "lock acquisition timed out".
- Phase 7.1 boundary is clean: when `lock_windows.go` lands, it adds `//go:build windows` symmetric implementations of `newLocker`/`NewLocker` and a `lock_windows_test.go` mirroring `lock_test.go`. `lock.go`, `doc.go`, `path.go`, and `path_test.go` do NOT need to change.

## Threat Surface Scan

No new threat surface introduced. Lock-file path is constructed via `filepath.Join(achDir, "lock")` — no path-traversal risk because `achDir` is caller-provided and already validated by the hydrate command's flag/env resolver (out of scope for this plan). The lock file itself carries no contents (lock state is in the kernel flock table), so no data exposure path exists.

---
*Phase: 07-cli-hydrate-engine-adapters-safe-extraction-state-distributi*
*Completed: 2026-05-29*
