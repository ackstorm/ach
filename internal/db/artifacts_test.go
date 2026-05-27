//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/artifacts.go.
//
// Includes a regression for the scope CHECK constraint — Upsert with a
// scope outside {'object','directory'} MUST fail with a wrapped (terminal)
// error, not silently succeed.

package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertArtifact_InsertThenUpdate: round-trip both scope variants and
// verify UPDATE-in-place.
func TestUpsertArtifact_InsertThenUpdate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	row := db.ArtifactRow{
		Namespace:             "default",
		Name:                  "templates",
		StorageLocation:       "/var/cache/ach/artifact/templates.tar.gz",
		Scope:                 "directory",
		LastSuccessfulRefresh: &now,
		MaxStalenessSeconds:   600,
		ResourceVersion:       "1",
	}
	if err := db.UpsertArtifact(ctx, pool, row); err != nil {
		t.Fatalf("UpsertArtifact: %v", err)
	}
	got, err := db.GetArtifactByName(ctx, pool, "default", "templates")
	if err != nil {
		t.Fatalf("GetArtifactByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetArtifactByName returned nil after insert")
	}
	if got.Scope != "directory" {
		t.Errorf("Scope: got %q, want directory", got.Scope)
	}
	if got.StorageLocation != "/var/cache/ach/artifact/templates.tar.gz" {
		t.Errorf("StorageLocation: got %q", got.StorageLocation)
	}
	firstUpdatedAt := got.UpdatedAt

	// Mutate scope + storage_location; round-trip.
	time.Sleep(50 * time.Millisecond)
	row.Scope = "object"
	row.StorageLocation = "/var/cache/ach/artifact/templates"
	row.ResourceVersion = "2"
	if err := db.UpsertArtifact(ctx, pool, row); err != nil {
		t.Fatalf("second UpsertArtifact: %v", err)
	}
	got2, err := db.GetArtifactByName(ctx, pool, "default", "templates")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got2.Scope != "object" {
		t.Errorf("after Upsert: Scope got %q, want object", got2.Scope)
	}
	if got2.StorageLocation != "/var/cache/ach/artifact/templates" {
		t.Errorf("after Upsert: StorageLocation got %q", got2.StorageLocation)
	}
	if !got2.UpdatedAt.After(firstUpdatedAt) {
		t.Errorf("UpdatedAt: got %v, want strictly after %v", got2.UpdatedAt, firstUpdatedAt)
	}
}

// TestUpsertArtifact_InvalidScopeRejected: an Upsert with a scope outside
// the SQL CHECK enum MUST fail with a wrapped (non-transient) error and
// MUST NOT insert a row. Locks in T-05-02-Tampering on the scope CHECK.
func TestUpsertArtifact_InvalidScopeRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	err := db.UpsertArtifact(ctx, pool, db.ArtifactRow{
		Namespace: "default", Name: "bad", StorageLocation: "/x",
		Scope: "wrong-value", MaxStalenessSeconds: 60, ResourceVersion: "1",
	})
	if err == nil {
		t.Fatal("UpsertArtifact with invalid scope: got nil error, want CHECK violation")
	}
	if !strings.Contains(err.Error(), "UpsertArtifact") {
		t.Errorf("error wrap: got %v, want fmt.Errorf wrapping with UpsertArtifact prefix", err)
	}
	// Row MUST NOT exist.
	got, getErr := db.GetArtifactByName(ctx, pool, "default", "bad")
	if getErr != nil {
		t.Fatalf("post-fail Get: %v", getErr)
	}
	if got != nil {
		t.Errorf("CHECK was bypassed: row exists for invalid scope %+v", got)
	}
}

// TestGetArtifactByName_AbsenceReturnsNilNil.
func TestGetArtifactByName_AbsenceReturnsNilNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.GetArtifactByName(ctx, pool, "default", "ghost")
	if err != nil {
		t.Fatalf("absent Get: got error %v", err)
	}
	if got != nil {
		t.Errorf("absent Get: got %+v, want nil", got)
	}
}

// TestSoftDeleteArtifact_PreservesRow.
func TestSoftDeleteArtifact_PreservesRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertArtifact(ctx, pool, db.ArtifactRow{
		Namespace: "ns", Name: "a", StorageLocation: "/x",
		Scope: "object", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.SoftDeleteArtifact(ctx, pool, "ns", "a"); err != nil {
		t.Fatalf("SoftDeleteArtifact: %v", err)
	}
	got, err := db.GetArtifactByName(ctx, pool, "ns", "a")
	if err != nil {
		t.Fatalf("Get after SoftDelete: %v", err)
	}
	if got == nil {
		t.Fatal("Get after SoftDelete: row missing (CS-09 violated)")
	}
	if got.DeletionTimestamp == nil {
		t.Error("DeletionTimestamp: got nil, want non-nil")
	}
	if err := db.SoftDeleteArtifact(ctx, pool, "ns", "a"); err != nil {
		t.Errorf("idempotent SoftDelete: %v", err)
	}
}

// TestDeleteArtifact_RemovesRow.
func TestDeleteArtifact_RemovesRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertArtifact(ctx, pool, db.ArtifactRow{
		Namespace: "ns", Name: "a", StorageLocation: "/x",
		Scope: "object", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.DeleteArtifact(ctx, pool, "ns", "a"); err != nil {
		t.Fatalf("DeleteArtifact: %v", err)
	}
	got, err := db.GetArtifactByName(ctx, pool, "ns", "a")
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if got != nil {
		t.Errorf("Get after Delete: got %+v, want nil", got)
	}
	if err := db.DeleteArtifact(ctx, pool, "ns", "a"); err != nil {
		t.Errorf("idempotent Delete: %v", err)
	}
}
