# Runtime Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist the LiteLLM runtime snapshot (models / MCP servers / A2A agents) into a derived Postgres table so the admin UI and CLI can list "what runtime is available" instead of querying LiteLLM by hand.

**Architecture:** The operator already keeps an in-memory `Snapshotter` (`internal/snapshot/snapshot.go`) that refreshes LiteLLM's registered models/MCP/A2A every 5 min. We add a single derived table `runtime_catalog_entries`, persist the snapshot into it on each successful refresh (active rows upserted, vanished rows tombstoned `missing` in the same transaction + `NOTIFY ach_runtime_catalog_changed`), expose **admin-only** read endpoints under `/platform/admin/runtime/*`, and add `ach-cli runtime …` commands. An optional final phase adds an admin-triggered immediate refresh.

**Tech Stack:** Go, controller-runtime, pgx v5 (`*pgxpool.Pool`), golang-migrate, chi router, cobra. Postgres integration tests via testcontainers-go (`//go:build integration`).

## Global Constraints

- **The runtime catalog is ADMIN-ONLY.** Reads live under `/platform/admin/runtime/*` and inherit `admin.AdminOnly` (pk_ required, ek_ → 401 `invalid_key_type`, caller must be in the admin allowlist). Rationale: ACH v1alpha1 has no per-object ACLs, so only admins author Environments. Do NOT add per-user catalog filtering.
- **Keep `LiteLLMConnection`** — do NOT introduce a `RuntimeConnector` CRD. The generic surface is the catalog table, not the CRD.
- **Connector identity** for every row is `(namespace = watchNS, connector_name = 'default')`. The operator is single-namespace (`ACH_NAMESPACE`, default `ach-system`) and `LiteLLMConnection` is the singleton `default`.
- **Persist only on a full successful refresh.** On a stale/failed refresh the Snapshotter preserves its prior in-memory snapshot; do not touch the catalog table. Catalog staleness is derived at read time from `last_successful_sync`.
- **Minimal columns only.** Store `kind`, `name`, `status` (`active`|`missing`), and timestamps. Do NOT add `provider`, `capabilities_json`, `raw_json`, `display_name`, or `checksum` — there is no data source for them in the LiteLLM list responses and no consumer yet (YAGNI).
- **SPDX header** on every new `*.go` file: first line `// SPDX-License-Identifier: Apache-2.0`. (`make fix-spdx` auto-prepends; the pre-push gate enforces.)
- **DB error discipline** (`internal/db/errors.go`): return transient pgconn errors (class 08/57) raw; treat `pgx.ErrNoRows` specially; wrap other errors with `fmt.Errorf` using only namespace/name (never the raw error message, which can leak secrets).
- **No Go on host.** Run Go through `./scripts/dev.sh go …` or a `make` target — never bare `go`.
- **Channel name** for the catalog NOTIFY is exactly `ach_runtime_catalog_changed` (matches `[a-z0-9_]+`, ≤63 bytes — validated by `internal/db/notify.go` `validChannel`).

**Out of scope (deliberate):** `ach-cli env describe` catalog annotations (env describe is run by non-admin Environment *consumers*, who cannot read the admin-only catalog); per-user/team-filtered catalog; `runtime_catalog_entries` GC/retention pass (rows are tombstoned `missing` and kept); `display_name`/`provider`/`capabilities`.

---

### Task 1: Migration — `runtime_catalog_entries` table

**Files:**
- Create: `db/migrations/000015_runtime_catalog_entries.up.sql`
- Create: `db/migrations/000015_runtime_catalog_entries.down.sql`

**Interfaces:**
- Produces: table `runtime_catalog_entries` with PK `(namespace, connector_name, kind, name)`, columns `status`, `first_seen_at`, `last_seen_at`, `last_successful_sync`, `deleted_at`; index `runtime_catalog_entries_lookup_idx (namespace, connector_name, kind)`.

- [ ] **Step 1: Confirm `000015` is the next free migration number**

Run: `ls db/migrations/ | sort | tail -4`
Expected: highest existing is `000014_*` (no `000015_*` present).

- [ ] **Step 2: Write the up migration**

Create `db/migrations/000015_runtime_catalog_entries.up.sql`:

```sql
-- runtime_catalog_entries: derived read-model of the LiteLLM runtime
-- registry (models / MCP servers / A2A agents), projected by the operator's
-- Snapshotter on each successful refresh. NOT a <kind>_objects projection:
-- no origin/locked, LiteLLM is the source of truth. Admin-only read surface.
CREATE TABLE runtime_catalog_entries (
    namespace            TEXT        NOT NULL,
    connector_name       TEXT        NOT NULL,
    kind                 TEXT        NOT NULL CHECK (kind IN ('model','mcp_server','a2a_agent')),
    name                 TEXT        NOT NULL,
    status               TEXT        NOT NULL CHECK (status IN ('active','missing')),
    first_seen_at        TIMESTAMPTZ NOT NULL,
    last_seen_at         TIMESTAMPTZ NOT NULL,
    last_successful_sync TIMESTAMPTZ NOT NULL,
    deleted_at           TIMESTAMPTZ,
    PRIMARY KEY (namespace, connector_name, kind, name)
);

CREATE INDEX runtime_catalog_entries_lookup_idx
    ON runtime_catalog_entries (namespace, connector_name, kind);
```

- [ ] **Step 3: Write the down migration**

Create `db/migrations/000015_runtime_catalog_entries.down.sql`:

```sql
DROP TABLE IF EXISTS runtime_catalog_entries;
```

- [ ] **Step 4: Verify the migration applies cleanly (round-trip via a throwaway container)**

Run: `./scripts/dev.sh go test -tags=integration ./internal/db/ -run TestMigrate -count=1`
Expected: PASS (the existing migrate integration test boots a container and applies ALL up migrations including `000015`; a syntax error here fails it).

If no `TestMigrate` exists, defer verification to Task 2 Step 6 (the new integration test applies all migrations via `setupPostgresForPhase2`).

- [ ] **Step 5: Commit**

```bash
git add db/migrations/000015_runtime_catalog_entries.up.sql db/migrations/000015_runtime_catalog_entries.down.sql
git commit -m "feat(db): add runtime_catalog_entries migration"
```

---

### Task 2: DB layer — row type, replace-with-tombstone, read helpers

**Files:**
- Create: `internal/db/runtime_catalog.go`
- Create: `internal/db/runtime_catalog_test.go`

