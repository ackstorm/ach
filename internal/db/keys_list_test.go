//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

func TestListKeys_MergesPkAndEkOrderedByCreatedDesc(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, created_at)
		VALUES ('pkid_a', 'h_pk_a', 'u@x.example', now() + interval '1 day', now() - interval '4 minutes')`)
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, created_at)
		VALUES
		    ('ekid_b', 'h_ek_b', 'env1', 'u@x.example', 'k-b', now() - interval '3 minutes'),
		    ('ekid_c', 'h_ek_c', 'env1', 'other@x.example', 'k-c', now() - interval '2 minutes')`)

	owner := "u@x.example"
	got, next, err := db.ListKeys(ctx, pool, db.KeyListFilter{OwnerEmail: &owner}, 100, "")
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if next != "" {
		t.Errorf("nextCursor=%q; want \"\"", next)
	}
	if len(got) != 2 {
		t.Fatalf("len got=%d; want 2 (owner-scoped)", len(got))
	}
	// created_at DESC, key_id DESC: ekid_b (3m ago) before pkid_a (4m ago)
	if got[0].KeyID != "ekid_b" || got[0].Type != "ek" {
		t.Errorf("got[0]=%+v; want ekid_b/ek", got[0])
	}
	if got[1].KeyID != "pkid_a" || got[1].Type != "pk" {
		t.Errorf("got[1]=%+v; want pkid_a/pk", got[1])
	}
	if got[1].Environment != nil || got[1].Name != nil {
		t.Errorf("pk row should have nil Environment/Name; got %+v", got[1])
	}
}

func TestListKeys_FiltersByTypeAndStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, status, revoked_at)
		VALUES
		    ('ekid_act', 'h1', 'env1', 'u@x.example', 'a', 'active', NULL),
		    ('ekid_rev', 'h2', 'env1', 'u@x.example', 'r', 'revoked', now())`)
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, status)
		VALUES ('pkid_act', 'h3', 'u@x.example', now() + interval '1 day', 'active')`)

	owner := "u@x.example"
	// status=active hides the revoked ek
	got, _, err := db.ListKeys(ctx, pool, db.KeyListFilter{OwnerEmail: &owner, Status: "active"}, 100, "")
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("status=active len=%d; want 2", len(got))
	}
	for _, k := range got {
		if k.Status != "active" {
			t.Errorf("unexpected non-active row %+v", k)
		}
	}
	// type=ek + status=active -> only ekid_act
	got, _, err = db.ListKeys(ctx, pool, db.KeyListFilter{OwnerEmail: &owner, Type: "ek", Status: "active"}, 100, "")
	if err != nil {
		t.Fatalf("ListKeys type=ek: %v", err)
	}
	if len(got) != 1 || got[0].KeyID != "ekid_act" {
		t.Fatalf("type=ek/active got=%+v; want [ekid_act]", got)
	}
}

func TestListKeys_CursorPaginatesAcrossPkAndEk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Insert 3 keys for the same owner with strictly ordered created_at (DESC: ek1 > pk1 > ek2).
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, created_at)
		VALUES
		    ('ekid_cur1', 'h_cur_ek1', 'envA', 'cur@x.example', 'k1', now() - interval '1 minutes'),
		    ('ekid_cur2', 'h_cur_ek2', 'envA', 'cur@x.example', 'k2', now() - interval '3 minutes')`)
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, created_at)
		VALUES ('pkid_cur1', 'h_cur_pk1', 'cur@x.example', now() + interval '1 day', now() - interval '2 minutes')`)

	owner := "cur@x.example"
	filter := db.KeyListFilter{OwnerEmail: &owner}

	// Page 1: limit=2, no cursor -> the two most-recent rows.
	page1, next, err := db.ListKeys(ctx, pool, filter, 2, "")
	if err != nil {
		t.Fatalf("ListKeys page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len=%d; want 2", len(page1))
	}
	if next == "" {
		t.Fatal("page1 nextCursor is empty; want a cursor for the remaining row")
	}
	// created_at DESC: ekid_cur1 (1m ago) then pkid_cur1 (2m ago).
	if page1[0].KeyID != "ekid_cur1" {
		t.Errorf("page1[0].KeyID=%q; want ekid_cur1", page1[0].KeyID)
	}
	if page1[1].KeyID != "pkid_cur1" {
		t.Errorf("page1[1].KeyID=%q; want pkid_cur1", page1[1].KeyID)
	}

	// Page 2: use cursor -> the remaining row.
	page2, next2, err := db.ListKeys(ctx, pool, filter, 2, next)
	if err != nil {
		t.Fatalf("ListKeys page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("page2 len=%d; want 1", len(page2))
	}
	if next2 != "" {
		t.Errorf("page2 nextCursor=%q; want \"\" (exhausted)", next2)
	}
	if page2[0].KeyID != "ekid_cur2" {
		t.Errorf("page2[0].KeyID=%q; want ekid_cur2", page2[0].KeyID)
	}

	// No overlap: page1 IDs and page2 IDs are disjoint.
	seen := map[string]bool{}
	for _, k := range page1 {
		seen[k.KeyID] = true
	}
	for _, k := range page2 {
		if seen[k.KeyID] {
			t.Errorf("key %q appears on both pages (overlap)", k.KeyID)
		}
	}
}

