//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for the G8 skill / skillmarketplace routing added to
// db.SetForceRefresh. "skill" targets external_refs (like plugin/prompt/
// artifact); "skillmarketplace" targets skill_marketplace_skills (like
// pluginmarketplace targets marketplace_plugins).

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestSetForceRefresh_Skill — a "skill" kind sets force_refresh_requested_at
// on its external_refs row.
func TestSetForceRefresh_Skill(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertExternalRef(ctx, pool, db.ExternalRef{
		Kind:                  "skill",
		Name:                  "demo-skill",
		StorageLocation:       "/cache/skill/demo-skill.tar.gz",
		UpstreamRev:           "rev1",
		LastSuccessfulRefresh: time.Now().UTC(),
		NextRefreshAt:         time.Now().Add(time.Hour).UTC(),
		MaxStalenessSeconds:   3600,
	}); err != nil {
		t.Fatalf("UpsertExternalRef(skill): %v", err)
	}

	if err := db.SetForceRefresh(ctx, pool, "ach-system", "skill", "demo-skill"); err != nil {
		t.Fatalf("SetForceRefresh(skill): %v", err)
	}

	var set bool
	if err := pool.QueryRow(ctx,
		`SELECT force_refresh_requested_at IS NOT NULL FROM external_refs WHERE kind='skill' AND name=$1`,
		"demo-skill").Scan(&set); err != nil {
		t.Fatalf("read force_refresh_requested_at: %v", err)
	}
	if !set {
		t.Fatal("force_refresh_requested_at not set on external_refs skill row")
	}
}

// TestSetForceRefresh_SkillMarketplace — a "skillmarketplace" kind sets
// force_refresh_requested_at on every skill_marketplace_skills row under the
// named marketplace.
func TestSetForceRefresh_SkillMarketplace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertSkillMarketplaceSkill(ctx, pool, db.SkillMarketplaceSkill{
		MarketplaceName:       "demo-mkt",
		Name:                  "sk1",
		StorageLocation:       "/cache/skill-marketplace/demo-mkt/sk1.tar.gz",
		UpstreamRev:           "rev1",
		LastSuccessfulRefresh: time.Now().UTC(),
		NextRefreshAt:         time.Now().Add(time.Hour).UTC(),
		MaxStalenessSeconds:   3600,
	}); err != nil {
		t.Fatalf("UpsertSkillMarketplaceSkill: %v", err)
	}

	if err := db.SetForceRefresh(ctx, pool, "ach-system", "skillmarketplace", "demo-mkt"); err != nil {
		t.Fatalf("SetForceRefresh(skillmarketplace): %v", err)
	}

	var set bool
	if err := pool.QueryRow(ctx,
		`SELECT force_refresh_requested_at IS NOT NULL FROM skill_marketplace_skills WHERE marketplace_name=$1 AND name='sk1'`,
		"demo-mkt").Scan(&set); err != nil {
		t.Fatalf("read force_refresh_requested_at: %v", err)
	}
	if !set {
		t.Fatal("force_refresh_requested_at not set on skill_marketplace_skills row")
	}
}
