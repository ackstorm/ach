//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/user_limits.go.

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

func TestUserKeyAllowance_NoRowFallsBackToDefault(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	used, max, err := db.UserKeyAllowance(ctx, pool, "nobody@example.com", 7)
	if err != nil {
		t.Fatalf("UserKeyAllowance: %v", err)
	}
	if used != 0 || max != 7 {
		t.Fatalf("want used=0 max=7, got used=%d max=%d", used, max)
	}
}

func TestUserKeyAllowance_RowOverridesDefault(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.SetUserMaxKeys(ctx, pool, "over@example.com", 3); err != nil {
		t.Fatalf("SetUserMaxKeys: %v", err)
	}
	_, max, err := db.UserKeyAllowance(ctx, pool, "over@example.com", 7)
	if err != nil {
		t.Fatalf("UserKeyAllowance: %v", err)
	}
	if max != 3 {
		t.Fatalf("want max=3 (row wins over default 7), got %d", max)
	}
}

func TestSetUserMaxKeys_IsUpsert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.SetUserMaxKeys(ctx, pool, "up@example.com", 1); err != nil {
		t.Fatalf("first SetUserMaxKeys: %v", err)
	}
	if err := db.SetUserMaxKeys(ctx, pool, "up@example.com", 9); err != nil {
		t.Fatalf("second SetUserMaxKeys: %v", err)
	}
	_, max, err := db.UserKeyAllowance(ctx, pool, "up@example.com", 0)
	if err != nil {
		t.Fatalf("UserKeyAllowance: %v", err)
	}
	if max != 9 {
		t.Fatalf("want max=9 after upsert, got %d", max)
	}
}

func TestUserKeyAllowance_EmailIsCaseInsensitive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	if err := db.SetUserMaxKeys(ctx, pool, "  Mixed@Example.COM ", 4); err != nil {
		t.Fatalf("SetUserMaxKeys: %v", err)
	}
	_, max, err := db.UserKeyAllowance(ctx, pool, "mixed@example.com", 0)
	if err != nil {
		t.Fatalf("UserKeyAllowance: %v", err)
	}
	if max != 4 {
		t.Fatalf("want max=4 regardless of case/space, got %d", max)
	}
}

// Counting rule: non-revoked keys count (active + suspended), revoked do not.
// Uses the same insert helper the create path uses so the test tracks the
// real column set rather than a hand-rolled INSERT.
func TestUserKeyAllowance_CountsNonRevokedOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	const owner = "counter@example.com"
	for _, id := range []string{"ekid_c1", "ekid_c2", "ekid_c3"} {
		mustInsertEk(t, pool, db.EkInsertRow{
			KeyID:          id,
			CredentialHash: "h_" + id,
			Environment:    "env1",
			OwnerEmail:     owner,
			Name:           id,
			LiteLLMUserID:  strPtr("user-1"),
			LiteLLMToken:   strPtr("tok-" + id),
		})
	}
	if _, err := db.RevokeEnvironmentKey(ctx, pool, "ekid_c3"); err != nil {
		t.Fatalf("RevokeEnvironmentKey: %v", err)
	}
	if _, err := db.SuspendEnvironmentKey(ctx, pool, "ekid_c2"); err != nil {
		t.Fatalf("SuspendEnvironmentKey: %v", err)
	}
	used, _, err := db.UserKeyAllowance(ctx, pool, owner, 0)
	if err != nil {
		t.Fatalf("UserKeyAllowance: %v", err)
	}
	if used != 2 {
		t.Fatalf("want used=2 (active + suspended; revoked excluded), got %d", used)
	}
}
