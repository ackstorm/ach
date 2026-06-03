//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/plugins.go.
//
// Covers the standard CRUD round-trip per-kind (Upsert, Get, SoftDelete,
// Delete) PLUS the ResolvePluginByName two-arm precedence matrix:
//
// Bare-name arm (marketplace == ""):
//   - TestResolvePluginByName_CRDWins: plugins row wins for bare name.
//   - TestResolvePluginByName_BareNameNoMarketplaceFallback: bare name that
//     exists ONLY in marketplace_plugins resolves to nil (no fallback).
//   - TestResolvePluginByName_NoMatch_NilNil: both tables empty → (nil, nil).
//   - TestResolvePluginByName_SoftDeletedCRDReturnsNil: soft-deleted CRD row
//     is excluded by deletion_timestamp IS NULL; bare resolution → nil.
//
// Scoped arm (marketplace != ""):
//   - TestResolvePluginByName_ScopedExactPK: exact (marketplace_name, name)
//     PK lookup returns the correct marketplace row.
//   - TestResolvePluginByName_ScopedNoMatch_NilNil: scoped name not in the
//     specified marketplace → (nil, nil).

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// ----- Standard CRUD tests -----

// TestUpsertPlugin_InsertThenUpdate.
func TestUpsertPlugin_InsertThenUpdate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	row := db.PluginRow{
		Namespace:             "default",
		Name:                  "caveman",
		StorageLocation:       "/var/cache/ach/plugin/caveman.tar.gz",
		LastSuccessfulRefresh: &now,
		MaxStalenessSeconds:   600,
		ResourceVersion:       "1",
	}
	if err := db.UpsertPlugin(ctx, pool, row); err != nil {
		t.Fatalf("UpsertPlugin: %v", err)
	}
	got, err := db.GetPluginByName(ctx, pool, "default", "caveman")
	if err != nil {
		t.Fatalf("GetPluginByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetPluginByName returned nil after insert")
	}
	if got.StorageLocation != row.StorageLocation {
		t.Errorf("StorageLocation: got %q", got.StorageLocation)
	}
	if got.MaxStalenessSeconds != 600 {
		t.Errorf("MaxStalenessSeconds: got %d", got.MaxStalenessSeconds)
	}
	firstUpdatedAt := got.UpdatedAt

	time.Sleep(50 * time.Millisecond)
	row.MaxStalenessSeconds = 1200
	row.ResourceVersion = "2"
	if err := db.UpsertPlugin(ctx, pool, row); err != nil {
		t.Fatalf("second UpsertPlugin: %v", err)
	}
	got2, err := db.GetPluginByName(ctx, pool, "default", "caveman")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got2.MaxStalenessSeconds != 1200 {
		t.Errorf("MaxStalenessSeconds: got %d, want 1200", got2.MaxStalenessSeconds)
	}
	if got2.ResourceVersion != "2" {
		t.Errorf("ResourceVersion: got %q", got2.ResourceVersion)
	}
	if !got2.UpdatedAt.After(firstUpdatedAt) {
		t.Errorf("UpdatedAt: got %v, want strictly after %v", got2.UpdatedAt, firstUpdatedAt)
	}
}

// TestGetPluginByName_AbsenceReturnsNilNil.
func TestGetPluginByName_AbsenceReturnsNilNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.GetPluginByName(ctx, pool, "default", "missing")
	if err != nil {
		t.Fatalf("absent Get: got error %v", err)
	}
	if got != nil {
		t.Errorf("absent Get: got %+v, want nil", got)
	}
}

