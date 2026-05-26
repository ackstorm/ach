//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/ek_resolve.go (Hub §8.1).

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestEkResolve_Active verifies the happy path: an active environment_keys
// row returns *EkKeyInfo with all six exported fields populated; last_used_at
// advances on the row.
func TestEkResolve_Active(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment,
			owner_email, name, status, litellm_user_id, litellm_token)
		VALUES ('ekid_active', 'h_ek_active', 'env1',
			'a@b.example', 'k-active', 'active', 'user-1', 'tok-abc')
	`)

	info, err := db.EkResolve(ctx, pool, "h_ek_active")
	if err != nil {
		t.Fatalf("EkResolve: %v", err)
	}
	if info == nil {
		t.Fatal("EkResolve returned (nil, nil); want non-nil")
	}
	if info.KeyID != "ekid_active" {
		t.Errorf("KeyID=%q; want ekid_active", info.KeyID)
	}
	if info.Environment != "env1" {
		t.Errorf("Environment=%q; want env1", info.Environment)
	}
	if info.OwnerEmail != "a@b.example" {
		t.Errorf("OwnerEmail=%q; want a@b.example", info.OwnerEmail)
	}
	if info.Name != "k-active" {
		t.Errorf("Name=%q; want k-active", info.Name)
	}
	if info.LiteLLMUserID == nil || *info.LiteLLMUserID != "user-1" {
		t.Errorf("LiteLLMUserID=%v; want non-nil 'user-1'", info.LiteLLMUserID)
	}
	if info.LiteLLMToken == nil || *info.LiteLLMToken != "tok-abc" {
		t.Errorf("LiteLLMToken=%v; want non-nil 'tok-abc'", info.LiteLLMToken)
	}

	// Re-read the row: last_used_at MUST have advanced (was NULL → now()).
	var lastUsedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT last_used_at FROM environment_keys WHERE key_id = 'ekid_active'
	`).Scan(&lastUsedAt); err != nil {
		t.Fatalf("re-read row: %v", err)
	}
	if lastUsedAt == nil {
		t.Fatal("last_used_at still NULL after EkResolve; debounce CASE should have set it")
	}
	if d := time.Since(*lastUsedAt); d < 0 || d > 5*time.Second {
		t.Errorf("last_used_at=%v not within 5s of now (delta=%v)", *lastUsedAt, d)
	}
}

// TestEkResolve_Revoked verifies KEY-06: a revoked row returns (nil, nil)
// because the status='active' predicate excludes it. The status='active'
// CHECK constraint is authoritative for the auth decision.
func TestEkResolve_Revoked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment,
			owner_email, name, status, revoked_at)
		VALUES ('ekid_rev', 'h_ek_rev', 'env1', 'a@b.example', 'k-rev',
			'revoked', now())
	`)

	info, err := db.EkResolve(ctx, pool, "h_ek_rev")
	if err != nil {
		t.Fatalf("EkResolve: %v", err)
	}
	if info != nil {
		t.Errorf("revoked row returned non-nil EkKeyInfo: %+v", info)
	}
}

// TestEkResolve_Debounce verifies the 5-minute debounce: an active row whose
// last_used_at is within the past 5 minutes returns non-nil EkKeyInfo AND
// the row's last_used_at remains unchanged.
func TestEkResolve_Debounce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment,
			owner_email, name, status, last_used_at)
		VALUES ('ekid_deb', 'h_ek_deb', 'env1', 'a@b.example', 'k-deb',
			'active', now() - interval '1 minute')
	`)

	// Snapshot the original last_used_at.
	var orig time.Time
	if err := pool.QueryRow(ctx, `
		SELECT last_used_at FROM environment_keys WHERE key_id = 'ekid_deb'
	`).Scan(&orig); err != nil {
		t.Fatalf("snapshot orig: %v", err)
	}

	info, err := db.EkResolve(ctx, pool, "h_ek_deb")
	if err != nil {
		t.Fatalf("EkResolve: %v", err)
	}
	if info == nil {
		t.Fatal("returned (nil, nil); want non-nil for active row")
	}

	// Re-read; last_used_at must be unchanged on debounce path.
	var newVal time.Time
	if err := pool.QueryRow(ctx, `
		SELECT last_used_at FROM environment_keys WHERE key_id = 'ekid_deb'
	`).Scan(&newVal); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !newVal.Equal(orig) {
		t.Errorf("last_used_at advanced on debounce path: orig=%v new=%v", orig, newVal)
	}
}

// TestEkResolve_NullLitellmToken verifies a row with NULL litellm_token
// (the steady-state for Phase 02.2 / Phase 3 pre-SSO-write rows) returns
// *EkKeyInfo with LiteLLMToken==nil — the *string pointer correctly
// distinguishes NULL from empty-string.
func TestEkResolve_NullLitellmToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// No litellm_user_id, no litellm_token — both NULL by default.
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment,
			owner_email, name, status)
		VALUES ('ekid_null', 'h_ek_null', 'env1', 'a@b.example', 'k-null', 'active')
	`)

	info, err := db.EkResolve(ctx, pool, "h_ek_null")
	if err != nil {
		t.Fatalf("EkResolve: %v", err)
	}
	if info == nil {
		t.Fatal("returned (nil, nil); want non-nil")
	}
	if info.LiteLLMUserID != nil {
		t.Errorf("LiteLLMUserID=%v; want nil for NULL column", *info.LiteLLMUserID)
	}
	if info.LiteLLMToken != nil {
		t.Errorf("LiteLLMToken=%v; want nil for NULL column", *info.LiteLLMToken)
	}
}

// TestEkResolve_UnknownHash: an unknown credential_hash returns (nil, nil).
func TestEkResolve_UnknownHash(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	info, err := db.EkResolve(ctx, pool, "h_ek_never_seen")
	if err != nil {
		t.Fatalf("EkResolve: %v", err)
	}
	if info != nil {
		t.Errorf("unknown hash returned non-nil: %+v", info)
	}
}
