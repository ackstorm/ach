//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Shared Postgres + migration spin-up helper for the Phase 2 db helper tests
// (external_refs_test.go, marketplace_plugins_test.go, litellm_users_test.go).
//
// The Phase 1 db_test.go file's TestOpenAndMigrate has its own inline
// container spin-up (no extracted helper); the Phase 2 tests need many
// fresh-DB spawns so an extracted helper keeps each test self-contained
// without polluting the Phase 1 file.
//
// One container per test: every t.Run launches its own Postgres so a row
// inserted by test A is invisible to test B. The trade-off (~3-5s per test
// container spin-up) is acceptable for the small Phase 2 test set and
// keeps assertions independent of test execution order.

package db_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ackstorm/ach/internal/db"
)

// setupPostgresForPhase2 boots a fresh postgres:16-alpine via testcontainers-go,
// applies migrations 000001 (Phase 1) + 000002 (Phase 2), and returns a
// pgxpool against the container. The cleanup closes the pool and terminates
// the container.
//
// If Docker is unavailable on the host, the test SKIPs (matches Phase 1
// db_test.go's policy of opt-in Docker via `make test-integration`).
func setupPostgresForPhase2(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
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

	migrationsPath, err := filepath.Abs("../../db/migrations")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("abs migrationsPath: %v", err)
	}

	// Apply 000001 + 000002.
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
