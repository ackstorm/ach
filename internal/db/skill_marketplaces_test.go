//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertAndListSkillMarketplaces: upsert two rows, assert
// ListSkillMarketplaces returns them ns-scoped + name-ordered, that a re-upsert
// updates in place, and that DeleteSkillMarketplace removes the row. Mirrors
// TestUpsertAndListMarketplaces (#111).
func TestUpsertAndListSkillMarketplaces(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	for _, name := range []string{"bravo", "alpha"} {
		if err := db.UpsertSkillMarketplace(ctx, pool, db.SkillMarketplaceRow{
			Namespace: "ach", Name: name, SyncedStatus: "True", SyncedReason: "",
			SkillsCount: 3, ResourceVersion: "100",
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	// Other-namespace row must be excluded by ns scoping.
	if err := db.UpsertSkillMarketplace(ctx, pool, db.SkillMarketplaceRow{
		Namespace: "other", Name: "charlie", SyncedStatus: "False", SyncedReason: "UpstreamInvalid",
		SkillsCount: 0, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed other-ns: %v", err)
	}

	got, err := db.ListSkillMarketplaces(ctx, pool, "ach")
	if err != nil {
		t.Fatalf("ListSkillMarketplaces: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSkillMarketplaces returned %d rows, want 2 (ns-scoped)", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "bravo" {
		t.Errorf("rows not name-ordered: %q, %q", got[0].Name, got[1].Name)
	}
	if got[0].SyncedStatus != "True" || got[0].SkillsCount != 3 {
		t.Errorf("alpha row = %+v, want SyncedStatus=True SkillsCount=3", got[0])
	}

	// Re-upsert updates in place (no duplicate row).
	if err := db.UpsertSkillMarketplace(ctx, pool, db.SkillMarketplaceRow{
		Namespace: "ach", Name: "alpha", SyncedStatus: "False", SyncedReason: "UpstreamInvalid",
		SkillsCount: 5, ResourceVersion: "101",
	}); err != nil {
		t.Fatalf("re-upsert alpha: %v", err)
	}
	got, err = db.ListSkillMarketplaces(ctx, pool, "ach")
	if err != nil {
		t.Fatalf("ListSkillMarketplaces #2: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("re-upsert created a duplicate: %d rows", len(got))
	}
	if got[0].SyncedStatus != "False" || got[0].SyncedReason != "UpstreamInvalid" || got[0].SkillsCount != 5 {
		t.Errorf("alpha after re-upsert = %+v, want False/UpstreamInvalid/5", got[0])
	}

	// Delete removes the row.
	if err := db.DeleteSkillMarketplace(ctx, pool, "ach", "alpha"); err != nil {
		t.Fatalf("DeleteSkillMarketplace: %v", err)
	}
	got, err = db.ListSkillMarketplaces(ctx, pool, "ach")
	if err != nil {
		t.Fatalf("ListSkillMarketplaces #3: %v", err)
	}
	if len(got) != 1 || got[0].Name != "bravo" {
		t.Errorf("after delete want only bravo; got %+v", got)
	}
}
