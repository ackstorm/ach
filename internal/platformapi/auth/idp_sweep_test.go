// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Each owner in use is replayed at Dex at most once per idpCheckEvery, and
// an owner without a stored Dex token is skipped, not refused.
func TestSweepIdP_ReplaysEachOwnerOncePerPeriod(t *testing.T) {
	f := newAS(t)
	installFakePKs(f)
	ctx := context.Background()
	_ = f.store.Put(ctx, dexRefreshKind, "a@x.com", "rt-a", time.Hour)
	_ = f.store.Put(ctx, dexRefreshKind, "b@x.com", "rt-b", time.Hour)
	f.deps.EkOwnersInUse = func(context.Context) ([]string, error) {
		return []string{"a@x.com", "b@x.com", "nodex@x.com"}, nil
	}

	f.deps.sweepIdP(ctx)
	if len(f.dexSeen) != 2 {
		t.Fatalf("first sweep replayed %v, want rt-a and rt-b", f.dexSeen)
	}
	f.deps.sweepIdP(ctx)
	if len(f.dexSeen) != 2 {
		t.Fatalf("second sweep inside the period replayed again: %v", f.dexSeen)
	}
	if len(f.refusals) != 0 {
		t.Fatalf("refusals = %v, want none", f.refusals)
	}
}

// A check that could not reach a verdict releases the claim: an unreachable
// Dex, or a Valkey read error, is retried at the next sweep.
func TestSweepIdP_RetriesAnOwnerWithoutAVerdict(t *testing.T) {
	f := newAS(t)
	installFakePKs(f)
	ctx := context.Background()
	_ = f.store.Put(ctx, dexRefreshKind, "a@x.com", "rt-a", time.Hour)
	_ = f.store.RDB.Set(ctx, oauthKey(dexRefreshKind, "bad@x.com"), "not json", time.Hour).Err()
	f.deps.EkOwnersInUse = func(context.Context) ([]string, error) {
		return []string{"a@x.com", "bad@x.com"}, nil
	}
	f.dexRefresh = func(string) (string, error) { return "", errors.New("dial tcp: connection refused") }

	f.deps.sweepIdP(ctx)
	f.deps.sweepIdP(ctx)
	if len(f.dexSeen) != 2 {
		t.Fatalf("Dex replays = %v, want rt-a on each sweep", f.dexSeen)
	}
	if claimed, err := f.store.Claim(ctx, idpCheckKind, "bad@x.com", time.Minute); err != nil || !claimed {
		t.Fatalf("claim after a Valkey read error: %v %v, want released", claimed, err)
	}
}

// A Valkey error after Dex answered (here: storing the rotated token) is no
// verdict either: the claim is released and the next sweep checks again.
// Only an IdP refusal keeps it.
func TestSweepIdP_RetriesAnOwnerAfterAValkeyWriteError(t *testing.T) {
	f := newAS(t)
	installFakePKs(f)
	ctx := context.Background()
	_ = f.store.Put(ctx, dexRefreshKind, "a@x.com", "rt-a", time.Hour)
	f.deps.EkOwnersInUse = func(context.Context) ([]string, error) { return []string{"a@x.com"}, nil }
	f.store.RDB.AddHook(&failSetOnce{key: oauthKey(dexRefreshKind, "a@x.com")})

	f.deps.sweepIdP(ctx)
	if claimed, err := f.store.Claim(ctx, idpCheckKind, "a@x.com", time.Minute); err != nil || !claimed {
		t.Fatalf("claim after a Valkey write error: %v %v, want released", claimed, err)
	}
}

// failSetOnce fails the first SET of key, as a dropped Valkey connection.
type failSetOnce struct {
	key  string
	done bool
}

func (h *failSetOnce) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *failSetOnce) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *failSetOnce) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if args := cmd.Args(); !h.done && cmd.Name() == "set" && len(args) > 1 && args[1] == h.key {
			h.done = true
			err := &net.OpError{Op: "write", Net: "tcp", Err: errors.New("connection reset by peer")}
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	}
}
