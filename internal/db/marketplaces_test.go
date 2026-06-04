//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertAndListMarketplaces: upsert two rows, assert ListMarketplaces
// returns them ns-scoped + name-ordered, that a re-upsert updates in place,
// and that DeleteMarketplace removes the row.
func TestUpsertAndListMarketplaces(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	for _, name := range []string{"bravo", "alpha"} {
		if err := db.UpsertMarketplace(ctx, pool, db.MarketplaceRow{
			Namespace: "ach", Name: name, SyncedStatus: "True", SyncedReason: "",
			PluginsCount: 3, ResourceVersion: "100",
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	// Other-namespace row must be excluded by ns scoping.
	if err := db.UpsertMarketplace(ctx, pool, db.MarketplaceRow{
		Namespace: "other", Name: "charlie", SyncedStatus: "False", SyncedReason: "UpstreamInvalid",
		PluginsCount: 0, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed other-ns: %v", err)
	}

	got, err := db.ListMarketplaces(ctx, pool, "ach")
	if err != nil {
		t.Fatalf("ListMarketplaces: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListMarketplaces returned %d rows, want 2 (ns-scoped)", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "bravo" {
		t.Errorf("rows not name-ordered: %q, %q", got[0].Name, got[1].Name)
	}
	if got[0].SyncedStatus != "True" || got[0].PluginsCount != 3 {
		t.Errorf("alpha row = %+v, want SyncedStatus=True PluginsCount=3", got[0])
	}

	// Re-upsert updates in place (no duplicate row).
	if err := db.UpsertMarketplace(ctx, pool, db.MarketplaceRow{
		Namespace: "ach", Name: "alpha", SyncedStatus: "False", SyncedReason: "Unreachable",
		PluginsCount: 5, ResourceVersion: "101",
	}); err != nil {
		t.Fatalf("re-upsert alpha: %v", err)
	}
	got, err = db.ListMarketplaces(ctx, pool, "ach")
	if err != nil {
		t.Fatalf("ListMarketplaces #2: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("re-upsert created a duplicate: %d rows", len(got))
	}
	if got[0].SyncedStatus != "False" || got[0].SyncedReason != "Unreachable" || got[0].PluginsCount != 5 {
		t.Errorf("alpha after re-upsert = %+v, want False/Unreachable/5", got[0])
	}

	// Delete removes the row.
	if err := db.DeleteMarketplace(ctx, pool, "ach", "alpha"); err != nil {
		t.Fatalf("DeleteMarketplace: %v", err)
	}
	got, err = db.ListMarketplaces(ctx, pool, "ach")
	if err != nil {
		t.Fatalf("ListMarketplaces #3: %v", err)
	}
	if len(got) != 1 || got[0].Name != "bravo" {
		t.Errorf("after delete want only bravo; got %+v", got)
	}
}
