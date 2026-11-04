// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeCapsSource struct {
	deps    map[string]ModelCaps
	aliases map[string]string
	err     error
	delay   time.Duration
}

func (f *fakeCapsSource) ListDeploymentCapabilities(context.Context) (map[string]ModelCaps, error) {
	time.Sleep(f.delay)
	return f.deps, f.err
}

func (f *fakeCapsSource) ModelGroupAliases(context.Context) (map[string]string, error) {
	return f.aliases, f.err
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestDeploymentCaps_FollowsAlias(t *testing.T) {
	limit := 524288.0
	deps := map[string]ModelCaps{"vertex.flash": {MaxInputTokens: &limit}}
	aliases := map[string]string{"ackstorm.smart": "vertex.flash"}

	for _, name := range []string{"ackstorm.smart", "vertex.flash"} {
		if got := DeploymentCaps(deps, aliases, name); got.MaxInputTokens == nil || *got.MaxInputTokens != limit {
			t.Errorf("DeploymentCaps(%q) = %+v, want max_input_tokens %v", name, got, limit)
		}
	}
	if got := DeploymentCaps(deps, aliases, "unknown"); got.MaxInputTokens != nil {
		t.Errorf("DeploymentCaps(unknown) = %+v, want zero value", got)
	}
}

func TestCapsCache_StaleServedWithoutWaiting(t *testing.T) {
	src := &fakeCapsSource{deps: map[string]ModelCaps{"m": {}}}
	c := NewCapsCache(src, discard())
	if deps, _ := c.Get(context.Background()); len(deps) != 1 {
		t.Fatalf("cold Get = %v", deps)
	}
	c.mu.Lock()
	c.at = time.Now().Add(-2 * capsTTL)
	c.mu.Unlock()
	src.delay = 3 * time.Second

	start := time.Now()
	deps, _ := c.Get(context.Background())
	if elapsed := time.Since(start); elapsed > time.Second || len(deps) != 1 {
		t.Fatalf("waited %v or lost stale caps: %v", elapsed, deps)
	}
}

func TestCapsCache_FailedColdLoadReturnsNothing(t *testing.T) {
	c := NewCapsCache(&fakeCapsSource{err: errors.New("down")}, discard())
	if deps, aliases := c.Get(context.Background()); deps != nil || aliases != nil {
		t.Fatalf("Get = %v, %v; want nil", deps, aliases)
	}
}