// TestSoftDeletePlugin_PreservesRow.
func TestSoftDeletePlugin_PreservesRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertPlugin(ctx, pool, db.PluginRow{
		Namespace: "ns", Name: "p", StorageLocation: "/x", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.SoftDeletePlugin(ctx, pool, "ns", "p"); err != nil {
		t.Fatalf("SoftDeletePlugin: %v", err)
	}
	got, err := db.GetPluginByName(ctx, pool, "ns", "p")
	if err != nil {
		t.Fatalf("Get after SoftDelete: %v", err)
	}
	if got == nil {
		t.Fatal("Get after SoftDelete: row missing (CS-09 violated)")
	}
	if got.DeletionTimestamp == nil {
		t.Error("DeletionTimestamp: got nil, want non-nil")
	}
	if err := db.SoftDeletePlugin(ctx, pool, "ns", "p"); err != nil {
		t.Errorf("idempotent SoftDelete: %v", err)
	}
}

// TestDeletePlugin_RemovesRow.
func TestDeletePlugin_RemovesRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertPlugin(ctx, pool, db.PluginRow{
		Namespace: "ns", Name: "p", StorageLocation: "/x", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.DeletePlugin(ctx, pool, "ns", "p"); err != nil {
		t.Fatalf("DeletePlugin: %v", err)
	}
	got, err := db.GetPluginByName(ctx, pool, "ns", "p")
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if got != nil {
		t.Errorf("Get after Delete: got %+v, want nil", got)
	}
	if err := db.DeletePlugin(ctx, pool, "ns", "p"); err != nil {
		t.Errorf("idempotent Delete: %v", err)
	}
}

// ----- §12.3 ResolvePluginByName precedence tests -----

// TestResolvePluginByName_CRDWins: bare-name lookup (marketplace="") with
// a plugins row present → returns the CRD row, even when a marketplace row
// with the same name exists.
func TestResolvePluginByName_CRDWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	if err := db.UpsertPlugin(ctx, pool, db.PluginRow{
		Namespace: "test", Name: "foo",
		StorageLocation:       "/crd/foo.tar.gz",
		LastSuccessfulRefresh: &now,
		MaxStalenessSeconds:   600,
		ResourceVersion:       "1",
	}); err != nil {
		t.Fatalf("seed plugins: %v", err)
	}
	if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
		MarketplaceName:       "zzz-mkt",
		Name:                  "foo",
		StorageLocation:       "/mkt/foo.tar.gz",
		UpstreamRev:           "rev",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(time.Hour),
		MaxStalenessSeconds:   600,
	}); err != nil {
		t.Fatalf("seed marketplace: %v", err)
	}
	// bare name — marketplace arm must NOT be consulted.
	got, err := db.ResolvePluginByName(ctx, pool, "test", "foo", "")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got == nil {
		t.Fatal("ResolvePluginByName: got nil, want CRD hit")
	}
	if got.Source != "plugin" {
		t.Errorf("Source: got %q, want plugin", got.Source)
	}
	if got.Namespace != "test" {
		t.Errorf("Namespace: got %q, want test", got.Namespace)
	}
	if got.Name != "foo" {
		t.Errorf("Name: got %q", got.Name)
	}
	if got.StorageLocation != "/crd/foo.tar.gz" {
		t.Errorf("StorageLocation: got %q, want /crd/foo.tar.gz (CRD path)", got.StorageLocation)
	}
}

// TestResolvePluginByName_BareNameNoMarketplaceFallback: a name that exists
// only in marketplace_plugins must NOT be returned for a bare-name lookup
// (marketplace=""). Bare resolution is CRD-only; no marketplace fallback.
func TestResolvePluginByName_BareNameNoMarketplaceFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
		MarketplaceName:       "anthropic-mkt",
		Name:                  "only-in-mkt",
		StorageLocation:       "/mkt/only-in-mkt.tar.gz",
		UpstreamRev:           "rev",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(time.Hour),
		MaxStalenessSeconds:   600,
	}); err != nil {
		t.Fatalf("seed marketplace: %v", err)
	}
	// bare name with no CRD row — must resolve to nil, NOT fall back to marketplace.
	got, err := db.ResolvePluginByName(ctx, pool, "ach", "only-in-mkt", "")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil (bare name must not fall back to marketplace)", got)
	}
}

// TestResolvePluginByName_ScopedExactPK: scoped lookup (marketplace != "")
// resolves the exact (marketplace_name, name) PK row.  When multiple
// marketplace rows share the same name, only the requested marketplace is
// returned — no alphabetical tiebreak.
func TestResolvePluginByName_ScopedExactPK(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	// Seed two marketplace rows for the same plugin name "shared".
	for _, m := range []string{"mkt-a", "mkt-b"} {
		if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
			MarketplaceName:       m,
			Name:                  "shared",
			StorageLocation:       "/mkt/" + m + "/shared.tar.gz",
			UpstreamRev:           "rev-" + m,
			LastSuccessfulRefresh: now,
			NextRefreshAt:         now.Add(time.Hour),
			MaxStalenessSeconds:   600,
		}); err != nil {
			t.Fatalf("seed %s: %v", m, err)
		}
	}
	// Request the scoped name from mkt-b specifically.
	got, err := db.ResolvePluginByName(ctx, pool, "ach", "shared", "mkt-b")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got == nil {
		t.Fatal("ResolvePluginByName: got nil, want mkt-b hit")
	}
	if got.Source != "marketplace" {
		t.Errorf("Source: got %q, want marketplace", got.Source)
	}
	if got.Namespace != "mkt-b" {
		t.Errorf("Namespace: got %q, want mkt-b", got.Namespace)
	}
	if got.Name != "shared" {
		t.Errorf("Name: got %q, want shared", got.Name)
	}
	if got.StorageLocation != "/mkt/mkt-b/shared.tar.gz" {
		t.Errorf("StorageLocation: got %q, want /mkt/mkt-b/shared.tar.gz", got.StorageLocation)
	}
}

