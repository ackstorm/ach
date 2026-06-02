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
	_, err := Do(ctx, &g, "k", time.Second,
		func(context.Context) (int, error) { return 1, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
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
