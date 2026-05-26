//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/marketplace_plugins.go.

package db_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertMarketplacePlugin_Insert: fresh row scans back via ListMarketplacePlugins.
func TestUpsertMarketplacePlugin_Insert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	want := db.MarketplacePlugin{
		MarketplaceName:       "acme",
		Name:                  "tool-1",
		StorageLocation:       "/var/cache/ach/marketplace/acme/tool-1.tar.gz",
		UpstreamRev:           "sha256:abc",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(time.Hour),
		MaxStalenessSeconds:   3600,
	}
	if err := db.UpsertMarketplacePlugin(ctx, pool, want); err != nil {
		t.Fatalf("UpsertMarketplacePlugin: %v", err)
	}
	got, err := db.ListMarketplacePlugins(ctx, pool, "acme")
	if err != nil {
		t.Fatalf("ListMarketplacePlugins: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListMarketplacePlugins len: got %d, want 1", len(got))
	}
	assertMarketplacePluginEqual(t, want, got[0])
}

// TestUpsertMarketplacePlugin_Update: a second UPSERT with the same PK
// updates in place — no duplicate row.
func TestUpsertMarketplacePlugin_Update(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	first := db.MarketplacePlugin{
		MarketplaceName: "acme", Name: "t",
		StorageLocation:       "/loc/v1",
		UpstreamRev:           "rev-1",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(10 * time.Minute),
		MaxStalenessSeconds:   60,
	}
	if err := db.UpsertMarketplacePlugin(ctx, pool, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second := first
	second.StorageLocation = "/loc/v2"
	second.UpstreamRev = "rev-2"
	if err := db.UpsertMarketplacePlugin(ctx, pool, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, err := db.ListMarketplacePlugins(ctx, pool, "acme")
	if err != nil {
		t.Fatalf("ListMarketplacePlugins: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row after upsert-twice; got %d", len(got))
	}
	if got[0].StorageLocation != "/loc/v2" {
		t.Errorf("StorageLocation: got %q, want /loc/v2", got[0].StorageLocation)
	}
	if got[0].UpstreamRev != "rev-2" {
		t.Errorf("UpstreamRev: got %q, want rev-2", got[0].UpstreamRev)
	}
}

// TestUpsertMarketplacePlugin_ClearsForceRefresh: D-07 — UPSERT clears
// force_refresh_requested_at in the same UPDATE.
func TestUpsertMarketplacePlugin_ClearsForceRefresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	row := db.MarketplacePlugin{
		MarketplaceName: "m", Name: "p",
		StorageLocation:       "/x",
		UpstreamRev:           "r",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now,
		MaxStalenessSeconds:   60,
	}
	if err := db.UpsertMarketplacePlugin(ctx, pool, row); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE marketplace_plugins SET force_refresh_requested_at = now() WHERE marketplace_name=$1 AND name=$2`,
		"m", "p"); err != nil {
		t.Fatalf("manually set force_refresh_requested_at: %v", err)
	}
	row.LastSuccessfulRefresh = now.Add(time.Minute)
	if err := db.UpsertMarketplacePlugin(ctx, pool, row); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	var nonNull bool
	if err := pool.QueryRow(ctx,
		`SELECT force_refresh_requested_at IS NOT NULL FROM marketplace_plugins WHERE marketplace_name=$1 AND name=$2`,
		"m", "p").Scan(&nonNull); err != nil {
		t.Fatalf("post-check: %v", err)
	}
	if nonNull {
		t.Error("force_refresh_requested_at MUST be NULL after UpsertMarketplacePlugin (D-07)")
	}
}

// TestListMarketplacePlugins: three rows under the same marketplace, sorted
// by Name for deterministic assertion.
func TestListMarketplacePlugins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	names := []string{"alpha", "bravo", "charlie"}
	for _, n := range names {
		if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
			MarketplaceName: "shared", Name: n,
			StorageLocation:       "/cache/" + n,
			UpstreamRev:           "r-" + n,
			LastSuccessfulRefresh: now,
			NextRefreshAt:         now.Add(time.Hour),
			MaxStalenessSeconds:   3600,
		}); err != nil {
			t.Fatalf("seed upsert %s: %v", n, err)
		}
	}
	// Insert one row under a different marketplace; ListMarketplacePlugins("shared")
	// must NOT see it.
	if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
		MarketplaceName: "other", Name: "alpha",
		StorageLocation: "/cache/other/alpha", UpstreamRev: "x",
		LastSuccessfulRefresh: now, NextRefreshAt: now, MaxStalenessSeconds: 60,
	}); err != nil {
		t.Fatalf("seed other-marketplace row: %v", err)
	}

	got, err := db.ListMarketplacePlugins(ctx, pool, "shared")
	if err != nil {
		t.Fatalf("ListMarketplacePlugins: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows; want 3", len(got))
	}
	gotNames := make([]string, len(got))
	for i, p := range got {
		gotNames[i] = p.Name
	}
	sort.Strings(gotNames)
	want := []string{"alpha", "bravo", "charlie"}
	for i, n := range want {
		if gotNames[i] != n {
			t.Errorf("row %d: got %q, want %q", i, gotNames[i], n)
		}
	}

	// Empty marketplace returns empty slice + nil error.
	got, err = db.ListMarketplacePlugins(ctx, pool, "nonexistent")
	if err != nil {
		t.Errorf("ListMarketplacePlugins(empty): got error %v, want nil", err)
	}
	if got == nil {
		t.Error("ListMarketplacePlugins(empty): got nil slice, want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("ListMarketplacePlugins(empty): got %d rows, want 0", len(got))
	}
}

// TestDeleteMarketplacePlugin: insert + delete; subsequent List returns one
// fewer row (and zero rows when only one was inserted).
func TestDeleteMarketplacePlugin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
		MarketplaceName: "m", Name: "to-delete",
		StorageLocation:       "/x",
		UpstreamRev:           "v",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now,
		MaxStalenessSeconds:   60,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.DeleteMarketplacePlugin(ctx, pool, "m", "to-delete"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := db.ListMarketplacePlugins(ctx, pool, "m")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d rows after delete; want 0", len(got))
	}
	// Idempotent delete.
	if err := db.DeleteMarketplacePlugin(ctx, pool, "m", "to-delete"); err != nil {
		t.Errorf("delete-of-absent should be idempotent; got %v", err)
	}
}

// TestResetMarketplacePluginsRefreshOnEmptyCache: OP-11 reset.
func TestResetMarketplacePluginsRefreshOnEmptyCache(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	rows := []db.MarketplacePlugin{
		{MarketplaceName: "m", Name: "p1", StorageLocation: "/p1", UpstreamRev: "r1",
			LastSuccessfulRefresh: now, NextRefreshAt: now.Add(time.Hour), MaxStalenessSeconds: 3600},
		{MarketplaceName: "m", Name: "p2", StorageLocation: "/p2", UpstreamRev: "r2",
			LastSuccessfulRefresh: now, NextRefreshAt: now.Add(time.Hour), MaxStalenessSeconds: 3600},
	}
	for _, r := range rows {
		if err := db.UpsertMarketplacePlugin(ctx, pool, r); err != nil {
			t.Fatalf("seed upsert: %v", err)
		}
	}
	if err := db.ResetMarketplacePluginsRefreshOnEmptyCache(ctx, pool); err != nil {
		t.Fatalf("ResetMarketplacePluginsRefreshOnEmptyCache: %v", err)
	}
	var nonNullCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM marketplace_plugins WHERE last_successful_refresh IS NOT NULL`,
	).Scan(&nonNullCount); err != nil {
		t.Fatalf("count non-NULL rows: %v", err)
	}
	if nonNullCount != 0 {
		t.Errorf("after reset: %d rows still have non-NULL last_successful_refresh; want 0", nonNullCount)
	}
}

