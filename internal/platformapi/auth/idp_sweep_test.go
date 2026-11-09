// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"testing"
	"time"
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
