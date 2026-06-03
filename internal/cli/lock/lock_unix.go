//go:build !windows

// SPDX-License-Identifier: Apache-2.0

// Package lock — POSIX flock(LOCK_EX) implementation. The Windows
// LockFileEx variant lives in lock_windows.go (CLI spec §6.7 / D-23).
package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// newLocker is the package-private OS-dispatched constructor. The
// public NewLocker wrapper (also defined here behind the same build
// tag) is the entry point external callers reach for. Phase 7.1's
// lock_windows.go provides its own `newLocker`/`NewLocker` pair —
// Go's build-tag system picks one at compile time.
func newLocker(path string) Locker {
	return &unixLocker{path: path}
}

// NewLocker returns a Locker that operates on the file at path.
// See the package doc for the contention contract. NewLocker itself
// is build-tag dispatched (Phase 7 ships only the !windows variant
// here; Phase 7.1 adds a windows variant in lock_windows.go).
func NewLocker(path string) Locker {
	return newLocker(path)
}

// unixLocker is the !windows concrete impl. It carries no state
// beyond the file path — every Acquire opens a fresh fd so the
// kernel's per-fd advisory-lock semantics line up with the
// per-process lease lifecycle.
type unixLocker struct {
	path string
}

// Acquire opens the lock file (creating it if absent) and acquires a
// POSIX exclusive advisory lock per the mode argument. Returns a
// Lease the caller MUST Release; on error the file descriptor is
// closed before return so no fd leaks on the failure path.
func (l *unixLocker) Acquire(ctx context.Context, mode AcquireMode, timeout time.Duration) (Lease, error) {
	// Short-circuit on a pre-cancelled context so callers cannot get
	// a lease past a deadline that has already fired.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Open the lock file. 0o644 is fine — the file content is empty
	// (lock state is in the kernel flock table, not on disk) and a
	// shared read-mode is harmless.
	//nolint:gosec // G302: 0o644 is intentional for an advisory-lock file (empty contents, no secrets).
	file, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock: open %q: %w", l.path, err)
	}

	switch mode {
	case AcquireFailFast:
		if err := acquireFailFast(file); err != nil {
			_ = file.Close()
			return nil, err
		}
	case AcquireWait:
		if err := acquireBlocking(ctx, file); err != nil {
			_ = file.Close()
			return nil, err
		}
	case AcquireWithTimeout:
		boundedCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := acquireBlocking(boundedCtx, file); err != nil {
			_ = file.Close()
			// Map a deadline-exceeded ctx error to the public
			// sentinel so callers can errors.Is(err, ErrLockTimeout)
			// without reaching into context internals.
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, ErrLockTimeout
			}
			return nil, err
		}
	default:
		_ = file.Close()
		return nil, ErrInvalidMode
	}

	return &unixLease{file: file}, nil
}

// acquireFailFast issues unix.Flock(LOCK_EX|LOCK_NB) and translates
// EWOULDBLOCK to ErrLockContended. Any other error is wrapped for
// caller diagnosis.
func acquireFailFast(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EWOULDBLOCK):
		return ErrLockContended
	default:
		return fmt.Errorf("lock: flock fail-fast: %w", err)
	}
}

// acquireBlocking polls unix.Flock(LOCK_EX|LOCK_NB) on an
// exponentially-backed schedule, capped at pollMax, checking ctx
// between attempts. POSIX flock(2) on Linux blocks in the kernel
// when called without LOCK_NB and is not reliably unblockable from
// another goroutine via close(2) — Go's runtime keeps the file
// descriptor alive past File.Close in subtle ways, so the natural
// "spawn-a-goroutine, race with ctx.Done, close to unblock" pattern
// either races with the race detector or hangs. The poll-and-sleep
// approach is a few extra syscalls per second of waiting (cheap,
// since lock contention is rare) in exchange for a clean cancellation
// surface and no goroutine ownership of the fd.
//
// Returns nil on lock acquired, ctx.Err() on cancellation /
// deadline-exceeded, or a wrapped flock error on any other failure.
func acquireBlocking(ctx context.Context, file *os.File) error {
	fd := int(file.Fd())
	delay := pollMin
	for {
		// Probe ctx before each syscall so a pre-cancelled or
		// already-deadlined ctx short-circuits immediately.
		if err := ctx.Err(); err != nil {
			return err
		}

		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.EWOULDBLOCK):
			// Locked by someone else — sleep with backoff, then
			// retry. The select on ctx.Done makes the sleep
			// cancellable.
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
			delay *= 2
			if delay > pollMax {
				delay = pollMax
			}
		default:
			return fmt.Errorf("lock: flock blocking: %w", err)
		}
	}
}

// pollMin / pollMax cap the poll-and-backoff schedule. 1ms initial
// is fast enough to keep the §6.7 fast-path latency negligible
// (single retry on a microsecond-scale contention window) and 50ms
// cap is short enough that a --lock-timeout=100ms test sees a real
// retry inside its window. These constants are not tunable via
// flag — lock contention is rare and a one-size-fits-all schedule
// keeps the public surface minimal.
const (
	pollMin = 1 * time.Millisecond
	pollMax = 50 * time.Millisecond
)

// unixLease is the per-Acquire handle. The atomic released gate makes
// Release idempotent so deferred-Release-then-explicit-Release call
// patterns don't double-close the fd.
type unixLease struct {
	file     *os.File
	released atomic.Int32
}

// Release issues LOCK_UN and closes the file descriptor. Either one
// is sufficient for the kernel to reap the advisory lock, but the
// explicit LOCK_UN keeps lsof output crisp during in-process
// debugging. A second Release call returns nil without re-issuing
// either syscall.
func (l *unixLease) Release() error {
	if !l.released.CompareAndSwap(0, 1) {
		// Already released — idempotent no-op.
		return nil
	}

	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()

	// Prefer surfacing the unlock error — the file-close is the
	// belt-and-suspenders safety net and its error is informational.
	if unlockErr != nil {
		return fmt.Errorf("lock: flock unlock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("lock: close lock file: %w", closeErr)
	}
	return nil
}
