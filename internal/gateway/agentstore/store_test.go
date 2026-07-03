//go:build integration

// SPDX-License-Identifier: Apache-2.0

package agentstore_test

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

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/gateway/agentstore"
)

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
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("docker required for integration tests: %v", err)
	}
	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	migrationsPath, err := filepath.Abs("../../../db/migrations")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(connStr, migrationsPath))
	pool, err := db.Open(ctx, connStr)
	require.NoError(t, err)
	cleanup := func() {
		pool.Close()
		_ = pgC.Terminate(context.Background())
	}
	return pool, cleanup
}

func TestAgentstore_RefreshAndUpstream(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	require.NoError(t, db.UpsertAgent(ctx, pool, db.AgentRow{
		Namespace: "ach-system", Name: "gh", ServiceName: "achagent-gh", ServicePort: 8080,
		HasWebhook: true, ResourceVersion: "1",
	}))
	require.NoError(t, db.UpsertAgent(ctx, pool, db.AgentRow{
		Namespace: "ach-system", Name: "cronjob", ResourceVersion: "1", // no webhook, no service
	}))

	s := agentstore.New(pool, logr.Discard())
	require.NoError(t, s.Refresh(ctx))

	got, ok := s.Upstream("ach-system", "gh")
	require.True(t, ok)
	require.Equal(t, "http://achagent-gh.ach-system.svc.cluster.local:8080", got)

	_, ok = s.Upstream("ach-system", "cronjob")
	require.False(t, ok, "cron-only agent must not be routable")
}

func TestAgentstore_NotifyDrivenRefresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	s := agentstore.New(pool, logr.Discard())
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go func() { _ = s.Run(runCtx) }()

	time.Sleep(200 * time.Millisecond) // let initial refresh + LISTEN settle
	_, ok := s.Upstream("ach-system", "later")
	require.False(t, ok)

	require.NoError(t, db.UpsertAgent(ctx, pool, db.AgentRow{
		Namespace: "ach-system", Name: "later", ServiceName: "achagent-later", ServicePort: 8080,
		HasWebhook: true, ResourceVersion: "1",
	}))
	require.NoError(t, db.Emit(ctx, pool, db.AgentsChannel, "ach-system/later"))

	require.Eventually(t, func() bool {
		_, ok := s.Upstream("ach-system", "later")
		return ok
	}, 10*time.Second, 50*time.Millisecond, "Upstream should observe NOTIFY-driven insert")
}
