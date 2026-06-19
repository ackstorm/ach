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
