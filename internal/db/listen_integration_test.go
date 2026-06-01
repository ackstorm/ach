//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration coverage for db.RunRefreshLoop — the shared cache-freshness
// lifecycle the forwarder's bipcache/envstore Run methods delegate to.
//
// NewListener dereferences pool.Config().ConnString(), so a nil pool
// panics — no nil-pool unit test is possible; a real pool is required.
// The existing bipcache/envstore TestNotifyInvalidates cover ONLY the
// NOTIFY-driven leg; this test covers the two behaviors RunRefreshLoop's
// interval + cancel handling add: the periodic ticker firing refresh,
// and returning ctx.Err() promptly on cancel.

package db_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/ackstorm/ach/internal/db"
)

func TestRunRefreshLoop_TickAndCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	var calls int32
	refresh := func(context.Context) error { atomic.AddInt32(&calls, 1); return nil }

	done := make(chan error, 1)
	go func() {
		// Short interval so the ticker fires quickly. "ach_test_refresh"
		// need not be a real NOTIFY producer — the ticker path is
		// independent of LISTEN; an unknown channel only affects the
		// (discarded) listener leg.
		done <- db.RunRefreshLoop(ctx, pool, "ach_test_refresh", 100*time.Millisecond, logr.Discard(), refresh)
	}()

	// (a) initial refresh ran up-front, and (b) the ticker fires >=1 time.
	deadline := time.After(3 * time.Second)
	for atomic.LoadInt32(&calls) < 2 {
		select {
		case <-deadline:
			t.Fatalf("want >=2 refresh calls (initial + >=1 tick); got %d", atomic.LoadInt32(&calls))
		case <-time.After(20 * time.Millisecond):
		}
	}

	// (c) cancel makes RunRefreshLoop return ctx.Err() promptly.
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("RunRefreshLoop returned nil; want ctx.Err() on cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("RunRefreshLoop did not return within 2s of cancel")
	}
}
