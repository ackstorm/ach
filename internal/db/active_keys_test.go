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

// TestListActiveACHKeyTokens_Empty: fresh DB → empty slice (not nil).
func TestListActiveACHKeyTokens_Empty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.ListActiveACHKeyTokens(ctx, pool)
	if err != nil {
		t.Fatalf("ListActiveACHKeyTokens (empty): %v", err)
	}
	if got == nil {
		t.Error("returned nil; want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("got %d entries; want 0", len(got))
	}
}

// TestListActiveACHKeyTokens_Distinct: 2 active personal_keys + 2 active
// environment_keys, with one PK token = one EK token (shared) — DISTINCT
// collapses to 3 unique tokens.
func TestListActiveACHKeyTokens_Distinct(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, litellm_token)
		VALUES
		    ('pkid_tok1', 'h_pk_tok1', 'a@b.example', now() + interval '1 hour', 'tok-shared'),
		    ('pkid_tok2', 'h_pk_tok2', 'a@b.example', now() + interval '1 hour', 'tok-pk-only')
	`)
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, litellm_token)
		VALUES
		    ('ekid_tok1', 'h_ek_tok1', 'env1', 'a@b.example', 'k-shared',  'tok-shared'),
		    ('ekid_tok2', 'h_ek_tok2', 'env1', 'a@b.example', 'k-ek-only', 'tok-ek-only')
	`)

	got, err := db.ListActiveACHKeyTokens(ctx, pool)
	if err != nil {
		t.Fatalf("ListActiveACHKeyTokens: %v", err)
	}
	sort.Strings(got)
	want := []string{"tok-ek-only", "tok-pk-only", "tok-shared"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries; want 3 (DISTINCT over shared token). got=%v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestListActiveACHKeyTokens_ExcludesNull: a row with NULL litellm_token
// MUST NOT appear — the IS NOT NULL filter is what makes the helper precise.
func TestListActiveACHKeyTokens_ExcludesNull(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Insert an active row WITHOUT specifying litellm_token (column NULL).
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at)
		VALUES ('pkid_null_tok', 'h_pk_null_tok', 'a@b.example', now() + interval '1 hour')
	`)
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name)
		VALUES ('ekid_null_tok', 'h_ek_null_tok', 'env1', 'a@b.example', 'k-null')
	`)

	got, err := db.ListActiveACHKeyTokens(ctx, pool)
	if err != nil {
		t.Fatalf("ListActiveACHKeyTokens: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries; want 0 (NULL litellm_token MUST NOT contribute). got=%v", len(got), got)
	}
}

// TestListActiveACHKeyTokens_ExcludesRevoked: a revoked row with a populated
// litellm_token MUST NOT appear — status='active' is authoritative.
func TestListActiveACHKeyTokens_ExcludesRevoked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, litellm_token, status, revoked_at)
		VALUES
		    ('pkid_act_tok', 'h_pk_act_tok', 'a@b.example', now() + interval '1 hour', 'tok-active',  'active',  NULL),
		    ('pkid_rev_tok', 'h_pk_rev_tok', 'a@b.example', now() + interval '1 hour', 'tok-revoked', 'revoked', now())
	`)

	got, err := db.ListActiveACHKeyTokens(ctx, pool)
	if err != nil {
		t.Fatalf("ListActiveACHKeyTokens: %v", err)
	}
	sort.Strings(got)
	if len(got) != 1 || got[0] != "tok-active" {
		t.Errorf("got=%v; want [tok-active] (revoked row MUST NOT contribute)", got)
	}
}
