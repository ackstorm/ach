//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

func set(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// First sync inserts active rows; a second sync that drops a model tombstones
// the vanished one as status='missing' while keeping first_seen_at stable.
func TestReplaceRuntimeCatalog_TombstonesVanishedEntries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	t1 := time.Now().Truncate(time.Microsecond)
	if err := db.ReplaceRuntimeCatalog(ctx, pool, "ach-system", "default",
		set("gpt-4o", "sonnet"), set("github"), set("vendor-research"), set("default"), t1); err != nil {
		t.Fatalf("ReplaceRuntimeCatalog #1: %v", err)
	}

	models, err := db.ListRuntimeCatalog(ctx, pool, "ach-system", "default", "model")
	if err != nil {
		t.Fatalf("ListRuntimeCatalog: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models after #1: got %d, want 2", len(models))
	}

	// Second sync: "sonnet" model is gone; "default" team is also gone.
	t2 := t1.Add(5 * time.Minute)
	if err := db.ReplaceRuntimeCatalog(ctx, pool, "ach-system", "default",
		set("gpt-4o"), set("github"), set("vendor-research"), nil, t2); err != nil {
		t.Fatalf("ReplaceRuntimeCatalog #2: %v", err)
	}

	models, _ = db.ListRuntimeCatalog(ctx, pool, "ach-system", "default", "model")
	byName := map[string]db.RuntimeCatalogRow{}
	for _, m := range models {
		byName[m.Name] = m
	}
	if got := byName["gpt-4o"].Status; got != "active" {
		t.Fatalf("gpt-4o status: got %q, want active", got)
	}
	if got := byName["sonnet"].Status; got != "missing" {
		t.Fatalf("sonnet status: got %q, want missing", got)
	}
	if byName["sonnet"].DeletedAt == nil {
		t.Fatalf("sonnet deleted_at should be set when tombstoned")
	}
	if !byName["sonnet"].FirstSeenAt.Equal(t1) {
		t.Fatalf("sonnet first_seen_at drifted: got %v, want %v", byName["sonnet"].FirstSeenAt, t1)
	}

	// Team "default" should be tombstoned in the second sync.
	teamRows, _ := db.ListRuntimeCatalog(ctx, pool, "ach-system", "default", "team")
	byTeamName := map[string]db.RuntimeCatalogRow{}
	for _, r := range teamRows {
		byTeamName[r.Name] = r
	}
	if got := byTeamName["default"].Status; got != "missing" {
		t.Fatalf("team 'default' status: got %q, want missing", got)
	}
	if byTeamName["default"].DeletedAt == nil {
		t.Fatalf("team 'default' deleted_at should be set when tombstoned")
	}
}

// A tombstoned entry that reappears flips back to active and clears deleted_at.
func TestReplaceRuntimeCatalog_ReappearReactivates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	t1 := time.Now().Truncate(time.Microsecond)
	_ = db.ReplaceRuntimeCatalog(ctx, pool, "ach-system", "default", set("sonnet"), nil, nil, nil, t1)
	_ = db.ReplaceRuntimeCatalog(ctx, pool, "ach-system", "default", nil, nil, nil, nil, t1.Add(time.Minute))             // sonnet → missing
	_ = db.ReplaceRuntimeCatalog(ctx, pool, "ach-system", "default", set("sonnet"), nil, nil, nil, t1.Add(2*time.Minute)) // back

	models, _ := db.ListRuntimeCatalog(ctx, pool, "ach-system", "default", "model")
	if len(models) != 1 || models[0].Status != "active" || models[0].DeletedAt != nil {
		t.Fatalf("expected sonnet active with nil deleted_at, got %+v", models)
	}

	ts, ok, err := db.MaxRuntimeCatalogSync(ctx, pool, "ach-system", "default")
	if err != nil || !ok {
		t.Fatalf("MaxRuntimeCatalogSync: ok=%v err=%v", ok, err)
	}
	if !ts.Equal(t1.Add(2 * time.Minute)) {
		t.Fatalf("max sync: got %v, want %v", ts, t1.Add(2*time.Minute))
	}
}
