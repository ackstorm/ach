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

	// A webhook agent (routable) and a cron-only agent (not routable).
	if err := db.UpsertAgent(ctx, pool, db.AgentRow{
		Namespace: "ach-system", Name: "gh", ProfileRef: "prof",
		ServiceName: "achagent-gh", ServicePort: 8080, HasWebhook: true, Ready: true,
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

	got, err := db.ListWebhookAgents(ctx, pool)
	if err != nil {
		t.Fatalf("ListWebhookAgents: %v", err)
	}
	if len(got) != 1 || got[0].Name != "gh" || got[0].ServiceName != "achagent-gh" || got[0].ServicePort != 8080 {
		t.Fatalf("ListWebhookAgents = %+v, want only gh", got)
	}

	// Upsert is idempotent / updates in place.
	if err := db.UpsertAgent(ctx, pool, db.AgentRow{
		Namespace: "ach-system", Name: "gh", ServiceName: "achagent-gh", ServicePort: 8080,
		HasWebhook: true, Ready: false, ResourceVersion: "2",
	}); err != nil {
		t.Fatalf("re-upsert gh: %v", err)
	}
	got, _ = db.ListWebhookAgents(ctx, pool)
	if len(got) != 1 {
		t.Fatalf("after re-upsert want 1 webhook agent, got %d", len(got))
	}

	// Delete removes it from the route set.
	if err := db.DeleteAgent(ctx, pool, "ach-system", "gh"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	got, _ = db.ListWebhookAgents(ctx, pool)
	if len(got) != 0 {
		t.Fatalf("after delete want 0 webhook agents, got %+v", got)
	}
}
