---
phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy
plan: 03
subsystem: db
tags: [postgres, golang-migrate, pgx, pgxpool, testcontainers-go, hub-section-16, hub-section-16.1]

# Dependency graph
requires:
  - phase: 01-01
    provides: "kubebuilder v4 scaffold, go.mod at github.com/ackstorm/ach, Makefile manifests/generate/test/fmt/vet targets, internal/ stub tree"
provides:
  - "db/migrations/000001_init.up.sql — Hub §16 four-table schema: personal_keys, environment_keys, external_refs, marketplace_plugins"
  - "DB-02 CHECK constraints at SQL layer: personal_keys.key_id LIKE 'pkid_%', environment_keys.key_id LIKE 'ekid_%'"
  - "DB-01/DB-03 UNIQUE NOT NULL on personal_keys.credential_hash and environment_keys.credential_hash"
  - "DB-04 (no raw bearer columns) enforced by construction — no `*_plaintext`, `bearer_*`, `secret_value` columns exist"
  - "db/migrations/000001_init.down.sql — reverse migration (DROP TABLE all four in reverse declaration order)"
  - "internal/db.Open(ctx, url) — pgxpool.Pool constructor; MaxConns=10; ErrEmptyURL on empty url (D-08)"
  - "internal/db.Migrate(url, migrationsPath) — golang-migrate/v4 with pgx/v5 driver entry point for the Plan 08 migration init container; transparent postgres:// → pgx5:// scheme rewrite; ErrNoChange collapsed to nil"
  - "Makefile `test-integration` target — `go test -tags=integration ./internal/db/...` requires Docker, kept out of `make test`"
  - "Pinned go.mod entries: pgx/v5 v5.7.6, golang-migrate/v4 v4.18.3, testcontainers-go v0.38.0 (Go 1.23 baseline preserved)"
