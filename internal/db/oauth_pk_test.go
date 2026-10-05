//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/oauth_pk.go.

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// An active oauth row past its expires_at: the resolver must not resolve it
// (ActiveOAuthPK), yet it still occupies the one-active-oauth-per-owner
// index, so the token endpoint's lookup (ActiveOAuthPKAnyExpiry) must
// return it — that is the row it revokes before minting a new one.
func TestOAuthPK_ExpiredActiveRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, purpose)
		VALUES ('pkid_old', 'h_old', 'svc@example.com', now() - interval '1 minute', 'oauth')
	`)

	if got, err := db.ActiveOAuthPK(ctx, pool, "svc@example.com"); err != nil || got != nil {
		t.Fatalf("resolver must not see an expired row: got=%+v err=%v", got, err)
	}
	got, err := db.ActiveOAuthPKAnyExpiry(ctx, pool, "svc@example.com")
	if err != nil || got == nil || got.KeyID != "pkid_old" {
		t.Fatalf("token endpoint must see the expired row: got=%+v err=%v", got, err)
	}

	// The index ignores expiry: a second active oauth row collides until
	// the expired one is revoked.
	next := db.PkInsertRow{KeyID: "pkid_new", CredentialHash: "h_new", OwnerEmail: "svc@example.com", ExpiresAt: time.Now().Add(time.Hour), Purpose: "oauth"}
	if err := db.InsertPersonalKey(ctx, pool, next); err == nil {
		t.Fatal("insert beside an expired active oauth row must violate the unique index")
	}
	if _, err := db.RevokePersonalKey(ctx, pool, got.KeyID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := db.InsertPersonalKey(ctx, pool, next); err != nil {
		t.Fatalf("insert after revoke: %v", err)
	}
	if got, err := db.ActiveOAuthPK(ctx, pool, "svc@example.com"); err != nil || got == nil || got.KeyID != "pkid_new" {
		t.Fatalf("resolver sees the new row: got=%+v err=%v", got, err)
	}
}