func TestListKeys_FiltersByEnvironment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Two ek_ rows for the same owner in different environments, plus one pk_.
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name, created_at)
		VALUES
		    ('ekid_envf1', 'h_ef1', 'env1', 'envf@x.example', 'k-e1', now() - interval '1 minutes'),
		    ('ekid_envf2', 'h_ef2', 'env2', 'envf@x.example', 'k-e2', now() - interval '2 minutes')`)
	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, created_at)
		VALUES ('pkid_envf1', 'h_pf1', 'envf@x.example', now() + interval '1 day', now() - interval '3 minutes')`)

	owner := "envf@x.example"
	// environment=env1 -> only the env1 ek_ row; env2 ek_ and pk_ (NULL environment) are excluded.
	got, next, err := db.ListKeys(ctx, pool, db.KeyListFilter{OwnerEmail: &owner, Environment: "env1"}, 100, "")
	if err != nil {
		t.Fatalf("ListKeys env1: %v", err)
	}
	if next != "" {
		t.Errorf("nextCursor=%q; want \"\"", next)
	}
	if len(got) != 1 {
		t.Fatalf("env1 len=%d; want 1 (only ekid_envf1)", len(got))
	}
	if got[0].KeyID != "ekid_envf1" {
		t.Errorf("got[0].KeyID=%q; want ekid_envf1", got[0].KeyID)
	}
	if got[0].Environment == nil || *got[0].Environment != "env1" {
		t.Errorf("got[0].Environment=%v; want env1", got[0].Environment)
	}
}

// TestListKeys_DerivesExpiredStatus pins the fix for the "ach keys list lies"
// bug: nothing ever writes status='expired' to personal_keys (expiry is only
// enforced by the PkCheckAndExtend auth predicate), so a dead pk_ used to list
// as active. ListKeys must derive the status — both for the past-expires_at
// case and for the created_at + 90d hard cap — and expose the effective
// expires_at. environment_keys are perpetual: ExpiresAt stays nil.
func TestListKeys_DerivesExpiredStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustExec(t, ctx, pool, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at, created_at, status)
		VALUES
		    ('pkid_live', 'h_live', 'exp@x.example', now() + interval '1 day',   now() - interval '1 hour',  'active'),
		    ('pkid_gone', 'h_gone', 'exp@x.example', now() - interval '1 hour',  now() - interval '8 days',  'active'),
		    ('pkid_cap',  'h_cap',  'exp@x.example', now() + interval '1 day',   now() - interval '91 days', 'active')`)
	mustExec(t, ctx, pool, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name)
		VALUES ('ekid_perp', 'h_perp', 'envA', 'exp@x.example', 'perp')`)

	owner := "exp@x.example"
	all, _, err := db.ListKeys(ctx, pool, db.KeyListFilter{OwnerEmail: &owner}, 100, "")
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	byID := map[string]db.KeyListItem{}
	for _, k := range all {
		byID[k.KeyID] = k
	}
	for id, want := range map[string]string{
		"pkid_live": "active",
		"pkid_gone": "expired", // expires_at in the past
		"pkid_cap":  "expired", // past created_at + 90 days
		"ekid_perp": "active",
	} {
		if got := byID[id].Status; got != want {
			t.Errorf("%s status=%q; want %q", id, got, want)
		}
	}
	// ?status=expired must no longer be a dead filter, and ?status=active must
	// hide the two dead pk_ rows.
	expired, _, err := db.ListKeys(ctx, pool, db.KeyListFilter{OwnerEmail: &owner, Status: "expired"}, 100, "")
	if err != nil {
		t.Fatalf("ListKeys status=expired: %v", err)
	}
	if len(expired) != 2 {
		t.Errorf("status=expired len=%d; want 2, got %+v", len(expired), expired)
	}
	active, _, err := db.ListKeys(ctx, pool, db.KeyListFilter{OwnerEmail: &owner, Status: "active"}, 100, "")
	if err != nil {
		t.Fatalf("ListKeys status=active: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("status=active len=%d; want 2 (pkid_live + ekid_perp), got %+v", len(active), active)
	}
}
