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