affects:
  - 01-04 (internal/credhash — computes the HMAC-SHA-256 hash that lands in personal_keys.credential_hash / environment_keys.credential_hash; the contract surface is now schema-level)
  - 01-05 (operator reconcilers — Phase 1 reconcilers do not write rows; Phase 3 will INSERT against the schema landed here)
  - 01-06 (cmd/operator/main.go — will call db.Open(ctx, os.Getenv(\"ACH_DB_URL\")) and store the *pgxpool.Pool on the reconciler struct in Phase 3)
  - 01-08 (manifests — the migration init container will run `migrate up`-equivalent via db.Migrate(url, \"/migrations\") against this exact file set)
  - 01-10 (cmd/migrate/main.go — Plan 10's tiny binary will be a 10-line wrapper calling db.Migrate)
  - 03-* (pk_/ek_ lifecycle — first row-writer against this schema)

# Tech tracking
tech-stack:
  added:
    - github.com/jackc/pgx/v5 v5.7.6 (native pgx driver + pgxpool — D-06; v5.9.2 latest requires Go 1.25, pinned to 5.7.6 for Go 1.23 baseline preservation)
    - github.com/jackc/pgx/v5/pgxpool (subpackage of pgx; sized at 10 conns per replica for Operator)
    - github.com/jackc/pgx/v5/pgconn (test-only; used for SQLSTATE assertions in integration test)
    - github.com/golang-migrate/migrate/v4 v4.18.3 (migration runner — D-06; v4.19.1 latest requires Go 1.24)
    - github.com/golang-migrate/migrate/v4/database/pgx/v5 (registers `pgx5://` scheme with migrate)
    - github.com/golang-migrate/migrate/v4/source/file (file:// migration source)
    - github.com/testcontainers/testcontainers-go v0.38.0 (test infra — v0.42.0 latest requires Go 1.25)
    - github.com/testcontainers/testcontainers-go/modules/postgres v0.38.0 (postgres:16-alpine helper module)
  patterns:
    - "Containerized toolchain via ./scripts/dev.sh — every go/make/migrate/psql invocation routed through the dev container (D-16)"
    - "Plain pgx + pgxpool — no ORM, no database/sql wrapper for application paths (D-06)"
    - "URL scheme rewrite at the migrate boundary — postgres:// in deployment env, pgx5:// only inside db.Migrate (matches the migrate library's driver-registration scheme)"
    - "Build-tagged integration tests — `//go:build integration` so Docker is not a prerequisite for `make test` (envtest-only unit pass stays Docker-free)"
    - "Testcontainers-go for live Postgres tests — sibling container via host docker.sock mounted by scripts/dev.sh"

key-files:
  created:
    - db/migrations/000001_init.up.sql (Hub §16 four-table schema + DB-02 CHECK + DB-01/03 UNIQUE)
    - db/migrations/000001_init.down.sql (reverse: DROP TABLE all four)
    - internal/db/db.go (Open + Migrate + ErrEmptyURL)
    - internal/db/db_test.go (integration tests, build tag `//go:build integration`)
  modified:
    - go.mod (added pgx/v5, golang-migrate/v4, testcontainers-go and their transitive deps)
    - go.sum (lockfile entries)
    - Makefile (added `test-integration` target)

key-decisions:
  - "Version pins below latest to preserve Go 1.23 baseline. pgx/v5 v5.7.6 (latest 5.9.2 requires Go 1.25); golang-migrate/v4 v4.18.3 (latest 4.19.1 requires Go 1.24); testcontainers-go v0.38.0 (latest 0.42.0 requires Go 1.25). Tracked as Rule 3 (blocking) auto-fixes — the plan said `@latest` but @latest no longer compiles on the Plan 01 toolchain pin. The Go 1.23 baseline is the project-wide invariant from 01-01 (D-02)."
  - "URL scheme rewrite inside db.Migrate. The pgx/v5 migrate driver registers ONLY the `pgx5://` URL scheme (verified by reading the driver source at .gocache/.../database/pgx/v5/pgx.go: `database.Register(\"pgx5\", &db)`). Deployments configure ACH_DB_URL with the standard postgres:// or postgresql:// scheme expected by pgxpool. db.Migrate transparently rewrites the scheme so callers (Plan 08 init container, Plan 10 cmd/migrate) don't have to know about the driver's internal scheme. Tracked as Rule 2 (missing critical functionality) — without the rewrite, db.Migrate against a real ACH_DB_URL would fail with `unknown driver` at runtime."
  - "Idempotency assertion in TestOpenAndMigrate. The plan's acceptance criteria did not explicitly require it, but D-07 (the migration init container) restarts on Pod restart — re-running a fully-applied migration must NOT be a failure. Added a second db.Migrate() call to the same test to assert ErrNoChange is collapsed to nil. Rule 2 (correctness for Phase 1 SC #2 — Pod must reach Ready)."
  - "Status enum CHECK exercised in integration test. The plan's acceptance criteria covered pkid_/ekid_ CHECK + credential_hash UNIQUE; the migration also adds CHECK constraints on the `status` column for both key tables (status IN ('active','revoked','expired') for personal_keys; status IN ('active','revoked') for environment_keys). The integration test exercises the personal_keys status enum CHECK to confirm the constraint is wired. Rule 2 (defense in depth — future Phase 3 INSERTs that accidentally pass an unknown status get rejected at the DB layer)."

patterns-established:
  - "Schema-level invariant enforcement — pkid_/ekid_ prefix and credential_hash uniqueness are SQL CHECK + UNIQUE constraints, not application-layer asserts. Future phases that INSERT against personal_keys / environment_keys cannot accidentally bypass the contract (Rule 3 in our threat-register parlance: defense in depth)."
  - "Migration file numbering — `000001_init.{up,down}.sql` matches golang-migrate conventions (6-digit zero-padded version, snake_case description, `.up.sql`/`.down.sql` suffix). Phase 2+ will use `000002_*` for schema evolution."
  - "Integration tests live next to the package they exercise, build-tagged. `internal/db/db_test.go` requires `-tags=integration` to compile; default `go test ./...` skips it entirely. This pattern will repeat for any package needing live external services (Phase 2 cache PVC tests, Phase 3 Dex/SSO tests, Phase 4 JWKS tests)."

requirements-completed: [DB-01, DB-02, DB-03, DB-04, DB-05, DB-06]

# Metrics
duration: ~9min
completed: 2026-05-15
---

# Phase 1 Plan 3: Foundation — Postgres §16 Schema + internal/db Wrapper Summary

**Postgres §16 schema (four tables) shipped as `db/migrations/000001_init.{up,down}.sql` with `pkid_`/`ekid_` CHECK constraints and UNIQUE `credential_hash`; thin `internal/db` package wraps `pgxpool.New` and `golang-migrate/v4` (pgx/v5 driver) behind two functions (`Open`, `Migrate`); integration test exercises the migration against a real `postgres:16-alpine` via testcontainers-go and asserts SQLSTATE 23514/23505 on the relevant constraint violations.**

## Performance

- **Duration:** ~9 min
- **Started:** 2026-05-15T13:29:39Z
- **Completed:** 2026-05-15T13:39Z (approx, post-final-commit)
- **Tasks:** 2 / 2
- **Files modified:** 5 (+2 in metadata commit)

## Accomplishments

- **Hub §16 schema landed at SQL layer.** All four tables (`personal_keys`, `environment_keys`, `external_refs`, `marketplace_plugins`) exist at migration apply with the planner-vetted column lists. The `pkid_`/`ekid_` prefix invariant (Hub §16) is a CHECK constraint on the SQL side; the `credential_hash` UNIQUE NOT NULL constraint on the two key tables makes hash collisions an INSERT failure (audit-actionable) rather than a silent override. DB-04 (no raw bearer columns) is enforced by construction — there's no column named `*_plaintext`, `bearer_*`, `secret_value` anywhere in the migration.
- **`internal/db` package shipped with a 2-function surface.** `Open(ctx, url) → *pgxpool.Pool` and `Migrate(url, migrationsPath) → error`. Both return `ErrEmptyURL` on empty URL (D-08 fail-fast). `Open` sizes the pool at 10 conns (the Operator's per-replica default; Phase 3 Platform API can construct its own larger pool via `pgxpool.ParseConfig`). `Migrate` transparently rewrites `postgres://` → `pgx5://` so the pgx/v5 migrate driver finds itself by URL scheme, and collapses `ErrNoChange` to `nil` for re-apply idempotency.
- **Integration test passes against live Postgres.** `TestOpenAndMigrate` boots `postgres:16-alpine` via testcontainers-go (sibling container — host docker.sock mounted by `scripts/dev.sh`), applies the migration, opens a pool, asserts all four §16 tables exist via `pg_catalog.pg_tables`, asserts SQLSTATE `23514` (check_violation) on `INSERT` with a `key_id` lacking the `pkid_`/`ekid_` prefix, asserts SQLSTATE `23505` (unique_violation) on duplicate `credential_hash` on both key tables, asserts SQLSTATE `23514` on a bad `status` value, and verifies migration idempotency on a second `db.Migrate` call.
- **`make test-integration` target added.** Excluded from default `make test` so the envtest-only unit pass remains Docker-free; build-tagged `integration` so `go build ./...` and `go vet ./...` don't try to compile testcontainers-go into the application paths.
- **Plan 08 + Plan 10 unblocked.** Plan 08's migration init container has its entry point (`db.Migrate`) and target migration files (`db/migrations/000001_init.{up,down}.sql`). Plan 10's `cmd/migrate/main.go` is now a 10-line wrapper that calls `db.Migrate`. Phase 3 row-writers have the table shapes and constraint surface they need.

## Schema (column-by-column, Phase 3 INSERT reference)

### `personal_keys` (Hub §16, with `pkid_` prefix invariant)

| Column            | Type        | Constraints                                                                 |
|-------------------|-------------|------------------------------------------------------------------------------|
| `key_id`          | `text`      | `PRIMARY KEY`; CHECK (`key_id LIKE 'pkid_%'`) — DB-02                        |
| `credential_hash` | `text`      | `NOT NULL UNIQUE` — DB-01, DB-03                                             |
| `owner_email`     | `text`      | `NOT NULL` — SSO-verified email, no normalization (Hub §16, §18.3)           |
| `status`          | `text`      | `NOT NULL DEFAULT 'active'`; CHECK in (`'active'`, `'revoked'`, `'expired'`) |
| `created_at`      | `timestamptz` | `NOT NULL DEFAULT now()`                                                   |
| `last_used_at`    | `timestamptz` | nullable; sliding-window updated atomically on use (Hub §7.1)              |
| `expires_at`      | `timestamptz` | `NOT NULL`                                                                 |
| `revoked_at`      | `timestamptz` | nullable                                                                   |

### `environment_keys` (Hub §16, with `ekid_` prefix invariant)

| Column            | Type        | Constraints                                                                 |
|-------------------|-------------|------------------------------------------------------------------------------|
| `key_id`          | `text`      | `PRIMARY KEY`; CHECK (`key_id LIKE 'ekid_%'`) — DB-02                        |
| `credential_hash` | `text`      | `NOT NULL UNIQUE` — DB-01, DB-03                                             |
| `environment`     | `text`      | `NOT NULL` — denormalized environment name (Hub §16)                         |
| `owner_email`     | `text`      | `NOT NULL`                                                                   |
| `name`            | `text`      | `NOT NULL` — human-friendly label written into LiteLLM `key_alias` (Hub §16) |
| `status`          | `text`      | `NOT NULL DEFAULT 'active'`; CHECK in (`'active'`, `'revoked'`)              |
| `created_at`      | `timestamptz` | `NOT NULL DEFAULT now()`                                                   |
| `last_used_at`    | `timestamptz` | nullable                                                                   |
| `revoked_at`      | `timestamptz` | nullable                                                                   |

### `external_refs` (Hub §16)

| Column                    | Type          | Constraints                            |
|---------------------------|---------------|-----------------------------------------|
| `kind`                    | `text`        | `NOT NULL`; composite PK                |
| `name`                    | `text`        | `NOT NULL`; composite PK                |
| `storage_location`        | `text`        | `NOT NULL`; filesystem path under cache root (Hub §10.3) |
| `last_successful_refresh` | `timestamptz` | nullable                               |
| `next_refresh_at`         | `timestamptz` | nullable                               |
| `max_staleness_seconds`   | `bigint`      | `NOT NULL`                             |

Primary key: `(kind, name)`.

### `marketplace_plugins` (Hub §16)

| Column                    | Type          | Constraints                            |
|---------------------------|---------------|-----------------------------------------|
| `marketplace_name`        | `text`        | `NOT NULL`; composite PK                |
| `name`                    | `text`        | `NOT NULL`; composite PK                |
| `storage_location`        | `text`        | `NOT NULL`                              |
| `last_successful_refresh` | `timestamptz` | nullable                               |
| `next_refresh_at`         | `timestamptz` | nullable                               |
| `max_staleness_seconds`   | `bigint`      | `NOT NULL`                             |

Primary key: `(marketplace_name, name)`.

## Task Commits

1. **Task 1: Author `000001_init.up.sql` and `000001_init.down.sql`** — `e1e188c` (feat)
2. **Task 2: Write `internal/db/db.go` and `internal/db/db_test.go` (testcontainers-go)** — `74006da` (feat)

**Plan metadata commit:** appended below this SUMMARY commit.

## Files Created/Modified

See `key-files.created` and `key-files.modified` in the frontmatter. Concretely:

- `db/migrations/000001_init.up.sql` — Hub §16 four-table CREATE TABLE block + CHECK + UNIQUE constraints + leading comment block citing §16 / §16.1 / DB-02 / DB-04 traceability.
- `db/migrations/000001_init.down.sql` — `DROP TABLE IF EXISTS` × 4 in reverse declaration order.
- `internal/db/db.go` — `Open`, `Migrate`, `ErrEmptyURL`; Apache-2.0 boilerplate header verbatim from `hack/boilerplate.go.txt`; package comment cites D-06/D-07/D-08 and Hub §16/§16.1.
- `internal/db/db_test.go` — `//go:build integration`; `TestOpenAndMigrate` plus `TestOpenRejectsEmptyURL` and `TestMigrateRejectsEmptyURL`; uses `tcpostgres.Run(ctx, "postgres:16-alpine", ...)` (testcontainers-go v0.38 module API).
- `go.mod` — pinned version additions (see frontmatter `tech-stack.added`).
- `go.sum` — lockfile additions.
- `Makefile` — `test-integration` PHONY target.

## Decisions Made

- **Version pins below `@latest`.** The plan's Task 2 action block said `go get ...@latest` for pgx, golang-migrate, and testcontainers-go. `@latest` for all three now requires Go ≥1.24 or ≥1.25; the project pin from 01-01 is Go 1.23.0 (D-02). Resolved by pinning each module to the highest version still compatible with Go 1.23 (pgx v5.7.6, migrate v4.18.3, testcontainers v0.38.0). Tracked as a Rule 3 (blocking) auto-fix below. The pinned versions are widely deployed and `[KNOWN]` per the threat register's `T-03-SC` row.
- **URL scheme rewrite inside `db.Migrate`.** The pgx/v5 migrate driver registers only the `pgx5://` URL scheme. Inspection of `.gocache/.../golang-migrate/migrate/v4@v4.18.3/database/pgx/v5/pgx.go` shows `database.Register("pgx5", &db)` — there is no `postgres://` registration on the v5 driver. Deployments wire ACH_DB_URL as `postgres://…` (the universal scheme expected by every other Postgres client tool); to bridge the gap, `db.Migrate` strips the `postgres://` (or `postgresql://`) prefix and prepends `pgx5://`. Documented in the function doc comment.
- **Build-tagged integration tests.** `//go:build integration` on line 1 of `db_test.go` excludes the testcontainers test from the default `go test ./...` and from `make test`. The envtest-only unit pass stays Docker-free. `make test-integration` is the explicit opt-in.
- **Plain `pgxpool.NewWithConfig` over `pgxpool.New`.** The plan was unspecific about which constructor to use. Picked `NewWithConfig` because `MaxConns` cannot be set via the URL — it has to be set on the parsed config. This matches the PATTERNS.md snippet (line 706) verbatim.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Pin pgx/v5, golang-migrate/v4, testcontainers-go below `@latest` to preserve the Go 1.23 baseline**

- **Found during:** Task 2 step 1 (the `go get @latest` invocations)
- **Issue:** The plan's Task 2 action block said `go get github.com/jackc/pgx/v5@latest && go get github.com/golang-migrate/migrate/v4@latest && go get github.com/testcontainers/testcontainers-go@latest`. All three `@latest` resolutions now fail on Go 1.23: pgx v5.9.2 requires Go ≥1.25, migrate v4.19.1 requires Go ≥1.24, testcontainers v0.42.0 requires Go ≥1.25. The project-wide Go pin from 01-01 (D-02) is `go 1.23.0`; upgrading the toolchain to 1.24 or 1.25 to chase `@latest` is a project-wide invariant change outside the scope of Plan 03.
- **Fix:** Pinned to the highest version of each module still compatible with Go 1.23: `github.com/jackc/pgx/v5@v5.7.6`, `github.com/golang-migrate/migrate/v4@v4.18.3`, `github.com/testcontainers/testcontainers-go@v0.38.0` + `github.com/testcontainers/testcontainers-go/modules/postgres@v0.38.0`. All three are widely deployed (`[KNOWN]` per the plan's `T-03-SC` threat-register row).
- **Files modified:** `go.mod`, `go.sum`.
- **Verification:** `./scripts/dev.sh go build ./...` exits 0; integration test passes.
- **Committed in:** `74006da` (Task 2 commit).

**2. [Rule 2 — Missing Critical] URL scheme rewrite in `db.Migrate` (`postgres://` → `pgx5://`)**

- **Found during:** Task 2 step "write db.go" (cross-checked the migrate driver source).
- **Issue:** The PATTERNS.md `Open()` snippet correctly uses `pgxpool.ParseConfig(url)` which accepts `postgres://` / `postgresql://`. The plan's Task 2 action block then describes `db.Migrate` as calling `migrate.New("file://"+migrationsPath, url)` with the *same* `url`. But the pgx/v5 migrate driver registers itself only under `pgx5://` (verified via `database.Register("pgx5", &db)` in `database/pgx/v5/pgx.go`). Passing a `postgres://` URL to `migrate.New` would fail at runtime with `unknown driver`.
- **Fix:** `db.Migrate` strips the `postgres://` (or `postgresql://`) scheme prefix and prepends `pgx5://` before calling `migrate.New`. Other URL schemes pass through unchanged. Documented in the function doc comment.
- **Files modified:** `internal/db/db.go`.
- **Verification:** Integration test calls `db.Migrate(connStr, "../../db/migrations")` where `connStr` is `postgres://…` (returned by testcontainers `ConnectionString`) and passes.
- **Committed in:** `74006da` (Task 2 commit).

**3. [Rule 2 — Missing Critical] Migration idempotency assertion**

- **Found during:** Writing the integration test.
- **Issue:** The plan's acceptance criteria did not explicitly assert that re-running the migration is a no-op. But D-07 (the migration init container) restarts on every Pod restart of the Operator + Content Service pod (`strategy: Recreate`, single replica). A migration that errors on the second-and-subsequent calls would block the Pod from reaching Ready and break Phase 1 SC #2.
- **Fix:** Added a second `db.Migrate(connStr, migrationsPath)` call after the first in `TestOpenAndMigrate` and asserted the result is `nil`. The package's `ErrNoChange` collapse to nil makes this trivial; the test locks in the contract.
- **Files modified:** `internal/db/db_test.go`.
- **Verification:** Test passes (5.12 s end-to-end).
- **Committed in:** `74006da` (Task 2 commit).

**4. [Rule 2 — Missing Critical] Status enum CHECK exercised in integration test**

- **Found during:** Writing the integration test.
- **Issue:** The migration adds CHECK constraints on the `status` column for both key tables (`status IN ('active','revoked','expired')` for personal_keys; `status IN ('active','revoked')` for environment_keys). The plan's acceptance criteria explicitly covered the key_id prefix CHECK and credential_hash UNIQUE only — silent on status enum.
- **Fix:** Added an extra `INSERT … status = 'NOPE'` assertion expecting SQLSTATE 23514. Defense in depth — Phase 3 row-writers that pass an unknown status (e.g. a typo in a future state transition) are rejected at the DB layer rather than silently surfacing with bogus data.
- **Files modified:** `internal/db/db_test.go`.
- **Verification:** Test passes.
- **Committed in:** `74006da` (Task 2 commit).

---

**Total deviations:** 4 (1 blocking auto-fix, 3 missing-critical additions)
**Impact on plan:** All adjustments preserve plan intent and strengthen the contract surface. The version pin preserves the project-wide Go 1.23 baseline from 01-01. The URL-scheme rewrite makes `db.Migrate` actually work against the deployment-supplied `ACH_DB_URL`. The idempotency and status-enum assertions tighten the integration-test contract without changing the migration's surface area.

## Issues Encountered

- **`go mod tidy` removes test-only dependencies until a source file references them.** First run: added pgx + migrate via `go get`, then `go mod tidy`. Since neither was yet imported by a Go source file, tidy removed them. The subsequent `go build ./...` then tried to bump to `@latest` versions and tripped over Go 1.23 incompatibility. Resolved by re-running `go get …@<pinned>` after writing both `db.go` (uses pgx + migrate) and `db_test.go` (uses testcontainers). The pinned versions stuck because the Go source files now reference them.
- **testcontainers-go v0.38 module API uses `tcpostgres.Run` (not `tcpostgres.RunContainer`).** Older testcontainers-go versions exposed `postgres.RunContainer`; in v0.38 the canonical entry point is `postgres.Run(ctx, image, opts...)`. Documented inline in the test for future readers.
- **Postgres container readiness needs `WithOccurrence(2)` on the log-wait strategy.** A common testcontainers-go pitfall: Postgres logs "database system is ready to accept connections" twice — once during init, once when it starts accepting external connections. The first occurrence is too early to issue queries. Added `wait.ForLog(...).WithOccurrence(2)` for reliability. Captured in the test.

## User Setup Required

None. Docker on the host (already required by Plan 01's `scripts/dev.sh` for sibling-container access) is the only runtime prerequisite for the integration test. Phase 1 unit tests (`make test`) do NOT require Docker.

## Next Phase Readiness

- **Plan 01-04 (internal/credhash):** Ready. Independent — credhash computes the HMAC-SHA-256 hash that lands in `personal_keys.credential_hash` / `environment_keys.credential_hash`, but the column is `text` and the package boundary is type-clean. credhash has no internal/db dependency.
- **Plan 01-05 (reconcilers):** Unchanged readiness — Phase 1 reconcilers do not write rows. The DB pool will be plumbed via the Operator main constructor in Plan 06.
- **Plan 01-06 (cmd/operator/main.go):** Will now call `db.Open(ctx, os.Getenv("ACH_DB_URL"))` and store the `*pgxpool.Pool` on the manager's `Runnable` struct. The `ErrEmptyURL` fast-fail surfaces as a startup-time abort per D-08.
- **Plan 01-08 (Pod manifest + migration init container):** Has its entry point. The init container will be the operator image with `--migrate` flag (Plan 10), which is a 10-line wrapper around `db.Migrate(os.Getenv("ACH_DB_URL"), "/migrations")` and Pod-mounts `/migrations` from a `ConfigMap` populated from `db/migrations/`.
- **Plan 01-10 (Dockerfiles + cmd/migrate/main.go):** Has its target. `cmd/migrate/main.go` wraps `db.Migrate`; the Dockerfile copies `db/migrations/` into the image at `/migrations`.
- **Phase 3 (pk_/ek_ lifecycle):** Has its column-by-column schema (see the Schema section above) and the constraint contract (DB-02 prefix CHECK + DB-01/03 credential_hash UNIQUE). First INSERT statements can be written against this shape directly.
- **No blockers, no concerns.**

## Self-Check: PASSED

- [x] `db/migrations/000001_init.up.sql` exists and contains `CREATE TABLE personal_keys`, `CREATE TABLE environment_keys`, `CREATE TABLE external_refs`, `CREATE TABLE marketplace_plugins`. Confirmed via grep.
- [x] `db/migrations/000001_init.up.sql` contains the literal CEL constraint expressions `CHECK (key_id LIKE 'pkid_%')` and `CHECK (key_id LIKE 'ekid_%')`. Confirmed via grep.
- [x] `db/migrations/000001_init.up.sql` declares `credential_hash text NOT NULL UNIQUE` exactly twice (verified by `grep -c "credential_hash  text NOT NULL UNIQUE"` returning 2).
- [x] `db/migrations/000001_init.up.sql` contains zero matches for `credential_plaintext`, `bearer_token`, `plaintext`, `secret_value` patterns. Confirmed via grep (count 0 — even the comment block carefully avoids these literal tokens).
- [x] `db/migrations/000001_init.up.sql` contains zero `FOREIGN KEY` clauses. Confirmed.
- [x] `db/migrations/000001_init.down.sql` issues `DROP TABLE IF EXISTS` for all four tables. Confirmed (count 4).
- [x] `internal/db/db.go` contains `func Open(ctx context.Context, url string) (*pgxpool.Pool, error)` and `var ErrEmptyURL`. Confirmed.
- [x] `internal/db/db.go` contains `func Migrate(url string, migrationsPath string) error` referencing `migrate.New`. Confirmed.
- [x] `internal/db/db_test.go` has build tag `//go:build integration` on line 1. Confirmed.
- [x] `go.mod` lists `github.com/jackc/pgx/v5 v5.7.6`, `github.com/golang-migrate/migrate/v4 v4.18.3`, `github.com/testcontainers/testcontainers-go v0.38.0`. Confirmed.
- [x] `./scripts/dev.sh go build ./...` exits 0. Confirmed.
- [x] `./scripts/dev.sh make fmt vet` exits 0. Confirmed.
- [x] `./scripts/dev.sh go test -tags=integration ./internal/db/... -count=1 -timeout 180s` passes 3/3 tests (`TestOpenAndMigrate`, `TestOpenRejectsEmptyURL`, `TestMigrateRejectsEmptyURL`). Confirmed (5.13 s end-to-end).
- [x] Integration test confirms all four tables exist after migration. Confirmed by `pg_catalog.pg_tables` query in the test.
- [x] Integration test confirms CHECK violation on non-`pkid_` `key_id` (SQLSTATE 23514). Confirmed.
- [x] Integration test confirms CHECK violation on non-`ekid_` `key_id` (SQLSTATE 23514). Confirmed.
- [x] Integration test confirms UNIQUE violation on duplicate `credential_hash` on both key tables (SQLSTATE 23505). Confirmed.
- [x] Both task commits present: `e1e188c` (Task 1), `74006da` (Task 2). Confirmed via `git log --oneline -3`.

---

*Phase: 01-foundation-crds-db-schema-operator-skeleton-multi-tenancy*
*Completed: 2026-05-15*
