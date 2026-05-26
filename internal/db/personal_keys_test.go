//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/personal_keys.go.

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

func strPtr(s string) *string { return &s }

// TestInsertPersonalKey_HappyPath: insert a fully-populated row and re-read it.
func TestInsertPersonalKey_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	row := db.PkInsertRow{
		KeyID:          "pkid_ins1",
		CredentialHash: "h_pk_ins1",
		OwnerEmail:     "a@b.example",
		ExpiresAt:      time.Now().UTC().Add(7 * 24 * time.Hour),
		LiteLLMUserID:  strPtr("user-1"),
		LiteLLMToken:   strPtr("tok-1"),
	}
	if err := db.InsertPersonalKey(ctx, pool, row); err != nil {
		t.Fatalf("InsertPersonalKey: %v", err)
	}

	got, err := db.GetPersonalKey(ctx, pool, "pkid_ins1")
	if err != nil {
		t.Fatalf("GetPersonalKey: %v", err)
	}
	if got == nil {
		t.Fatal("GetPersonalKey returned (nil,nil); want non-nil")
	}
	if got.KeyID != "pkid_ins1" || got.OwnerEmail != "a@b.example" || got.Status != "active" {
		t.Errorf("row mismatch: %+v", got)
	}
	if got.LiteLLMUserID == nil || *got.LiteLLMUserID != "user-1" {
		t.Errorf("LiteLLMUserID=%v; want user-1", got.LiteLLMUserID)
	}
	if got.LiteLLMToken == nil || *got.LiteLLMToken != "tok-1" {
		t.Errorf("LiteLLMToken=%v; want tok-1", got.LiteLLMToken)
	}
}

// TestInsertPersonalKey_UniqueViolation: re-inserting the same credential_hash
// triggers SQLSTATE 23505; the wrapped error mentions the key_id so the
// handler can disambiguate and retry with a new ulid.
func TestInsertPersonalKey_UniqueViolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	r1 := db.PkInsertRow{
		KeyID: "pkid_dup1", CredentialHash: "h_dup", OwnerEmail: "a@b.example",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := db.InsertPersonalKey(ctx, pool, r1); err != nil {
		t.Fatalf("first InsertPersonalKey: %v", err)
	}
	r2 := db.PkInsertRow{
		KeyID: "pkid_dup2", CredentialHash: "h_dup", OwnerEmail: "c@d.example",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	err := db.InsertPersonalKey(ctx, pool, r2)
	if err == nil {
		t.Fatal("expected error on duplicate credential_hash; got nil")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("expected SQLSTATE 23505 wrapped error; got %v", err)
	}
	if !strings.Contains(err.Error(), "pkid_dup2") {
		t.Errorf("wrapped error must mention key_id pkid_dup2; got %q", err)
	}
}

// TestGetPersonalKey_Absent: GetPersonalKey returns (nil, nil) on absent row.
func TestGetPersonalKey_Absent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.GetPersonalKey(ctx, pool, "pkid_does_not_exist")
	if err != nil {
		t.Fatalf("GetPersonalKey: %v", err)
	}
	if got != nil {
		t.Errorf("absent row returned non-nil: %+v", got)
	}
}

// TestRevokePersonalKey_HappyPath: flips status active → revoked, stamps
// revoked_at, and returns the row in PkKeyInfo.
func TestRevokePersonalKey_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at)
		VALUES ('pkid_rev1', 'h_rev1', 'a@b.example', now() + interval '1 hour')
	`)
	got, err := db.RevokePersonalKey(ctx, pool, "pkid_rev1")
	if err != nil {
		t.Fatalf("RevokePersonalKey: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil PkKeyInfo on successful revoke")
	}
	if got.Status != "revoked" {
		t.Errorf("Status=%q; want revoked", got.Status)
	}
	if got.RevokedAt == nil {
		t.Error("RevokedAt is nil; want non-nil timestamp")
	}

	// Confirm the row in DB.
	var dbStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM personal_keys WHERE key_id='pkid_rev1'`).Scan(&dbStatus); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if dbStatus != "revoked" {
		t.Errorf("dbStatus=%q; want revoked", dbStatus)
	}
}

