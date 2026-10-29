// SPDX-License-Identifier: Apache-2.0

//go:build integration

package db_test

import (
	"context"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertEnvironment_DescriptionRoundTrips asserts spec.description survives
// the upsert → GetEnvironmentByName → ListEnvironments projection round-trip.
func TestUpsertEnvironment_DescriptionRoundTrips(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	row := db.EnvironmentRow{
		Namespace:         "ach-system",
		Name:              "desc-demo",
		AuthorizedTeams:   []string{"default"},
		ContextPrompts:    []string{},
		ContextPlugins:    []string{},
		ContextArtifacts:  []string{},
		ContextSkills:     []string{},
		RuntimeModels:     []string{},
		RuntimeMCPServers: []string{},
		RuntimeA2AAgents:  []string{},
		ResourceVersion:   "1",
		Description:       "production env for the data team",
	}
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}
	got, err := db.GetEnvironmentByName(ctx, pool, "ach-system", "desc-demo")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName: got=%v err=%v", got, err)
	}
	if got.Description != row.Description {
		t.Errorf("Get description = %q, want %q", got.Description, row.Description)
	}
	list, err := db.ListEnvironments(ctx, pool, "ach-system")
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	for _, e := range list {
		if e.Name == "desc-demo" && e.Description != row.Description {
			t.Errorf("List description = %q, want %q", e.Description, row.Description)
		}
	}
}
