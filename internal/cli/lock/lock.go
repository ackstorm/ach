// SPDX-License-Identifier: Apache-2.0

package lock

import (
	"context"
	"errors"
	"time"
)

// AcquireMode is a typed enum the caller passes to Locker.Acquire to
// select the contention behavior. Typed-int prevents accidental
// string/int confusion at call sites (mirrors the synthetic.Gate
// idiom in internal/cli/synthetic/synthetic.go lines 22-72).
//
// The three values line up with the CLI's flag surface (spec §6.7):
//
//   - AcquireFailFast — default; no flag.
//   - AcquireWait     — `--wait`.
//   - AcquireWithTimeout — `--lock-timeout=<dur>`.
//
// The zero value is intentionally not a valid mode — callers MUST pick
// one of the named constants, otherwise Acquire returns an error.
type AcquireMode int

// AcquireMode constants. The iota+1 pattern follows
// internal/cli/synthetic.Gate so the zero value cannot accidentally
// alias one of the modes.
const (
	// AcquireFailFast returns ErrLockContended immediately if another
	// process holds the lock (LOCK_EX|LOCK_NB → EWOULDBLOCK). This is
	// the default mode when neither `--wait` nor `--lock-timeout` is
	// passed; main.go maps ErrLockContended → exit 1 (General) with
	// the user-facing "another ach-cli is running" message.
	AcquireFailFast AcquireMode = iota + 1

	// AcquireWait blocks on flock(LOCK_EX) until either the lock is
	// granted or the parent context is cancelled. Used by `--wait`.
	// The timeout parameter is ignored in this mode.
	AcquireWait

	// AcquireWithTimeout caps the wait at the timeout argument.
	// Implementation runs context.WithTimeout(ctx, timeout) and
	// returns ErrLockTimeout when the deadline fires before the
	// lock is granted. Used by `--lock-timeout=<dur>`.
	AcquireWithTimeout
)

// Locker is the single-method interface the CLI hydrate orchestrator
// holds. The POSIX impl lives behind //go:build !windows in
// lock_unix.go; Phase 7.1 adds lock_windows.go without touching this
// file (D-19 + D-23).
type Locker interface {
	// Acquire opens the lock file at the Locker's configured path and
	// blocks (or fails fast) per mode. On success returns a Lease the
	// caller MUST Release. The ctx is honored in AcquireWait /
	// AcquireWithTimeout modes; in AcquireFailFast it is consulted
	// only to short-circuit before the flock syscall.
	//
	// The timeout argument is consumed only in AcquireWithTimeout
	// mode; AcquireFailFast and AcquireWait ignore it.
	Acquire(ctx context.Context, mode AcquireMode, timeout time.Duration) (Lease, error)
}

// Lease is the per-Acquire handle. Release MUST be called exactly
// once per successful Acquire — double-Release is a documented no-op
// (returns nil) so deferred Release calls under a follow-up early
// return are safe. The kernel releases the underlying advisory lock
// on file-descriptor close, which is also the SIGKILL-safety
// guarantee referenced by CLI spec §6.7.
type Lease interface {
	// Release closes the file descriptor (kernel releases the flock
	// with it) and returns the first non-nil error encountered.
	// Idempotent: a second Release call returns nil without
	// re-issuing the close syscall.
	Release() error
}

// Sentinel errors. Callers gate behavior via errors.Is. Inline `var`
// declarations (not a `var (...)` block) so a literal `grep -q "var
// ErrLockContended"` matches — the W1-03 plan's acceptance criteria
// rely on that string anchor.

// ErrLockContended is returned by AcquireFailFast when another
// process holds the lock (flock returned EWOULDBLOCK). main.go
// maps this to exit 1 (General). Spec §6.7.
var ErrLockContended = errors.New("lock: already held by another process")

// ErrLockTimeout is returned by AcquireWithTimeout when the
// configured timeout elapses before the lock is granted.
// main.go maps this to exit 1 (General). Spec §6.7.
var ErrLockTimeout = errors.New("lock: acquisition timed out")

// ErrInvalidMode is returned by Acquire when the AcquireMode is
// not one of the three documented values (e.g. the caller
// forgot to set it and passed the zero value). Defensive guard
// so a future refactor that adds a fourth mode cannot silently
// fall through to the default branch.
var ErrInvalidMode = errors.New("lock: invalid acquire mode")

// The public NewLocker constructor is defined in lock_unix.go (and
// Phase 7.1's lock_windows.go) behind build tags, so the cross-OS
// dispatch happens at compile time rather than via runtime.GOOS
// branching. Phase 7 ships linux-amd64 only per D-18; on Windows the
// package fails to link until Phase 7.1 adds lock_windows.go.
//
// Callers should resolve the lock-file path via Path(achDir) so
// every hydrate run agrees on the same `<ach-dir>/lock` location.
