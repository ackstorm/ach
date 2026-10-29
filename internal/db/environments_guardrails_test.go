// SPDX-License-Identifier: Apache-2.0

//go:build integration

package db_test

import (
	"context"
	"slices"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// baseGuardrailRow is the minimal valid projection row for these tests. Every
// slice is explicitly non-nil because the columns are `text[] NOT NULL`.
func baseGuardrailRow(name string, guardrails []string) db.EnvironmentRow {
	return db.EnvironmentRow{
		Namespace:         "ach-system",
		Name:              name,
		AuthorizedTeams:   []string{"default"},
		ContextPrompts:    []string{},
		ContextPlugins:    []string{},
		ContextArtifacts:  []string{},
		ContextSkills:     []string{},
		RuntimeModels:     []string{},
		RuntimeMCPServers: []string{},
		RuntimeA2AAgents:  []string{},
		RuntimeGuardrails: guardrails,
		ResourceVersion:   "1",
	}
}

// TestUpsertEnvironment_GuardrailsRoundTrip asserts spec.runtime.guardrails
// survives upsert -> every read path, and that clearing it converges.
//
// All THREE read paths are exercised deliberately: they are byte-identical
// 18-column projections, and a Scan target added to only two of them silently
// shifts every following column into the wrong field on the third.
func TestUpsertEnvironment_GuardrailsRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	want := []string{"pii-filter", "credential-filter"}
	row := baseGuardrailRow("guardrail-demo", want)
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}

	got, err := db.GetEnvironmentByName(ctx, pool, "ach-system", "guardrail-demo")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName: got=%v err=%v", got, err)
	}
	if !slices.Equal(got.RuntimeGuardrails, want) {
		t.Errorf("Get guardrails = %v, want %v", got.RuntimeGuardrails, want)
	}
	// The neighbouring columns must not have shifted.
	if !slices.Equal(got.AuthorizedTeams, []string{"default"}) || got.ResourceVersion != "1" {
		t.Errorf("column shift: teams=%v rv=%q", got.AuthorizedTeams, got.ResourceVersion)
	}

	list, err := db.ListEnvironments(ctx, pool, "ach-system")
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	if len(list) != 1 || !slices.Equal(list[0].RuntimeGuardrails, want) {
		t.Errorf("List guardrails = %+v", list)
	}

	drain, err := db.ListEnvironmentsIncludingDraining(ctx, pool, "ach-system")
	if err != nil {
		t.Fatalf("ListEnvironmentsIncludingDraining: %v", err)
	}
	if len(drain) != 1 || !slices.Equal(drain[0].RuntimeGuardrails, want) {
		t.Errorf("ListIncludingDraining guardrails = %+v", drain)
	}

	// Removal must converge — the EXCLUDED assignment has to overwrite, not
	// leave the previous value behind.
	cleared := baseGuardrailRow("guardrail-demo", []string{})
	cleared.ResourceVersion = "2"
	if err := db.UpsertEnvironment(ctx, pool, cleared); err != nil {
		t.Fatalf("UpsertEnvironment(clear): %v", err)
	}
	after, err := db.GetEnvironmentByName(ctx, pool, "ach-system", "guardrail-demo")
	if err != nil || after == nil {
		t.Fatalf("GetEnvironmentByName after clear: got=%v err=%v", after, err)
	}
	if len(after.RuntimeGuardrails) != 0 {
		t.Errorf("guardrails after clear = %v, want empty", after.RuntimeGuardrails)
	}
}

// TestUpsertEnvironment_GuardrailsDefaultEmpty: a row written without the field
// reads back as an empty slice, never NULL — the column is `text[] NOT NULL
// DEFAULT '{}'` and pgx would fail the Scan into []string on a NULL.
func TestUpsertEnvironment_GuardrailsDefaultEmpty(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	row := baseGuardrailRow("guardrail-absent", nil)
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ach-system", "guardrail-absent")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName: got=%v err=%v", got, err)
	}
	if len(got.RuntimeGuardrails) != 0 {
		t.Errorf("guardrails = %v, want empty", got.RuntimeGuardrails)
	}
}
