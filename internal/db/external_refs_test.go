//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/external_refs.go.
//
// Each test boots a fresh postgres:16-alpine container via testcontainers-go,
// applies migrations 000001 + 000002, exercises one or more ExternalRef
// helpers, and asserts schema-level invariants (force_refresh_requested_at
// clears on UPSERT; pgx.ErrNoRows maps to (nil, nil); reset NULLs every row).
//
// Run via: `make test-integration` or
// `go test -tags=integration ./internal/db/... -count=1`.

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertExternalRef_Insert: a fresh row scans back equal under GetExternalRef.
func TestUpsertExternalRef_Insert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	want := db.ExternalRef{
		Kind:                  "plugin",
		Name:                  "demo-1",
		StorageLocation:       "/var/cache/ach/plugin/demo-1.tar.gz",
		UpstreamRev:           "sha256:abc123",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(15 * time.Minute),
		MaxStalenessSeconds:   3600,
	}
	if err := db.UpsertExternalRef(ctx, pool, want); err != nil {
		t.Fatalf("UpsertExternalRef: %v", err)
	}
	got, err := db.GetExternalRef(ctx, pool, "plugin", "demo-1")
	if err != nil {
		t.Fatalf("GetExternalRef: %v", err)
	}
	if got == nil {
		t.Fatalf("GetExternalRef returned nil after insert")
	}
	assertExternalRefEqual(t, want, *got)
}

// TestUpsertExternalRef_Update: a second UPSERT with the same PK but different
// StorageLocation drives UPDATE not INSERT (no duplicate row).
func TestUpsertExternalRef_Update(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	first := db.ExternalRef{
		Kind: "prompt", Name: "p",
		StorageLocation:       "/var/cache/ach/prompt/p",
		UpstreamRev:           "etag-1",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(10 * time.Minute),
		MaxStalenessSeconds:   60,
	}
	if err := db.UpsertExternalRef(ctx, pool, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second := first
	second.StorageLocation = "/var/cache/ach/prompt/p.v2"
	second.UpstreamRev = "etag-2"
	if err := db.UpsertExternalRef(ctx, pool, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	// Row count must be 1 — the second UPSERT must UPDATE in place.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM external_refs WHERE kind=$1 AND name=$2`,
		"prompt", "p").Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after upsert-twice; got %d", count)
	}
	got, err := db.GetExternalRef(ctx, pool, "prompt", "p")
	if err != nil {
		t.Fatalf("GetExternalRef: %v", err)
	}
	if got.StorageLocation != "/var/cache/ach/prompt/p.v2" {
		t.Errorf("StorageLocation: got %q, want updated value", got.StorageLocation)
	}
	if got.UpstreamRev != "etag-2" {
		t.Errorf("UpstreamRev: got %q, want etag-2", got.UpstreamRev)
	}
}

// TestUpsertExternalRef_ClearsForceRefresh: set force_refresh_requested_at
// manually via raw SQL, call UpsertExternalRef, assert the column is NULL.
// This is the D-07 contract the Phase 3 force-refresh marker depends on.
func TestUpsertExternalRef_ClearsForceRefresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	row := db.ExternalRef{
		Kind: "artifact", Name: "a",
		StorageLocation:       "/var/cache/ach/artifact/a",
		UpstreamRev:           "rev-1",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(time.Hour),
		MaxStalenessSeconds:   3600,
	}
	if err := db.UpsertExternalRef(ctx, pool, row); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	// Simulate Platform API (Phase 3) writing the force-refresh marker.
	if _, err := pool.Exec(ctx,
		`UPDATE external_refs SET force_refresh_requested_at = now() WHERE kind=$1 AND name=$2`,
		"artifact", "a"); err != nil {
		t.Fatalf("manually set force_refresh_requested_at: %v", err)
	}
	// Confirm the column is non-NULL before the reconciler runs.
	var beforeNonNull bool
	if err := pool.QueryRow(ctx,
		`SELECT force_refresh_requested_at IS NOT NULL FROM external_refs WHERE kind=$1 AND name=$2`,
		"artifact", "a").Scan(&beforeNonNull); err != nil {
		t.Fatalf("pre-check force_refresh: %v", err)
	}
	if !beforeNonNull {
		t.Fatal("force_refresh_requested_at should be non-NULL before UpsertExternalRef")
	}
	// Run the reconciler-style UPSERT and confirm the column clears.
	row.LastSuccessfulRefresh = now.Add(time.Minute)
	if err := db.UpsertExternalRef(ctx, pool, row); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	var afterNonNull bool
	if err := pool.QueryRow(ctx,
		`SELECT force_refresh_requested_at IS NOT NULL FROM external_refs WHERE kind=$1 AND name=$2`,
		"artifact", "a").Scan(&afterNonNull); err != nil {
		t.Fatalf("post-check force_refresh: %v", err)
	}
	if afterNonNull {
		t.Error("force_refresh_requested_at MUST be NULL after UpsertExternalRef (D-07 contract)")
	}
}