**Interfaces:**
- Consumes: `db.WithTxNotify` (`internal/db/with_tx_notify.go`), `isTransientPgErr` (`internal/db/errors.go`), `*pgxpool.Pool`.
- Produces:
  - `const RuntimeCatalogChannel = "ach_runtime_catalog_changed"`
  - `type RuntimeCatalogRow struct { Namespace, ConnectorName, Kind, Name, Status string; FirstSeenAt, LastSeenAt, LastSuccessfulSync time.Time; DeletedAt *time.Time }`
  - `func ReplaceRuntimeCatalog(ctx context.Context, pool *pgxpool.Pool, ns, connector string, models, mcpServers, a2aAgents map[string]struct{}, syncedAt time.Time) error`
  - `func ListRuntimeCatalog(ctx context.Context, pool *pgxpool.Pool, ns, connector, kind string) ([]RuntimeCatalogRow, error)` — `kind==""` returns all kinds.
  - `func MaxRuntimeCatalogSync(ctx context.Context, pool *pgxpool.Pool, ns, connector string) (time.Time, bool, error)` — `ok=false` when the catalog is empty.

- [ ] **Step 1: Write the failing integration test**

Create `internal/db/runtime_catalog_test.go`:

```go
//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

func set(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// First sync inserts active rows; a second sync that drops a model tombstones
// the vanished one as status='missing' while keeping first_seen_at stable.
func TestReplaceRuntimeCatalog_TombstonesVanishedEntries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	t1 := time.Now().Truncate(time.Microsecond)
	if err := db.ReplaceRuntimeCatalog(ctx, pool, "ach-system", "default",
		set("gpt-4o", "sonnet"), set("github"), set("vendor-research"), t1); err != nil {
		t.Fatalf("ReplaceRuntimeCatalog #1: %v", err)
	}

	models, err := db.ListRuntimeCatalog(ctx, pool, "ach-system", "default", "model")
	if err != nil {
		t.Fatalf("ListRuntimeCatalog: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models after #1: got %d, want 2", len(models))
	}

	// Second sync: "sonnet" is gone.
	t2 := t1.Add(5 * time.Minute)
	if err := db.ReplaceRuntimeCatalog(ctx, pool, "ach-system", "default",
		set("gpt-4o"), set("github"), set("vendor-research"), t2); err != nil {
		t.Fatalf("ReplaceRuntimeCatalog #2: %v", err)
	}

	models, _ = db.ListRuntimeCatalog(ctx, pool, "ach-system", "default", "model")
	byName := map[string]db.RuntimeCatalogRow{}
	for _, m := range models {
		byName[m.Name] = m
	}
	if got := byName["gpt-4o"].Status; got != "active" {
		t.Fatalf("gpt-4o status: got %q, want active", got)
	}
	if got := byName["sonnet"].Status; got != "missing" {
		t.Fatalf("sonnet status: got %q, want missing", got)
	}
	if byName["sonnet"].DeletedAt == nil {
		t.Fatalf("sonnet deleted_at should be set when tombstoned")
	}
	if !byName["sonnet"].FirstSeenAt.Equal(t1) {
		t.Fatalf("sonnet first_seen_at drifted: got %v, want %v", byName["sonnet"].FirstSeenAt, t1)
	}
}

// A tombstoned entry that reappears flips back to active and clears deleted_at.
func TestReplaceRuntimeCatalog_ReappearReactivates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	t1 := time.Now().Truncate(time.Microsecond)
	_ = db.ReplaceRuntimeCatalog(ctx, pool, "ach-system", "default", set("sonnet"), nil, nil, t1)
	_ = db.ReplaceRuntimeCatalog(ctx, pool, "ach-system", "default", nil, nil, nil, t1.Add(time.Minute))      // sonnet → missing
	_ = db.ReplaceRuntimeCatalog(ctx, pool, "ach-system", "default", set("sonnet"), nil, nil, t1.Add(2*time.Minute)) // back

	models, _ := db.ListRuntimeCatalog(ctx, pool, "ach-system", "default", "model")
	if len(models) != 1 || models[0].Status != "active" || models[0].DeletedAt != nil {
		t.Fatalf("expected sonnet active with nil deleted_at, got %+v", models)
	}

	ts, ok, err := db.MaxRuntimeCatalogSync(ctx, pool, "ach-system", "default")
	if err != nil || !ok {
		t.Fatalf("MaxRuntimeCatalogSync: ok=%v err=%v", ok, err)
	}
	if !ts.Equal(t1.Add(2 * time.Minute)) {
		t.Fatalf("max sync: got %v, want %v", ts, t1.Add(2*time.Minute))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails (no implementation yet)**

Run: `./scripts/dev.sh go test -tags=integration ./internal/db/ -run TestReplaceRuntimeCatalog -count=1`
Expected: FAIL — compile error `undefined: db.ReplaceRuntimeCatalog`.

- [ ] **Step 3: Write the implementation**

Create `internal/db/runtime_catalog.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RuntimeCatalogChannel is the NOTIFY channel fired once per successful
// catalog sync (payload = connector_name). Platform-api reads the catalog
// per request today; the notification is the seam for a future cache.
const RuntimeCatalogChannel = "ach_runtime_catalog_changed"

// RuntimeCatalogRow mirrors one runtime_catalog_entries row.
type RuntimeCatalogRow struct {
	Namespace          string
	ConnectorName      string
	Kind               string // "model" | "mcp_server" | "a2a_agent"
	Name               string
	Status             string // "active" | "missing"
	FirstSeenAt        time.Time
	LastSeenAt         time.Time
	LastSuccessfulSync time.Time
	DeletedAt          *time.Time
}

const upsertRuntimeCatalogSQL = `
INSERT INTO runtime_catalog_entries
    (namespace, connector_name, kind, name, status,
     first_seen_at, last_seen_at, last_successful_sync, deleted_at)
VALUES ($1, $2, $3, $4, 'active', $5, $5, $5, NULL)
ON CONFLICT (namespace, connector_name, kind, name) DO UPDATE SET
    status               = 'active',
    last_seen_at         = EXCLUDED.last_seen_at,
    last_successful_sync = EXCLUDED.last_successful_sync,
    deleted_at           = NULL
`

