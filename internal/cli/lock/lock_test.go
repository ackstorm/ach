//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package lock_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/lock"
)

// hardTimeout caps every goroutine wait in this file so a hung impl
// surfaces as a fast t.Fatalf rather than a hung test binary. 200ms
// is well above the ~1-10ms flock round-trip on a loaded CI host and
// well below the default go test 10m killer.
const hardTimeout = 200 * time.Millisecond

// newPath returns a fresh lock-file path under t.TempDir(). Each
// test gets its own directory so parallel subtests cannot collide
// on the same advisory lock by accident.
func newPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "lock")
}

// TestAcquireFailFast_Uncontended asserts the happy path: the first
// AcquireFailFast on a fresh path succeeds and Release returns nil.
func TestAcquireFailFast_Uncontended(t *testing.T) {
	t.Parallel()
	l := lock.NewLocker(newPath(t))

	lease, err := l.Acquire(context.Background(), lock.AcquireFailFast, 0)
	if err != nil {
		t.Fatalf("Acquire(FailFast) uncontended: got err=%v, want nil", err)
	}
	if lease == nil {
		t.Fatal("Acquire(FailFast) uncontended: got nil lease")
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release: got err=%v, want nil", err)
	}
}

// TestAcquireFailFast_Contended asserts the contention path: a
// second AcquireFailFast against the same path returns
// ErrLockContended without blocking.
func TestAcquireFailFast_Contended(t *testing.T) {
	t.Parallel()
	path := newPath(t)
	first := lock.NewLocker(path)
	second := lock.NewLocker(path)

	lease1, err := first.Acquire(context.Background(), lock.AcquireFailFast, 0)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer func() { _ = lease1.Release() }()

	// Run the second Acquire in a goroutine with a hard ceiling so a
	// regression where FailFast blocks (instead of failing) surfaces
	// here instead of hanging the suite.
	done := make(chan error, 1)
	go func() {
		_, err := second.Acquire(context.Background(), lock.AcquireFailFast, 0)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, lock.ErrLockContended) {
			t.Errorf("second Acquire(FailFast): got err=%v, want ErrLockContended", err)
		}
	case <-time.After(hardTimeout):
		t.Fatalf("second Acquire(FailFast) blocked >%s; expected immediate ErrLockContended", hardTimeout)
	}
}

// TestAcquireWait_BlocksUntilRelease asserts the blocking path:
// AcquireWait against a held lock waits until the holder releases,
// then succeeds.
func TestAcquireWait_BlocksUntilRelease(t *testing.T) {
	t.Parallel()
	path := newPath(t)
	first := lock.NewLocker(path)
	second := lock.NewLocker(path)

	lease1, err := first.Acquire(context.Background(), lock.AcquireFailFast, 0)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	// Schedule the release on a short timer; AcquireWait should
	// return shortly after.
	released := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		if err := lease1.Release(); err != nil {
			t.Errorf("first Release: %v", err)
		}
		close(released)
	}()

	waitDone := make(chan struct {
		lease lock.Lease
		err   error
	}, 1)
	go func() {
		lease, err := second.Acquire(context.Background(), lock.AcquireWait, 0)
		waitDone <- struct {
			lease lock.Lease
			err   error
		}{lease, err}
	}()

	select {
	case got := <-waitDone:
		if got.err != nil {
			t.Fatalf("second Acquire(Wait): got err=%v, want nil", got.err)
		}
		if got.lease == nil {
			t.Fatal("second Acquire(Wait): got nil lease")
		}
		if err := got.lease.Release(); err != nil {
			t.Errorf("second Release: %v", err)
		}
	case <-time.After(hardTimeout):
		t.Fatalf("second Acquire(Wait) did not return within %s of holder release", hardTimeout)
	}

	<-released
}

// TestAcquireWait_CancelledByContext asserts AcquireWait honors
// context cancellation when it cannot acquire immediately.
func TestAcquireWait_CancelledByContext(t *testing.T) {
	t.Parallel()
	path := newPath(t)
	first := lock.NewLocker(path)
	second := lock.NewLocker(path)

	lease1, err := first.Acquire(context.Background(), lock.AcquireFailFast, 0)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer func() { _ = lease1.Release() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := second.Acquire(ctx, lock.AcquireWait, 0)
		done <- err
	}()

	// Let the goroutine reach the blocking flock before cancelling.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("second Acquire(Wait) after cancel: got err=%v, want context.Canceled", err)
		}
	case <-time.After(hardTimeout):
		t.Fatalf("second Acquire(Wait) did not honor ctx.Cancel within %s", hardTimeout)
	}
}

