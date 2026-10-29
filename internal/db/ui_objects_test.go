//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for the GitOps-wins UI-objects write path (internal/db/
// ui_objects.go) and the operator takeover (upsertEnvironmentSQL). Each test
// boots a fresh postgres:16-alpine via setupPostgresForPhase2 (applies every
// migration including 000005 origin/locked).

package db_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

func uiEnvRow(ns, name string) db.EnvironmentRow {
	return db.EnvironmentRow{
		Namespace:       ns,
		Name:            name,
		AuthorizedTeams: []string{"team-a"},
		ContextPrompts:  []string{"welcome"},
		RuntimeModels:   []string{"gpt-4o"},
		Notice:          "draft via UI",
		Description:     "a UI-authored environment",
	}
}

// crEnvRow builds an operator (origin='cr') row with all text[] columns
// non-nil — the operator's UpsertEnvironment does not coalesce nil slices, so
// raw nil would violate the `text[] NOT NULL` constraint (the real controller
// passes spec slices that are non-nil in practice).
func crEnvRow(ns, name, rv string) db.EnvironmentRow {
	return db.EnvironmentRow{
		Namespace:         ns,
		Name:              name,
		ResourceVersion:   rv,
		AuthorizedTeams:   []string{},
		ContextPrompts:    []string{},
		ContextPlugins:    []string{},
		ContextArtifacts:  []string{},
		ContextSkills:     []string{},
		RuntimeModels:     []string{},
		RuntimeMCPServers: []string{},
		RuntimeA2AAgents:  []string{},
	}
}

// readOriginLocked reads the origin + locked columns directly (GetEnvironmentByName
// does not project them).
func readOriginLocked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ns, name string) (string, bool) {
	t.Helper()
	var origin string
	var locked bool
	if err := pool.QueryRow(ctx,
		`SELECT origin, locked FROM environments WHERE namespace=$1 AND name=$2`, ns, name,
	).Scan(&origin, &locked); err != nil {
		t.Fatalf("read origin/locked(%s/%s): %v", ns, name, err)
	}
	return origin, locked
}

// TestUpsertEnvironment_TakesOverUIRow is the core GitOps-wins assertion: a CR
// reconcile (UpsertEnvironment) over a UI-owned row flips origin 'ui'→'cr',
// sets locked=TRUE, and overwrites the spec — never ErrOriginConflict.
func TestUpsertEnvironment_TakesOverUIRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.InsertUIEnvironment(ctx, pool, uiEnvRow("ach", "demo")); err != nil {
		t.Fatalf("InsertUIEnvironment: %v", err)
	}
	if origin, locked := readOriginLocked(t, ctx, pool, "ach", "demo"); origin != "ui" || locked {
		t.Fatalf("after UI insert: origin=%q locked=%v, want ui/false", origin, locked)
	}

	// Operator reconcile of the matching CR takes over.
	crRow := crEnvRow("ach", "demo", "1000")
	crRow.AuthorizedTeams = []string{"team-b"}
	crRow.RuntimeModels = []string{"claude-opus-4-8"}
	if err := db.UpsertEnvironment(ctx, pool, crRow); err != nil {
		t.Fatalf("UpsertEnvironment (takeover) returned error: %v", err)
	}
	if origin, locked := readOriginLocked(t, ctx, pool, "ach", "demo"); origin != "cr" || !locked {
		t.Fatalf("after takeover: origin=%q locked=%v, want cr/true", origin, locked)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ach", "demo")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName: %v (row=%v)", err, got)
	}
	if len(got.AuthorizedTeams) != 1 || got.AuthorizedTeams[0] != "team-b" {
		t.Fatalf("takeover did not overwrite spec: authorizedTeams=%v", got.AuthorizedTeams)
	}
	if got.ResourceVersion != "1000" {
		t.Fatalf("takeover resource_version=%q, want 1000", got.ResourceVersion)
	}
}

func TestInsertUIEnvironment_RoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.InsertUIEnvironment(ctx, pool, uiEnvRow("ach", "draft1")); err != nil {
		t.Fatalf("InsertUIEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ach", "draft1")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName: %v (row=%v)", err, got)
	}
	if got.Notice != "draft via UI" || got.Description != "a UI-authored environment" {
		t.Fatalf("round-trip mismatch: notice=%q description=%q", got.Notice, got.Description)
	}
	if len(got.ContextPrompts) != 1 || got.ContextPrompts[0] != "welcome" {
		t.Fatalf("round-trip context_prompts=%v", got.ContextPrompts)
	}
	// Draft has no status → condition columns NULL.
	if got.AvailableCondition != nil {
		t.Fatalf("UI draft available_condition = %s, want NULL", got.AvailableCondition)
	}
	if origin, locked := readOriginLocked(t, ctx, pool, "ach", "draft1"); origin != "ui" || locked {
		t.Fatalf("origin=%q locked=%v, want ui/false", origin, locked)
	}
}

