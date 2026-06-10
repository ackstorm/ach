//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/check_extend.go (Hub §7.1).
//
// The testcontainers-go Postgres setup is reused from phase2_helpers_test.go;
// each subtest gets its own container so seeded rows are invisible across
// tests (assertion isolation; Phase 2 convention).

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestPkCheckAndExtend_Active_LastUsedStale verifies the sliding-window
// extension fires when last_used_at is older than 5 minutes: BOTH
// last_used_at and expires_at advance to now() / now()+7d.
func TestPkCheckAndExtend_Active_LastUsedStale(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	const hash = "h_pk_stale"
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email,
			status, expires_at, last_used_at)
		VALUES ('pkid_stale', 'h_pk_stale', 'a@b.example',
			'active', now() + interval '1 day', now() - interval '10 minutes')
	`)

	info, err := db.PkCheckAndExtend(ctx, pool, hash)
	if err != nil {
		t.Fatalf("PkCheckAndExtend: %v", err)
	}
	if info == nil {
		t.Fatal("PkCheckAndExtend returned (nil, nil); want non-nil PkKeyInfo")
	}
	if info.KeyID != "pkid_stale" {
		t.Errorf("KeyID=%q; want pkid_stale", info.KeyID)
	}
	if info.OwnerEmail != "a@b.example" {
		t.Errorf("OwnerEmail=%q; want a@b.example", info.OwnerEmail)
	}

	// Re-read the row to confirm last_used_at advanced and expires_at extended.
	var lastUsedAt, expiresAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT last_used_at, expires_at FROM personal_keys WHERE key_id = 'pkid_stale'
	`).Scan(&lastUsedAt, &expiresAt); err != nil {
		t.Fatalf("re-read row: %v", err)
	}
	now := time.Now().UTC()
	if d := now.Sub(lastUsedAt); d < 0 || d > 5*time.Second {
		t.Errorf("last_used_at=%v not within 5s of now=%v (delta=%v)", lastUsedAt, now, d)
	}
	// expires_at should be approximately now() + 7d (within 5s).
	wantExp := now.Add(7 * 24 * time.Hour)
	if d := wantExp.Sub(expiresAt); d < -5*time.Second || d > 5*time.Second {
		t.Errorf("expires_at=%v not within 5s of now+7d=%v (delta=%v)", expiresAt, wantExp, d)
	}
	// The returned PkKeyInfo.ExpiresAt should equal the new wall-clock value.
	if !info.ExpiresAt.Equal(expiresAt) {
		t.Errorf("info.ExpiresAt=%v != row.expires_at=%v", info.ExpiresAt, expiresAt)
	}
}

// TestPkCheckAndExtend_Active_LastUsedFresh verifies the debounce: last_used_at
// within the past 5 minutes leaves BOTH columns unchanged.
func TestPkCheckAndExtend_Active_LastUsedFresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email,
			status, expires_at, last_used_at)
		VALUES ('pkid_fresh', 'h_pk_fresh', 'a@b.example',
			'active', now() + interval '5 days', now() - interval '1 minute')
	`)

	// Snapshot the original values before the call.
	var origLastUsed, origExpires time.Time
	if err := pool.QueryRow(ctx, `
		SELECT last_used_at, expires_at FROM personal_keys WHERE key_id = 'pkid_fresh'
	`).Scan(&origLastUsed, &origExpires); err != nil {
		t.Fatalf("snapshot orig: %v", err)
	}

	info, err := db.PkCheckAndExtend(ctx, pool, "h_pk_fresh")
	if err != nil {
		t.Fatalf("PkCheckAndExtend: %v", err)
	}
	if info == nil {
		t.Fatal("returned (nil, nil); want non-nil (active key with fresh last_used_at)")
	}

	// Re-read and confirm both columns unchanged (debounce path).
	var newLastUsed, newExpires time.Time
	if err := pool.QueryRow(ctx, `
		SELECT last_used_at, expires_at FROM personal_keys WHERE key_id = 'pkid_fresh'
	`).Scan(&newLastUsed, &newExpires); err != nil {
		t.Fatalf("re-read row: %v", err)
	}
	if !newLastUsed.Equal(origLastUsed) {
		t.Errorf("last_used_at advanced on debounce path: orig=%v new=%v", origLastUsed, newLastUsed)
	}
	if !newExpires.Equal(origExpires) {
		t.Errorf("expires_at extended on debounce path: orig=%v new=%v", origExpires, newExpires)
	}
}

// TestPkCheckAndExtend_Revoked verifies KEY-04: a revoked row returns
// (nil, nil). The status='active' CTE predicate excludes it.
func TestPkCheckAndExtend_Revoked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email,
			status, expires_at, revoked_at)
		VALUES ('pkid_rev', 'h_pk_rev', 'a@b.example',
			'revoked', now() + interval '1 day', now())
	`)

	info, err := db.PkCheckAndExtend(ctx, pool, "h_pk_rev")
	if err != nil {
		t.Fatalf("PkCheckAndExtend: %v", err)
	}
	if info != nil {
		t.Errorf("revoked row returned non-nil PkKeyInfo: %+v", info)
	}
}

