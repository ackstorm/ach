// SPDX-License-Identifier: Apache-2.0

// Package lock implements the advisory single-writer lock required by
// CLI spec §6.7 step 1 — every `ach-cli hydrate` (and any other writer
// to `<ach-dir>/`) acquires `<ach-dir>/lock` before touching the
// manifest cache, state.json, or workspace bytes. STATE-06 is the
// originating requirement.
//
// Contract
//
//   - The interface (Locker / Lease / AcquireMode / sentinel errors)
//     lives in lock.go with NO build tag — it is the cross-OS shape
//     downstream packages compile against.
//   - The POSIX implementation (flock(LOCK_EX)) lives in lock_unix.go
//     behind `//go:build !windows`. Per CONTEXT.md D-18, Phase 7 ships
//     `linux-amd64` only; the windows-amd64 build (and `lock_windows.go`
//     using `LockFileEx`) lands in Phase 7.1 per D-23.
//   - A test seam (`newLocker(path)`) is the package-private constructor;
//     callers go through `NewLocker(path)` (re-exported here). This
//     keeps the OS dispatch hidden behind one entry point and lets
//     tests substitute a noop locker via an interface-typed field.
//
// Contention semantics (spec §6.7)
//
// Three modes line up with the CLI's flag surface:
//
//   - AcquireFailFast (default — no flag): EWOULDBLOCK → ErrLockContended
//     → main.go exits 1 (General). The hydrate command turns this into
//     a one-line message: "another ach-cli is running; use --wait or
//     --lock-timeout".
//   - AcquireWait (`--wait`): block on flock(LOCK_EX) indefinitely,
//     cancellable only via ctx.Done (e.g. SIGINT).
//   - AcquireWithTimeout (`--lock-timeout=<dur>`): block on flock with
//     an internal context.WithTimeout shadow; on timeout return
//     ErrLockTimeout and main.go exits 1 (General).
//
// # Release path
//
// Lease.Release() calls flock(LOCK_UN) and closes the file descriptor.
// Either is sufficient on its own (kernel releases the advisory lock
// on fd-close), but doing the explicit LOCK_UN keeps `lsof` output
// crisp during in-process debugging. The SIGKILL-safety property
// (CLI spec §6.7) falls out of fd-close-on-exit: the kernel reaps the
// file descriptor before the process is gone, releasing the flock with
// it — no on-disk staleness, no recovery dance.
//
// # Lock path
//
// Path(achDir) returns filepath.Join(achDir, "lock"). The same
// `<ach-dir>` root is shared with state.json (D-09 / STATE-06): a
// single directory carries every per-workspace artifact the CLI owns,
// so a `rm -rf <ach-dir>` is a clean reset.
package lock
