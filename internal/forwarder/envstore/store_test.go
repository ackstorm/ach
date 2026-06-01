//go:build integration

// SPDX-License-Identifier: Apache-2.0

package envstore_test

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
	"github.com/ackstorm/ach/internal/forwarder/envstore"
)

const testNS = "ach-system"

// setupPostgres mirrors internal/db/phase2_helpers_test.go — duplicated
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

func upsertEnv(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, mcps, a2as, teams []string) {
	t.Helper()
	// The environments text[] columns are NOT NULL; UpsertEnvironment binds
	// every value explicitly, so a nil Go slice maps to SQL NULL and violates
	// the constraint (the column DEFAULT '{}' applies only when omitted). The
	// operator always sends non-nil slices from the CR spec — mirror that.
	nz := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	require.NoError(t, db.UpsertEnvironment(ctx, pool, db.EnvironmentRow{
		Namespace:         testNS,
		Name:              name,
		AuthorizedTeams:   nz(teams),
		ContextPrompts:    []string{},
		ContextPlugins:    []string{},
		ContextArtifacts:  []string{},
		RuntimeModels:     []string{},
		RuntimeMCPServers: nz(mcps),
		RuntimeA2AAgents:  nz(a2as),
		ResourceVersion:   "1",
	}))
}

func TestGetAndList(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	upsertEnv(t, ctx, pool, "alpha", []string{"a"}, nil, []string{"team-a"})
	upsertEnv(t, ctx, pool, "beta", []string{"b"}, nil, []string{"team-b"})

	s := envstore.New(pool, testNS, logr.Discard())
	require.NoError(t, s.Refresh(ctx))

	got, ok := s.Get("alpha")
	require.True(t, ok)
	require.Equal(t, "alpha", got.Name)
	require.Equal(t, []string{"a"}, got.RuntimeMCPServers)

	_, ok = s.Get("missing")
	require.False(t, ok)

	all := s.List()
	require.Len(t, all, 2)
}

func TestNotifyInvalidates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	s := envstore.New(pool, testNS, logr.Discard())
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go func() { _ = s.Run(runCtx) }()

	time.Sleep(200 * time.Millisecond)
	_, ok := s.Get("late")
	require.False(t, ok)

	upsertEnv(t, ctx, pool, "late", []string{"server-x"}, nil, nil)
	require.NoError(t, db.Emit(ctx, pool, envstore.Channel, "late"))

	require.Eventually(t, func() bool {
		_, ok := s.Get("late")
		return ok
	}, 10*time.Second, 50*time.Millisecond, "Get should observe NOTIFY-driven row insert")
}