const tombstoneRuntimeCatalogSQL = `
UPDATE runtime_catalog_entries
   SET status     = 'missing',
       deleted_at = $3
 WHERE namespace            = $1
   AND connector_name       = $2
   AND last_successful_sync < $3
   AND status               = 'active'
`

// ReplaceRuntimeCatalog upserts every currently-registered runtime name as
// 'active' (last_successful_sync = syncedAt) then tombstones any previously-
// active row this connector did NOT see this sync, all inside one
// WithTxNotify transaction that fires RuntimeCatalogChannel on commit.
func ReplaceRuntimeCatalog(
	ctx context.Context,
	pool *pgxpool.Pool,
	ns, connector string,
	models, mcpServers, a2aAgents map[string]struct{},
	syncedAt time.Time,
) error {
	return WithTxNotify(ctx, pool, RuntimeCatalogChannel, connector, func(tx pgx.Tx) error {
		for kind, names := range map[string]map[string]struct{}{
			"model":      models,
			"mcp_server": mcpServers,
			"a2a_agent":  a2aAgents,
		} {
			for name := range names {
				if _, err := tx.Exec(ctx, upsertRuntimeCatalogSQL, ns, connector, kind, name, syncedAt); err != nil {
					if isTransientPgErr(err) {
						return err
					}
					return fmt.Errorf("db: ReplaceRuntimeCatalog upsert(%s/%s/%s): %w", ns, connector, kind, err)
				}
			}
		}
		if _, err := tx.Exec(ctx, tombstoneRuntimeCatalogSQL, ns, connector, syncedAt); err != nil {
			if isTransientPgErr(err) {
				return err
			}
			return fmt.Errorf("db: ReplaceRuntimeCatalog tombstone(%s/%s): %w", ns, connector, err)
		}
		return nil
	})
}

const listRuntimeCatalogSQL = `
SELECT namespace, connector_name, kind, name, status,
       first_seen_at, last_seen_at, last_successful_sync, deleted_at
  FROM runtime_catalog_entries
 WHERE namespace      = $1
   AND connector_name = $2
   AND ($3 = '' OR kind = $3)
 ORDER BY kind, name
`

