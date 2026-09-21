//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/environment_keys.go.

package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

// TestInsertEnvironmentKey_HappyPath: insert + re-read.
func TestInsertEnvironmentKey_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	row := db.EkInsertRow{
		KeyID:          "ekid_ins1",
		CredentialHash: "h_ek_ins1",
		Environment:    "env1",
		OwnerEmail:     "a@b.example",
		Name:           "primary",
		LiteLLMUserID:  strPtr("user-1"),
		LiteLLMToken:   strPtr("tok-ek-1"),
	}
	if err := db.InsertEnvironmentKey(ctx, pool, row); err != nil {
		t.Fatalf("InsertEnvironmentKey: %v", err)
	}

	got, err := db.GetEnvironmentKey(ctx, pool, "ekid_ins1")
	if err != nil {
		t.Fatalf("GetEnvironmentKey: %v", err)
	}
	if got == nil {
		t.Fatal("GetEnvironmentKey returned (nil, nil); want non-nil")
	}
	if got.KeyID != "ekid_ins1" || got.Environment != "env1" || got.Name != "primary" {
		t.Errorf("row mismatch: %+v", got)
	}
	if got.Status != "active" {
		t.Errorf("Status=%q; want active", got.Status)
	}
}

// TestInsertEnvironmentKey_UniqueViolation: duplicate credential_hash → 23505.
func TestInsertEnvironmentKey_UniqueViolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	r1 := db.EkInsertRow{
		KeyID: "ekid_d1", CredentialHash: "h_ek_dup",
		Environment: "env1", OwnerEmail: "a@b.example", Name: "n1",
	}
	if err := db.InsertEnvironmentKey(ctx, pool, r1); err != nil {
		t.Fatalf("first InsertEnvironmentKey: %v", err)
	}
	r2 := db.EkInsertRow{
		KeyID: "ekid_d2", CredentialHash: "h_ek_dup",
		Environment: "env1", OwnerEmail: "a@b.example", Name: "n2",
	}
	err := db.InsertEnvironmentKey(ctx, pool, r2)
	if err == nil {
		t.Fatal("expected unique-violation error; got nil")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("expected SQLSTATE 23505; got %v", err)
	}
	if !strings.Contains(err.Error(), "ekid_d2") {
		t.Errorf("wrapped error must mention key_id ekid_d2; got %q", err)
	}
}

// TestGetEnvironmentKey_Absent: returns (nil, nil).
func TestGetEnvironmentKey_Absent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.GetEnvironmentKey(ctx, pool, "ekid_does_not_exist")
	if err != nil {
		t.Fatalf("GetEnvironmentKey: %v", err)
	}
	if got != nil {
		t.Errorf("absent row returned non-nil: %+v", got)
	}
}

// TestRevokeEnvironmentKey_HappyPath: flips active → revoked, stamps revoked_at.
func TestRevokeEnvironmentKey_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name)
		VALUES ('ekid_rev1', 'h_ek_rev1', 'env1', 'a@b.example', 'k-rev1')
	`)
	got, err := db.RevokeEnvironmentKey(ctx, pool, "ekid_rev1")
	if err != nil {
		t.Fatalf("RevokeEnvironmentKey: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil EkKeyInfo on successful revoke")
	}
	if got.Status != "revoked" {
		t.Errorf("Status=%q; want revoked", got.Status)
	}
	if got.RevokedAt == nil {
		t.Error("RevokedAt is nil; want non-nil")
	}
}

// TestRevokeEnvironmentKey_AlreadyRevoked: returns (nil, nil).
func TestRevokeEnvironmentKey_AlreadyRevoked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, status, revoked_at)
		VALUES ('ekid_already', 'h_ek_alr', 'env1', 'a@b.example', 'k-alr', 'revoked', now())
	`)
	got, err := db.RevokeEnvironmentKey(ctx, pool, "ekid_already")
	if err != nil {
		t.Fatalf("RevokeEnvironmentKey: %v", err)
	}
	if got != nil {
		t.Errorf("already-revoked row returned non-nil: %+v", got)
	}
}

