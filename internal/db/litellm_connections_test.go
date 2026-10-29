//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db/litellm_connections.go.
//
// Covers the resurrection invariant shared by every origin-gated projection
// table (incident 2026-06-04): a live upsert must clear a stale
// deletion_timestamp left by a prior soft-delete, else an IS-NULL-filtered
// read hides the resurrected row forever.

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// TestUpsertLiteLLMConnection_ClearsStaleDeletionTimestamp proves a LIVE
// reconcile un-sets a drain marker left by a prior soft-delete, so the
// IS-NULL-filtered read stops hiding the resurrected row.
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
	// Soft-deleted: deletion_timestamp must be set.
	pre, err := db.GetDefaultLiteLLMConnection(ctx, pool, "ns")
	if err != nil {
		t.Fatalf("GetDefaultLiteLLMConnection after soft-delete: %v", err)
	}
	if pre == nil || pre.DeletionTimestamp == nil {
		t.Fatalf("expected deletion_timestamp set after soft-delete, got %+v", pre)
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
}
