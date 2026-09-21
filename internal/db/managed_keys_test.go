//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/managed_keys.go.
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

// TestListManagedACHKeyIDs_Empty: fresh DB → empty slice + nil error.
func TestListManagedACHKeyIDs_Empty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.ListManagedACHKeyIDs(ctx, pool)
	if err != nil {
		t.Fatalf("ListManagedACHKeyIDs (empty): %v", err)
	}
	if got == nil {
		t.Error("returned nil; want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("got %d entries; want 0", len(got))
	}
}

// TestListManagedACHKeyIDs_PersonalKeysOnly: two active personal_keys rows.
// Both pkid_ ids must appear in the result.
func TestListManagedACHKeyIDs_PersonalKeysOnly(t *testing.T) {
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

	got, err := db.ListManagedACHKeyIDs(ctx, pool)
	if err != nil {
		t.Fatalf("ListManagedACHKeyIDs: %v", err)
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

// TestListManagedACHKeyIDs_BothTablesDedupNotApplicable: one personal_keys row
// and one environment_keys row. The UNION flattens to two ids; dedup is a
// no-op because the prefixes (pkid_ vs ekid_) are disjoint by Phase 1 CHECK
// constraint — the dedup behavior is still exercised via DISTINCT at the
// outer SELECT.
func TestListManagedACHKeyIDs_BothTablesDedupNotApplicable(t *testing.T) {
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

	got, err := db.ListManagedACHKeyIDs(ctx, pool)
	if err != nil {
		t.Fatalf("ListManagedACHKeyIDs: %v", err)
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

// TestListManagedACHKeyIDs_ExcludesOnlyRevoked: D-23 — the managed set is
// status <> 'revoked' on both tables. A personal_keys row with the literal
// 'expired' status (never written today, but CHECK-legal) is still managed;
// only 'revoked' rows on either table drop out.
func TestListManagedACHKeyIDs_ExcludesOnlyRevoked(t *testing.T) {
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

	got, err := db.ListManagedACHKeyIDs(ctx, pool)
	if err != nil {
		t.Fatalf("ListManagedACHKeyIDs: %v", err)
	}
	sort.Strings(got)
	want := []string{"ekid_c_active", "pkid_c_active", "pkid_c_expired"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries; want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// Confirm the revoked keys did not leak in.
	for _, g := range got {
		if g == "pkid_c_revoked" || g == "ekid_c_revoked" {
			t.Errorf("revoked key_id leaked into result: %q", g)
		}
	}
}

// TestListManagedACHKeyIDs_KeepsEverythingButRevoked: an ek_ in every
// non-revoked state (active, suspended, expired-but-active-status) is
// managed; only the revoked ek_ and the revoked pk_ drop out.
func TestListManagedACHKeyIDs_KeepsEverythingButRevoked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	past := time.Now().Add(-time.Hour)
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_m_active", CredentialHash: "h_m_active", Environment: "env1", OwnerEmail: "a@b.example", Name: "active"})
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_m_suspended", CredentialHash: "h_m_suspended", Environment: "env1", OwnerEmail: "a@b.example", Name: "suspended"})
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_m_expired", CredentialHash: "h_m_expired", Environment: "env1", OwnerEmail: "a@b.example", Name: "expired", ExpiresAt: &past})
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_m_revoked", CredentialHash: "h_m_revoked", Environment: "env1", OwnerEmail: "a@b.example", Name: "revoked"})
	_, _ = db.SuspendEnvironmentKey(ctx, pool, "ekid_m_suspended")
	_, _ = db.RevokeEnvironmentKey(ctx, pool, "ekid_m_revoked")

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, status)
		VALUES
		    ('pkid_m_active',  'h_pk_m_active',  'a@b.example', now() + interval '1 hour', 'active'),
		    ('pkid_m_revoked', 'h_pk_m_revoked', 'a@b.example', now() + interval '1 hour', 'revoked')
	`)

	got, err := db.ListManagedACHKeyIDs(ctx, pool)
	if err != nil {
		t.Fatalf("ListManagedACHKeyIDs: %v", err)
	}
	sort.Strings(got)
	want := []string{"ekid_m_active", "ekid_m_expired", "ekid_m_suspended", "pkid_m_active"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries; want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestListACHManagedLitellmUsers_IncludesUsersWithOnlyRevokedRows: D-23 — the
// user enumeration drops the status filter entirely, so a user whose only ek_
// row has been revoked must still be reachable by the orphan reaper (it needs
// to see the LiteLLM-side key to retry the revoke).
func TestListACHManagedLitellmUsers_IncludesUsersWithOnlyRevokedRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustInsertEk(t, pool, db.EkInsertRow{
		KeyID: "ekid_orl", CredentialHash: "h_orl", Environment: "env1",
		OwnerEmail: "a@b.example", Name: "orl", LiteLLMUserID: strPtr("only-revoked@x"),
	})
	if _, err := db.RevokeEnvironmentKey(ctx, pool, "ekid_orl"); err != nil {
		t.Fatalf("RevokeEnvironmentKey: %v", err)
	}

	got, err := db.ListACHManagedLitellmUsers(ctx, pool)
	if err != nil {
		t.Fatalf("ListACHManagedLitellmUsers: %v", err)
	}
	found := false
	for _, g := range got {
		if g == "only-revoked@x" {
			found = true
		}
	}
	if !found {
		t.Errorf("only-revoked@x not in %v; a revoked-only user must stay reachable by the reaper (D-23)", got)
	}
}
