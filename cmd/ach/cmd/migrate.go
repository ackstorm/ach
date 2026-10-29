// SPDX-License-Identifier: Apache-2.0

// `ach migrate` applies Postgres schema migrations against ACH_DB_URL
// using the file:// migrations bundled under ACH_MIGRATIONS_PATH
// (default /db/migrations baked into the operator image). Designed to
// run as the Plan 08 init container in the Operator + Content Service
// Pod (D-07). Refuses to start with empty/invalid ACH_DB_URL (D-08
// invariant) — non-zero exit leaves the Pod in Init:Error rather than
// silently skipping. Body lifted from ach-old/cmd/migrate/main.go;
// adapted to a cobra RunE for the single-binary layout.

package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/db"
)

const defaultMigrationsPath = "/db/migrations"

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply Postgres schema migrations from db/migrations against ACH_DB_URL",
	Long: `Apply Postgres schema migrations bundled under ACH_MIGRATIONS_PATH against
the database at ACH_DB_URL. Refuses to start with an empty ACH_DB_URL.

Environment:
  ACH_DB_URL              Postgres DSN (required)
  ACH_MIGRATIONS_PATH     Path to db/migrations directory (default: /db/migrations)
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

		url := os.Getenv("ACH_DB_URL")
		if url == "" {
			return fmt.Errorf("ACH_DB_URL is required for migrate")
		}
		path := os.Getenv("ACH_MIGRATIONS_PATH")
		if path == "" {
			path = defaultMigrationsPath
		}

		// Do NOT log the url — connection strings may carry credentials
		// (§16.1 plaintext-non-persistence rule applies to structured logs).
		logger.Info("running migrations", "path", path)
		if err := db.Migrate(url, path); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
		logger.Info("migrations complete")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
