// SPDX-License-Identifier: Apache-2.0

//go:build integration

package db_test

import (
	"context"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertEnvironment_NoticeRoundTrips asserts spec.notice survives the
// upsert → GetEnvironmentByName → ListEnvironments projection round-trip.
func TestUpsertEnvironment_NoticeRoundTrips(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Array columns are NOT NULL in the schema, so populate them (any
	// non-nil slice) — the assertion under test is the Notice round-trip.
	row := db.EnvironmentRow{
		Namespace:         "ach-system",
		Name:              "notice-demo",
		AuthorizedTeams:   []string{"default"},
		ContextPrompts:    []string{},
		ContextPlugins:    []string{},
		ContextArtifacts:  []string{},
		ContextSkills:     []string{},
		RuntimeModels:     []string{},
		RuntimeMCPServers: []string{},
		RuntimeA2AAgents:  []string{},
		ResourceVersion:   "1",
		Notice:            "remember to re-login after key rotation",
	}
	if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
		t.Fatalf("UpsertEnvironment: %v", err)
	}

	got, err := db.GetEnvironmentByName(ctx, pool, "ach-system", "notice-demo")
	if err != nil || got == nil {
		t.Fatalf("GetEnvironmentByName: got=%v err=%v", got, err)
	}
	if got.Notice != row.Notice {
		t.Errorf("Get notice = %q, want %q", got.Notice, row.Notice)
	}

	list, err := db.ListEnvironments(ctx, pool, "ach-system")
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	var found bool
	for _, e := range list {
		if e.Name == "notice-demo" {
			found = true
			if e.Notice != row.Notice {
				t.Errorf("List notice = %q, want %q", e.Notice, row.Notice)
			}
		}
	}
	if !found {
		t.Fatal("notice-demo not in ListEnvironments output")
	}
}
