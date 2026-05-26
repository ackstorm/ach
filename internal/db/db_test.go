//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for internal/db. Build-tagged 'integration' so the
// envtest-based unit test pass (`make test`) does not require a Docker
// daemon. Run with: `make test-integration` or
// `go test -tags=integration ./internal/db/... -count=1`.

package db_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ackstorm/ach/internal/db"
)

// TestOpenAndMigrate boots a postgres:16-alpine container, applies the Phase 1
// migration via db.Migrate, opens a pgxpool via db.Open, and verifies the
// schema-level invariants Phase 1 commits to:
//
//   - All four Hub §16 tables exist after migration (DB-01).
//   - personal_keys / environment_keys reject key_id values lacking the
//     pkid_/ekid_ prefix with SQLSTATE 23514 (check_violation) — DB-02.
//   - personal_keys / environment_keys reject duplicate credential_hash with
//     SQLSTATE 23505 (unique_violation) — DB-03.
func TestOpenAndMigrate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker; -short specified")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

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
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := pgC.Terminate(context.Background()); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	migrationsPath, err := filepath.Abs("../../db/migrations")
	if err != nil {
		t.Fatalf("abs migrationsPath: %v", err)
	}

	if err := db.Migrate(connStr, migrationsPath); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	// Re-applying the migration is a no-op and MUST NOT error (ErrNoChange
	// collapsed to nil per the package contract).
	if err := db.Migrate(connStr, migrationsPath); err != nil {
		t.Fatalf("db.Migrate (second call should be a no-op): %v", err)
	}

	pool, err := db.Open(ctx, connStr)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool.Ping: %v", err)
	}

	// --- Acceptance: all four §16 tables present.
	rows, err := pool.Query(ctx, `
		SELECT tablename
		FROM pg_catalog.pg_tables
		WHERE schemaname = 'public'
		  AND tablename IN ('personal_keys','environment_keys','external_refs','marketplace_plugins')
		ORDER BY tablename
	`)
	if err != nil {
		t.Fatalf("query pg_tables: %v", err)
	}
	got := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan tablename: %v", err)
		}
		got[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	for _, expected := range []string{
		"personal_keys", "environment_keys", "external_refs", "marketplace_plugins",
	} {
		if !got[expected] {
			t.Errorf("missing table %q after migration", expected)
		}
	}

	// --- Acceptance: pkid_ CHECK constraint rejects bad prefix (SQLSTATE 23514).
	_, err = pool.Exec(ctx, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at)
		VALUES ('NOTAPKID_xxx', 'h_pk_bad_prefix', 'a@b.example', now() + interval '1 hour')
	`)
	assertPgErrorCode(t, err, "23514", "personal_keys key_id prefix CHECK")

	// --- Acceptance: ekid_ CHECK constraint rejects bad prefix (SQLSTATE 23514).
	_, err = pool.Exec(ctx, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name)
		VALUES ('NOTAEKID_xxx', 'h_ek_bad_prefix', 'env1', 'a@b.example', 'a-key')
	`)
	assertPgErrorCode(t, err, "23514", "environment_keys key_id prefix CHECK")

	// --- Acceptance: valid prefix inserts succeed.
	if _, err := pool.Exec(ctx, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at)
		VALUES ('pkid_01', 'h_pk_uniq_1', 'a@b.example', now() + interval '1 hour')
	`); err != nil {
		t.Fatalf("insert valid personal_keys: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name)
		VALUES ('ekid_01', 'h_ek_uniq_1', 'env1', 'a@b.example', 'a-key')
	`); err != nil {
		t.Fatalf("insert valid environment_keys: %v", err)
	}

	// --- Acceptance: UNIQUE(credential_hash) on personal_keys (SQLSTATE 23505).
	_, err = pool.Exec(ctx, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, expires_at)
		VALUES ('pkid_02', 'h_pk_uniq_1', 'c@d.example', now() + interval '1 hour')
	`)
	assertPgErrorCode(t, err, "23505", "personal_keys credential_hash UNIQUE")

	// --- Acceptance: UNIQUE(credential_hash) on environment_keys (SQLSTATE 23505).
	_, err = pool.Exec(ctx, `
		INSERT INTO environment_keys (key_id, credential_hash, environment, owner_email, name)
		VALUES ('ekid_02', 'h_ek_uniq_1', 'env1', 'c@d.example', 'b-key')
	`)
	assertPgErrorCode(t, err, "23505", "environment_keys credential_hash UNIQUE")

	// --- Acceptance: status enum CHECK rejects unknown values (SQLSTATE 23514).
	_, err = pool.Exec(ctx, `
		INSERT INTO personal_keys (key_id, credential_hash, owner_email, status, expires_at)
		VALUES ('pkid_03', 'h_pk_status_bad', 'e@f.example', 'NOPE', now() + interval '1 hour')
	`)
	assertPgErrorCode(t, err, "23514", "personal_keys status enum CHECK")
}

// TestOpenRejectsEmptyURL verifies Open returns ErrEmptyURL on the empty
// string (D-08 fail-fast contract).
func TestOpenRejectsEmptyURL(t *testing.T) {
	_, err := db.Open(context.Background(), "")
	if !errors.Is(err, db.ErrEmptyURL) {
		t.Fatalf("Open(\"\") = %v, want ErrEmptyURL", err)
	}
}

// TestMigrateRejectsEmptyURL likewise.
func TestMigrateRejectsEmptyURL(t *testing.T) {
	err := db.Migrate("", "anything")
	if !errors.Is(err, db.ErrEmptyURL) {
		t.Fatalf("Migrate(\"\", _) = %v, want ErrEmptyURL", err)
	}
}

// assertPgErrorCode unwraps to a *pgconn.PgError and asserts its SQLSTATE Code
// matches the expected value. The label is included in the failure message to
// disambiguate among multiple constraint checks in a single test.
func assertPgErrorCode(t *testing.T, err error, code, label string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected SQLSTATE %s, got nil error", label, code)
		return
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Errorf("%s: expected *pgconn.PgError SQLSTATE %s, got %T: %v", label, code, err, err)
		return
	}
	if pgErr.Code != code {
		t.Errorf("%s: expected SQLSTATE %s, got %s (msg=%s)", label, code, pgErr.Code, pgErr.Message)
	}
}
