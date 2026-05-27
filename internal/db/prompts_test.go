//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/prompts.go.

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertPrompt_InsertThenUpdate: Upsert → Get round-trip + UPDATE-in-place
// on second Upsert with mutated content_type override.
func TestUpsertPrompt_InsertThenUpdate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	ct := "text/plain"
	now := time.Now().UTC().Truncate(time.Second)
	row := db.PromptRow{
		Namespace:             "default",
		Name:                  "welcome",
		StorageLocation:       "/var/cache/ach/prompt/welcome",
		ContentType:           &ct,
		LastSuccessfulRefresh: &now,
		MaxStalenessSeconds:   3600,
		ResourceVersion:       "1",
	}
	if err := db.UpsertPrompt(ctx, pool, row); err != nil {
		t.Fatalf("UpsertPrompt: %v", err)
	}
	got, err := db.GetPromptByName(ctx, pool, "default", "welcome")
	if err != nil {
		t.Fatalf("GetPromptByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetPromptByName returned nil after insert")
	}
	if got.StorageLocation != "/var/cache/ach/prompt/welcome" {
		t.Errorf("StorageLocation: got %q", got.StorageLocation)
	}
	if got.ContentType == nil || *got.ContentType != "text/plain" {
		t.Errorf("ContentType: got %v, want pointer to 'text/plain'", got.ContentType)
	}
	if got.LastSuccessfulRefresh == nil {
		t.Error("LastSuccessfulRefresh: got nil, want set")
	}
	if got.MaxStalenessSeconds != 3600 {
		t.Errorf("MaxStalenessSeconds: got %d", got.MaxStalenessSeconds)
	}
	firstUpdatedAt := got.UpdatedAt

	// Second Upsert — clear ContentType (override removed); MaxStaleness change.
	time.Sleep(50 * time.Millisecond)
	row.ContentType = nil
	row.MaxStalenessSeconds = 7200
	row.ResourceVersion = "2"
	if err := db.UpsertPrompt(ctx, pool, row); err != nil {
		t.Fatalf("second UpsertPrompt: %v", err)
	}
	got2, err := db.GetPromptByName(ctx, pool, "default", "welcome")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got2.ContentType != nil {
		t.Errorf("after Upsert with nil ContentType: got %v, want nil (SQL NULL)", got2.ContentType)
	}
	if got2.MaxStalenessSeconds != 7200 {
		t.Errorf("MaxStalenessSeconds: got %d, want 7200", got2.MaxStalenessSeconds)
	}
	if got2.ResourceVersion != "2" {
		t.Errorf("ResourceVersion: got %q, want 2", got2.ResourceVersion)
	}
	if !got2.UpdatedAt.After(firstUpdatedAt) {
		t.Errorf("UpdatedAt: got %v, want strictly after %v", got2.UpdatedAt, firstUpdatedAt)
	}
}

// TestGetPromptByName_AbsenceReturnsNilNil.
func TestGetPromptByName_AbsenceReturnsNilNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.GetPromptByName(ctx, pool, "default", "missing")
	if err != nil {
		t.Fatalf("absent Get: got error %v", err)
	}
	if got != nil {
		t.Errorf("absent Get: got %+v, want nil", got)
	}
}

// TestSoftDeletePrompt_PreservesRow: CS-09 — row remains after SoftDelete.
func TestSoftDeletePrompt_PreservesRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertPrompt(ctx, pool, db.PromptRow{
		Namespace: "ns", Name: "p", StorageLocation: "/x", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.SoftDeletePrompt(ctx, pool, "ns", "p"); err != nil {
		t.Fatalf("SoftDeletePrompt: %v", err)
	}
	got, err := db.GetPromptByName(ctx, pool, "ns", "p")
	if err != nil {
		t.Fatalf("Get after SoftDelete: %v", err)
	}
	if got == nil {
		t.Fatal("Get after SoftDelete: row missing (CS-09 violated)")
	}
	if got.DeletionTimestamp == nil {
		t.Error("DeletionTimestamp: got nil, want non-nil")
	}
	if err := db.SoftDeletePrompt(ctx, pool, "ns", "p"); err != nil {
		t.Errorf("idempotent SoftDelete: %v", err)
	}
}

// TestDeletePrompt_RemovesRow.
func TestDeletePrompt_RemovesRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertPrompt(ctx, pool, db.PromptRow{
		Namespace: "ns", Name: "p", StorageLocation: "/x", MaxStalenessSeconds: 60, ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.DeletePrompt(ctx, pool, "ns", "p"); err != nil {
		t.Fatalf("DeletePrompt: %v", err)
	}
	got, err := db.GetPromptByName(ctx, pool, "ns", "p")
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if got != nil {
		t.Errorf("Get after Delete: got %+v, want nil", got)
	}
	if err := db.DeletePrompt(ctx, pool, "ns", "p"); err != nil {
		t.Errorf("idempotent Delete: %v", err)
	}
}
