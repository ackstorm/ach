//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/active_keys.go.
//
// Each test inserts personal_keys / environment_keys rows that respect the
// Phase 1 CHECK constraints (key_id LIKE 'pkid_%' / 'ekid_%'; status enum;
// credential_hash UNIQUE — distinct per row). Reuses the setupPostgresForPhase2
// helper from phase2_helpers_test.go (one container per test for assertion
// isolation; Phase 2 convention).

package db_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestListActiveACHKeyIDs_Empty: fresh DB → empty slice + nil error.
func TestListActiveACHKeyIDs_Empty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.ListActiveACHKeyIDs(ctx, pool)
	if err != nil {
		t.Fatalf("ListActiveACHKeyIDs (empty): %v", err)
	}
	if got == nil {
		t.Error("returned nil; want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("got %d entries; want 0", len(got))
	}
}

// TestListActiveACHKeyIDs_PersonalKeysOnly: two active personal_keys rows.
// Both pkid_ ids must appear in the result.
func TestListActiveACHKeyIDs_PersonalKeysOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at)
		VALUES
		    ('pkid_a1', 'h_pk_a1', 'a@b.example', now() + interval '1 hour'),
		    ('pkid_a2', 'h_pk_a2', 'a@b.example', now() + interval '1 hour')
	`)

	got, err := db.ListActiveACHKeyIDs(ctx, pool)
	if err != nil {
		t.Fatalf("ListActiveACHKeyIDs: %v", err)
	}
	sort.Strings(got)
	want := []string{"pkid_a1", "pkid_a2"}
	if len(got) != len(want) {
		t.Fatalf("len got=%d, want=%d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestListActiveACHKeyIDs_BothTablesDedupNotApplicable: one personal_keys row
// and one environment_keys row. The UNION flattens to two ids; dedup is a
// no-op because the prefixes (pkid_ vs ekid_) are disjoint by Phase 1 CHECK
// constraint — the dedup behavior is still exercised via DISTINCT at the
// outer SELECT.
func TestListActiveACHKeyIDs_BothTablesDedupNotApplicable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at)
		VALUES ('pkid_b1', 'h_pk_b1', 'a@b.example', now() + interval '1 hour')
	`)
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name)
		VALUES ('ekid_b1', 'h_ek_b1', 'env1', 'a@b.example', 'k-b1')
	`)

	got, err := db.ListActiveACHKeyIDs(ctx, pool)
	if err != nil {
		t.Fatalf("ListActiveACHKeyIDs: %v", err)
	}
	sort.Strings(got)
	want := []string{"ekid_b1", "pkid_b1"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries; want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestListActiveACHKeyIDs_ExcludesInactive: a personal_keys row with
// status='revoked' (and one 'expired') must NOT appear; ditto an
// environment_keys row with status='revoked'.
func TestListActiveACHKeyIDs_ExcludesInactive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, status)
		VALUES
		    ('pkid_c_active',  'h_pk_c_active',  'a@b.example', now() + interval '1 hour', 'active'),
		    ('pkid_c_revoked', 'h_pk_c_revoked', 'a@b.example', now() + interval '1 hour', 'revoked'),
		    ('pkid_c_expired', 'h_pk_c_expired', 'a@b.example', now() + interval '1 hour', 'expired')
	`)
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, status)
		VALUES
		    ('ekid_c_active',  'h_ek_c_active',  'env1', 'a@b.example', 'k-a', 'active'),
		    ('ekid_c_revoked', 'h_ek_c_revoked', 'env1', 'a@b.example', 'k-r', 'revoked')
	`)

	got, err := db.ListActiveACHKeyIDs(ctx, pool)
	if err != nil {
		t.Fatalf("ListActiveACHKeyIDs: %v", err)
	}
	sort.Strings(got)
	want := []string{"ekid_c_active", "pkid_c_active"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries; want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// Confirm none of the inactive keys leaked in.
	for _, g := range got {
		if g == "pkid_c_revoked" || g == "pkid_c_expired" || g == "ekid_c_revoked" {
			t.Errorf("inactive key_id leaked into result: %q", g)
		}
	}
}
