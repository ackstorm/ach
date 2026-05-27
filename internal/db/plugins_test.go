//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/plugins.go.
//
// Covers the standard CRUD round-trip per-kind (Upsert, Get, SoftDelete,
// Delete) PLUS the §12.3 ResolvePluginByName precedence matrix:
//
//   - TestResolvePluginByName_CRDWins: plugins row wins when present.
//   - TestResolvePluginByName_MarketplaceFallback: marketplace_plugins
//     wins when no CRD row exists.
//   - TestResolvePluginByName_AlphabeticallyLowestMarketplace: among
//     multiple marketplace_plugins rows with the same `name`, the row
//     whose marketplace_name sorts alphabetically lowest (Unicode
//     code-point ASC) wins — locks in T-05-02-04.
//   - TestResolvePluginByName_NoMatch_NilNil: both tables empty → (nil, nil).
//   - TestResolvePluginByName_SoftDeletedCRDFallsThrough: a soft-deleted
//     CRD MUST NOT shadow live marketplace rows (regression for the §12.3
//     WHERE deletion_timestamp IS NULL clause).

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

// seedPluginsRow inserts a plugins row via the package's UpsertPlugin helper.
func seedPluginsRow(t *testing.T, ctx context.Context, pool interface {
	// minimal interface matching pool.Exec — defer to UpsertPlugin in practice
}, ns, name string) {
	t.Helper()
}

// TestResolvePluginByName_CRDWins: plugins row + marketplace_plugins row
// both present → CTE picks the plugins (CRD) row.
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
	got, err := db.ResolvePluginByName(ctx, pool, "test", "foo")
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

// TestResolvePluginByName_MarketplaceFallback: only marketplace row exists
// → CTE picks marketplace arm. Note: the namespace argument is irrelevant
// for the marketplace fallback (we still pass it to match the CRD-arm
// WHERE, but the marketplace match keys only on `name`).
func TestResolvePluginByName_MarketplaceFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
		MarketplaceName:       "anthropic-mkt",
		Name:                  "foo",
		StorageLocation:       "/mkt/foo.tar.gz",
		UpstreamRev:           "rev",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(time.Hour),
		MaxStalenessSeconds:   600,
	}); err != nil {
		t.Fatalf("seed marketplace: %v", err)
	}
	got, err := db.ResolvePluginByName(ctx, pool, "irrelevant-ns", "foo")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got == nil {
		t.Fatal("ResolvePluginByName: got nil, want marketplace hit")
	}
	if got.Source != "marketplace" {
		t.Errorf("Source: got %q, want marketplace", got.Source)
	}
	if got.Namespace != "anthropic-mkt" {
		t.Errorf("Namespace: got %q, want anthropic-mkt (marketplace_name surfaced)", got.Namespace)
	}
	if got.StorageLocation != "/mkt/foo.tar.gz" {
		t.Errorf("StorageLocation: got %q", got.StorageLocation)
	}
}

// TestResolvePluginByName_AlphabeticallyLowestMarketplace: 3 marketplace_plugins
// rows for the same `name`, inserted in NON-alphabetical order so a naive
// "first inserted wins" would FAIL. CTE MUST pick the alphabetically lowest
// marketplace_name ("anthropic-mkt" < "internal-mkt" < "zzz-mkt").
func TestResolvePluginByName_AlphabeticallyLowestMarketplace(t *testing.T) {
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
	got, err := db.ResolvePluginByName(ctx, pool, "any", "foo")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want marketplace hit")
	}
	if got.Source != "marketplace" {
		t.Errorf("Source: got %q, want marketplace", got.Source)
	}
	if got.Namespace != "anthropic-mkt" {
		t.Errorf("Namespace: got %q, want anthropic-mkt (alphabetically lowest)", got.Namespace)
	}
	if got.StorageLocation != "/mkt/anthropic-mkt/foo.tar.gz" {
		t.Errorf("StorageLocation: got %q, want /mkt/anthropic-mkt/foo.tar.gz", got.StorageLocation)
	}
}

// TestResolvePluginByName_NoMatch_NilNil: both tables empty → (nil, nil).
func TestResolvePluginByName_NoMatch_NilNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.ResolvePluginByName(ctx, pool, "default", "ghost")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

// TestResolvePluginByName_SoftDeletedCRDFallsThrough: plugins row exists
// but is soft-deleted → CTE's `deletion_timestamp IS NULL` filter routes
// past it and returns the marketplace match instead. Locks in
// T-05-02-04 (soft-deleted CRD MUST NOT shadow live marketplace rows).
func TestResolvePluginByName_SoftDeletedCRDFallsThrough(t *testing.T) {
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
	// Insert marketplace row with the same name.
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
	got, err := db.ResolvePluginByName(ctx, pool, "test", "foo")
	if err != nil {
		t.Fatalf("ResolvePluginByName: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want marketplace fallback (CRD was soft-deleted)")
	}
	if got.Source != "marketplace" {
		t.Errorf("Source: got %q, want marketplace (soft-deleted CRD must NOT shadow)", got.Source)
	}
	if got.Namespace != "any-mkt" {
		t.Errorf("Namespace: got %q, want any-mkt", got.Namespace)
	}
}
