//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

func TestConsentBIP_AlphaFirstAndFields(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	ns := "ns-" + t.Name()
	mk := func(name string, jwt bool, broker, audience string) db.BIPRow {
		return db.BIPRow{
			Namespace: ns, Name: name, TargetKind: "MCPServer", TargetName: "svc",
			ForwardIdentityJWT: jwt, ConsentBroker: broker, ConsentAudience: audience,
			ResourceVersion: "1",
		}
	}
	if err := db.UpsertBIP(ctx, pool, mk("b-on", true, "https://broker", "svc-name")); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertBIP(ctx, pool, mk("a-off", false, "https://x", "y")); err != nil {
		t.Fatal(err)
	}

	got, err := db.ConsentBIP(ctx, pool, ns, "svc")
	if err != nil || got != nil {
		t.Fatalf("alpha-first row opts out: got %+v err %v, want nil", got, err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SoftDeleteBIPTx(ctx, tx, ns, "a-off"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	got, err = db.ConsentBIP(ctx, pool, ns, "svc")
	if err != nil || got == nil || got.ConsentBroker != "https://broker" || got.ConsentAudience != "svc-name" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if got, err = db.ConsentBIP(ctx, pool, ns, "other"); err != nil || got != nil {
		t.Fatalf("unknown target: got %+v err %v", got, err)
	}
}