// assertMarketplacePluginEqual compares two MarketplacePlugin values.
func assertMarketplacePluginEqual(t *testing.T, want, got db.MarketplacePlugin) {
	t.Helper()
	if got.MarketplaceName != want.MarketplaceName {
		t.Errorf("MarketplaceName: got %q, want %q", got.MarketplaceName, want.MarketplaceName)
	}
	if got.Name != want.Name {
		t.Errorf("Name: got %q, want %q", got.Name, want.Name)
	}
	if got.StorageLocation != want.StorageLocation {
		t.Errorf("StorageLocation: got %q, want %q", got.StorageLocation, want.StorageLocation)
	}
	if got.UpstreamRev != want.UpstreamRev {
		t.Errorf("UpstreamRev: got %q, want %q", got.UpstreamRev, want.UpstreamRev)
	}
	if !got.LastSuccessfulRefresh.UTC().Truncate(time.Second).Equal(want.LastSuccessfulRefresh.UTC().Truncate(time.Second)) {
		t.Errorf("LastSuccessfulRefresh: got %v, want %v", got.LastSuccessfulRefresh, want.LastSuccessfulRefresh)
	}
	if !got.NextRefreshAt.UTC().Truncate(time.Second).Equal(want.NextRefreshAt.UTC().Truncate(time.Second)) {
		t.Errorf("NextRefreshAt: got %v, want %v", got.NextRefreshAt, want.NextRefreshAt)
	}
	if got.MaxStalenessSeconds != want.MaxStalenessSeconds {
		t.Errorf("MaxStalenessSeconds: got %d, want %d", got.MaxStalenessSeconds, want.MaxStalenessSeconds)
	}
}
