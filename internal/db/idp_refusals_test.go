//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// A refused owner's ek_ keys (manual and exchange) stop resolving, the
// in-flight re-check and the listing see the refusal, and lifting it
// brings the keys back. The owner is matched case-insensitively.
func TestIdPRefusal_BlocksTheOwnersKeysUntilLifted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment,
			owner_email, name, status, litellm_user_id, litellm_token)
		VALUES ('ekid_manual', 'h_manual', 'env1', 'Leaver@B.example', 'ci', 'active', 'u1', 't1')`)
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment,
			owner_email, name, status, litellm_user_id, litellm_token, issued_via, requester)
		VALUES ('ekid_exch', 'h_exch', 'env1', 'leaver@b.example', 'exchange:ns/sa', 'active', 'u1', 't2', 'exchange', 'ns/sa')`)

	if err := db.SetIdPRefused(ctx, pool, "leaver@b.example", true); err != nil {
		t.Fatal(err)
	}
	if refused, err := db.IdPRefused(ctx, pool, "Leaver@B.example"); err != nil || !refused {
		t.Fatalf("IdPRefused: %v %v, want true", refused, err)
	}
	if info, err := db.EkResolve(ctx, pool, "h_manual"); err != nil || info != nil {
		t.Fatalf("EkResolve of a refused owner: %v %v, want nil nil", info, err)
	}
	if info, err := db.ExchangeKeyResolve(ctx, pool, "ekid_exch"); err != nil || info != nil {
		t.Fatalf("ExchangeKeyResolve of a refused owner: %v %v, want nil nil", info, err)
	}
	row, err := db.GetEnvironmentKey(ctx, pool, "ekid_manual")
	if err != nil || row == nil || !row.OwnerRefused {
		t.Fatalf("GetEnvironmentKey: %+v %v, want OwnerRefused", row, err)
	}
	owner := "Leaver@B.example"
	items, _, err := db.ListKeys(ctx, pool, db.KeyListFilter{OwnerEmail: &owner}, 10, "")
	if err != nil || len(items) != 1 || !items[0].OwnerRefused {
		t.Fatalf("ListKeys: %+v %v, want one row with OwnerRefused", items, err)
	}

	if err := db.SetIdPRefused(ctx, pool, "LEAVER@b.example", false); err != nil {
		t.Fatal(err)
	}
	if info, err := db.EkResolve(ctx, pool, "h_manual"); err != nil || info == nil {
		t.Fatalf("EkResolve after the refusal is lifted: %v %v, want a row", info, err)
	}
	if refused, err := db.IdPRefused(ctx, pool, "leaver@b.example"); err != nil || refused {
		t.Fatalf("IdPRefused after the lift: %v %v, want false", refused, err)
	}
}

// Only active, unexpired keys used inside the window count, and an owner
// already refused is not listed again.
func TestEkOwnersInUse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, status, last_used_at)
		VALUES ('ekid_a', 'h_a', 'env1', 'A@x.example', 'a', 'active',  now() - interval '1 hour'),
		       ('ekid_a2','h_a2','env1', 'a@x.example', 'a2','active',  now() - interval '2 hours'),
		       ('ekid_b', 'h_b', 'env1', 'b@x.example', 'b', 'active',  now() - interval '10 days'),
		       ('ekid_c', 'h_c', 'env1', 'c@x.example', 'c', 'revoked', now()),
		       ('ekid_d', 'h_d', 'env1', 'd@x.example', 'd', 'active',  now())`)
	if err := db.SetIdPRefused(ctx, pool, "d@x.example", true); err != nil {
		t.Fatal(err)
	}
	got, err := db.EkOwnersInUse(ctx, pool, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "a@x.example" {
		t.Fatalf("EkOwnersInUse = %v, want [a@x.example]", got)
	}
}