// TestPkCheckAndExtend_Expired verifies KEY-04: an active row with
// expires_at in the past returns (nil, nil) — the expires_at > now()
// CTE predicate excludes it.
func TestPkCheckAndExtend_Expired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email,
			status, expires_at)
		VALUES ('pkid_exp', 'h_pk_exp', 'a@b.example',
			'active', now() - interval '1 day')
	`)

	info, err := db.PkCheckAndExtend(ctx, pool, "h_pk_exp")
	if err != nil {
		t.Fatalf("PkCheckAndExtend: %v", err)
	}
	if info != nil {
		t.Errorf("expired row returned non-nil PkKeyInfo: %+v", info)
	}
}

// TestPkCheckAndExtend_UnknownHash verifies KEY-04: an unknown credential_hash
// returns (nil, nil). All three causes (revoked/expired/unknown) are
// indistinguishable to the caller.
func TestPkCheckAndExtend_UnknownHash(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	info, err := db.PkCheckAndExtend(ctx, pool, "h_pk_never_seen")
	if err != nil {
		t.Fatalf("PkCheckAndExtend: %v", err)
	}
	if info != nil {
		t.Errorf("unknown hash returned non-nil PkKeyInfo: %+v", info)
	}
}

// TestPkCheckAndExtend_ReturnsKeyMaterial verifies the round-trip of the
// TESTING-PHASE (reverts FIX01 §A.6) litellm_key_material column: a row
// inserted with the plaintext virtual key surfaces it on the resolve path.
func TestPkCheckAndExtend_ReturnsKeyMaterial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	const hash = "h_pk_material"
	material := "sk-test-pk-material"
	if err := db.InsertPersonalKey(ctx, pool, db.PkInsertRow{
		KeyID:              "pkid_material",
		CredentialHash:     hash,
		OwnerEmail:         "a@b.example",
		ExpiresAt:          time.Now().UTC().Add(time.Hour),
		LiteLLMKeyMaterial: &material,
	}); err != nil {
		t.Fatalf("InsertPersonalKey: %v", err)
	}

	row, err := db.PkCheckAndExtend(ctx, pool, hash)
	if err != nil {
		t.Fatalf("PkCheckAndExtend: %v", err)
	}
	if row == nil || row.LiteLLMKeyMaterial == nil || *row.LiteLLMKeyMaterial != material {
		t.Fatalf("LiteLLMKeyMaterial = %v; want %q", row.LiteLLMKeyMaterial, material)
	}
}

// TestPkCheckAndExtend_NullableLitellmFields verifies a row with NULL
// litellm_user_id + NULL litellm_token returns *PkKeyInfo where both
// pointer fields are nil (not empty strings).
func TestPkCheckAndExtend_NullableLitellmFields(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// litellm_user_id + litellm_token both default to NULL.
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email,
			status, expires_at)
		VALUES ('pkid_null', 'h_pk_null', 'a@b.example',
			'active', now() + interval '1 day')
	`)

	info, err := db.PkCheckAndExtend(ctx, pool, "h_pk_null")
	if err != nil {
		t.Fatalf("PkCheckAndExtend: %v", err)
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
