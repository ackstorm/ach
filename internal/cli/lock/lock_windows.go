//go:build windows

// SPDX-License-Identifier: Apache-2.0

// Package lock — Windows LockFileEx implementation (CLI spec §6.7 /
// D-23). Mirrors the POSIX flock impl in lock_unix.go: every Acquire
// opens a fresh handle and takes an exclusive byte-range lock via
// LockFileEx. The blocking modes reuse the same cancellable
// poll-and-backoff loop as the unix path — LockFileEx's native
// blocking wait (omitting LOCKFILE_FAIL_IMMEDIATELY) is not
// ctx-cancellable, so we always lock with LOCKFILE_FAIL_IMMEDIATELY and
// retry on ERROR_LOCK_VIOLATION exactly as the unix path retries on
// EWOULDBLOCK. The advisory lock is released when the handle closes,
// preserving the SIGKILL-safety guarantee referenced by spec §6.7
// (process death closes all handles → the OS reaps the lock).
package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

// newLocker is the package-private OS-dispatched constructor; the
// public NewLocker wraps it. The !windows pair lives in lock_unix.go
// and Go's build tags pick exactly one at compile time.
func newLocker(path string) Locker {
	return &windowsLocker{path: path}
}

// NewLocker returns a Locker that operates on the file at path. See the
// package doc for the contention contract.
func NewLocker(path string) Locker {
	return newLocker(path)
}

// pollMin / pollMax cap the poll-and-backoff schedule. Redeclared here
// (lock_unix.go's copy is behind //go:build !windows so it is invisible
// to this file) — keep the two in sync; the values match by design.
const (
	pollMin = 1 * time.Millisecond
	pollMax = 50 * time.Millisecond
)

// lockBytesLow is the size of the locked region. Windows byte-range
// locks are mandatory and per-range, so every process MUST lock the
// identical region; we lock one byte at offset 0. The file content is
// empty — lock state lives in the OS table, not on disk.
const lockBytesLow uint32 = 1

// windowsLocker is the windows concrete impl. Like unixLocker it
// carries only the path; every Acquire opens a fresh handle so the
// per-handle lock lifetime matches the per-process lease.
type windowsLocker struct {
	path string
}

// Acquire opens the lock file (creating it if absent) and takes an
// exclusive LockFileEx lock per the mode argument. Returns a Lease the
// caller MUST Release; on error the handle is closed before return so
// no handle leaks on the failure path.
func (l *windowsLocker) Acquire(ctx context.Context, mode AcquireMode, timeout time.Duration) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	//nolint:gosec // G302: 0o644 is intentional for an advisory-lock file (empty contents, no secrets).
	file, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock: open %q: %w", l.path, err)
	}

	switch mode {
	case AcquireFailFast:
		if err := lockFileFailFast(file); err != nil {
			_ = file.Close()
			return nil, err
		}
	case AcquireWait:
		if err := lockFileBlocking(ctx, file); err != nil {
			_ = file.Close()
			return nil, err
		}
	case AcquireWithTimeout:
		boundedCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := lockFileBlocking(boundedCtx, file); err != nil {
			_ = file.Close()
			// Map deadline-exceeded to the public sentinel so callers
			// can errors.Is(err, ErrLockTimeout) (mirrors lock_unix.go).
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, ErrLockTimeout
			}
			return nil, err
		}
	default:
		_ = file.Close()
		return nil, ErrInvalidMode
	}

	return &windowsLease{file: file}, nil
}

// lockFileFailFast issues a non-blocking exclusive LockFileEx and maps
// ERROR_LOCK_VIOLATION (the contended result under
// LOCKFILE_FAIL_IMMEDIATELY) to ErrLockContended. Any other error is
// wrapped for caller diagnosis.
func lockFileFailFast(file *os.File) error {
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,                     // reserved — must be 0
		lockBytesLow,          // nNumberOfBytesToLockLow
		0,                     // nNumberOfBytesToLockHigh
		&windows.Overlapped{}, // offset 0
	)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_IO_PENDING):
		return ErrLockContended
	default:
		return fmt.Errorf("lock: LockFileEx fail-fast: %w", err)
	}
}

// lockFileBlocking polls the non-blocking LockFileEx on an
// exponentially-backed schedule capped at pollMax, checking ctx between
// attempts. See lock_unix.go's acquireBlocking for the rationale behind
// poll-and-sleep over a native blocking wait — here it is mandatory
// because LockFileEx without LOCKFILE_FAIL_IMMEDIATELY blocks
// uninterruptibly. Returns nil on lock acquired, ctx.Err() on
// cancellation / deadline, or a wrapped error otherwise.
func lockFileBlocking(ctx context.Context, file *os.File) error {
	h := windows.Handle(file.Fd())
	delay := pollMin
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := windows.LockFileEx(
			h,
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0, lockBytesLow, 0, &windows.Overlapped{},
		)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_IO_PENDING):
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
			return fmt.Errorf("lock: LockFileEx blocking: %w", err)
		}
	}
}

// windowsLease is the per-Acquire handle. The atomic released gate
// makes Release idempotent so deferred-then-explicit Release patterns
// don't double-unlock or double-close.
type windowsLease struct {
	file     *os.File
	released atomic.Int32
}

// Release unlocks the byte range and closes the handle. Either reaps
// the lock (handle close alone suffices), but the explicit UnlockFileEx
// keeps the OS lock table crisp during debugging. A second Release call
// returns nil without re-issuing either syscall.
func (l *windowsLease) Release() error {
	if !l.released.CompareAndSwap(0, 1) {
		return nil
	}

	unlockErr := windows.UnlockFileEx(
		windows.Handle(l.file.Fd()),
		0, lockBytesLow, 0, &windows.Overlapped{},
	)
	closeErr := l.file.Close()

	// Prefer the unlock error; the close is the safety net and its
	// error is informational (mirrors lock_unix.go).
	if unlockErr != nil {
		return fmt.Errorf("lock: UnlockFileEx: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("lock: close lock file: %w", closeErr)
	}
	return nil
}
