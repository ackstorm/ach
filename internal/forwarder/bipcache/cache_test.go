//go:build integration

// SPDX-License-Identifier: Apache-2.0

package bipcache_test

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
	"github.com/ackstorm/ach/internal/forwarder/bipcache"
)

const testNS = "ach-system"

// setupPostgres boots a fresh postgres:16-alpine via testcontainers-go,
// applies every migration in db/migrations (currently through 000007 so
// backend_identity_policies exists), and returns a pgxpool. Mirrors
// internal/db/phase2_helpers_test.go setupPostgresForPhase2 — duplicated
// here because cross-package helpers in _test files don't link.
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

func upsertBIP(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, kind, target string, forwardJWT bool) {
	t.Helper()
	err := db.UpsertBIP(ctx, pool, db.BIPRow{
		Namespace:          testNS,
		Name:               name,
		TargetKind:         kind,
		TargetName:         target,
		ForwardIdentityJWT: forwardJWT,
		ObservedGeneration: 1,
		ResourceVersion:    "1",
	})
	require.NoError(t, err)
}

// TestResolve_AlphaLastWinnerOptIn — three BIPs target MCPServer/foo with
// names a/b/c, all forwardJWT=true; resolve returns row "c".
func TestResolve_AlphaLastWinnerOptIn(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	upsertBIP(t, ctx, pool, "a", "MCPServer", "foo", true)
	upsertBIP(t, ctx, pool, "b", "MCPServer", "foo", true)
	upsertBIP(t, ctx, pool, "c", "MCPServer", "foo", true)

	c := bipcache.New(pool, testNS, logr.Discard())
	require.NoError(t, c.Refresh(ctx))

	got := c.Resolve("MCPServer", "foo")
	require.NotNil(t, got)
	require.Equal(t, "c", got.Name)
}

// TestResolve_SingleOptOut — B4 fixture: one BIP, forwardJWT=false →
// Resolve returns nil (explicit opt-out at alpha-LAST).
func TestResolve_SingleOptOut(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	upsertBIP(t, ctx, pool, "only", "MCPServer", "foo", false)

	c := bipcache.New(pool, testNS, logr.Discard())
	require.NoError(t, c.Refresh(ctx))

	require.Nil(t, c.Resolve("MCPServer", "foo"))
}

// TestResolve_OptInThenOptOut — B6 fixture: {a:opt-in, b:opt-out} → the
// alpha-LAST is b (opt-out), so Resolve returns nil.
func TestResolve_OptInThenOptOut(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	upsertBIP(t, ctx, pool, "a", "MCPServer", "foo", true)
	upsertBIP(t, ctx, pool, "b", "MCPServer", "foo", false)

	c := bipcache.New(pool, testNS, logr.Discard())
	require.NoError(t, c.Refresh(ctx))

	require.Nil(t, c.Resolve("MCPServer", "foo"))
}

// TestResolve_NoMatches — empty set returns nil.
func TestResolve_NoMatches(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	c := bipcache.New(pool, testNS, logr.Discard())
	require.NoError(t, c.Refresh(ctx))

	require.Nil(t, c.Resolve("MCPServer", "nope"))
}

// TestNotifyInvalidates — Run subscribes to the NOTIFY channel and
// refreshes when a row is inserted + signal emitted.
func TestNotifyInvalidates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	c := bipcache.New(pool, testNS, logr.Discard())
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go func() { _ = c.Run(runCtx) }()

	// Allow the initial refresh + LISTEN to settle. We don't poll on
	// internal state — instead we use require.Eventually below to drive
	// the assertion timing.
	time.Sleep(200 * time.Millisecond)
	require.Nil(t, c.Resolve("MCPServer", "later"))

	upsertBIP(t, ctx, pool, "x", "MCPServer", "later", true)
	require.NoError(t, db.Emit(ctx, pool, bipcache.Channel, "x"))

	require.Eventually(t, func() bool {
		return c.Resolve("MCPServer", "later") != nil
	}, 10*time.Second, 50*time.Millisecond, "Resolve should observe NOTIFY-driven row insert")
}
