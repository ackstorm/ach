//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestMigration000031_DropsPromptData migrates to version 30, seeds Prompt
// rows, then applies 000031 and asserts nothing of the old kind survives.
func TestMigration000031_DropsPromptData(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker; -short specified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pgC, err := tcpostgres.Run(ctx, "postgres:16-alpine",
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
		t.Skipf("docker required for integration tests: %v", err)
	}
	defer func() { _ = pgC.Terminate(context.Background()) }()

	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	migrationsPath, err := filepath.Abs("../../db/migrations")
	if err != nil {
		t.Fatalf("abs migrationsPath: %v", err)
	}
	m, err := migrate.New("file://"+migrationsPath, "pgx5://"+strings.TrimPrefix(connStr, "postgres://"))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Migrate(30); err != nil {
		t.Fatalf("migrate to 30: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	for _, q := range []string{
		`INSERT INTO prompts (namespace, name, content_type, locked) VALUES ('ns', 'p', 'text/plain', TRUE)`,
		`INSERT INTO environments (namespace, name, resource_version, locked, context_prompts, spec)
		   VALUES ('ns', 'e', '1', TRUE, '{p}', '{"context":{"prompts":["p"]}}')`,
		`INSERT INTO external_refs (kind, name, storage_location, max_staleness_seconds, locked)
		   VALUES ('prompt', 'p', '/x', 60, TRUE)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}

	if err := m.Migrate(31); err != nil {
		t.Fatalf("migrate to 31: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM instructions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("instructions rows = %d, err %v; want 0", n, err)
	}
	var ctxInstr []string
	var hasPrompts bool
	if err := pool.QueryRow(ctx,
		`SELECT context_instructions, spec->'context' ? 'prompts' FROM environments WHERE name = 'e'`,
	).Scan(&ctxInstr, &hasPrompts); err != nil {
		t.Fatalf("read environment: %v", err)
	}
	if len(ctxInstr) != 0 {
		t.Errorf("context_instructions = %v, want empty", ctxInstr)
	}
	if hasPrompts {
		t.Error("spec.context still has a prompts key")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM external_refs WHERE kind = 'prompt'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("prompt external_refs rows = %d, err %v; want 0", n, err)
	}
}