// TestRevokePersonalKey_AlreadyRevoked: re-revoking a revoked row returns
// (nil, nil) — the WHERE status='active' predicate matched zero rows.
func TestRevokePersonalKey_AlreadyRevoked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, status, expires_at, revoked_at)
		VALUES ('pkid_already', 'h_already', 'a@b.example', 'revoked', now() + interval '1 hour', now())
	`)
	got, err := db.RevokePersonalKey(ctx, pool, "pkid_already")
	if err != nil {
		t.Fatalf("RevokePersonalKey: %v", err)
	}
	if got != nil {
		t.Errorf("already-revoked row returned non-nil: %+v", got)
	}
}

// TestRevokePersonalKey_Absent: revoking a non-existent key returns (nil, nil).
func TestRevokePersonalKey_Absent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.RevokePersonalKey(ctx, pool, "pkid_does_not_exist")
	if err != nil {
		t.Fatalf("RevokePersonalKey: %v", err)
	}
	if got != nil {
		t.Errorf("absent row returned non-nil: %+v", got)
	}
}

// TestListPersonalKeysByOwner_FiltersAndOrders: seed 3 rows for a@b and 1 for
// c@d; list for a@b returns 3 rows ordered created_at DESC.
func TestListPersonalKeysByOwner_FiltersAndOrders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Use distinct created_at via pg_sleep so DESC ordering is deterministic.
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, created_at)
		VALUES
		    ('pkid_l1', 'h_l1', 'a@b.example', now() + interval '1 hour', now() - interval '3 minutes'),
		    ('pkid_l2', 'h_l2', 'a@b.example', now() + interval '1 hour', now() - interval '2 minutes'),
		    ('pkid_l3', 'h_l3', 'a@b.example', now() + interval '1 hour', now() - interval '1 minute'),
		    ('pkid_l4', 'h_l4', 'c@d.example', now() + interval '1 hour', now())
	`)

	got, next, err := db.ListPersonalKeysByOwner(ctx, pool, "a@b.example", 100, "")
	if err != nil {
		t.Fatalf("ListPersonalKeysByOwner: %v", err)
	}
	if next != "" {
		t.Errorf("nextCursor=%q; want \"\" (single-page result)", next)
	}
	if len(got) != 3 {
		t.Fatalf("len got=%d; want 3", len(got))
	}
	// Expected DESC ordering: l3, l2, l1.
	wantOrder := []string{"pkid_l3", "pkid_l2", "pkid_l1"}
	for i, w := range wantOrder {
		if got[i].KeyID != w {
			t.Errorf("idx %d: got %q; want %q", i, got[i].KeyID, w)
		}
		if got[i].OwnerEmail != "a@b.example" {
			t.Errorf("idx %d: owner=%q; want a@b.example", i, got[i].OwnerEmail)
		}
	}
}

// TestListPersonalKeysByOwner_PaginationCursor: with limit=2 over 3 rows,
// page 1 returns 2 rows + nextCursor; page 2 returns 1 row + empty cursor.
func TestListPersonalKeysByOwner_PaginationCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, created_at)
		VALUES
		    ('pkid_p1', 'h_p1', 'p@x.example', now() + interval '1 hour', now() - interval '3 minutes'),
		    ('pkid_p2', 'h_p2', 'p@x.example', now() + interval '1 hour', now() - interval '2 minutes'),
		    ('pkid_p3', 'h_p3', 'p@x.example', now() + interval '1 hour', now() - interval '1 minute')
	`)

	page1, cursor1, err := db.ListPersonalKeysByOwner(ctx, pool, "p@x.example", 2, "")
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len=%d; want 2", len(page1))
	}
	if cursor1 == "" {
		t.Fatal("page1 cursor empty; want non-empty (more pages available)")
	}

	page2, cursor2, err := db.ListPersonalKeysByOwner(ctx, pool, "p@x.example", 2, cursor1)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("page2 len=%d; want 1", len(page2))
	}
	if cursor2 != "" {
		t.Errorf("page2 cursor=%q; want \"\" (last page)", cursor2)
	}

	// Confirm the union of pages covers all 3 rows exactly once.
	all := map[string]bool{}
	for _, r := range page1 {
		all[r.KeyID] = true
	}
	for _, r := range page2 {
		if all[r.KeyID] {
			t.Errorf("row %q appeared on both pages", r.KeyID)
		}
		all[r.KeyID] = true
	}
	if len(all) != 3 {
		t.Errorf("union covered %d distinct rows; want 3", len(all))
	}
}

// TestListPersonalKeysByOwner_LimitClamping: limit=0 → defaults to 100;
// limit=999 → clamped to 500.
func TestListPersonalKeysByOwner_LimitClamping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Seed no rows — we only care about the clamping not panicking on
	// the upper bound query plan.
	got, _, err := db.ListPersonalKeysByOwner(ctx, pool, "nobody@x.example", 0, "")
	if err != nil {
		t.Fatalf("limit=0: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("limit=0 over empty owner returned %d rows; want 0", len(got))
	}
	got, _, err = db.ListPersonalKeysByOwner(ctx, pool, "nobody@x.example", 999, "")
	if err != nil {
		t.Fatalf("limit=999: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("limit=999 over empty owner returned %d rows; want 0", len(got))
	}
}

// TestListPersonalKeysByOwner_InvalidCursor: a malformed cursor returns a
// non-nil error so the handler can render 400 invalid_cursor.
func TestListPersonalKeysByOwner_InvalidCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// "!" is not valid base64.
	_, _, err := db.ListPersonalKeysByOwner(ctx, pool, "a@b.example", 100, "!!!!")
	if err == nil {
		t.Fatal("expected error on malformed cursor; got nil")
	}
	if !strings.Contains(err.Error(), "cursor") {
		t.Errorf("error %q should mention cursor", err)
	}
}