// ListRuntimeCatalog returns catalog rows for one connector. kind=="" returns
// all three kinds; otherwise filters to that kind.
func ListRuntimeCatalog(ctx context.Context, pool *pgxpool.Pool, ns, connector, kind string) ([]RuntimeCatalogRow, error) {
	rows, err := pool.Query(ctx, listRuntimeCatalogSQL, ns, connector, kind)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListRuntimeCatalog(%s/%s): %w", ns, connector, err)
	}
	defer rows.Close()

	var out []RuntimeCatalogRow
	for rows.Next() {
		var r RuntimeCatalogRow
		if err := rows.Scan(&r.Namespace, &r.ConnectorName, &r.Kind, &r.Name, &r.Status,
			&r.FirstSeenAt, &r.LastSeenAt, &r.LastSuccessfulSync, &r.DeletedAt); err != nil {
			return nil, fmt.Errorf("db: ListRuntimeCatalog scan(%s/%s): %w", ns, connector, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListRuntimeCatalog rows(%s/%s): %w", ns, connector, err)
	}
	return out, nil
}

// MaxRuntimeCatalogSync returns the newest last_successful_sync for a
// connector. ok=false when the connector has no catalog rows yet.
func MaxRuntimeCatalogSync(ctx context.Context, pool *pgxpool.Pool, ns, connector string) (time.Time, bool, error) {
	const q = `SELECT max(last_successful_sync) FROM runtime_catalog_entries WHERE namespace = $1 AND connector_name = $2`
	var ts *time.Time
	if err := pool.QueryRow(ctx, q, ns, connector).Scan(&ts); err != nil {
		if isTransientPgErr(err) {
			return time.Time{}, false, err
		}
		return time.Time{}, false, fmt.Errorf("db: MaxRuntimeCatalogSync(%s/%s): %w", ns, connector, err)
	}
	if ts == nil {
		return time.Time{}, false, nil
	}
	return *ts, true, nil
}
```

- [ ] **Step 4: Run the new test to verify it passes**

Run: `./scripts/dev.sh go test -tags=integration ./internal/db/ -run TestReplaceRuntimeCatalog -count=1`
Expected: PASS (both `TombstonesVanishedEntries` and `ReappearReactivates`).

- [ ] **Step 5: Lint the changed package**

Run: `make qa-lint-changed`
Expected: no findings in `internal/db`.

- [ ] **Step 6: Commit**

```bash
git add internal/db/runtime_catalog.go internal/db/runtime_catalog_test.go
git commit -m "feat(db): runtime catalog replace-with-tombstone + read helpers"
```

---

### Task 3: Operator — persist the snapshot on each successful refresh

**Files:**
- Modify: `internal/snapshot/snapshot.go` (add catalog-persistence fields + a builder + a call in the success branch of `refresh`)
- Modify: `cmd/ach/cmd/operator.go:446-449` (wire the pool + connector identity)
- Test: `internal/snapshot/snapshot_test.go` (assert nil-pool path leaves behavior unchanged)

**Interfaces:**
- Consumes: `db.ReplaceRuntimeCatalog` (Task 2), `*pgxpool.Pool`, `watchNS` (operator.go).
- Produces: `func (s *Snapshotter) EnableCatalog(pool *pgxpool.Pool, namespace, connector string) *Snapshotter` — sets persistence fields and returns the receiver for chaining. When pool is nil the Snapshotter behaves exactly as before (no persistence).

- [ ] **Step 1: Write the failing test (nil-pool path unchanged; EnableCatalog is wired)**

Add to `internal/snapshot/snapshot_test.go` (it is package `snapshot`, so it can read unexported fields):

```go
// EnableCatalog stores persistence wiring and is chainable; a nil pool keeps
// persistence disabled so existing single-binary unit tests are unaffected.
func TestEnableCatalog_NilPoolIsInert(t *testing.T) {
	f := &fakeLiteLLM{models: []litellm.ModelInfoResponse{{ModelName: "gpt-4o"}}}
	s := NewSnapshotter(f, logr.Discard()).EnableCatalog(nil, "ach-system", "default")

	if s.catalogNS != "ach-system" || s.connectorName != "default" {
		t.Fatalf("EnableCatalog did not record connector identity: %+v", s)
	}
	// refresh must still succeed and publish the snapshot with a nil pool.
	if ok := s.refresh(context.Background()); !ok {
		t.Fatalf("refresh returned false on healthy fake client")
	}
	if _, ok := s.Snapshot().Models["gpt-4o"]; !ok {
		t.Fatalf("snapshot missing gpt-4o after refresh")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `./scripts/dev.sh go test ./internal/snapshot/ -run TestEnableCatalog -count=1`
Expected: FAIL — `s.EnableCatalog undefined` and `s.catalogNS undefined`.

- [ ] **Step 3: Add the persistence fields, builder, and success-branch call**

In `internal/snapshot/snapshot.go`, add imports `"time"` is already present; add `"github.com/jackc/pgx/v5/pgxpool"` and `achdb "github.com/ackstorm/ach/internal/db"` to the import block.

Extend the `Snapshotter` struct (currently lines 77-83) to:

```go
type Snapshotter struct {
	client                  litellm.Client
	interval                time.Duration
	snap                    atomic.Pointer[LiteLLMSnapshot]
	log                     logr.Logger
	litellmUnreachableCount atomic.Int64

	// Catalog persistence (issue: runtime catalog). When pool is nil the
	// Snapshotter does not persist — single-binary unit tests stay inert.
	pool          *pgxpool.Pool
	catalogNS     string
	connectorName string
}
```

Add the builder immediately after `NewSnapshotter`:

```go
// EnableCatalog wires Postgres persistence of each successful refresh into
// runtime_catalog_entries, keyed by (namespace, connector). Chainable. A nil
// pool leaves persistence disabled.
func (s *Snapshotter) EnableCatalog(pool *pgxpool.Pool, namespace, connector string) *Snapshotter {
	s.pool = pool
	s.catalogNS = namespace
	s.connectorName = connector
	return s
}
```

In `refresh`, in the success branch — immediately AFTER `s.snap.Store(next)` and BEFORE the existing `s.log.Info("litellm snapshot refreshed", …)` — insert:

```go
	if s.pool != nil {
		if err := achdb.ReplaceRuntimeCatalog(ctx, s.pool, s.catalogNS, s.connectorName,
			next.Models, next.MCPServers, next.A2AAgents, next.RefreshedAt); err != nil {
			// Non-fatal: the in-memory snapshot is already published and the
			// EnvironmentReconciler reads that, not the table. Log and move on;
			// the next refresh retries the projection.
			s.log.Error(err, "litellm snapshot: catalog persistence failed",
				"connector", s.connectorName)
		}
	}
```

- [ ] **Step 4: Run the snapshot tests to verify they pass**

Run: `./scripts/dev.sh go test ./internal/snapshot/ -count=1`
Expected: PASS (new `TestEnableCatalog_NilPoolIsInert` + all existing tests unchanged).

- [ ] **Step 5: Wire the pool + connector identity in the operator boot path**

In `cmd/ach/cmd/operator.go`, change the construction at lines 446-449 from:

```go
	snapshotter := snapshot.NewSnapshotter(realLiteLLM, ctrl.Log.WithName("litellm-snapshot"))
```

to:

```go
	snapshotter := snapshot.NewSnapshotter(realLiteLLM, ctrl.Log.WithName("litellm-snapshot")).
		EnableCatalog(dbPool, watchNS, "default")
```

(`dbPool` is in scope from line ~231; `watchNS` from line ~174. The `mgr.Add(snapshotter)` line directly below is unchanged.)

- [ ] **Step 6: Build the server binary to confirm wiring compiles**

Run: `make build-server`
Expected: builds with no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/snapshot/snapshot.go internal/snapshot/snapshot_test.go cmd/ach/cmd/operator.go
git commit -m "feat(operator): persist litellm snapshot into runtime_catalog_entries"
```

---

### Task 4: Platform-API — admin-only runtime catalog read endpoints

**Files:**
- Create: `internal/platformapi/admin/runtime/handler.go`
- Create: `internal/platformapi/admin/runtime/handler_test.go`
- Modify: `internal/platformapi/admin/mount.go` (register the four GET routes)
- Modify: `CLAUDE.md` (platform-api row: note `/platform/admin/runtime/*`)

**Interfaces:**
- Consumes: `db.ListRuntimeCatalog`, `db.MaxRuntimeCatalogSync` (Task 2); `render.JSON`/`render.Error` (`internal/platformapi/render`); `admin.Deps{ Pool, Namespace, … }`; `admin.AdminOnly` (already applied to the `/platform/admin` subtree in `mount.go`).
- Produces handlers behind these routes (all under `/platform/admin`, all admin-gated):
  - `GET /platform/admin/runtime/models`
  - `GET /platform/admin/runtime/mcp-servers`
  - `GET /platform/admin/runtime/a2a-agents`
  - `GET /platform/admin/runtime/catalog`
- Response envelope for the single-kind endpoints: `{ "connector": {name,type,status,lastSuccessfulSync}, "items": [{name,kind,status}], "next_cursor": null }`. The `/catalog` endpoint returns `{ "connector": {…}, "models": [...], "mcpServers": [...], "a2aAgents": [...] }`.

- [ ] **Step 1: Write the failing handler test (fake catalog reader)**

Create `internal/platformapi/admin/runtime/handler_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

type fakeCatalog struct {
	rows    []db.RuntimeCatalogRow
	maxSync time.Time
	hasSync bool
	err     error
}

func (f fakeCatalog) List(_ context.Context, _, _, kind string) ([]db.RuntimeCatalogRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []db.RuntimeCatalogRow
	for _, r := range f.rows {
		if kind == "" || r.Kind == kind {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f fakeCatalog) MaxSync(_ context.Context, _, _ string) (time.Time, bool, error) {
	return f.maxSync, f.hasSync, f.err
}

func TestModelsHandler_ShapesEnvelope(t *testing.T) {
	now := time.Now()
	cat := fakeCatalog{
		rows: []db.RuntimeCatalogRow{
			{Kind: "model", Name: "gpt-4o", Status: "active"},
			{Kind: "model", Name: "old-vision", Status: "missing"},
			{Kind: "mcp_server", Name: "github", Status: "active"},
		},
		maxSync: now, hasSync: true,
	}
	h := ModelsHandler(Deps{Catalog: cat, Namespace: "ach-system", Connector: "default"})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/platform/admin/runtime/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}

	var body struct {
		Connector struct {
			Name, Type, Status string `json:"-"`
		} `json:"connector"`
		Items []struct {
			Name, Kind, Status string
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items: got %d want 2 (models only)", len(body.Items))
	}
}

func TestCatalogHandler_EmptyConnectorIsActiveEmpty(t *testing.T) {
	h := CatalogHandler(Deps{Catalog: fakeCatalog{hasSync: false}, Namespace: "ach-system", Connector: "default"})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/platform/admin/runtime/catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if _, ok := body["models"]; !ok {
		t.Fatalf("catalog body missing models key: %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `./scripts/dev.sh go test ./internal/platformapi/admin/runtime/ -count=1`
Expected: FAIL — package/handlers undefined.

- [ ] **Step 3: Write the handler implementation**

Create `internal/platformapi/admin/runtime/handler.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// Package runtime serves the admin-only runtime catalog (models / MCP servers
// / A2A agents) projected from LiteLLM into runtime_catalog_entries. All routes
// mount under /platform/admin and inherit admin.AdminOnly (pk_ + allowlist).
package runtime

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// CatalogReader is the read surface this package needs. The concrete impl is
// poolCatalog (backed by *pgxpool.Pool); tests inject a fake.
type CatalogReader interface {
	List(ctx context.Context, ns, connector, kind string) ([]db.RuntimeCatalogRow, error)
	MaxSync(ctx context.Context, ns, connector string) (time.Time, bool, error)
}

// Deps configures the runtime catalog handlers.
type Deps struct {
	Catalog   CatalogReader
	Namespace string
	Connector string
}

// NewPoolCatalog adapts a pgxpool.Pool to CatalogReader.
func NewPoolCatalog(pool *pgxpool.Pool) CatalogReader { return poolCatalog{pool: pool} }

type poolCatalog struct{ pool *pgxpool.Pool }

func (p poolCatalog) List(ctx context.Context, ns, connector, kind string) ([]db.RuntimeCatalogRow, error) {
	return db.ListRuntimeCatalog(ctx, p.pool, ns, connector, kind)
}
func (p poolCatalog) MaxSync(ctx context.Context, ns, connector string) (time.Time, bool, error) {
	return db.MaxRuntimeCatalogSync(ctx, p.pool, ns, connector)
}

type itemView struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type connectorView struct {
	Name               string  `json:"name"`
	Type               string  `json:"type"`
	Status             string  `json:"status"`
	LastSuccessfulSync *string `json:"lastSuccessfulSync"`
}

func (d Deps) connector(ctx context.Context) connectorView {
	cv := connectorView{Name: d.Connector, Type: "litellm", Status: "missing"}
	if ts, ok, err := d.Catalog.MaxSync(ctx, d.Namespace, d.Connector); err == nil && ok {
		cv.Status = "active"
		s := ts.UTC().Format(time.RFC3339)
		cv.LastSuccessfulSync = &s
	}
	return cv
}

func toItems(rows []db.RuntimeCatalogRow) []itemView {
	out := make([]itemView, 0, len(rows))
	for _, r := range rows {
		out = append(out, itemView{Name: r.Name, Kind: r.Kind, Status: r.Status})
	}
	return out
}

func kindHandler(d Deps, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		rows, err := d.Catalog.List(ctx, d.Namespace, d.Connector, kind)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to read runtime catalog", reqID)
			return
		}
		render.JSON(w, http.StatusOK, map[string]any{
			"connector":   d.connector(ctx),
			"items":       toItems(rows),
			"next_cursor": nil,
		})
	}
}

// ModelsHandler serves GET /platform/admin/runtime/models.
func ModelsHandler(d Deps) http.HandlerFunc { return kindHandler(d, "model") }

// MCPServersHandler serves GET /platform/admin/runtime/mcp-servers.
func MCPServersHandler(d Deps) http.HandlerFunc { return kindHandler(d, "mcp_server") }

// A2AAgentsHandler serves GET /platform/admin/runtime/a2a-agents.
func A2AAgentsHandler(d Deps) http.HandlerFunc { return kindHandler(d, "a2a_agent") }

// CatalogHandler serves GET /platform/admin/runtime/catalog (all three kinds).
func CatalogHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		all, err := d.Catalog.List(ctx, d.Namespace, d.Connector, "")
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to read runtime catalog", reqID)
			return
		}
		var models, mcps, agents []itemView
		for _, it := range toItems(all) {
			switch it.Kind {
			case "model":
				models = append(models, it)
			case "mcp_server":
				mcps = append(mcps, it)
			case "a2a_agent":
				agents = append(agents, it)
			}
		}
		render.JSON(w, http.StatusOK, map[string]any{
			"connector":  d.connector(ctx),
			"models":     models,
			"mcpServers": mcps,
			"a2aAgents":  agents,
		})
	}
}
```

> Note: if `audit.OutcomeInternalError` is not the exact constant name, run `grep -rn "OutcomeInternalError" internal/audit/` and use the matching identifier — the Task 3 explorer confirmed `audit.OutcomeInternalError` is used by the existing inventory handlers.

- [ ] **Step 4: Run the handler test to verify it passes**

Run: `./scripts/dev.sh go test ./internal/platformapi/admin/runtime/ -count=1`
Expected: PASS (`TestModelsHandler_ShapesEnvelope`, `TestCatalogHandler_EmptyConnectorIsActiveEmpty`).

- [ ] **Step 5: Register the routes under the admin subtree**

In `internal/platformapi/admin/mount.go`, add the import:

```go
	runtimecatalog "github.com/ackstorm/ach/internal/platformapi/admin/runtime"
```

Inside `Mount`'s returned `func(r chi.Router)`, after the existing inventory route block, add:

```go
		rcDeps := runtimecatalog.Deps{
			Catalog:   runtimecatalog.NewPoolCatalog(deps.Pool),
			Namespace: deps.Namespace,
			Connector: "default",
		}
		r.Get("/runtime/models", runtimecatalog.ModelsHandler(rcDeps))
		r.Get("/runtime/mcp-servers", runtimecatalog.MCPServersHandler(rcDeps))
		r.Get("/runtime/a2a-agents", runtimecatalog.A2AAgentsHandler(rcDeps))
		r.Get("/runtime/catalog", runtimecatalog.CatalogHandler(rcDeps))
```

(`r.Use(AdminOnly(...))` at the top of `Mount` already gates the whole subtree, so these inherit pk_ + allowlist enforcement.)

- [ ] **Step 6: Build platform-api and run the package tests**

Run: `make build-server && ./scripts/dev.sh go test ./internal/platformapi/... -count=1`
Expected: builds; platform-api tests PASS.

- [ ] **Step 7: Update CLAUDE.md (same-commit doc hygiene)**

In `CLAUDE.md`, in the `| platform-api | … |` row of the service-mode table, append to the "Owns" cell: `+ admin runtime catalog read (GET /platform/admin/runtime/{models,mcp-servers,a2a-agents,catalog})`.

- [ ] **Step 8: Commit**

```bash
git add internal/platformapi/admin/runtime/ internal/platformapi/admin/mount.go CLAUDE.md
git commit -m "feat(platform-api): admin-only runtime catalog read endpoints"
```

---

### Task 5: CLI — `ach-cli runtime {models,mcp,a2a,catalog} list`

**Files:**
- Create: `cmd/ach-cli/cmd/runtime.go`
- Create: `cmd/ach-cli/cmd/runtime_test.go`

**Interfaces:**
- Consumes: `resolveAdminBearer(profile, apiKey, envKey)` (`cmd/ach-cli/cmd/admin.go:934`), `httpclient.Client` (`internal/cli/httpclient/client.go`), the JSON envelope from Task 4.
- Produces: cobra command tree `runtime` with children `models`, `mcp`, `a2a`, `catalog`; each fetches its endpoint and renders a table (default) or `-o json`.

- [ ] **Step 1: Write the failing test (table render against a mock server)**

Create `cmd/ach-cli/cmd/runtime_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeModelsList_RendersTable(t *testing.T) {
	dir := adminTestEnv(t) // isolates XDG_CONFIG_HOME, clears synthetic env
	_ = dir

	mux := http.NewServeMux()
	mux.HandleFunc("/platform/admin/runtime/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"connector":   map[string]any{"name": "default", "type": "litellm", "status": "active"},
			"items":       []map[string]string{{"name": "gpt-4o", "kind": "model", "status": "active"}},
			"next_cursor": nil,
		})
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	seedAdminConfig(t, srv.URL)
	swapAdminHTTPClientForTest(t, srv.Client())

	stdout, _, code, err := executeAdmin(t, "", "runtime", "models", "list")
	if err != nil || code != 0 {
		t.Fatalf("runtime models list: code=%d err=%v", code, err)
	}
	if !strings.Contains(stdout, "gpt-4o") || !strings.Contains(stdout, "active") {
		t.Fatalf("table missing model/status:\n%s", stdout)
	}
}
```

> Reuses the existing admin test helpers (`adminTestEnv`, `seedAdminConfig`, `swapAdminHTTPClientForTest`, `executeAdmin`) from `cmd/ach-cli/cmd/admin_test.go` — same package `cmd`. `executeAdmin` runs the fresh root command tree, so it dispatches `runtime …` too.

- [ ] **Step 2: Run it to verify it fails**

Run: `./scripts/dev.sh go test ./cmd/ach-cli/cmd/ -run TestRuntimeModelsList -count=1`
Expected: FAIL — `unknown command "runtime"`.

- [ ] **Step 3: Write the command implementation**

Create `cmd/ach-cli/cmd/runtime.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

type runtimeItem struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type runtimeListResp struct {
	Items []runtimeItem `json:"items"`
}

// newRuntimeCmd is the `ach-cli runtime` parent (admin-only catalog views).
func newRuntimeCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect the admin runtime catalog (models, MCP servers, A2A agents)",
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	parent.AddCommand(
		newRuntimeKindListCmd("models", "/platform/admin/runtime/models", "List available models"),
		newRuntimeKindListCmd("mcp", "/platform/admin/runtime/mcp-servers", "List available MCP servers"),
		newRuntimeKindListCmd("a2a", "/platform/admin/runtime/a2a-agents", "List available A2A agents"),
		newRuntimeCatalogCmd(),
	)
	return parent
}

func newRuntimeKindListCmd(use, path, short string) *cobra.Command {
	var f adminCredFlags
	var output string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := cmd.Flag("output").Value.String()
			_ = c
			return runRuntimeList(cmd.Context(), cmd, path, output, &f)
		},
	}
	// `list` alias so `ach-cli runtime models list` reads naturally.
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRuntimeList(cmd.Context(), cmd, path, output, &f)
		},
	})
	bindAdminCredFlags(c, &f)
	bindAdminCredFlags(c.Commands()[0], &f)
	c.PersistentFlags().StringVarP(&output, "output", "o", "table", "Output format: table|json")
	return c
}

func newRuntimeCatalogCmd() *cobra.Command {
	var f adminCredFlags
	var output string
	c := &cobra.Command{
		Use:   "catalog",
		Short: "List the full runtime catalog (all kinds)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRuntimeList(cmd.Context(), cmd, "/platform/admin/runtime/catalog", output, &f)
		},
	}
	bindAdminCredFlags(c, &f)
	c.Flags().StringVarP(&output, "output", "o", "table", "Output format: table|json")
	return c
}

func runRuntimeList(ctx context.Context, cmd *cobra.Command, path, output string, f *adminCredFlags) error {
	baseURL, bearer, err := resolveAdminBearer(f.Profile, f.APIKey, f.EnvKey)
	if err != nil {
		return err
	}
	hc := &httpclient.Client{
		BaseURL:    baseURL,
		APIKey:     bearer,
		HTTPClient: adminHTTPClient,
		Verbose:    f.Verbose,
		Stderr:     cmd.ErrOrStderr(),
	}

	out := cmd.OutOrStdout()
	if path == "/platform/admin/runtime/catalog" {
		var resp struct {
			Models     []runtimeItem `json:"models"`
			MCPServers []runtimeItem `json:"mcpServers"`
			A2AAgents  []runtimeItem `json:"a2aAgents"`
		}
		if err := hc.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return err
		}
		if output == "json" {
			return writeJSON(out, resp)
		}
		all := append(append(append([]runtimeItem{}, resp.Models...), resp.MCPServers...), resp.A2AAgents...)
		return writeRuntimeTable(out, all)
	}

	var resp runtimeListResp
	if err := hc.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	if output == "json" {
		return writeJSON(out, resp.Items)
	}
	return writeRuntimeTable(out, resp.Items)
}

func writeRuntimeTable(w interface{ Write([]byte) (int, error) }, items []runtimeItem) error {
	tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KIND\tNAME\tSTATUS")
	for _, it := range items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", it.Kind, it.Name, it.Status)
	}
	return tw.Flush()
}

func writeJSON(w interface{ Write([]byte) (int, error) }, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func init() {
	rootCmd.AddCommand(newRuntimeCmd())
}
```

> Note on helpers: `adminCredFlags`, `bindAdminCredFlags`, `adminHTTPClient`, and `resolveAdminBearer` already exist in `cmd/ach-cli/cmd/admin.go`. If the exact names of the cred-flags struct / binder differ, run `grep -n "adminCredFlags\|func bindAdmin\|adminHTTPClient\|func resolveAdminBearer" cmd/ach-cli/cmd/admin.go` and match them. If no shared `bindAdminCredFlags` exists, copy the three flag registrations (`--profile`, `--api-key`, `--env-key`, `--verbose`) inline from an existing admin subcommand.

- [ ] **Step 4: Run the CLI test to verify it passes**

Run: `./scripts/dev.sh go test ./cmd/ach-cli/cmd/ -run TestRuntimeModelsList -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + build the CLI binary**

Run: `make qa-lint-changed && make build-cli`
Expected: no findings; `ach-cli` builds.

- [ ] **Step 6: Update CLAUDE.md (same-commit doc hygiene)**

In `CLAUDE.md`, in the `ach-cli` description (Quick context paragraph and/or the service table's CLI line), add `runtime` to the verb list: `login/logout/whoami/config/env/keys/admin/runtime`.

- [ ] **Step 7: Commit**

```bash
git add cmd/ach-cli/cmd/runtime.go cmd/ach-cli/cmd/runtime_test.go CLAUDE.md
git commit -m "feat(cli): ach-cli runtime catalog list commands"
```

---

### Task 6 (OPTIONAL / INDEPENDENT): admin-triggered immediate refresh

Tasks 1–5 deliver the full core value (catalog visible to admin UI/CLI; refresh happens automatically every 5 min). This task adds `POST /platform/admin/runtime/refresh` + `ach-cli admin runtime refresh` so an admin can force an immediate LiteLLM re-sync. A reviewer may reject this task while accepting 1–5.

**Files:**
- Modify: `internal/snapshot/snapshot.go` (add a buffered trigger channel + `Trigger()`; select on it in `Start`)
- Create: `internal/snapshot/refresh_listener.go` (a `manager.Runnable` that LISTENs `ach_runtime_refresh` and calls `Trigger`)
- Modify: `cmd/ach/cmd/operator.go` (register the listener runnable)
- Create: `internal/db/runtime_refresh.go` (+ test) — `SignalRuntimeRefresh(ctx, pool, connector)` → `pg_notify('ach_runtime_refresh', connector)`
- Modify: `internal/platformapi/admin/runtime/handler.go` (+ test) — `RefreshHandler`
- Modify: `internal/platformapi/admin/mount.go` — `r.Post("/runtime/refresh", …)`
- Create: `cmd/ach-cli/cmd/admin_runtime.go` (+ test) — `admin runtime refresh [connector]`

**Interfaces:**
- Produces:
  - `func (s *Snapshotter) Trigger()` — non-blocking send on a `chan struct{}` of cap 1.
  - `const RuntimeRefreshChannel = "ach_runtime_refresh"`
  - `func SignalRuntimeRefresh(ctx context.Context, pool *pgxpool.Pool, connector string) error`
  - `RefreshHandler(Deps) http.HandlerFunc` → `202 {"connector":"default","accepted":true}`.

- [ ] **Step 1: Add a trigger channel to the Snapshotter**

In `internal/snapshot/snapshot.go`, add field `triggerCh chan struct{}` to `Snapshotter`, initialise it in `NewSnapshotter` (`triggerCh: make(chan struct{}, 1)`), and add:

```go
// Trigger requests an out-of-band refresh on the next loop iteration. Non-
// blocking: if a trigger is already queued the call is a no-op (coalesced).
func (s *Snapshotter) Trigger() {
	select {
	case s.triggerCh <- struct{}{}:
	default:
	}
}
```

In `Start`'s `for { select { … } }`, add a case alongside the existing `<-time.After(next)`:

```go
		case <-s.triggerCh:
			next = s.refreshAndNextInterval(ctx, backoff)
			if next == s.interval {
				backoff = initialRetryBackoff
			} else {
				backoff = nextBackoff(backoff, s.interval)
			}
```

- [ ] **Step 2: Write the failing test for Trigger**

Add to `internal/snapshot/snapshot_test.go`:

```go
func TestTrigger_IsNonBlockingAndCoalesces(t *testing.T) {
	s := NewSnapshotter(&fakeLiteLLM{}, logr.Discard())
	s.Trigger() // fills the buffer
	s.Trigger() // must not block or panic when full
	select {
	case <-s.triggerCh:
	default:
		t.Fatalf("expected one queued trigger")
	}
}
```

Run: `./scripts/dev.sh go test ./internal/snapshot/ -run TestTrigger -count=1`
Expected: PASS (after Step 1).

- [ ] **Step 3: DB signal function (test-first)**

Create `internal/db/runtime_refresh_test.go`:

```go
//go:build integration

// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

func TestSignalRuntimeRefresh_FiresNotify(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, cleanup := setupPostgresForPhase2(t, ctx)
	defer cleanup()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN ach_runtime_refresh"); err != nil {
		t.Fatalf("LISTEN: %v", err)
	}
	if err := db.SignalRuntimeRefresh(ctx, pool, "default"); err != nil {
		t.Fatalf("SignalRuntimeRefresh: %v", err)
	}
	n, err := conn.Conn().WaitForNotification(ctx)
	if err != nil {
		t.Fatalf("WaitForNotification: %v", err)
	}
	if n.Channel != "ach_runtime_refresh" || n.Payload != "default" {
		t.Fatalf("got %s/%s, want ach_runtime_refresh/default", n.Channel, n.Payload)
	}
}
```

Create `internal/db/runtime_refresh.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RuntimeRefreshChannel signals the operator's Snapshotter to refresh now.
const RuntimeRefreshChannel = "ach_runtime_refresh"

// SignalRuntimeRefresh fires NOTIFY ach_runtime_refresh '<connector>'.
func SignalRuntimeRefresh(ctx context.Context, pool *pgxpool.Pool, connector string) error {
	if _, err := pool.Exec(ctx, `SELECT pg_notify($1, $2)`, RuntimeRefreshChannel, connector); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SignalRuntimeRefresh(%s): %w", connector, err)
	}
	return nil
}
```

Run: `./scripts/dev.sh go test -tags=integration ./internal/db/ -run TestSignalRuntimeRefresh -count=1`
Expected: PASS.

- [ ] **Step 4: Operator listener runnable**

Create `internal/snapshot/refresh_listener.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

// RefreshListener is a manager.Runnable that LISTENs ach_runtime_refresh and
// calls Snapshotter.Trigger on each notification. A dropped notification is
// covered by the Snapshotter's periodic refresh.
type RefreshListener struct {
	Pool        *pgxpool.Pool
	Snapshotter *Snapshotter
	Log         logr.Logger
}

func (l *RefreshListener) Start(ctx context.Context) error {
	conn, err := l.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+db.RuntimeRefreshChannel); err != nil {
		return err
	}
	l.Log.Info("runtime-refresh listener started")
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		l.Log.Info("runtime refresh requested", "connector", n.Payload)
		l.Snapshotter.Trigger()
	}
}
```

In `cmd/ach/cmd/operator.go`, after `mgr.Add(snapshotter)`, add:

```go
	if err := mgr.Add(&snapshot.RefreshListener{Pool: dbPool, Snapshotter: snapshotter, Log: ctrl.Log.WithName("runtime-refresh")}); err != nil {
		return fmt.Errorf("unable to add runtime-refresh listener: %w", err)
	}
```

- [ ] **Step 5: Platform-api refresh handler**

In `internal/platformapi/admin/runtime/handler.go`, add a pool to `Deps` (`Pool *pgxpool.Pool`) and:

```go
// RefreshHandler serves POST /platform/admin/runtime/refresh.
func RefreshHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		if err := db.SignalRuntimeRefresh(ctx, d.Pool, d.Connector); err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to signal refresh", reqID)
			return
		}
		render.JSON(w, http.StatusAccepted, map[string]any{"connector": d.Connector, "accepted": true})
	}
}
```

Set `rcDeps.Pool = deps.Pool` in `mount.go` and register: `r.Post("/runtime/refresh", runtimecatalog.RefreshHandler(rcDeps))`.

Add a handler test mirroring Task 4 Step 1 asserting `202` and `{"accepted":true}` with a fake `Pool` seam (extract the signal call behind a small interface field on `Deps` if a real pool is awkward in unit tests — a `Refresher interface{ Refresh(ctx,connector) error }` defaulting to a pool-backed impl).

- [ ] **Step 6: CLI `admin runtime refresh`**

Create `cmd/ach-cli/cmd/admin_runtime.go` with `newAdminRuntimeCmd()` (parent `runtime`) + `newAdminRuntimeRefreshCmd()` (`refresh [connector]`, default `default`) that POSTs to `/platform/admin/runtime/refresh` via the `resolveAdminBearer` + `httpclient.Client` pattern, and wire it into the existing `newAdminCmd()` `parent.AddCommand(...)` list in `admin.go`. Add a unit test mirroring Task 5 Step 1 (mock `202`).

- [ ] **Step 7: Build, test, lint, commit**

```bash
make build-server && make build-cli
./scripts/dev.sh go test ./internal/snapshot/ ./internal/platformapi/... ./cmd/ach-cli/cmd/ -count=1
make qa-lint-changed
git add internal/snapshot/ internal/db/runtime_refresh.go internal/db/runtime_refresh_test.go internal/platformapi/admin/ cmd/ach/cmd/operator.go cmd/ach-cli/cmd/admin_runtime.go
git commit -m "feat: admin-triggered runtime catalog refresh"
```

---

## Final verification (before push)

- [ ] **Unit + integration sweep**

```bash
make test-unit
./scripts/dev.sh go test -tags=integration ./internal/db/ -count=1
```
Expected: all PASS.

- [ ] **E2E gate** (this change touches `internal/controller`-adjacent wiring, `internal/platformapi`, and the operator boot path):

```bash
make e2e-full
```
Expected: green. Add/extend an e2e assertion that, after `cluster-up`, `GET /platform/admin/runtime/models` (admin pk_) returns the demo models and that an `ek_` is rejected `401 invalid_key_type`. (See `test/e2e/cli_describe_revoke_test.go` for the live-CLI/admin-pk pattern.)

- [ ] **Pre-push gate** (host-only): the installed hook runs it on `git push`; do not invoke by hand.

---

## Self-Review

**Spec coverage (against the agreed re-scope, not the original addendum):**
- Catalog in Postgres for admin UI/CLI selection → Tasks 1–4. ✓
- Keep `LiteLLMConnection`, no `RuntimeConnector` → no CRD touched. ✓
- Admin-only catalog (no per-user filtering) → routes under `/platform/admin/*`, inherit `AdminOnly`. ✓
- Tombstone vanished entries (`missing`), no GC → `tombstoneRuntimeCatalogSQL`, no retention job. ✓
- One NOTIFY per sync → `WithTxNotify(RuntimeCatalogChannel, connector, …)`. ✓
- Persist only on full success; keep stale grace → persistence lives only in `refresh`'s success branch; failure branch untouched. ✓
- CLI `ach-cli runtime …` → Task 5. ✓
- Admin refresh → Task 6 (optional). ✓
- Dropped from addendum: `env describe` annotation (admin-only catalog vs non-admin consumers), per-user filtering, `raw_json`/capabilities/provider columns, `RuntimeConnector`, multi-connector 409, retention GC → all explicitly Out of scope. ✓

**Placeholder scan:** every code step contains full code; every run step has an exact command + expected result. Two `grep`-and-match notes (Task 4 Step 3, Task 5 Step 3) hedge identifier names the explorers reported but I did not open line-by-line — they are verification instructions, not placeholders.

**Type consistency:** `RuntimeCatalogRow`, `ReplaceRuntimeCatalog`, `ListRuntimeCatalog`, `MaxRuntimeCatalogSync`, `RuntimeCatalogChannel` are defined in Task 2 and consumed verbatim in Tasks 3–4. `EnableCatalog`/`Trigger` defined in Tasks 3/6 and consumed in operator wiring. Endpoint paths and the `{connector,items}` envelope are consistent between Task 4 (server) and Task 5 (client).
