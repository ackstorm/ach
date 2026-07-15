//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for the admin-inventory List helpers added alongside the
// `ach admin list` object inventory feature: ListPlugins, ListPrompts,
// ListArtifacts, ListAllMarketplacePlugins.
//
// Each asserts: live rows returned in stable order, soft-deleted rows excluded
// (for tables that have deletion_timestamp), namespace scoping (for ns-keyed
// tables), and an empty (non-nil) slice on zero matches. One container per
// test via setupPostgresForPhase2 (db.Migrate applies the full migrations dir,
// so every projection table exists).

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestListPlugins: live rows in ns ordered by name; soft-deleted + other-ns
// rows excluded.
func TestListPlugins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	for _, n := range []string{"bravo", "alpha"} {
		if err := db.UpsertPlugin(ctx, pool, db.PluginRow{
			Namespace: "ns1", Name: n, StorageLocation: "/x/" + n,
			LastSuccessfulRefresh: &now, MaxStalenessSeconds: 600, ResourceVersion: "1",
		}); err != nil {
			t.Fatalf("seed %s: %v", n, err)
		}
	}
	// Soft-deleted row in ns1 must be excluded.
	if err := db.UpsertPlugin(ctx, pool, db.PluginRow{
		Namespace: "ns1", Name: "gone", StorageLocation: "/x/gone", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed gone: %v", err)
	}
	if err := db.SoftDeletePlugin(ctx, pool, "ns1", "gone"); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	// Row in another namespace must be excluded.
	if err := db.UpsertPlugin(ctx, pool, db.PluginRow{
		Namespace: "ns2", Name: "other", StorageLocation: "/x/other", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed other-ns: %v", err)
	}

	got, err := db.ListPlugins(ctx, pool, "ns1")
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows; want 2", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "bravo" {
		t.Errorf("order: got [%q %q], want [alpha bravo]", got[0].Name, got[1].Name)
	}

	empty, err := db.ListPlugins(ctx, pool, "nonexistent")
	if err != nil {
		t.Fatalf("ListPlugins(empty): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("ListPlugins(empty): got %v, want empty non-nil slice", empty)
	}
}

// TestListPrompts: live rows in ns ordered by name; soft-deleted excluded.
func TestListPrompts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	for _, n := range []string{"charlie", "alpha"} {
		if err := db.UpsertPrompt(ctx, pool, db.PromptRow{
			Namespace: "ns1", Name: n, StorageLocation: "/p/" + n,
			LastSuccessfulRefresh: &now, MaxStalenessSeconds: 600, ResourceVersion: "1",
		}); err != nil {
			t.Fatalf("seed %s: %v", n, err)
		}
	}
	if err := db.UpsertPrompt(ctx, pool, db.PromptRow{
		Namespace: "ns1", Name: "gone", StorageLocation: "/p/gone", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed gone: %v", err)
	}
	if err := db.SoftDeletePrompt(ctx, pool, "ns1", "gone"); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	got, err := db.ListPrompts(ctx, pool, "ns1")
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows; want 2", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "charlie" {
		t.Errorf("order: got [%q %q], want [alpha charlie]", got[0].Name, got[1].Name)
	}
}

// TestListArtifacts: live rows in ns ordered by name; soft-deleted excluded.
func TestListArtifacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	for _, n := range []string{"bravo", "alpha"} {
		if err := db.UpsertArtifact(ctx, pool, db.ArtifactRow{
			Namespace: "ns1", Name: n, StorageLocation: "/a/" + n, Scope: "object",
			LastSuccessfulRefresh: &now, MaxStalenessSeconds: 600, ResourceVersion: "1",
		}); err != nil {
			t.Fatalf("seed %s: %v", n, err)
		}
	}
	if err := db.UpsertArtifact(ctx, pool, db.ArtifactRow{
		Namespace: "ns1", Name: "gone", StorageLocation: "/a/gone", Scope: "directory", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed gone: %v", err)
	}
	if err := db.SoftDeleteArtifact(ctx, pool, "ns1", "gone"); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	got, err := db.ListArtifacts(ctx, pool, "ns1")
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows; want 2", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "bravo" {
		t.Errorf("order: got [%q %q], want [alpha bravo]", got[0].Name, got[1].Name)
	}
	if got[0].Scope != "object" {
		t.Errorf("Scope: got %q, want object", got[0].Scope)
	}
}

// TestListAllMarketplacePlugins: rows across marketplaces ordered by
// (marketplace_name, name); empty DB returns empty non-nil slice.
func TestListAllMarketplacePlugins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	empty, err := db.ListAllMarketplacePlugins(ctx, pool)
	if err != nil {
		t.Fatalf("ListAllMarketplacePlugins(empty): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("empty DB: got %v, want empty non-nil slice", empty)
	}

	now := time.Now().UTC().Truncate(time.Second)
	seed := []struct{ mkt, name string }{
		{"zzz-mkt", "foo"},
		{"anthropic-mkt", "bar"},
		{"anthropic-mkt", "aaa"},
	}
	for _, s := range seed {
		if err := db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
			MarketplaceName: s.mkt, Name: s.name, StorageLocation: "/m/" + s.mkt + "/" + s.name,
			UpstreamRev: "rev", LastSuccessfulRefresh: now, NextRefreshAt: now.Add(time.Hour), MaxStalenessSeconds: 600,
		}); err != nil {
			t.Fatalf("seed %s/%s: %v", s.mkt, s.name, err)
		}
	}

	got, err := db.ListAllMarketplacePlugins(ctx, pool)
	if err != nil {
		t.Fatalf("ListAllMarketplacePlugins: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows; want 3", len(got))
	}
	// Order: anthropic-mkt/aaa, anthropic-mkt/bar, zzz-mkt/foo.
	want := []string{"anthropic-mkt/aaa", "anthropic-mkt/bar", "zzz-mkt/foo"}
	for i, w := range want {
		key := got[i].MarketplaceName + "/" + got[i].Name
		if key != w {
			t.Errorf("row %d: got %q, want %q", i, key, w)
		}
	}
}
