//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestSkillMarketplaceSkills_UpsertListDelete: upsert rows under one
// marketplace, assert ListSkillMarketplaceSkills is marketplace-scoped, a
// re-upsert updates in place, ListAll spans marketplaces name-ordered, and
// DeleteSkillMarketplaceSkill removes the row.
func TestSkillMarketplaceSkills_UpsertListDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	last := time.Now()
	for _, name := range []string{"bravo", "alpha"} {
		if err := db.UpsertSkillMarketplaceSkill(ctx, pool, db.SkillMarketplaceSkill{
			MarketplaceName: "ackstorm", Name: name,
			StorageLocation:       "/var/cache/ach/skill-marketplace/ackstorm/" + name + ".tar.gz",
			UpstreamRev:           "abc1234",
			LastSuccessfulRefresh: last, NextRefreshAt: last.Add(time.Hour),
			MaxStalenessSeconds: 600,
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	// A row under another marketplace must be excluded by scoping.
	if err := db.UpsertSkillMarketplaceSkill(ctx, pool, db.SkillMarketplaceSkill{
		MarketplaceName: "other", Name: "charlie",
		StorageLocation:       "/var/cache/ach/skill-marketplace/other/charlie.tar.gz",
		LastSuccessfulRefresh: last, NextRefreshAt: last.Add(time.Hour), MaxStalenessSeconds: 1,
	}); err != nil {
		t.Fatalf("seed other-mkt: %v", err)
	}

	got, err := db.ListSkillMarketplaceSkills(ctx, pool, "ackstorm")
	if err != nil {
		t.Fatalf("ListSkillMarketplaceSkills: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSkillMarketplaceSkills returned %d rows, want 2 (mkt-scoped)", len(got))
	}

	// Re-upsert updates in place (no duplicate row).
	if err := db.UpsertSkillMarketplaceSkill(ctx, pool, db.SkillMarketplaceSkill{
		MarketplaceName: "ackstorm", Name: "alpha",
		StorageLocation:       "/var/cache/ach/skill-marketplace/ackstorm/alpha.tar.gz",
		UpstreamRev:           "def5678",
		LastSuccessfulRefresh: last, NextRefreshAt: last.Add(time.Hour), MaxStalenessSeconds: 900,
	}); err != nil {
		t.Fatalf("re-upsert alpha: %v", err)
	}
	got, err = db.ListSkillMarketplaceSkills(ctx, pool, "ackstorm")
	if err != nil || len(got) != 2 {
		t.Fatalf("re-upsert created a duplicate: %d rows err=%v", len(got), err)
	}

	// ListAll spans marketplaces, ordered (marketplace_name, name).
	all, err := db.ListAllSkillMarketplaceSkills(ctx, pool)
	if err != nil {
		t.Fatalf("ListAllSkillMarketplaceSkills: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListAll returned %d rows, want 3", len(all))
	}
	if all[0].MarketplaceName != "ackstorm" || all[0].Name != "alpha" ||
		all[1].Name != "bravo" || all[2].MarketplaceName != "other" {
		t.Errorf("ListAll not (mkt,name) ordered: %+v", all)
	}

	// Delete removes the row.
	if err := db.DeleteSkillMarketplaceSkill(ctx, pool, "ackstorm", "alpha"); err != nil {
		t.Fatalf("DeleteSkillMarketplaceSkill: %v", err)
	}
	got, _ = db.ListSkillMarketplaceSkills(ctx, pool, "ackstorm")
	if len(got) != 1 || got[0].Name != "bravo" {
		t.Errorf("after delete want only bravo; got %+v", got)
	}
}

// TestResolveSkillByName_MarketplaceArm: the scoped arm resolves the
// skill_marketplace_skills row with Source="marketplace" and Namespace carrying
// the marketplace_name; an unknown (name, marketplace) returns (nil, nil).
func TestResolveSkillByName_MarketplaceArm(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	last := time.Now()
	if err := db.UpsertSkillMarketplaceSkill(ctx, pool, db.SkillMarketplaceSkill{
		MarketplaceName: "ackstorm", Name: "branding",
		StorageLocation:       "/var/cache/ach/skill-marketplace/ackstorm/branding.tar.gz",
		LastSuccessfulRefresh: last, NextRefreshAt: last.Add(time.Hour), MaxStalenessSeconds: 600,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := db.ResolveSkillByName(ctx, pool, "ach", "branding", "ackstorm")
	if err != nil || res == nil {
		t.Fatalf("ResolveSkillByName(scoped) = %+v err=%v", res, err)
	}
	if res.Source != "marketplace" || res.Namespace != "ackstorm" || res.Name != "branding" ||
		res.StorageLocation == "" || res.LastSuccessfulRefresh == nil {
		t.Errorf("scoped resolution = %+v, want Source=marketplace Namespace=ackstorm", res)
	}

	// Unknown scoped name → (nil, nil).
	miss, err := db.ResolveSkillByName(ctx, pool, "ach", "ghost", "ackstorm")
	if err != nil || miss != nil {
		t.Errorf("unknown scoped = %+v err=%v, want (nil,nil)", miss, err)
	}
	// Bare arm with no skills row → (nil, nil).
	bare, err := db.ResolveSkillByName(ctx, pool, "ach", "branding", "")
	if err != nil || bare != nil {
		t.Errorf("bare arm (no skills row) = %+v err=%v, want (nil,nil)", bare, err)
	}
}