// TestGetExternalRef_Absent: absent row returns (nil, nil) — never an error.
func TestGetExternalRef_Absent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.GetExternalRef(ctx, pool, "plugin", "does-not-exist")
	if err != nil {
		t.Fatalf("GetExternalRef absent: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("GetExternalRef absent: got %+v, want nil", got)
	}
}

// TestResetExternalRefRefreshOnEmptyCache: insert two rows, call reset, every
// row's last_successful_refresh is NULL (OP-11 PVC-loss recovery contract).
func TestResetExternalRefRefreshOnEmptyCache(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	rows := []db.ExternalRef{
		{Kind: "plugin", Name: "p1", StorageLocation: "/p1", UpstreamRev: "r1",
			LastSuccessfulRefresh: now, NextRefreshAt: now.Add(time.Hour), MaxStalenessSeconds: 3600},
		{Kind: "prompt", Name: "p2", StorageLocation: "/p2", UpstreamRev: "r2",
			LastSuccessfulRefresh: now, NextRefreshAt: now.Add(time.Hour), MaxStalenessSeconds: 3600},
	}
	for _, r := range rows {
		if err := db.UpsertExternalRef(ctx, pool, r); err != nil {
			t.Fatalf("seed upsert %s/%s: %v", r.Kind, r.Name, err)
		}
	}
	if err := db.ResetExternalRefRefreshOnEmptyCache(ctx, pool); err != nil {
		t.Fatalf("ResetExternalRefRefreshOnEmptyCache: %v", err)
	}
	// Verify every row's last_successful_refresh is NULL.
	var nonNullCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM external_refs WHERE last_successful_refresh IS NOT NULL`,
	).Scan(&nonNullCount); err != nil {
		t.Fatalf("count non-NULL rows: %v", err)
	}
	if nonNullCount != 0 {
		t.Errorf("after reset: %d rows still have non-NULL last_successful_refresh; want 0", nonNullCount)
	}
}

// TestDeleteExternalRef: insert then delete; subsequent Get returns (nil, nil).
func TestDeleteExternalRef(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	r := db.ExternalRef{
		Kind: "plugin", Name: "to-delete",
		StorageLocation: "/x", UpstreamRev: "v",
		LastSuccessfulRefresh: now, NextRefreshAt: now, MaxStalenessSeconds: 60,
	}
	if err := db.UpsertExternalRef(ctx, pool, r); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.DeleteExternalRef(ctx, pool, "plugin", "to-delete"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := db.GetExternalRef(ctx, pool, "plugin", "to-delete")
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Errorf("get after delete: got %+v, want nil", got)
	}
	// Delete-on-absent is a no-op (idempotent).
	if err := db.DeleteExternalRef(ctx, pool, "plugin", "to-delete"); err != nil {
		t.Errorf("delete-of-absent should be idempotent; got %v", err)
	}
}

// assertExternalRefEqual compares two ExternalRef values, normalizing
// timestamps to UTC truncated to seconds (Postgres stores microseconds
// but Go time.Now() carries nanoseconds; truncation aligns both sides).
func assertExternalRefEqual(t *testing.T, want, got db.ExternalRef) {
	t.Helper()
	if got.Kind != want.Kind {
		t.Errorf("Kind: got %q, want %q", got.Kind, want.Kind)
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