// TestResolvePluginByName_ScopedNoMatch_NilNil: scoped lookup where the
// name exists in a different marketplace → (nil, nil).
func TestResolvePluginByName_ScopedNoMatch_NilNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
		MarketplaceName:       "mkt-a",
		Name:                  "shared",
		StorageLocation:       "/mkt/mkt-a/shared.tar.gz",
		UpstreamRev:           "rev",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(time.Hour),
		MaxStalenessSeconds:   600,
	}); err != nil {
		t.Fatalf("seed mkt-a: %v", err)
	}
	// Ask for mkt-b which has no row for "shared".
	got, err := db.ResolvePluginByName(ctx, pool, "ach", "shared", "mkt-b")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil (mkt-b has no row for 'shared')", got)
	}
}

// TestResolvePluginByName_ScopedDisambiguatesMarketplace: when multiple
// marketplace rows share the same plugin name, the scoped arm returns ONLY
// the requested marketplace — no alphabetical tiebreak, no UNION.
func TestResolvePluginByName_ScopedDisambiguatesMarketplace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	// Insert in deliberately wrong order: zzz first, anthropic last.
	insertOrder := []string{"zzz-mkt", "internal-mkt", "anthropic-mkt"}
	for _, m := range insertOrder {
		if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
			MarketplaceName:       m,
			Name:                  "foo",
			StorageLocation:       "/mkt/" + m + "/foo.tar.gz",
			UpstreamRev:           "rev-" + m,
			LastSuccessfulRefresh: now,
			NextRefreshAt:         now.Add(time.Hour),
			MaxStalenessSeconds:   600,
		}); err != nil {
			t.Fatalf("seed %s: %v", m, err)
		}
	}
	// Ask for zzz-mkt explicitly — must get that row, NOT anthropic-mkt.
	got, err := db.ResolvePluginByName(ctx, pool, "any", "foo", "zzz-mkt")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want zzz-mkt hit")
	}
	if got.Source != "marketplace" {
		t.Errorf("Source: got %q, want marketplace", got.Source)
	}
	if got.Namespace != "zzz-mkt" {
		t.Errorf("Namespace: got %q, want zzz-mkt (exact scoped match)", got.Namespace)
	}
	if got.StorageLocation != "/mkt/zzz-mkt/foo.tar.gz" {
		t.Errorf("StorageLocation: got %q, want /mkt/zzz-mkt/foo.tar.gz", got.StorageLocation)
	}
}

// TestResolvePluginByName_NoMatch_NilNil: bare lookup with both tables
// empty → (nil, nil).
func TestResolvePluginByName_NoMatch_NilNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.ResolvePluginByName(ctx, pool, "default", "ghost", "")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

// TestResolvePluginByName_SoftDeletedCRDReturnsNil: bare lookup where the
// plugins row is soft-deleted → deletion_timestamp IS NULL excludes it, and
// bare resolution has no marketplace fallback → (nil, nil).
// The marketplace row (if any) is reachable ONLY via a scoped lookup by the
// caller once pluginref.Parse returns marketplace != "".
func TestResolvePluginByName_SoftDeletedCRDReturnsNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	// Insert CRD row, then soft-delete it.
	if err := db.UpsertPlugin(ctx, pool, db.PluginRow{
		Namespace: "test", Name: "foo",
		StorageLocation: "/crd/foo.tar.gz", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed plugins: %v", err)
	}
	if err := db.SoftDeletePlugin(ctx, pool, "test", "foo"); err != nil {
		t.Fatalf("SoftDeletePlugin: %v", err)
	}
	// Insert marketplace row with the same name — should NOT be returned for bare lookup.
	if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
		MarketplaceName:       "any-mkt",
		Name:                  "foo",
		StorageLocation:       "/mkt/foo.tar.gz",
		UpstreamRev:           "rev",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(time.Hour),
		MaxStalenessSeconds:   600,
	}); err != nil {
		t.Fatalf("seed marketplace: %v", err)
	}
	// Bare lookup: soft-deleted CRD excluded, no marketplace fallback → nil.
	got, err := db.ResolvePluginByName(ctx, pool, "test", "foo", "")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil (soft-deleted CRD, no bare fallback)", got)
	}
}
