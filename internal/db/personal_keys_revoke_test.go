//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/personal_keys_revoke.go.

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestRevokePersonalKeyByOwner_OwnActiveKey: revoking an active key owned by
// the caller flips status to 'revoked' and returns the litellm_token.
func TestRevokePersonalKeyByOwner_OwnActiveKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, litellm_token)
		VALUES ('pkid_rob1', 'h_rob1', 'alice@example.com', now() + interval '1 hour', 'sk-tok-alice')
	`)

	tok, err := db.RevokePersonalKeyByOwner(ctx, pool, "pkid_rob1", "alice@example.com")
	if err != nil {
		t.Fatalf("RevokePersonalKeyByOwner: %v", err)
	}
	if tok == nil {
		t.Fatal("returned litellmToken is nil; want non-nil")
	}
	if *tok != "sk-tok-alice" {
		t.Errorf("litellmToken=%q; want sk-tok-alice", *tok)
	}

	// Confirm the row was actually flipped in the DB.
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM personal_keys WHERE key_id = 'pkid_rob1'`).Scan(&status); err != nil {
		t.Fatalf("re-read status: %v", err)
	}
	if status != "revoked" {
		t.Errorf("DB status=%q; want revoked", status)
	}
}

// TestRevokePersonalKeyByOwner_WrongOwner: another owner's key returns
// ErrKeyNotFoundOrNotOwner and leaves the row untouched.
func TestRevokePersonalKeyByOwner_WrongOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at)
		VALUES ('pkid_rob2', 'h_rob2', 'alice@example.com', now() + interval '1 hour')
	`)

	_, err := db.RevokePersonalKeyByOwner(ctx, pool, "pkid_rob2", "mallory@example.com")
	if err == nil {
		t.Fatal("expected ErrKeyNotFoundOrNotOwner; got nil")
	}
	if !errors.Is(err, db.ErrKeyNotFoundOrNotOwner) {
		t.Errorf("err=%v; want ErrKeyNotFoundOrNotOwner", err)
	}

	// Row MUST still be active.
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM personal_keys WHERE key_id = 'pkid_rob2'`).Scan(&status); err != nil {
		t.Fatalf("re-read status: %v", err)
	}
	if status != "active" {
		t.Errorf("DB status=%q after wrong-owner attempt; want active", status)
	}
}

// TestRevokePersonalKeyByOwner_AlreadyRevoked: re-revoking a key that is
// already revoked returns ErrKeyNotFoundOrNotOwner.
func TestRevokePersonalKeyByOwner_AlreadyRevoked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, status, expires_at, revoked_at)
		VALUES ('pkid_rob3', 'h_rob3', 'alice@example.com', 'revoked', now() + interval '1 hour', now())
	`)

	_, err := db.RevokePersonalKeyByOwner(ctx, pool, "pkid_rob3", "alice@example.com")
	if err == nil {
		t.Fatal("expected ErrKeyNotFoundOrNotOwner on already-revoked key; got nil")
	}
	if !errors.Is(err, db.ErrKeyNotFoundOrNotOwner) {
		t.Errorf("err=%v; want ErrKeyNotFoundOrNotOwner", err)
	}
}