// TestAcquireWithTimeout_Elapses asserts that AcquireWithTimeout
// returns ErrLockTimeout when the configured timeout fires before
// the lock is granted.
func TestAcquireWithTimeout_Elapses(t *testing.T) {
	t.Parallel()
	path := newPath(t)
	first := lock.NewLocker(path)
	second := lock.NewLocker(path)

	lease1, err := first.Acquire(context.Background(), lock.AcquireFailFast, 0)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer func() { _ = lease1.Release() }()

	start := time.Now()
	_, err = second.Acquire(context.Background(), lock.AcquireWithTimeout, 30*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, lock.ErrLockTimeout) {
		t.Errorf("second Acquire(WithTimeout) blocked: got err=%v, want ErrLockTimeout", err)
	}
	if elapsed > hardTimeout {
		t.Errorf("second Acquire(WithTimeout) returned in %s; expected <%s", elapsed, hardTimeout)
	}
}

// TestAcquireWithTimeout_Succeeds asserts the happy path of
// AcquireWithTimeout: an uncontended lock is granted well within
// the timeout and returns a usable lease.
func TestAcquireWithTimeout_Succeeds(t *testing.T) {
	t.Parallel()
	l := lock.NewLocker(newPath(t))

	lease, err := l.Acquire(context.Background(), lock.AcquireWithTimeout, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Acquire(WithTimeout) uncontended: got err=%v, want nil", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// TestRelease_Idempotent asserts a second Release call on the same
// lease returns nil without re-issuing flock(LOCK_UN) or Close.
// Required so deferred-Release-plus-explicit-Release patterns are
// safe at the call site.
func TestRelease_Idempotent(t *testing.T) {
	t.Parallel()
	l := lock.NewLocker(newPath(t))

	lease, err := l.Acquire(context.Background(), lock.AcquireFailFast, 0)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := lease.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Errorf("second Release: got err=%v, want nil (idempotent)", err)
	}
}

// TestAcquire_InvalidMode asserts a caller passing the zero value
// (or any unrecognized AcquireMode) gets ErrInvalidMode rather than
// silently falling through to a default path.
func TestAcquire_InvalidMode(t *testing.T) {
	t.Parallel()
	l := lock.NewLocker(newPath(t))

	var zero lock.AcquireMode // intentionally invalid

	_, err := l.Acquire(context.Background(), zero, 0)
	if !errors.Is(err, lock.ErrInvalidMode) {
		t.Errorf("Acquire(zero mode): got err=%v, want ErrInvalidMode", err)
	}
}

// TestAcquire_PreCancelledContext asserts a context that is already
// cancelled short-circuits the open + flock syscalls and returns
// the ctx error verbatim.
func TestAcquire_PreCancelledContext(t *testing.T) {
	t.Parallel()
	l := lock.NewLocker(newPath(t))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := l.Acquire(ctx, lock.AcquireFailFast, 0)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Acquire with pre-cancelled ctx: got err=%v, want context.Canceled", err)
	}
}

// TestAcquireFailFast_SequentialReuseAfterRelease asserts that
// after a Release the same path can be re-acquired by a new Locker
// — i.e. Release truly relinquishes the kernel lock and does not
// leak it past the lease lifecycle.
func TestAcquireFailFast_SequentialReuseAfterRelease(t *testing.T) {
	t.Parallel()
	path := newPath(t)
	first := lock.NewLocker(path)
	second := lock.NewLocker(path)

	lease1, err := first.Acquire(context.Background(), lock.AcquireFailFast, 0)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := lease1.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}

	lease2, err := second.Acquire(context.Background(), lock.AcquireFailFast, 0)
	if err != nil {
		t.Fatalf("second Acquire after release: got err=%v, want nil", err)
	}
	if err := lease2.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}
