//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/litellm_connections.go.
//
// Covers the resurrection invariant shared by every origin-gated projection
// table (incident 2026-06-04): a live upsert must clear a stale
// deletion_timestamp left by a prior soft-delete, else the IS-NULL-filtered
// read (ListLitellmConnections) hides the resurrected row forever.

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertLiteLLMConnection_ClearsStaleDeletionTimestamp proves a LIVE
// reconcile un-sets a drain marker left by a prior soft-delete. The
// IS-NULL-filtered read is ListLitellmConnections (admin inventory): a
// resurrected connection must reappear there after the live upsert.
func TestUpsertLiteLLMConnection_ClearsStaleDeletionTimestamp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	row := db.LiteLLMConnectionRow{
		Namespace:                "ns",
		Name:                     "default",
		Endpoint:                 "http://litellm.ns.svc:4000",
		MasterKeySecretNamespace: "ns",
		MasterKeySecretName:      "litellm-master",
		MasterKeySecretKey:       "key",
		ResourceVersion:          "1",
	}
	if err := db.UpsertLiteLLMConnection(ctx, pool, row); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}
	if err := db.SoftDeleteLiteLLMConnection(ctx, pool, "ns", "default"); err != nil {
		t.Fatalf("SoftDeleteLiteLLMConnection: %v", err)
	}
	// Soft-deleted: the IS-NULL-filtered inventory read must NOT list it.
	pre, err := db.ListLitellmConnections(ctx, pool, "ns")
	if err != nil {
		t.Fatalf("ListLitellmConnections after soft-delete: %v", err)
	}
	for _, c := range pre {
		if c.Name == "default" {
			t.Fatal("ListLitellmConnections returned soft-deleted row (IS NULL filter broken)")
		}
	}
	// Live reconcile of the recreated CR upserts again.
	row.ResourceVersion = "2"
	if err := db.UpsertLiteLLMConnection(ctx, pool, row); err != nil {
		t.Fatalf("live upsert: %v", err)
	}

	got, err := db.GetDefaultLiteLLMConnection(ctx, pool, "ns")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.DeletionTimestamp != nil {
		t.Fatalf("expected deletion_timestamp cleared on live upsert, got %+v", got)
	}
	// The IS-NULL-filtered inventory read must list the resurrected row.
	post, err := db.ListLitellmConnections(ctx, pool, "ns")
	if err != nil {
		t.Fatalf("ListLitellmConnections after live upsert: %v", err)
	}
	var found bool
	for _, c := range post {
		if c.Name == "default" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("resurrected litellm_connection missing from ListLitellmConnections after live upsert")
	}
}