// TestListEnvironmentKeysByOwner_FiltersAndOrders: 3 rows for a@b, 1 for c@d.
func TestListEnvironmentKeysByOwner_FiltersAndOrders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, created_at)
		VALUES
		    ('ekid_lf1', 'h_ek_lf1', 'env1', 'a@b.example', 'k1', now() - interval '3 minutes'),
		    ('ekid_lf2', 'h_ek_lf2', 'env1', 'a@b.example', 'k2', now() - interval '2 minutes'),
		    ('ekid_lf3', 'h_ek_lf3', 'env1', 'a@b.example', 'k3', now() - interval '1 minute'),
		    ('ekid_lf4', 'h_ek_lf4', 'env1', 'c@d.example', 'k4', now())
	`)

	got, next, err := db.ListEnvironmentKeysByOwner(ctx, pool, "a@b.example", 100, "")
	if err != nil {
		t.Fatalf("ListEnvironmentKeysByOwner: %v", err)
	}
	if next != "" {
		t.Errorf("nextCursor=%q; want \"\"", next)
	}
	if len(got) != 3 {
		t.Fatalf("len got=%d; want 3", len(got))
	}
	wantOrder := []string{"ekid_lf3", "ekid_lf2", "ekid_lf1"}
	for i, w := range wantOrder {
		if got[i].KeyID != w {
			t.Errorf("idx %d: got %q; want %q", i, got[i].KeyID, w)
		}
	}
}

// TestListEnvironmentKeysByOwner_Pagination: limit=2 walks 3 rows.
func TestListEnvironmentKeysByOwner_Pagination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, created_at)
		VALUES
		    ('ekid_pg1', 'h_ek_pg1', 'env1', 'p@x.example', 'k1', now() - interval '3 minutes'),
		    ('ekid_pg2', 'h_ek_pg2', 'env1', 'p@x.example', 'k2', now() - interval '2 minutes'),
		    ('ekid_pg3', 'h_ek_pg3', 'env1', 'p@x.example', 'k3', now() - interval '1 minute')
	`)

	page1, cursor1, err := db.ListEnvironmentKeysByOwner(ctx, pool, "p@x.example", 2, "")
	if err != nil || len(page1) != 2 || cursor1 == "" {
		t.Fatalf("page1: rows=%d cursor=%q err=%v", len(page1), cursor1, err)
	}
	page2, cursor2, err := db.ListEnvironmentKeysByOwner(ctx, pool, "p@x.example", 2, cursor1)
	if err != nil || len(page2) != 1 || cursor2 != "" {
		t.Fatalf("page2: rows=%d cursor=%q err=%v", len(page2), cursor2, err)
	}
}

// TestListEnvironmentKeysByOwner_InvalidCursor: malformed cursor → error.
func TestListEnvironmentKeysByOwner_InvalidCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	_, _, err := db.ListEnvironmentKeysByOwner(ctx, pool, "a@b.example", 100, "!!!!")
	if err == nil {
		t.Fatal("expected cursor error; got nil")
	}
}

// mustInsertEk inserts an environment_keys row via InsertEnvironmentKey and
// fatals on error.
func mustInsertEk(t *testing.T, pool *pgxpool.Pool, row db.EkInsertRow) {
	t.Helper()
	if err := db.InsertEnvironmentKey(context.Background(), pool, row); err != nil {
		t.Fatalf("InsertEnvironmentKey(%s): %v", row.KeyID, err)
	}
}

// TestEnvironmentKey_SuspendResumeRevokeTransitions walks the ek_ state
// machine (§7-§8): active → suspended → active → revoked, with each
// transition's no-op case (second suspend, revoke-twice, resume-after-revoke).
func TestEnvironmentKey_SuspendResumeRevokeTransitions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	future := time.Now().Add(time.Hour)
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_s1", CredentialHash: "h-s1", Environment: "demo", OwnerEmail: "u@x.com", Name: "s1", ExpiresAt: &future})

	got, err := db.GetEnvironmentKey(ctx, pool, "ekid_s1")
	if err != nil || got.ExpiresAt == nil || !got.ExpiresAt.Equal(future.Truncate(time.Microsecond)) {
		t.Fatalf("expires_at round-trip: %+v %v", got, err)
	}
	// active → suspended; suspend again is a no-op (nil, nil).
	if r, err := db.SuspendEnvironmentKey(ctx, pool, "ekid_s1"); err != nil || r == nil || r.Status != "suspended" {
		t.Fatalf("suspend: %+v %v", r, err)
	}
	if r, err := db.SuspendEnvironmentKey(ctx, pool, "ekid_s1"); err != nil || r != nil {
		t.Fatalf("second suspend: %+v %v", r, err)
	}
	// suspended → active.
	if r, err := db.ResumeEnvironmentKey(ctx, pool, "ekid_s1"); err != nil || r == nil || r.Status != "active" {
		t.Fatalf("resume: %+v %v", r, err)
	}
	// revoke from suspended; repeat is (nil, nil); resume after revoke is (nil, nil).
	_, _ = db.SuspendEnvironmentKey(ctx, pool, "ekid_s1")
	if r, err := db.RevokeEnvironmentKey(ctx, pool, "ekid_s1"); err != nil || r == nil || r.Status != "revoked" || r.RevokedAt == nil {
		t.Fatalf("revoke from suspended: %+v %v", r, err)
	}
	if r, err := db.RevokeEnvironmentKey(ctx, pool, "ekid_s1"); err != nil || r != nil {
		t.Fatalf("revoke twice: %+v %v", r, err)
	}
	if r, err := db.ResumeEnvironmentKey(ctx, pool, "ekid_s1"); err != nil || r != nil {
		t.Fatalf("resume after revoke must not resurrect: %+v %v", r, err)
	}
}

