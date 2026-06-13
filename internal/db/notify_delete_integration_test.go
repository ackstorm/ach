//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration coverage for the *Tx delete variants routed through
// WithTxNotify (#3): a marketplace/skill row removal MUST emit the same
// ach_*_changed NOTIFY that the upsert path emits, so the forwarder and
// other LISTENers invalidate their caches on delete (previously the bare
// pool.Exec deletes were silent).

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/ackstorm/ach/internal/db"
)

// TestDeleteMarketplacePluginTx_EmitsNotify mirrors the bipcache NOTIFY
// pattern: subscribe, seed via the upsert path, then assert a NOTIFY
// arrives when the new tx-form delete runs through WithTxNotify.
func TestDeleteMarketplacePluginTx_EmitsNotify(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	// Seed a row so the delete has something to remove.
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.UpsertMarketplacePlugin(ctx, pool, db.MarketplacePlugin{
		MarketplaceName:       "mkt",
		Name:                  "pluginA",
		StorageLocation:       "/var/cache/ach/marketplace/mkt/pluginA.tar.gz",
		UpstreamRev:           "sha256:seed",
		LastSuccessfulRefresh: now,
		NextRefreshAt:         now.Add(time.Hour),
		MaxStalenessSeconds:   3600,
	}))

	got := make(chan string, 1)
	lis := db.NewListener(pool, logr.Discard())
	lis.Subscribe("ach_marketplace_plugins_changed", func(payload string) { got <- payload })
	go func() { _ = lis.Run(ctx) }()
	time.Sleep(200 * time.Millisecond)

	require.NoError(t, db.WithTxNotify(ctx, pool, "ach_marketplace_plugins_changed", "mkt/pluginA",
		func(tx pgx.Tx) error { return db.DeleteMarketplacePluginTx(ctx, tx, "mkt", "pluginA") }))

	select {
	case payload := <-got:
		require.Equal(t, "mkt/pluginA", payload)
	case <-time.After(10 * time.Second):
		t.Fatal("no NOTIFY received for delete via WithTxNotify")
	}
}
