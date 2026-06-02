// SPDX-License-Identifier: Apache-2.0

// Package sfdetach wraps golang.org/x/sync/singleflight so a single
// caller's context cancellation cannot abort the shared flight for other
// live callers (the leader-cancellation-cascade fixed in finding C1).
//
// The leader runs fn on a context detached from the caller's cancellation
// (context.WithoutCancel) but bounded by leaderTimeout, so the flight can
// neither be killed by one caller nor hang forever. Each caller still
// observes its OWN ctx cancellation via the select.
package sfdetach

import (
	"context"
	"time"

	"golang.org/x/sync/singleflight"
)

// Do executes fn under g keyed by key, isolating the shared flight from
// per-caller cancellation. Followers joined on the same key receive the
// leader's value/error; a follower whose own ctx ends returns ctx.Err().
func Do[T any](
	ctx context.Context,
	g *singleflight.Group,
	key string,
	leaderTimeout time.Duration,
	fn func(context.Context) (T, error),
) (T, error) {
	var zero T
	ch := g.DoChan(key, func() (any, error) {
		leaderCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaderTimeout)
		defer cancel()
		return fn(leaderCtx)
	})
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return zero, res.Err
		}
		v, _ := res.Val.(T) // preserves typed-nil (e.g. (*KeyInfo)(nil))
		return v, nil
	}
}
