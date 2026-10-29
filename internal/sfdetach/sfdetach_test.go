// SPDX-License-Identifier: Apache-2.0

package sfdetach

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"
)

func TestDo_ReturnsValue(t *testing.T) {
	var g singleflight.Group
	got, err := Do(context.Background(), &g, "k", time.Second,
		func(context.Context) (int, error) { return 42, nil })
	if err != nil || got != 42 {
		t.Fatalf("got (%d,%v), want (42,nil)", got, err)
	}
}

func TestDo_CallerCancelReturnsCtxErr(t *testing.T) {
	var g singleflight.Group
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release := make(chan struct{})
	defer close(release) // let the detached leader finish after Do returns
	_, err := Do(ctx, &g, "k", time.Second, func(context.Context) (int, error) {
		<-release // block so the result channel is never ready before select sees ctx.Done()
		return 1, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestDo_LeaderTimeoutExpires(t *testing.T) {
	var g singleflight.Group
	_, err := Do(context.Background(), &g, "k", 10*time.Millisecond,
		func(c context.Context) (int, error) {
			<-c.Done() // leader ctx must expire via leaderTimeout
			return 0, c.Err()
		})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
}

func TestDo_LeaderContextDetachedFromParentCancel(t *testing.T) {
	// Regression for C1: the leader fn must run on a context that is NOT
	// cancelled when the parent caller's context is cancelled.
	var g singleflight.Group
	parent, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	leaderCtxErr := make(chan error, 1)
	go func() {
		_, _ = Do(parent, &g, "k", time.Second, func(c context.Context) (int, error) {
			close(started)
			time.Sleep(30 * time.Millisecond) // let the parent cancel land
			leaderCtxErr <- c.Err()
			return 1, nil
		})
	}()
	<-started
	cancel()
	if err := <-leaderCtxErr; err != nil {
		t.Fatalf("leader ctx should be alive despite parent cancel, got %v", err)
	}
}
