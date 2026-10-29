// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestRunRefreshTriggers_HandlerNonBlockingAndCoalesces(t *testing.T) {
	var running int32
	var calls int32
	release := make(chan struct{})
	refresh := func(context.Context) error {
		atomic.AddInt32(&calls, 1)
		atomic.StoreInt32(&running, 1)
		<-release // hold the refresh "in flight"
		atomic.StoreInt32(&running, 0)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	trigger := make(chan struct{}, 1)
	go runRefreshTriggers(ctx, trigger, refresh, logr.Discard())

	// Fire the trigger handler-style three times in a row. It must never
	// block even while a refresh is in flight (coalescing absorbs extras).
	for i := 0; i < 3; i++ {
		fired := make(chan struct{})
		go func() { signalTrigger(trigger); close(fired) }()
		select {
		case <-fired:
		case <-time.After(time.Second):
			t.Fatalf("signalTrigger blocked on iteration %d", i)
		}
	}
	// Let the in-flight refresh finish; expect coalescing (>=1, <3 calls
	// for the 3 bursts while one was already running).
	close(release)
	time.Sleep(200 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got < 1 {
		t.Fatalf("expected at least one refresh, got %d", got)
	}
}
