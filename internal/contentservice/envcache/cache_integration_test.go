//go:build integration

// SPDX-License-Identifier: Apache-2.0

package envcache_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ackstorm/ach/internal/contentservice/envcache"
	"github.com/ackstorm/ach/internal/db"
)

const testNS = "ach-system"

// setupPostgres mirrors internal/forwarder/envstore/store_test.go — duplicated
// because cross-package _test helpers don't link.
func setupPostgres(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test requires Docker; -short specified")
	}

	pgC, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("ach_test"),
		tcpostgres.WithUsername("ach_test"),
		tcpostgres.WithPassword("ach_test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("docker required for integration tests: postgres container failed to start: %v", err)
	}

	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("connection string: %v", err)
	}

	migrationsPath, err := filepath.Abs("../../../db/migrations")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("abs migrationsPath: %v", err)
	}

	if err := db.Migrate(connStr, migrationsPath); err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("db.Migrate: %v", err)
	}

	pool, err := db.Open(ctx, connStr)
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("db.Open: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		_ = pgC.Terminate(context.Background())
		t.Fatalf("pool.Ping: %v", err)
	}

	cleanup := func() {
		pool.Close()
		if err := pgC.Terminate(context.Background()); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	}
	return pool, cleanup
}

func upsertEnv(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) {
	t.Helper()
	require.NoError(t, db.UpsertEnvironment(ctx, pool, db.EnvironmentRow{
		Namespace:         testNS,
		Name:              name,
		AuthorizedTeams:   []string{"team-" + name},
		ContextPrompts:    []string{},
		ContextPlugins:    []string{},
		ContextArtifacts:  []string{},
		ContextSkills:     []string{},
		RuntimeModels:     []string{},
		RuntimeMCPServers: []string{},
		RuntimeA2AAgents:  []string{},
		ResourceVersion:   "1",
	}))
}

// TestNotifyInvalidates: a row upserted after Run starts becomes visible to
// Get once the NOTIFY fires, mirroring envstore.TestNotifyInvalidates.
func TestNotifyInvalidates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	c := envcache.New(pool, testNS, logr.Discard())
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go func() { _ = c.Run(runCtx) }()

	time.Sleep(200 * time.Millisecond)
	if _, ok := c.Get(testNS, "late"); ok {
		t.Fatal("Get(late) hit before the row was created")
	}

	upsertEnv(t, ctx, pool, "late")
	require.NoError(t, db.Emit(ctx, pool, envcache.Channel, "late"))

	require.Eventually(t, func() bool {
		_, ok := c.Get(testNS, "late")
		return ok
	}, 10*time.Second, 50*time.Millisecond, "Get should observe NOTIFY-driven row insert")
}

// TestDrainModeStillVisible: a soft-deleted (drain-mode) environment remains
// visible to Get — the CS-09 contract the content-service relies on.
func TestDrainModeStillVisible(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	upsertEnv(t, ctx, pool, "draining")
	require.NoError(t, db.SoftDeleteEnvironment(ctx, pool, testNS, "draining"))

	c := envcache.New(pool, testNS, logr.Discard())
	require.NoError(t, c.Refresh(ctx))

	row, ok := c.Get(testNS, "draining")
	require.True(t, ok, "drain-mode env must stay visible (CS-09)")
	require.Equal(t, []string{"team-draining"}, row.AuthorizedTeams)
}
