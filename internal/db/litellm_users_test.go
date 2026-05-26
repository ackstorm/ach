//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/litellm_users.go.
//
// Each test inserts personal_keys / environment_keys rows that respect the
// Phase 1 CHECK constraints (key_id LIKE 'pkid_%' / 'ekid_%'; status enum;
// credential_hash UNIQUE — distinct per row).

package db_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

// TestListACHManagedLitellmUsers_Empty: fresh DB → empty slice + nil error.
func TestListACHManagedLitellmUsers_Empty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	got, err := db.ListACHManagedLitellmUsers(ctx, pool)
	if err != nil {
		t.Fatalf("ListACHManagedLitellmUsers (empty): %v", err)
	}
	if got == nil {
		t.Error("returned nil; want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("got %d entries; want 0", len(got))
	}
}

// TestListACHManagedLitellmUsers_PersonalKeysOnly: two active personal_keys
// with different litellm_user_id → both returned.
func TestListACHManagedLitellmUsers_PersonalKeysOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Two active pk rows, distinct litellm_user_id, distinct credential_hash.
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, litellm_user_id)
		VALUES
		    ('pkid_a1', 'h_a1', 'a@b.example', now() + interval '1 hour', 'user-1'),
		    ('pkid_a2', 'h_a2', 'a@b.example', now() + interval '1 hour', 'user-2')
	`)

	got, err := db.ListACHManagedLitellmUsers(ctx, pool)
	if err != nil {
		t.Fatalf("ListACHManagedLitellmUsers: %v", err)
	}
	sort.Strings(got)
	want := []string{"user-1", "user-2"}
	if len(got) != len(want) {
		t.Fatalf("len got=%d, want=%d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestListACHManagedLitellmUsers_Dedup: one personal_keys row + one
// environment_keys row with the SAME litellm_user_id → returned slice has
// length 1 (UNION dedups).
func TestListACHManagedLitellmUsers_Dedup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, litellm_user_id)
		VALUES ('pkid_d1', 'h_pk_d1', 'a@b.example', now() + interval '1 hour', 'shared-user')
	`)
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, litellm_user_id)
		VALUES ('ekid_d1', 'h_ek_d1', 'env1', 'a@b.example', 'k1', 'shared-user')
	`)

	got, err := db.ListACHManagedLitellmUsers(ctx, pool)
	if err != nil {
		t.Fatalf("ListACHManagedLitellmUsers: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 deduped row; got %d: %v", len(got), got)
	}
	if got[0] != "shared-user" {
		t.Errorf("got %q, want \"shared-user\"", got[0])
	}
}

// TestListACHManagedLitellmUsers_ExcludesInactive: a personal_keys row with
// status='revoked' must NOT be in the result; ditto environment_keys revoked.
func TestListACHManagedLitellmUsers_ExcludesInactive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, status, litellm_user_id)
		VALUES
		    ('pkid_i1', 'h_pk_active',  'a@b.example', now() + interval '1 hour', 'active',  'active-pk-user'),
		    ('pkid_i2', 'h_pk_revoked', 'a@b.example', now() + interval '1 hour', 'revoked', 'revoked-pk-user'),
		    ('pkid_i3', 'h_pk_expired', 'a@b.example', now() + interval '1 hour', 'expired', 'expired-pk-user')
	`)
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, status, litellm_user_id)
		VALUES
		    ('ekid_i1', 'h_ek_active',  'env1', 'a@b.example', 'k-a', 'active',  'active-ek-user'),
		    ('ekid_i2', 'h_ek_revoked', 'env1', 'a@b.example', 'k-r', 'revoked', 'revoked-ek-user')
	`)

	got, err := db.ListACHManagedLitellmUsers(ctx, pool)
	if err != nil {
		t.Fatalf("ListACHManagedLitellmUsers: %v", err)
	}
	sort.Strings(got)
	want := []string{"active-ek-user", "active-pk-user"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries; want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// Confirm none of the inactive-user IDs leaked in.
	for _, g := range got {
		if g == "revoked-pk-user" || g == "expired-pk-user" || g == "revoked-ek-user" {
			t.Errorf("inactive user id leaked: %q", g)
		}
	}
}

// TestListACHManagedLitellmUsers_ExcludesNullAndEmpty: a personal_keys row
// with litellm_user_id IS NULL and one with litellm_user_id = ” must both
// be excluded (Phase 2 schema invariant + defense against pre-SSO empty).
func TestListACHManagedLitellmUsers_ExcludesNullAndEmpty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// NULL litellm_user_id (status active).
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at)
		VALUES ('pkid_null', 'h_pk_null', 'a@b.example', now() + interval '1 hour')
	`)
	// Empty-string litellm_user_id (status active).
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, litellm_user_id)
		VALUES ('pkid_empty', 'h_pk_empty', 'a@b.example', now() + interval '1 hour', '')
	`)
	// One legitimate non-empty row to confirm we'd see SOMETHING if the filter were broken.
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, litellm_user_id)
		VALUES ('pkid_real', 'h_pk_real', 'a@b.example', now() + interval '1 hour', 'real-user')
	`)
	// Parity: a NULL + empty on environment_keys.
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name)
		VALUES ('ekid_null', 'h_ek_null', 'env1', 'a@b.example', 'k-null')
	`)
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, litellm_user_id)
		VALUES ('ekid_empty', 'h_ek_empty', 'env1', 'a@b.example', 'k-empty', '')
	`)

	got, err := db.ListACHManagedLitellmUsers(ctx, pool)
	if err != nil {
		t.Fatalf("ListACHManagedLitellmUsers: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries; want 1 (only \"real-user\"). got=%v", len(got), got)
	}
	if got[0] != "real-user" {
		t.Errorf("got %q, want \"real-user\"", got[0])
	}
}

// mustExec runs a write SQL statement and fatals on error.
func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatalf("Exec %q: %v", firstLine(sql), err)
	}
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
