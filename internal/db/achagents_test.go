//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

func TestUpsertListDeleteAgents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// An exposed agent (routable) and a private cron-only agent (not routable).
	if err := db.UpsertAgent(ctx, pool, db.AgentRow{
		Namespace: "ach-system", Name: "gh", ProfileRef: "prof",
		ServiceName: "achagent-gh", ServicePort: 8080, Exposed: true, Ready: true,
		Channels:        []db.ChannelSummary{{Name: "github-review", Type: "webhook", Source: "github"}},
		ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("upsert gh: %v", err)
	}
	if err := db.UpsertAgent(ctx, pool, db.AgentRow{
		Namespace: "ach-system", Name: "nightly", ProfileRef: "prof",
		Channels:        []db.ChannelSummary{{Name: "n", Type: "cron"}},
		ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("upsert nightly: %v", err)
	}
	// A service-only a2a peer: has a Service but is NOT exposed → not routable.
	if err := db.UpsertAgent(ctx, pool, db.AgentRow{
		Namespace: "ach-system", Name: "peer", ProfileRef: "prof",
		ServiceName: "achagent-peer", ServicePort: 8080, Exposed: false, Ready: true,
		Channels:        []db.ChannelSummary{{Name: "a2a-in", Type: "a2a"}},
		ResourceVersion: "1",
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

	got, err := db.ListExposedAgents(ctx, pool)
	if err != nil {
		t.Fatalf("ListExposedAgents: %v", err)
	}
	if len(got) != 1 || got[0].Name != "gh" || got[0].ServiceName != "achagent-gh" || got[0].ServicePort != 8080 {
		t.Fatalf("ListExposedAgents = %+v, want only gh", got)
	}

	// Upsert is idempotent / updates in place.
	if err := db.UpsertAgent(ctx, pool, db.AgentRow{
		Namespace: "ach-system", Name: "gh", ServiceName: "achagent-gh", ServicePort: 8080,
		Exposed: true, Ready: false, ResourceVersion: "2",
	}); err != nil {
		t.Fatalf("re-upsert gh: %v", err)
	}
	got, _ = db.ListExposedAgents(ctx, pool)
	if len(got) != 1 {
		t.Fatalf("after re-upsert want 1 exposed agent, got %d", len(got))
	}

	// Delete removes it from the route set.
	if err := db.DeleteAgent(ctx, pool, "ach-system", "gh"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	got, _ = db.ListExposedAgents(ctx, pool)
	if len(got) != 0 {
		t.Fatalf("after delete want 0 exposed agents, got %+v", got)
	}
}