// TestEnvironmentKey_ResumeRefusesExpired: Expired outranks Suspended (§7.1)
// — a suspended row past its expires_at must not resume to active.
func TestEnvironmentKey_ResumeRefusesExpired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	past := time.Now().Add(-time.Minute)
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_x1", CredentialHash: "h-x1", Environment: "demo", OwnerEmail: "u@x.com", Name: "x1", ExpiresAt: &past})
	_, _ = db.SuspendEnvironmentKey(ctx, pool, "ekid_x1")
	if r, err := db.ResumeEnvironmentKey(ctx, pool, "ekid_x1"); err != nil || r != nil {
		t.Fatalf("resume of an expired key: %+v %v", r, err)
	}
}

// TestDrainEkRows_FlipsEveryNonRevokedRow runs the exact SQL the Environment
// finalizer's drainEkRows loop executes (db.DrainEnvironmentKeysSQL /
// db.CountUndrainedEnvironmentKeysSQL) against active + suspended + expired
// rows: all three must end 'revoked', and the leftover-count query with the
// same predicate must then return 0 (D-23, AC-12).
func TestDrainEkRows_FlipsEveryNonRevokedRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	past := time.Now().Add(-time.Minute)
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_drain_a", CredentialHash: "h-drain-a", Environment: "drain-env", OwnerEmail: "u@x.com", Name: "a"})
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_drain_b", CredentialHash: "h-drain-b", Environment: "drain-env", OwnerEmail: "u@x.com", Name: "b"})
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_drain_c", CredentialHash: "h-drain-c", Environment: "drain-env", OwnerEmail: "u@x.com", Name: "c", ExpiresAt: &past})
	if _, err := db.SuspendEnvironmentKey(ctx, pool, "ekid_drain_b"); err != nil {
		t.Fatalf("SuspendEnvironmentKey: %v", err)
	}

	if _, err := pool.Exec(ctx, db.DrainEnvironmentKeysSQL, "drain-env"); err != nil {
		t.Fatalf("DrainEnvironmentKeysSQL: %v", err)
	}

	var remaining int64
	if err := pool.QueryRow(ctx, db.CountUndrainedEnvironmentKeysSQL, "drain-env").Scan(&remaining); err != nil {
		t.Fatalf("CountUndrainedEnvironmentKeysSQL: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining undrained rows = %d; want 0", remaining)
	}

	for _, keyID := range []string{"ekid_drain_a", "ekid_drain_b", "ekid_drain_c"} {
		got, err := db.GetEnvironmentKey(ctx, pool, keyID)
		if err != nil {
			t.Fatalf("GetEnvironmentKey(%s): %v", keyID, err)
		}
		if got == nil || got.Status != "revoked" {
			t.Errorf("%s status = %+v; want revoked", keyID, got)
		}
	}
}

// TestListEnvironmentKeysForRevoke_EveryNonRevokedRow: active, suspended,
// and expired rows are all revoke candidates; only a revoked row is excluded.
func TestListEnvironmentKeysForRevoke_EveryNonRevokedRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Distinct tokens: environment_keys_litellm_token_uniq (migration 000003)
	// is a partial UNIQUE index on litellm_token WHERE NOT NULL — four rows
	// cannot share one token.
	tokA, tokB, tokC, tokD := "tok-a", "tok-b", "tok-c", "tok-d"
	past := time.Now().Add(-time.Minute)
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_a", CredentialHash: "h-a", Environment: "fin", OwnerEmail: "u@x.com", Name: "a", LiteLLMToken: &tokA})
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_b", CredentialHash: "h-b", Environment: "fin", OwnerEmail: "u@x.com", Name: "b", LiteLLMToken: &tokB})
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_c", CredentialHash: "h-c", Environment: "fin", OwnerEmail: "u@x.com", Name: "c", LiteLLMToken: &tokC, ExpiresAt: &past})
	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_d", CredentialHash: "h-d", Environment: "fin", OwnerEmail: "u@x.com", Name: "d", LiteLLMToken: &tokD})
	_, _ = db.SuspendEnvironmentKey(ctx, pool, "ekid_b")
	_, _ = db.RevokeEnvironmentKey(ctx, pool, "ekid_d")
	rows, err := db.ListEnvironmentKeysForRevoke(ctx, pool, "fin")
	if err != nil || len(rows) != 3 { // active, suspended, expired — not revoked
		t.Fatalf("%v %v", rows, err)
	}
}
