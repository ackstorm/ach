//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/environments.go.
//
// Each test boots a fresh postgres:16-alpine container via testcontainers-go
// (reusing setupPostgresForPhase2 — applies migrations 000001 through 000004),
// exercises one or more EnvironmentRow helpers, and asserts schema-level
// invariants (Upsert round-trip, GetEnvironmentByName absence → (nil,nil),
// SoftDeleteEnvironment preserves the row per CS-09, DeleteEnvironment hard-
// removes).
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

// TestUpsertEnvironment_InsertThenUpdate: Upsert → Get returns hydrated row;
// a second Upsert with a mutated field is reflected on the next Get and is
// performed as UPDATE-in-place (row count remains 1).
func TestUpsertEnvironment_InsertThenUpdate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	row := db.EnvironmentRow{
		Namespace:         "default",
		Name:              "env1",
		AuthorizedTeams:   []string{"team-a", "team-b"},
		ContextPrompts:    []string{"welcome"},
		ContextPlugins:    []string{"caveman"},
		ContextArtifacts:  []string{"openclaw"},
		ContextSkills:     []string{"pdf-processing"},
		RuntimeModels:     []string{"gpt-4o"},
		RuntimeMCPServers: []string{"context7"},
		RuntimeA2AAgents:  []string{"agent-1"},
		ResourceVersion:   "100",
	}
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "default", "env1")
	if err != nil {
		t.Fatalf("GetEnvironmentByName: %v", err)
	}
	if got == nil {
		t.Fatalf("GetEnvironmentByName returned nil after insert")
	}
	if got.Namespace != "default" || got.Name != "env1" {
		t.Errorf("PK roundtrip: got %s/%s, want default/env1", got.Namespace, got.Name)
	}
	if len(got.AuthorizedTeams) != 2 || got.AuthorizedTeams[0] != "team-a" {
		t.Errorf("AuthorizedTeams: got %v, want [team-a team-b]", got.AuthorizedTeams)
	}
	if len(got.ContextPlugins) != 1 || got.ContextPlugins[0] != "caveman" {
		t.Errorf("ContextPlugins: got %v, want [caveman]", got.ContextPlugins)
	}
	if got.ResourceVersion != "100" {
		t.Errorf("ResourceVersion: got %q, want 100", got.ResourceVersion)
	}
	firstUpdatedAt := got.UpdatedAt

	// Second UPSERT with mutated field — sleep 50ms so updated_at can advance.
	time.Sleep(50 * time.Millisecond)
	row.ContextPlugins = []string{"caveman", "extra"}
	row.ResourceVersion = "101"
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("second UpsertEnvironment: %v", err)
	}
	got2, err := db.GetEnvironmentByName(ctx, pool, "default", "env1")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got2 == nil {
		t.Fatal("second Get returned nil")
	}
	if len(got2.ContextPlugins) != 2 {
		t.Errorf("after update: ContextPlugins len got %d, want 2", len(got2.ContextPlugins))
	}
	if got2.ResourceVersion != "101" {
		t.Errorf("after update: ResourceVersion got %q, want 101", got2.ResourceVersion)
	}
	if !got2.UpdatedAt.After(firstUpdatedAt) {
		t.Errorf("UpdatedAt: got %v, want strictly after %v", got2.UpdatedAt, firstUpdatedAt)
	}
	// Row count must be exactly 1 (UPDATE-in-place, not duplicate INSERT).
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM environments WHERE namespace=$1 AND name=$2`,
		"default", "env1").Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after upsert-twice; got %d", count)
	}
}

// TestUpsertEnvironment_ClearsStaleDeletionTimestamp proves a LIVE reconcile
// un-sets a drain marker left by a prior soft-delete (incident 2026-06-04:
// a resurrected Environment stayed hidden from env list because the upsert
// preserved the old deletion_timestamp).
func TestUpsertEnvironment_ClearsStaleDeletionTimestamp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	row := db.EnvironmentRow{
		Namespace:       "ach",
		Name:            "resurrect-me",
		AuthorizedTeams: []string{"default"},
		ResourceVersion: "1",
		// text[] columns are NOT NULL; explicit binds map nil → SQL NULL.
		ContextPrompts: []string{}, ContextPlugins: []string{},
		ContextArtifacts: []string{}, ContextSkills: []string{}, RuntimeModels: []string{},
		RuntimeMCPServers: []string{}, RuntimeA2AAgents: []string{},
	}
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}
	// Soft-delete sets deletion_timestamp.
	if err := db.SoftDeleteEnvironment(ctx, pool, "ach", "resurrect-me"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// Live reconcile of the recreated CR (same name) upserts again.
	row.ResourceVersion = "2"
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("live upsert: %v", err)
	}

	got, err := db.GetEnvironmentByName(ctx, pool, "ach", "resurrect-me")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("get: row missing after live upsert")
	}
	if got.DeletionTimestamp != nil {
		t.Fatalf("expected deletion_timestamp cleared on live upsert, got %v", *got.DeletionTimestamp)
	}
	// The resurrected env must reappear in the IS-NULL-filtered list read.
	list, err := db.ListEnvironments(ctx, pool, "ach")
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	var found bool
	for _, e := range list {
		if e.Name == "resurrect-me" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("resurrected env missing from ListEnvironments (deletion_timestamp IS NULL filter)")
	}
}

// TestGetEnvironmentByName_AbsenceReturnsNilNil: a Get on a (ns, name) that
// was never inserted returns (nil, nil) — never an error.
func TestGetEnvironmentByName_AbsenceReturnsNilNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.GetEnvironmentByName(ctx, pool, "default", "does-not-exist")
	if err != nil {
		t.Fatalf("GetEnvironmentByName absent: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("absent Get: got %+v, want nil", got)
	}
}

// TestListEnvironmentsIncludingDraining: a drain-mode row (deletion_timestamp
// set) is returned by ListEnvironmentsIncludingDraining but NOT by
// ListEnvironments. This preserves the CS-09 contract for the content-service
// in-memory env cache (which must keep serving environments under finalizer
// drain) while the steady-state ListEnvironments still excludes them.
func TestListEnvironmentsIncludingDraining(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	const ns = "ach-system"
	seed := func(name string) {
		if err := db.UpsertEnvironment(ctx, pool, db.EnvironmentRow{
			Namespace: ns, Name: name, ResourceVersion: "1",
			AuthorizedTeams: []string{}, ContextPrompts: []string{},
			ContextPlugins: []string{}, ContextArtifacts: []string{},
			ContextSkills: []string{},
			RuntimeModels: []string{}, RuntimeMCPServers: []string{},
			RuntimeA2AAgents: []string{},
		}); err != nil {
			t.Fatalf("seed Upsert(%s): %v", name, err)
		}
	}
	names := func(rows []db.EnvironmentRow) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Name)
		}
		return out
	}
	contains := func(xs []string, want string) bool {
		for _, x := range xs {
			if x == want {
				return true
			}
		}
		return false
	}

	seed("live")
	seed("draining")
	if err := db.SoftDeleteEnvironment(ctx, pool, ns, "draining"); err != nil {
		t.Fatalf("SoftDeleteEnvironment(draining): %v", err)
	}

	all, err := db.ListEnvironmentsIncludingDraining(ctx, pool, ns)
	if err != nil {
		t.Fatalf("list incl draining: %v", err)
	}
	if got := names(all); !contains(got, "live") || !contains(got, "draining") {
		t.Fatalf("incl-draining = %v; want both live and draining", got)
	}

	live, err := db.ListEnvironments(ctx, pool, ns)
	if err != nil {
		t.Fatalf("list live: %v", err)
	}
	if contains(names(live), "draining") {
		t.Fatalf("ListEnvironments leaked a draining row: %v", names(live))
	}
}

// TestSoftDeleteEnvironment_PreservesRow: SoftDelete sets deletion_timestamp
// but Get still returns the row (CS-09 — Content Service keeps serving until
// hard-delete by finalizer drain). Re-SoftDelete is idempotent (no error).
func TestSoftDeleteEnvironment_PreservesRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertEnvironment(ctx, pool, db.EnvironmentRow{
		Namespace: "ns1", Name: "e", ResourceVersion: "1",
		// text[] columns are NOT NULL; explicit binds map nil → SQL NULL.
		AuthorizedTeams: []string{}, ContextPrompts: []string{},
		ContextPlugins: []string{}, ContextArtifacts: []string{},
		ContextSkills: []string{},
		RuntimeModels: []string{}, RuntimeMCPServers: []string{},
		RuntimeA2AAgents: []string{},
	}); err != nil {
		t.Fatalf("seed Upsert: %v", err)
	}
	if err := db.SoftDeleteEnvironment(ctx, pool, "ns1", "e"); err != nil {
		t.Fatalf("SoftDeleteEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ns1", "e")
	if err != nil {
		t.Fatalf("Get after SoftDelete: %v", err)
	}
	if got == nil {
		t.Fatal("Get after SoftDelete: row should still be present per CS-09")
	}
	if got.DeletionTimestamp == nil {
		t.Error("DeletionTimestamp: got nil, want non-nil after SoftDelete")
	}
	// Re-SoftDelete must be a no-error no-op (idempotent).
	if err := db.SoftDeleteEnvironment(ctx, pool, "ns1", "e"); err != nil {
		t.Errorf("idempotent SoftDelete: got %v, want nil", err)
	}
}

// TestDeleteEnvironment_RemovesRow: Delete removes the row outright; a
// subsequent Get returns (nil, nil). Re-Delete is idempotent.
func TestDeleteEnvironment_RemovesRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.UpsertEnvironment(ctx, pool, db.EnvironmentRow{
		Namespace: "ns1", Name: "e", ResourceVersion: "1",
		// text[] columns are NOT NULL; explicit binds map nil → SQL NULL.
		AuthorizedTeams: []string{}, ContextPrompts: []string{},
		ContextPlugins: []string{}, ContextArtifacts: []string{},
		ContextSkills: []string{},
		RuntimeModels: []string{}, RuntimeMCPServers: []string{},
		RuntimeA2AAgents: []string{},
	}); err != nil {
		t.Fatalf("seed Upsert: %v", err)
	}
	if err := db.DeleteEnvironment(ctx, pool, "ns1", "e"); err != nil {
		t.Fatalf("DeleteEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ns1", "e")
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if got != nil {
		t.Errorf("Get after Delete: got %+v, want nil", got)
	}
	// Idempotent re-Delete.
	if err := db.DeleteEnvironment(ctx, pool, "ns1", "e"); err != nil {
		t.Errorf("idempotent Delete: got %v, want nil", err)
	}
}