func TestInsertUIEnvironment_ConflictWithCR(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Operator owns the row first.
	if err := db.UpsertEnvironment(ctx, pool, crEnvRow("ach", "owned", "1")); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}
	err := db.InsertUIEnvironment(ctx, pool, uiEnvRow("ach", "owned"))
	if !errors.Is(err, db.ErrConflictWithCR) {
		t.Fatalf("InsertUIEnvironment over cr row: got %v, want ErrConflictWithCR", err)
	}
}

func TestInsertUIEnvironment_AlreadyExistsUI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.InsertUIEnvironment(ctx, pool, uiEnvRow("ach", "dup")); err != nil {
		t.Fatalf("first InsertUIEnvironment: %v", err)
	}
	err := db.InsertUIEnvironment(ctx, pool, uiEnvRow("ach", "dup"))
	if !errors.Is(err, db.ErrUIAlreadyExists) {
		t.Fatalf("second InsertUIEnvironment: got %v, want ErrUIAlreadyExists", err)
	}
}

func TestUpdateUIEnvironment_Success(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.InsertUIEnvironment(ctx, pool, uiEnvRow("ach", "edit")); err != nil {
		t.Fatalf("InsertUIEnvironment: %v", err)
	}
	row := uiEnvRow("ach", "edit")
	row.Description = "edited"
	row.RuntimeModels = []string{"gpt-4o", "claude-opus-4-8"}
	if err := db.UpdateUIEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("UpdateUIEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ach", "edit")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName: %v", err)
	}
	if got.Description != "edited" || len(got.RuntimeModels) != 2 {
		t.Fatalf("update not applied: description=%q models=%v", got.Description, got.RuntimeModels)
	}
	if origin, _ := readOriginLocked(t, ctx, pool, "ach", "edit"); origin != "ui" {
		t.Fatalf("update flipped origin to %q, want ui", origin)
	}
}

func TestUpdateUIEnvironment_ImmutableViaUI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertEnvironment(ctx, pool, crEnvRow("ach", "crobj", "1")); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}
	err := db.UpdateUIEnvironment(ctx, pool, uiEnvRow("ach", "crobj"))
	if !errors.Is(err, db.ErrImmutableViaUI) {
		t.Fatalf("UpdateUIEnvironment on cr row: got %v, want ErrImmutableViaUI", err)
	}
}

func TestUpdateUIEnvironment_NotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	err := db.UpdateUIEnvironment(ctx, pool, uiEnvRow("ach", "ghost"))
	if !errors.Is(err, db.ErrUINotFound) {
		t.Fatalf("UpdateUIEnvironment on missing row: got %v, want ErrUINotFound", err)
	}
}

func TestDeleteUIEnvironment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Success path.
	if err := db.InsertUIEnvironment(ctx, pool, uiEnvRow("ach", "del")); err != nil {
		t.Fatalf("InsertUIEnvironment: %v", err)
	}
	if err := db.DeleteUIEnvironment(ctx, pool, "ach", "del"); err != nil {
		t.Fatalf("DeleteUIEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ach", "del")
	if err != nil {
		t.Fatalf("GetEnvironmentByName: %v", err)
	}
	if got != nil {
		t.Fatalf("row still present after delete: %v", got)
	}

	// Immutable path: operator-owned row cannot be deleted via UI.
	if err := db.UpsertEnvironment(ctx, pool, crEnvRow("ach", "crdel", "1")); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}
	if err := db.DeleteUIEnvironment(ctx, pool, "ach", "crdel"); !errors.Is(err, db.ErrImmutableViaUI) {
		t.Fatalf("DeleteUIEnvironment on cr row: got %v, want ErrImmutableViaUI", err)
	}

	// Not-found path.
	if err := db.DeleteUIEnvironment(ctx, pool, "ach", "ghost"); !errors.Is(err, db.ErrUINotFound) {
		t.Fatalf("DeleteUIEnvironment on missing row: got %v, want ErrUINotFound", err)
	}
}

// TestInsertUIEnvironment_GuardrailsRoundTrip: a UI-drafted Environment carries
// its guardrails through insert AND update, so a draft exported to YAML
// reproduces what the author entered.
func TestInsertUIEnvironment_GuardrailsRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	row := baseGuardrailRow("ui-guardrails", []string{"pii-filter"})
	row.ResourceVersion = ""
	if err := db.InsertUIEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("InsertUIEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ach-system", "ui-guardrails")
	if err != nil || got == nil {
		t.Fatalf("Get: got=%v err=%v", got, err)
	}
	if !slices.Equal(got.RuntimeGuardrails, []string{"pii-filter"}) {
		t.Fatalf("after insert = %v", got.RuntimeGuardrails)
	}

	row.RuntimeGuardrails = []string{"a", "b"}
	if err := db.UpdateUIEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("UpdateUIEnvironment: %v", err)
	}
	got, err = db.GetEnvironmentByName(ctx, pool, "ach-system", "ui-guardrails")
	if err != nil || got == nil {
		t.Fatalf("Get after update: got=%v err=%v", got, err)
	}
	if !slices.Equal(got.RuntimeGuardrails, []string{"a", "b"}) {
		t.Fatalf("after update = %v", got.RuntimeGuardrails)
	}
}
