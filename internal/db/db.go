// SPDX-License-Identifier: Apache-2.0

// Package db is the thin Postgres bootstrap surface for ACH Hub binaries.
//
// Per CONTEXT D-06, application query paths use pgx/v5 natively (no ORM, no
// database/sql wrapper). Migrations are driven by golang-migrate/v4 with the
// pgx/v5 driver; per D-07 the migrate binary runs in a dedicated init container
// (Plan 08) — application code never executes DDL on its own.
//
// The package surface is intentionally narrow:
//
//   - Open(ctx, url) returns a *pgxpool.Pool sized for the Operator (10 conns)
//     after rejecting an empty url. Phase 3's Platform API may construct its
//     own larger pool by calling pgxpool.ParseConfig directly.
//
//   - Migrate(url, migrationsPath) applies db/migrations/*.sql against the
//     deployment-supplied URL. ErrNoChange is collapsed to nil because a
//     successful no-op migration is not a failure (D-08 applies only to
//     migration failure, not to "already applied").
//
// Schema source of truth: Hub §16 (four tables) + §16.1 (no raw bearer values,
// HMAC-SHA-256 with server-side pepper). This package opens a connection
// pool; Phase 3 will write the first pk_/ek_ rows through it.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	// Register the pgx/v5 database driver under migrate's "pgx5://" URL scheme.
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	// Register the file:// source driver for reading migrations from disk.
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrEmptyURL is returned by Open when the connection URL is empty. The
// Operator main (Plan 06) and Plan 08's migration init container surface this
// as a fast-fail startup error per D-08 — there is no best-effort/silent-skip
// behavior on a missing ACH_DB_URL.
var ErrEmptyURL = errors.New("db: ACH_DB_URL is empty")

// defaultMaxConns is the per-replica pgxpool size used by the Operator. Phase 3
// may override for the Platform API by calling pgxpool.ParseConfig directly
// rather than going through Open().
const defaultMaxConns = 10

// Open constructs a pgxpool.Pool against url. An empty url returns ErrEmptyURL.
// The returned pool has MaxConns set to defaultMaxConns; the caller is
// responsible for invoking Pool.Close() on shutdown.
//
// The function does not log the url under any circumstance — connection strings
// frequently contain credentials and per the §16.1 plaintext-non-persistence
// rule must not flow into structured logs (see T-03-05 in the threat register).
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, ErrEmptyURL
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse ACH_DB_URL: %w", err)
	}
	cfg.MaxConns = defaultMaxConns
	return pgxpool.NewWithConfig(ctx, cfg)
}

// Migrate applies the file:// migrations under migrationsPath against the
// deployment-supplied database url. It is the entry point of Plan 08's
// migration init container; application code (operator, platform-api,
// forwarder, content-service) never invokes Migrate. ErrNoChange is collapsed
// to nil because re-applying a fully-applied migration set is the expected
// steady-state.
//
// The pgx/v5 migration driver registers itself with golang-migrate under the
// "pgx5://" URL scheme (see github.com/golang-migrate/migrate/v4/database/pgx/v5).
// Deployments configure ACH_DB_URL with the standard "postgres://" or
// "postgresql://" scheme that the pgxpool driver expects; this function
// transparently rewrites either to "pgx5://" so the migrate library can find
// the registered driver. Other schemes pass through unchanged.
func Migrate(url string, migrationsPath string) error {
	if url == "" {
		return ErrEmptyURL
	}
	migrateURL := url
	switch {
	case strings.HasPrefix(url, "postgres://"):
		migrateURL = "pgx5://" + strings.TrimPrefix(url, "postgres://")
	case strings.HasPrefix(url, "postgresql://"):
		migrateURL = "pgx5://" + strings.TrimPrefix(url, "postgresql://")
	}
	m, err := migrate.New("file://"+migrationsPath, migrateURL)
	if err != nil {
		return fmt.Errorf("db: construct migrator: %w", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: apply migrations: %w", err)
	}
	return nil
}
