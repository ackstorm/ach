//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for the exchange data model (migration 000026).

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ackstorm/ach/internal/db"
)

const exOwner = "ai.run@ackstorm.com"

func exchangeRow(keyID, hash, requester string) db.EkInsertRow {
	r := requester
	return db.EkInsertRow{KeyID: keyID, CredentialHash: hash, Environment: "prod", OwnerEmail: exOwner,
		Name: "exchange:" + requester, IssuedVia: "exchange", Requester: &r}
}

func TestKeyGrants_UpsertListDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	budget := 50.0
	g := db.KeyGrantRow{Namespace: "ach", Name: "ai-run-doctor", Identity: exOwner,
		Environments: []string{"k8s-doctor"}, ServiceAccounts: []string{"agents/agent-k8s-doctor"},
		MaxBudget: &budget, BudgetDuration: "30d"}
	if err := db.UpsertKeyGrant(ctx, pool, g); err != nil {
		t.Fatal(err)
	}
	g.Environments = []string{"k8s-doctor", "dev"}
	if err := db.UpsertKeyGrant(ctx, pool, g); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertKeyGrant(ctx, pool, db.KeyGrantRow{Namespace: "ach", Name: "other", Identity: "x@ackstorm.com",
		Environments: []string{"dev"}, ServiceAccounts: []string{"agents/x"}}); err != nil {
		t.Fatal(err)
	}

	if all, err := db.ListKeyGrants(ctx, pool, "ach"); err != nil || len(all) != 2 {
		t.Fatalf("ListKeyGrants = %v, %v", all, err)
	}
	mine, err := db.ListKeyGrantsForIdentity(ctx, pool, "ach", exOwner)
	if err != nil || len(mine) != 1 {
		t.Fatalf("ListKeyGrantsForIdentity = %v, %v", mine, err)
	}
	if got := mine[0]; len(got.Environments) != 2 || got.MaxBudget == nil || *got.MaxBudget != 50 || got.BudgetDuration != "30d" {
		t.Fatalf("upsert did not replace the row: %+v", got)
	}
	if other, _ := db.ListKeyGrantsForIdentity(ctx, pool, "elsewhere", exOwner); len(other) != 0 {
		t.Fatal("namespace not honoured")
	}
	if err := db.DeleteKeyGrant(ctx, pool, "ach", "ai-run-doctor"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteKeyGrant(ctx, pool, "ach", "ai-run-doctor"); err != nil {
		t.Fatalf("deleting an absent row must be a no-op: %v", err)
	}
	if left, _ := db.ListKeyGrantsForIdentity(ctx, pool, "ach", exOwner); len(left) != 0 {
		t.Fatal("row survived delete")
	}
}

func TestExchangeKeys_OneLiveKeyPerTuple(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustInsertEk(t, pool, exchangeRow("ekid_a", "hash-a", "agents/doctor"))
	err := db.InsertEnvironmentKey(ctx, pool, exchangeRow("ekid_b", "hash-b", "agents/doctor"))
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != db.ExchangeTupleConstraint {
		t.Fatalf("second live key for one tuple: want %s violation, got %v", db.ExchangeTupleConstraint, err)
	}
	mustInsertEk(t, pool, exchangeRow("ekid_c", "hash-c", "agents/other")) // another requester, another tuple

	if _, err := db.RevokeEnvironmentKey(ctx, pool, "ekid_a"); err != nil {
		t.Fatal(err)
	}
	mustInsertEk(t, pool, exchangeRow("ekid_b", "hash-b", "agents/doctor")) // a revoked key frees its tuple

	got, err := db.FindExchangeKey(ctx, pool, exOwner, "prod", "agents/doctor")
	if err != nil || got == nil || got.KeyID != "ekid_b" || got.Requester != "agents/doctor" {
		t.Fatalf("FindExchangeKey = %+v, %v", got, err)
	}
	live, err := db.ListExchangeKeys(ctx, pool)
	if err != nil || len(live) != 2 || live[0].KeyID != "ekid_b" || live[1].KeyID != "ekid_c" {
		t.Fatalf("ListExchangeKeys = %+v, %v", live, err)
	}
}

func TestExchangeKeyResolve_OnlyActiveExchangeKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_manual", CredentialHash: "hash-m",
		Environment: "prod", OwnerEmail: exOwner, Name: "laptop"})
	mustInsertEk(t, pool, exchangeRow("ekid_ex", "hash-x", "agents/doctor"))

	if r, err := db.ExchangeKeyResolve(ctx, pool, "ekid_manual"); err != nil || r != nil {
		t.Fatalf("a manual key must never resolve as an exchange key: %+v, %v", r, err)
	}
	r, err := db.ExchangeKeyResolve(ctx, pool, "ekid_ex")
	if err != nil || r == nil || r.Requester != "agents/doctor" || r.OwnerEmail != exOwner || r.Environment != "prod" {
		t.Fatalf("ExchangeKeyResolve = %+v, %v", r, err)
	}
	if _, err := db.SuspendEnvironmentKey(ctx, pool, "ekid_ex"); err != nil {
		t.Fatal(err)
	}
	if r, _ := db.ExchangeKeyResolve(ctx, pool, "ekid_ex"); r != nil {
		t.Fatal("a suspended exchange key resolved")
	}
	if _, err := db.RevokeEnvironmentKey(ctx, pool, "ekid_ex"); err != nil {
		t.Fatal(err)
	}
	if r, _ := db.ExchangeKeyResolve(ctx, pool, "ekid_ex"); r != nil {
		t.Fatal("a revoked exchange key resolved")
	}
}

func TestUserKeyAllowance_ExcludesExchangeKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_manual", CredentialHash: "hash-m",
		Environment: "prod", OwnerEmail: exOwner, Name: "laptop"})
	mustInsertEk(t, pool, exchangeRow("ekid_ex", "hash-x", "agents/doctor"))
	used, _, err := db.UserKeyAllowance(ctx, pool, exOwner, 5)
	if err != nil || used != 1 {
		t.Fatalf("used = %d, %v; want 1 (the exchange key does not count)", used, err)
	}
}

func TestInsertEnvironmentKey_IssuedViaDefaultsAndCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	mustInsertEk(t, pool, db.EkInsertRow{KeyID: "ekid_manual", CredentialHash: "hash-m",
		Environment: "prod", OwnerEmail: exOwner, Name: "laptop"})
	var via string
	var requester *string
	if err := pool.QueryRow(ctx, `SELECT issued_via, requester FROM environment_keys WHERE key_id = 'ekid_manual'`).
		Scan(&via, &requester); err != nil {
		t.Fatal(err)
	}
	if via != "manual" || requester != nil {
		t.Fatalf("issued_via=%q requester=%v; want manual/NULL", via, requester)
	}
	r := "agents/x"
	if err := db.InsertEnvironmentKey(ctx, pool, db.EkInsertRow{KeyID: "ekid_bad", CredentialHash: "hash-bad",
		Environment: "prod", OwnerEmail: exOwner, Name: "bad", Requester: &r}); err == nil {
		t.Fatal("a manual key with a requester must be refused by environment_keys_requester_chk")
	}
}
