# Postgres as Source of Truth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Every shell command runs from `/home/coder/workspace/local/ach`. Go commands MUST be wrapped in `./scripts/dev.sh` (host has no Go) — see "Toolchain" in `CLAUDE.md`.

**Goal:** Make Postgres the sole source of truth for every ACH service except the operator. The operator becomes a CR-to-Postgres adapter (one writer among several future writers); platform-api, forwarder, and content-service stop touching the Kubernetes API for `ach.ackstorm.ai/v1alpha1` CRDs entirely. The downstream surface is origin-agnostic so a future UI can write directly to Postgres, with CR-origin rows tagged read-only relative to the UI.

**Architecture:**
- Every projection row carries two new columns:
  - `origin TEXT NOT NULL CHECK (origin IN ('cr','ui'))` — provenance: who wrote this row. Operator writes always set `origin='cr'`; a future UI will set `origin='ui'`.
  - `locked BOOLEAN NOT NULL DEFAULT FALSE` — mutability flag: is this row read-only to non-origin writers? Operator writes always set `locked=true` (CR-origin rows are never user-editable from the UI). UI inserts default to `locked=false`. The UI uses this single field to grey out edit controls.
- The DB-level cross-writer guard sits on `origin` (the `ON CONFLICT (...) DO UPDATE ... WHERE existing.origin = excluded.origin` clause). `locked` is informational and consulted by handler code (future UI endpoints) to refuse edits early.
- The `(namespace, name)` PK is unchanged — first writer claims the name; cross-origin collisions surface as a write error (logged as a status condition for CR-side conflicts).
- New projection tables: `litellm_connections` (one row, `default`), `backend_identity_policies` (indexed by `(target_kind, target_name)`).
- The operator's existing reconcilers (Environment, Plugin, Prompt, Artifact, PluginMarketplace) already write to Postgres; this plan adds the two missing kinds (LiteLLMConnection, BackendIdentityPolicy) and a 5-minute periodic full-resync runnable so all projections are reconciled even when no event fires.
- After every operator write, the operator emits `NOTIFY ach_<table>_changed '<namespace>/<name>'`. Forwarder and platform-api hold long-lived `pgx` LISTEN sessions and update their in-memory caches on receipt. Each cache also does a full Postgres-list refresh every 5 minutes as a safety net for missed notifications (reconnects, network blips).
- Platform-api's `POST /platform/admin/refresh` no longer patches the CR annotation. It writes `force_refresh_requested_at = now()` to `external_refs` / `marketplace_plugins` and fires `NOTIFY ach_refresh '<kind>/<name>'`. The operator's LISTEN session enqueues a reconcile for the named CR.
- Secrets stay as Kubernetes Secrets. The forwarder still reads `ach-jwt-signing-keys` (JWT signer seed) and the LiteLLM master-key Secret from the K8s API — only the CRD reads move to Postgres. The Helm RBAC strips ACH CRD verbs from the non-operator ServiceAccounts but keeps `secrets get/watch` (filtered by name).

**Tech Stack:** Go (controller-runtime, `pgx/v5`, `pgxpool`) · golang-migrate `db/migrations/` (next number `000005`) · stdlib `net/http/httptest` for unit tests · controller envtest for projection writes · pgxpool-backed table tests (`internal/db/*_test.go`) · `scripts/cluster.sh` for e2e.

**Issue reference:** [ackstorm/ach#34](https://github.com/ackstorm/ach/issues/34) — Migrate to Postgres as source of truth (operator stays sole CR watcher).

---

## Context & Analysis

What already works (verified via exploration agents 2026-05-28):

| Component | Current k8s footprint | Postgres footprint today |
|-----------|------------------------|--------------------------|
| Operator | Reconciles 7 CRD Kinds (Environment, Plugin, Prompt, Artifact, PluginMarketplace, LiteLLMConnection, BackendIdentityPolicy) | Writes 5 of 7 projections: `environments`, `plugins`, `prompts`, `artifacts`, `marketplace_plugins`, plus fetcher-state `external_refs` |
| Content Service | **Zero** (only a dead `ctrl` import in `cmd/ach/cmd/content_service.go`) | Reads `environments`, `plugins`, `prompts`, `artifacts` |
| Platform API | Cached `client.Client` over informers for: `Environment` (List/Get), `Plugin/Prompt/Artifact/PluginMarketplace` (Get + Patch via `/admin/refresh`), plus `Secret` and `BackendIdentityPolicy` registered but never read | Already writes/reads `personal_keys`, `environment_keys` |
| Forwarder | Informer-backed cache for `BackendIdentityPolicy` (field-indexed at request time), `Environment` (Get + List on every precheck), `Secret` (boot Get + hot-reload), uncached APIReader Get for `LiteLLMConnection` at boot | None at request time; `pgxpool` initialized for keystore resolver |

What's missing for the migration:
1. No Postgres projection for `BackendIdentityPolicy` — forwarder reads it via informer.
2. No Postgres projection for `LiteLLMConnection` — forwarder reads it via uncached APIReader at boot.
3. No `db.ListEnvironments` helper — forwarder precheck `List` path needs it.
4. No event-driven invalidation — operator writes Postgres but never emits `NOTIFY`; forwarder/platform-api can't subscribe to projection changes.
5. No origin/provenance metadata — schema can't distinguish CR-origin rows from a future UI's rows.
6. Platform-api's `POST /admin/refresh` still routes through the CR annotation, requiring k8s write access.

These six gaps are exactly what this plan closes.

## Design Decisions (commit and call out for review)

1. **Two columns, two roles: `origin` + `locked`.**
   - `origin TEXT` (`'cr'|'ui'`) is the provenance label — who created this row. Extends naturally to future writers (`'api'`, `'sync'`).
   - `locked BOOLEAN` is the mutability flag — is this row read-only to non-origin writers? Operator-written rows are always `locked=true`; UI-written rows are always `locked=false`. The UI uses this single field to decide whether to render edit controls, without needing to know the operator-origin rule.
   - The two are coupled but serve different audiences: `origin` is for the DB-level cross-writer guard; `locked` is for handler/UI-layer UX gating. We could derive one from the other today, but keeping both makes future divergence (e.g., a CR-origin row that the operator explicitly marks `locked=false` for some hand-off scenario) a non-migration change.
2. **Single PK on `(namespace, name)`.** No composite key with `origin`. First writer claims the name. Cross-origin collisions are surfaced as errors. This avoids the cognitive overhead of two records sharing a name.
3. **Operator-scoped upserts.** `db.UpsertX` (existing, all consumed by operator) becomes origin-aware: `INSERT ... (origin, locked) VALUES (..., 'cr', true) ON CONFLICT (namespace, name) DO UPDATE SET ... WHERE existing.origin = 'cr'`. If the existing row has `origin='ui'`, the UPDATE skips and the operator surfaces a `Synced=False reason=ConflictWithUIRow` condition. UI-side writers (out of scope, future plan) get the symmetric helper that asserts `existing.origin = 'ui' AND existing.locked = false`.
4. **pgx `LISTEN/NOTIFY` for event-driven cache invalidation.** A shared `internal/db/listen.go` runs the LISTEN loop with auto-reconnect; consumers register `(channel, handler)` pairs. Operator emits `NOTIFY` after every projection write. Forwarder + platform-api subscribe.
5. **5-minute periodic full-refresh as safety net.** Every consumer cache also does a `time.Tick(5*time.Minute)` full-list refresh from Postgres. Catches missed notifications during LISTEN reconnects. Matches the operator's existing Snapshotter cadence.
6. **Secrets stay in k8s.** The user's mandate is "no CR access for non-operator components". Secrets are not CRDs. The forwarder keeps its filtered Secret informer for `ach-jwt-signing-keys` hot-reload; the LiteLLM master-key Secret stays in k8s and is referenced from the Postgres-projected `LiteLLMConnection` row via `master_key_secret_namespace/name/key` columns.
7. **CR status writes stay.** Operators still get `kubectl describe environment X` operational visibility. Postgres is for application reads; CR status is for k8s-native operations.
8. **No backwards-compatibility shims.** This is a structural change; we don't keep a "k8s read-back" fallback path in platform-api or forwarder. The flag day is when Phases B and C ship together (Helm RBAC trim is the gate).

## Future UI (out of scope for this plan)

This plan does NOT build the UI. It sets up the schema and write protocol so that a future plan can add UI handlers that:
- INSERT rows with `origin='ui'`, `locked=false`.
- READ all rows uniformly (CR + UI), then check `row.locked` to decide whether to render edit controls — a single-field check, no need for the UI code to know the operator-origin rule.
- REJECT update/delete requests for any row with `locked=true` at the handler boundary (returns 403 "row is read-only"). The DB-level guard (origin mismatch on ON CONFLICT) is a secondary defense if the handler check is bypassed.
- Have a parallel `force_refresh` semantic that, for `origin='ui'` rows, means "re-upload via UI" rather than "re-fetch upstream" (handler-specific; UI rows have no fetcher state in `external_refs`). `db.SetForceRefresh` already rejects this case with `ErrUIOriginRefreshUnsupported`.

Phase B's platform-api work touches `/admin/refresh` in a way that's already aware of origin: it rejects force-refresh for `origin='ui'` rows with `400 invalid_argument` (`"UI-managed resource has no upstream to refresh"`).

## Revision 1 — Review Findings Folded In

This plan was peer-reviewed twice before execution (2026-05-28). The following corrections were folded into the tasks below. Each finding lists the affected task(s) and a one-line summary; full mechanics live in the task bodies.

**This plan doubles as the `docs/proposals/` proposal record.** `CONTRIBUTING.md:29-38` requires a proposal for "significant controller behavior change"; the `docs/proposals/` directory does not exist in this repo today (inherited language from the alitellm graft). Treat this plan as the proposal; if `docs/proposals/` is created later, symlink or copy this file there.

**High-severity correctness:**

1. **NOTIFY-without-transaction race (A1, A7, A8, A9; new A2b).** Original plan had `db.UpsertX` and `db.Emit` as two separate `pool.Exec` calls on different conns. NOTIFY delivers on Exec, not on the Upsert's commit; consumer can wake and SELECT a pre-update snapshot. Fix: introduce `db.WithTxNotify(ctx, pool, channel, payload, func(tx pgx.Tx) error)` in a new Task A2b that runs the projection write and `pg_notify` inside a single `BEGIN/COMMIT`. Postgres queues NOTIFYs inside a tx and only fires them post-commit, after writes are visible. Every Upsert call site refactors through this helper.
2. **BIP `Resolve` semantics drop the opt-out rule (C1, C4).** `internal/forwarder/bip.ResolveWinner` returns `nil` if the alpha-LAST BIP is `forwardIdentityJWT=false` (explicit opt-out — see `bip/index_test.go` B4/B6). The original `bipcache.Resolve` returned the row regardless. Fix: `bipcache.Resolve` returns `nil` when the alpha-LAST winner is opt-out, preserving today's semantics 1:1. Tests B4 and B6 (opt-out wins last) ported into `bipcache_test.go`.
3. **controller-runtime workqueue access is not public (A10, A11).** Original plan grabbed `workqueue.RateLimitingInterface` per controller after registration; that's not exposed by controller-runtime. Fix: switch to the supported `source.Channel{Source: <-chan event.GenericEvent}` pattern. Each controller's `Watches` block adds a `source.Channel`; the Resync runnable and `refreshsignal.Listener` push `event.GenericEvent` into the per-Kind channel to trigger reconcile.
4. **Migration backfill race (A1).** 000005 does `ALTER ADD origin/locked` → `UPDATE SET locked=TRUE WHERE origin='cr'` → `ALTER ADD CONSTRAINT cr_locked_chk`. If the operator inserts a `(origin='cr', locked=FALSE)` row between the UPDATE and the constraint ADD, constraint creation fails. Fix: wrap the migration in a single explicit `BEGIN/COMMIT` (golang-migrate runs each .up.sql in a tx by default but call it out so reviewers don't strip it), AND add an explicit prereq step to scale operator to 0 before applying.
5. **Master-key Secret cross-namespace RBAC (C7).** Forwarder SA lives in `ach-system`; LiteLLMConnection points at `litellm-system/litellm-master-key`. A namespace-scoped Role in `ach-system` with `resourceNames: [litellm-master-key]` cannot grant access to a Secret in `litellm-system`. Fix: split into a Role in `ach-system` (for `ach-jwt-signing-keys`) plus a Role + cross-namespace RoleBinding in `litellm-system` (for the master-key Secret), with the latter's name+namespace driven by Helm values mirroring `LiteLLMConnection.spec.masterKeySecretRef`.

**Medium-severity correctness:**

6. **Listener parks a pooled conn forever (A3).** `pool.Acquire(ctx)` held for the process lifetime starves the pool. Fix: open a dedicated `pgx.Conn` via `pgx.Connect(ctx, connString)` for each Listener; the conn lives outside the pool and survives reconnect via the existing backoff loop.
7. **`db.EnvironmentRow` impedance with hydrate handlers (new B0, B1).** `internal/platformapi/hydrate/handler.go` converts `achv1alpha1.RuntimeBlock`/`ContextBlock` (nested CR types) into the hydrate view. Flat `EnvironmentRow` columns don't shape the same way. Fix: add **Task B0** (`internal/platformapi/store/adapter.go` + tests) that converts `*db.EnvironmentRow` → an internal struct the hydrate handler already consumes. B1 then depends on B0.

**Test coverage (raised by the second review):**

8. **Backfill assertion in A1.** 000005 down→up cycle on a seeded DB must leave existing rows at `(origin='cr', locked=TRUE)`. Added as a sub-test of A1.
9. **`ConflictWithUIRow` status condition envtest (A7, A8).** Plan tested the DB error path but not the resulting `Synced=False reason=ConflictWithUIRow` condition on the CR. Added envtest assertions per controller.
10. **Missed-NOTIFY recovery (A3).** New unit test: stop the Listener's conn mid-flight, fire a NOTIFY during the dropped window, restart the conn, assert that the 5m periodic refresh path (in C1/C2) eventually catches up. (Listener itself doesn't replay; the consumer's periodic refresh does — the test asserts that contract.)
11. **Cold-restart smoke in D4.** Sharper than "scale operator to 0 and curl platform-api": scale operator to 0, then restart platform-api, then curl. Proves platform-api boots Postgres-only without ever talking to the k8s API for CRs.

**Process / metadata:**

12. **GH issue #34 needs labels** (e.g., `enhancement`, `area: operator`, `area: forwarder`, `area: platform-api`). Out-of-band from this plan; flagged for the PR that closes the issue.

## File Structure

```
db/migrations/
├── 000005_origin_column.up.sql            CREATE: ALTER existing 6 projection tables + external_refs/marketplace_plugins to add `origin` column with default 'cr', backfill, add CHECK
├── 000005_origin_column.down.sql          CREATE: drop the column
├── 000006_litellm_connections.up.sql      CREATE: new table for projected LiteLLMConnection spec
├── 000006_litellm_connections.down.sql
├── 000007_backend_identity_policies.up.sql CREATE: new table + index on (target_kind, target_name)
└── 000007_backend_identity_policies.down.sql

internal/db/
├── litellm_connections.go            CREATE: UpsertLiteLLMConnection, GetDefaultLiteLLMConnection, SoftDelete, Delete
├── litellm_connections_test.go       CREATE: pgxpool table tests
├── backend_identity_policies.go      CREATE: UpsertBIP, GetBIPByName, ListBIPsByTarget, ListAllBIPs, SoftDelete, Delete
├── backend_identity_policies_test.go CREATE: pgxpool table tests
├── environments.go                   MODIFY: add ListEnvironments(ctx, pool, ns); rewrite UpsertEnvironment to filter ON CONFLICT WHERE origin='cr'
├── environments_test.go              MODIFY: add ListEnvironments coverage + origin-conflict test
├── plugins.go                        MODIFY: ON CONFLICT WHERE origin='cr'
├── prompts.go                        MODIFY: ON CONFLICT WHERE origin='cr'
├── artifacts.go                      MODIFY: ON CONFLICT WHERE origin='cr'
├── marketplace_plugins.go            MODIFY: ON CONFLICT WHERE origin='cr'
├── external_refs.go                  MODIFY: ON CONFLICT WHERE origin='cr'; add SetForceRefresh helper for platform-api
├── refresh_signal.go                 CREATE: SetForceRefresh(kind, name) for plugin/prompt/artifact/marketplace_plugin from platform-api; emits NOTIFY ach_refresh
├── refresh_signal_test.go            CREATE: assert column write + NOTIFY received via listener
├── notify.go                         CREATE: Emit(ctx, pool, channel, payload) — wraps pg_notify
├── notify_test.go                    CREATE: assert NOTIFY delivered to a LISTEN subscriber
├── listen.go                         CREATE: Listener type — Subscribe(channel, handler), Start(ctx), reconnect-loop
└── listen_test.go                    CREATE: assert handler called on NOTIFY; assert reconnect after dropped conn

internal/controller/ach/
├── litellmconnection_controller.go   MODIFY: write spec+endpoint+secretRef to Postgres on reconcile; SoftDelete on finalizer; emit NOTIFY
├── litellmconnection_controller_test.go MODIFY: envtest asserts the Postgres row appears
├── backendidentitypolicy_controller.go MODIFY: write spec to Postgres on reconcile (the controller stops being a pure finalizer); SoftDelete on finalizer; emit NOTIFY
├── backendidentitypolicy_finalizer_test.go  MODIFY: assert Postgres row mutates on CR change
├── environment_controller.go         MODIFY: emit NOTIFY after Postgres write (one-line addition next to existing UpsertEnvironment call)
├── plugin_controller.go              MODIFY: emit NOTIFY after Postgres write
├── prompt_controller.go              MODIFY: emit NOTIFY after Postgres write
├── artifact_controller.go            MODIFY: emit NOTIFY after Postgres write
└── pluginmarketplace_controller.go   MODIFY: emit NOTIFY after Postgres write

internal/operator/
└── refreshsignal/
    ├── listener.go                   CREATE: LISTEN ach_refresh; enqueue reconcile.Request for the named CR
    ├── listener_test.go              CREATE: assert NOTIFY → workqueue entry
    └── doc.go                        CREATE: package doc

internal/operator/resync/
├── runnable.go                       CREATE: Runnable that re-Lists every ACH CRD every 5 minutes and re-enqueues to ensure projection convergence even when no event fires
└── runnable_test.go                  CREATE: envtest assertion

internal/platformapi/
├── store/store.go                    REWRITE: replace controller-runtime client.Client with pgxpool; methods become db.GetEnvironmentByName / db.ListEnvironments calls; condition lookup reads `available_condition` / `access_group_synced_condition` JSONB columns
├── store/store_test.go               REWRITE: pgxpool table tests
└── admin/handler.go                  MODIFY: ForceRefreshHandler no longer Get+Patch the CR; calls db.SetForceRefresh(kind, name) + db.Emit("ach_refresh", payload). Add origin guard (400 if row origin='ui').

internal/forwarder/
├── bipcache/                         CREATE: Postgres-backed BIP cache with NOTIFY invalidation + 5m periodic resync
│   ├── cache.go
│   ├── cache_test.go
│   └── doc.go
├── envstore/                         CREATE: Postgres-backed Environment cache with NOTIFY invalidation + 5m periodic resync
│   ├── store.go
│   ├── store_test.go
│   └── doc.go
├── litellmconn/                      MODIFY (rewrite Resolve): read endpoint + master-key-secret-ref from Postgres; resolve Secret value via k8s (unchanged)
│   ├── resolver.go
│   └── resolver_test.go
├── proxy/handlers.go                 MODIFY: ResolveWinner call switches from internal/forwarder/bip → internal/forwarder/bipcache
└── precheck/check.go                 MODIFY: Get + List Environment switch from K8sClient → envstore

internal/forwarder/bip/               DELETE: replaced by bipcache (kept only until C5 lands, then deleted in D2)

cmd/ach/cmd/
├── platform_api.go                   MODIFY: drop controller-runtime manager + informer registrations; wire Postgres-backed store; start db.Listener for ach_<resource>_changed channels (if/when needed — not in MVP)
├── forwarder.go                      MODIFY: keep controller-runtime manager but ONLY for Secret informer; drop LiteLLMConnection APIReader call; drop BIP + Environment informer registrations; wire bipcache + envstore + db.Listener
├── operator.go                       MODIFY: wire internal/operator/refreshsignal.Listener; wire internal/operator/resync.Runnable
└── content_service.go                MODIFY: remove the dead `ctrl` import

deploy/helm/ach/templates/
├── platform-api-rbac.yaml            MODIFY: drop ach.ackstorm.ai/* verbs; keep `secrets get/watch` filtered by name (if used at all by platform-api — verify)
└── forwarder-rbac.yaml               MODIFY: drop ach.ackstorm.ai/* verbs; keep `secrets get/watch` filtered to `ach-jwt-signing-keys` + `litellm-master-key`

CLAUDE.md                              MODIFY: update architecture diagram (operator → Postgres ← platform-api/forwarder/content-service); update "Common failure modes" with origin-conflict pattern
```

---

## Phase A — Schema + Operator Projections

### Task A1: Add `origin` column to existing projection tables

**Files:**
- Create: `db/migrations/000005_origin_column.up.sql`
- Create: `db/migrations/000005_origin_column.down.sql`
- Modify: `internal/db/environments.go` (Upsert SQL → ON CONFLICT WHERE origin='cr')
- Modify: `internal/db/plugins.go` (same)
- Modify: `internal/db/prompts.go` (same)
- Modify: `internal/db/artifacts.go` (same)
- Modify: `internal/db/marketplace_plugins.go` (same)
- Modify: `internal/db/external_refs.go` (same)
- Test: `internal/db/environments_test.go` (add cross-origin conflict assertion)

- [ ] **Step 1: Write the failing test (origin-conflict on Environment)**

Add to `internal/db/environments_test.go`:

```go
func TestUpsertEnvironment_OriginConflict_UIRowBlocksCRUpdate(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	// Pre-seed a UI-origin row directly.
	_, err := pool.Exec(ctx, `
		INSERT INTO environments (namespace, name, authorized_teams, context_prompts,
		    context_plugins, context_artifacts, runtime_models, runtime_mcp_servers,
		    runtime_a2a_agents, available_condition, access_group_synced_condition,
		    execution_resources_resolved_condition, resource_version, origin)
		VALUES ('ach-system','demo','{}','{}','{}','{}','{}','{}','{}','{}','{}','{}','rv-ui','ui')`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Operator attempts to upsert a CR-origin row with the same name.
	row := EnvironmentRow{
		Namespace: "ach-system",
		Name:      "demo",
		ResourceVersion: "rv-cr",
		// ... other fields elided; helper sets them to empty
	}
	err = UpsertEnvironment(ctx, pool, row)
	if !errors.Is(err, ErrOriginConflict) {
		t.Fatalf("expected ErrOriginConflict, got %v", err)
	}

	// Verify the UI row is untouched.
	var rv string
	if err := pool.QueryRow(ctx,
		`SELECT resource_version FROM environments WHERE namespace=$1 AND name=$2`,
		"ach-system", "demo").Scan(&rv); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if rv != "rv-ui" {
		t.Fatalf("UI row clobbered: got resource_version=%q want rv-ui", rv)
	}
}
```

Also add `ErrOriginConflict` to `internal/db/errors.go`:

```go
// ErrOriginConflict is returned when an Upsert from one origin would overwrite
// a row owned by a different origin (e.g. operator UPSERT against a UI-owned row).
var ErrOriginConflict = errors.New("db: origin conflict — row owned by different origin")
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: FAIL — `ErrOriginConflict` doesn't exist; the existing Upsert SQL has no origin gate.

- [ ] **Step 3: Write the migration SQL**

> **Prereq (revision 1):** Apply this migration with the operator scaled to 0 to remove the race window in which an operator INSERT lands a `(origin='cr', locked=FALSE)` row between the backfill UPDATE and the `cr_locked_chk` ADD. golang-migrate runs each `.up.sql` in a single transaction by default; the explicit `BEGIN; ... COMMIT;` below makes the atomicity contract visible to reviewers and is a no-op if the driver already opens one. In `make migrate-up`, document the scale-down/scale-up sequencing. Production rollout note: `kubectl -n ach-system scale deploy/ach-operator --replicas=0 && make migrate-up && kubectl -n ach-system scale deploy/ach-operator --replicas=1`.

`db/migrations/000005_origin_column.up.sql`:

```sql
BEGIN;

-- 000005: origin + locked columns for source-of-truth coexistence
--   origin TEXT  ('cr'|'ui'): provenance — who wrote the row.
--   locked BOOLEAN          : mutability — is this row read-only to a
--                             non-origin writer? Operator-written rows
--                             are always locked=true; UI inserts default
--                             to locked=false.
-- Cross-writer DB guard sits on origin via ON CONFLICT (...) WHERE
-- existing.origin = excluded.origin. The locked column is consulted by
-- handler/UI-layer code to refuse edits early. Existing rows are
-- backfilled (origin='cr', locked=true) since the operator is currently
-- the only writer.

ALTER TABLE environments        ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE plugins             ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE prompts             ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE artifacts           ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE external_refs       ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE marketplace_plugins ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;

-- Backfill: every existing row was written by the operator, so it's
-- CR-origin AND locked.
UPDATE environments        SET locked = TRUE WHERE origin = 'cr';
UPDATE plugins             SET locked = TRUE WHERE origin = 'cr';
UPDATE prompts             SET locked = TRUE WHERE origin = 'cr';
UPDATE artifacts           SET locked = TRUE WHERE origin = 'cr';
UPDATE external_refs       SET locked = TRUE WHERE origin = 'cr';
UPDATE marketplace_plugins SET locked = TRUE WHERE origin = 'cr';

ALTER TABLE environments        ADD CONSTRAINT environments_origin_chk        CHECK (origin IN ('cr','ui'));
ALTER TABLE plugins             ADD CONSTRAINT plugins_origin_chk             CHECK (origin IN ('cr','ui'));
ALTER TABLE prompts             ADD CONSTRAINT prompts_origin_chk             CHECK (origin IN ('cr','ui'));
ALTER TABLE artifacts           ADD CONSTRAINT artifacts_origin_chk           CHECK (origin IN ('cr','ui'));
ALTER TABLE external_refs       ADD CONSTRAINT external_refs_origin_chk       CHECK (origin IN ('cr','ui'));
ALTER TABLE marketplace_plugins ADD CONSTRAINT marketplace_plugins_origin_chk CHECK (origin IN ('cr','ui'));

-- Belt-and-suspenders: a CR-origin row must always be locked. Catches
-- accidental hand-edits of the operator's projection.
ALTER TABLE environments        ADD CONSTRAINT environments_cr_locked_chk        CHECK (origin <> 'cr' OR locked = TRUE);
ALTER TABLE plugins             ADD CONSTRAINT plugins_cr_locked_chk             CHECK (origin <> 'cr' OR locked = TRUE);
ALTER TABLE prompts             ADD CONSTRAINT prompts_cr_locked_chk             CHECK (origin <> 'cr' OR locked = TRUE);
ALTER TABLE artifacts           ADD CONSTRAINT artifacts_cr_locked_chk           CHECK (origin <> 'cr' OR locked = TRUE);
ALTER TABLE external_refs       ADD CONSTRAINT external_refs_cr_locked_chk       CHECK (origin <> 'cr' OR locked = TRUE);
ALTER TABLE marketplace_plugins ADD CONSTRAINT marketplace_plugins_cr_locked_chk CHECK (origin <> 'cr' OR locked = TRUE);

COMMIT;
```

`db/migrations/000005_origin_column.down.sql`:

```sql
ALTER TABLE environments        DROP CONSTRAINT IF EXISTS environments_cr_locked_chk;
ALTER TABLE plugins             DROP CONSTRAINT IF EXISTS plugins_cr_locked_chk;
ALTER TABLE prompts             DROP CONSTRAINT IF EXISTS prompts_cr_locked_chk;
ALTER TABLE artifacts           DROP CONSTRAINT IF EXISTS artifacts_cr_locked_chk;
ALTER TABLE external_refs       DROP CONSTRAINT IF EXISTS external_refs_cr_locked_chk;
ALTER TABLE marketplace_plugins DROP CONSTRAINT IF EXISTS marketplace_plugins_cr_locked_chk;

ALTER TABLE environments        DROP CONSTRAINT IF EXISTS environments_origin_chk;
ALTER TABLE plugins             DROP CONSTRAINT IF EXISTS plugins_origin_chk;
ALTER TABLE prompts             DROP CONSTRAINT IF EXISTS prompts_origin_chk;
ALTER TABLE artifacts           DROP CONSTRAINT IF EXISTS artifacts_origin_chk;
ALTER TABLE external_refs       DROP CONSTRAINT IF EXISTS external_refs_origin_chk;
ALTER TABLE marketplace_plugins DROP CONSTRAINT IF EXISTS marketplace_plugins_origin_chk;

ALTER TABLE environments        DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
ALTER TABLE plugins             DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
ALTER TABLE prompts             DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
ALTER TABLE artifacts           DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
ALTER TABLE external_refs       DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
ALTER TABLE marketplace_plugins DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
```

- [ ] **Step 3b (revision 1): Add backfill assertion test**

Add to `internal/db/environments_test.go`:

```go
func TestMigration000005_BackfillsExistingRowsAsCRLocked(t *testing.T) {
	// Provision a pool that has run 000004 but NOT 000005, seed a pre-migration
	// row, then apply 000005, then assert (origin='cr', locked=TRUE).
	pool := newTestPoolAtRevision(t, "000004")
	defer pool.Close()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO environments (namespace, name, resource_version)
		VALUES ('ach-system','pre-migration','rv-1')`)
	require.NoError(t, err)
	require.NoError(t, applyMigration(t, pool, "000005"))

	var origin string
	var locked bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT origin, locked FROM environments WHERE name='pre-migration'`).Scan(&origin, &locked))
	require.Equal(t, "cr", origin)
	require.True(t, locked)
}
```

If `newTestPoolAtRevision` / `applyMigration` don't yet exist in the test harness, add them as part of this task — they're useful for every future migration test.

- [ ] **Step 4: Rewrite each Upsert SQL with origin gate**

> **Revision 1:** The Upsert SQL change in this step is the SQL-only piece. The transactional `pg_notify` coupling is added later in Task A2b — every Upsert call site refactors there to use `db.WithTxNotify`. Do NOT add `db.Emit` calls in this task; the next-task refactor is what couples them.

In `internal/db/environments.go`, replace the existing `UpsertEnvironment` SQL body. The pattern (applies identically to plugins/prompts/artifacts/marketplace_plugins/external_refs — repeat per file):

```go
const upsertEnvironmentSQL = `
INSERT INTO environments
    (namespace, name, authorized_teams, context_prompts, context_plugins,
     context_artifacts, runtime_models, runtime_mcp_servers, runtime_a2a_agents,
     available_condition, access_group_synced_condition,
     execution_resources_resolved_condition, resource_version, updated_at,
     origin, locked)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,now(),'cr',TRUE)
ON CONFLICT (namespace, name)
DO UPDATE SET
    authorized_teams                        = EXCLUDED.authorized_teams,
    context_prompts                         = EXCLUDED.context_prompts,
    context_plugins                         = EXCLUDED.context_plugins,
    context_artifacts                       = EXCLUDED.context_artifacts,
    runtime_models                          = EXCLUDED.runtime_models,
    runtime_mcp_servers                     = EXCLUDED.runtime_mcp_servers,
    runtime_a2a_agents                      = EXCLUDED.runtime_a2a_agents,
    available_condition                     = EXCLUDED.available_condition,
    access_group_synced_condition           = EXCLUDED.access_group_synced_condition,
    execution_resources_resolved_condition  = EXCLUDED.execution_resources_resolved_condition,
    resource_version                        = EXCLUDED.resource_version,
    updated_at                              = now(),
    locked                                  = TRUE  -- CR rows are always locked
WHERE environments.origin = 'cr'
RETURNING namespace
`

func UpsertEnvironment(ctx context.Context, pool *pgxpool.Pool, row EnvironmentRow) error {
	var ns string
	err := pool.QueryRow(ctx, upsertEnvironmentSQL, /* ...positional args... */).Scan(&ns)
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT WHERE clause filtered the row out → existing row is non-CR.
		return ErrOriginConflict
	}
	return err
}
```

(Repeat the same `WHERE <table>.origin = 'cr'` + `RETURNING` + `ErrNoRows → ErrOriginConflict` pattern for `UpsertPlugin`, `UpsertPrompt`, `UpsertArtifact`, `UpsertMarketplacePlugin`, `UpsertExternalRef`.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: PASS — origin-conflict test green; all existing tests still green (existing rows are backfilled to `origin='cr'`, so the operator's writes behave identically).

- [ ] **Step 6: Commit**

```bash
git add db/migrations/000005_origin_column.up.sql \
        db/migrations/000005_origin_column.down.sql \
        internal/db/environments.go internal/db/environments_test.go \
        internal/db/plugins.go internal/db/prompts.go internal/db/artifacts.go \
        internal/db/marketplace_plugins.go internal/db/external_refs.go \
        internal/db/errors.go
git commit -m "feat(db): add origin column to projection tables for UI coexistence"
```

---

### Task A2: Postgres NOTIFY emit helper

**Files:**
- Create: `internal/db/notify.go`
- Create: `internal/db/notify_test.go`

- [ ] **Step 1: Write the failing test**

`internal/db/notify_test.go`:

```go
package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmit_DeliversNotification(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Subscribe via a dedicated conn from the pool.
	listenConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer listenConn.Release()
	if _, err := listenConn.Exec(ctx, `LISTEN ach_test_chan`); err != nil {
		t.Fatalf("LISTEN: %v", err)
	}

	// Emit on a different conn.
	if err := Emit(ctx, pool, "ach_test_chan", "ach-system/demo"); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	n, err := listenConn.Conn().WaitForNotification(ctx)
	if err != nil {
		t.Fatalf("WaitForNotification: %v", err)
	}
	if n.Channel != "ach_test_chan" || n.Payload != "ach-system/demo" {
		t.Fatalf("got channel=%q payload=%q", n.Channel, n.Payload)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: FAIL — `Emit` does not exist.

- [ ] **Step 3: Implement Emit**

`internal/db/notify.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Emit fires a Postgres NOTIFY on the given channel with the given payload.
// Payload MUST be a printable string ≤ 8000 bytes (Postgres limit).
//
// Channel name is interpolated unquoted into the NOTIFY statement, so callers
// MUST pass a constant identifier — this helper rejects channel names that
// aren't pure [a-z0-9_]+ at runtime to prevent injection.
func Emit(ctx context.Context, pool *pgxpool.Pool, channel, payload string) error {
	if !validChannel(channel) {
		return fmt.Errorf("db.Emit: invalid channel name %q", channel)
	}
	// pg_notify(text, text) is the parameterised form (NOTIFY <chan>, '<payload>'
	// cannot accept a $1 parameter for the channel).
	_, err := pool.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, payload)
	if err != nil {
		return fmt.Errorf("db.Emit(%s): %w", channel, err)
	}
	return nil
}

func validChannel(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/notify.go internal/db/notify_test.go
git commit -m "feat(db): add Emit helper for Postgres NOTIFY"
```

---

### Task A2b: `db.WithTxNotify` — transaction-coupled NOTIFY helper (revision 1)

**Why this task exists:** The two-Exec sequence `UpsertX(...); Emit(...)` has a race. NOTIFY fires on Exec, not on the Upsert's commit; a consumer can wake on the NOTIFY and SELECT a snapshot that doesn't yet see the Upsert. Postgres' contract is that NOTIFY fires inside a transaction are queued and only delivered at COMMIT — AND only after the same transaction's writes are visible to other backends. Every projection-mutating write in this plan goes through `db.WithTxNotify`.

The bare `db.Emit` (Task A2) is retained for one-off use cases that don't have a write to couple — specifically, the platform-api `/admin/refresh` handler emits `ach_refresh` after `UPDATE external_refs SET force_refresh_requested_at = now()` and those two CAN be coupled, but the helper is also useful for tests and operational debug paths.

**Files:**
- Create: `internal/db/with_tx_notify.go`
- Create: `internal/db/with_tx_notify_test.go`

- [ ] **Step 1: Write the failing test**

`internal/db/with_tx_notify_test.go`:

```go
package db

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestWithTxNotify_NotifyVisibleAfterCommit asserts the property that fails in
// the naive two-Exec sequence: the consumer that wakes on the NOTIFY must see
// the projection write on its own connection.
func TestWithTxNotify_NotifyVisibleAfterCommit(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Listener on a separate connection (mirrors consumer topology).
	listenConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer listenConn.Release()
	_, err = listenConn.Exec(ctx, "LISTEN ach_t_visibility")
	require.NoError(t, err)

	// Seed a row to UPDATE inside the tx.
	_, err = pool.Exec(ctx,
		`INSERT INTO environments (namespace, name, resource_version)
		 VALUES ('ach-system','vis-test','rv-0')`)
	require.NoError(t, err)

	// Write + NOTIFY in one tx.
	err = WithTxNotify(ctx, pool, "ach_t_visibility", "ach-system/vis-test",
		func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx,
				`UPDATE environments SET resource_version='rv-1' WHERE name='vis-test'`)
			return err
		})
	require.NoError(t, err)

	// Consumer wakes, SELECTs on its OWN conn — must see rv-1.
	n, err := listenConn.Conn().WaitForNotification(ctx)
	require.NoError(t, err)
	require.Equal(t, "ach_t_visibility", n.Channel)

	var rv string
	require.NoError(t, listenConn.QueryRow(ctx,
		`SELECT resource_version FROM environments WHERE name='vis-test'`).Scan(&rv))
	require.Equal(t, "rv-1", rv, "consumer woke on NOTIFY but write not visible")
}

// TestWithTxNotify_RollbackSuppressesNotify asserts that a failing fn leaves
// no NOTIFY delivered (the tx rolls back, queued NOTIFYs are discarded).
func TestWithTxNotify_RollbackSuppressesNotify(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	listenConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer listenConn.Release()
	_, _ = listenConn.Exec(ctx, "LISTEN ach_t_rollback")

	err = WithTxNotify(ctx, pool, "ach_t_rollback", "x",
		func(tx pgx.Tx) error {
			_, _ = tx.Exec(ctx,
				`INSERT INTO environments (namespace, name, resource_version)
				 VALUES ('ach-system','rollback-me','rv-0')`)
			return assertErr // forces rollback
		})
	require.Error(t, err)

	// Wait briefly; no NOTIFY should arrive.
	short, c2 := context.WithTimeout(ctx, 300*time.Millisecond)
	defer c2()
	_, err = listenConn.Conn().WaitForNotification(short)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Row also rolled back.
	var n int64
	require.NoError(t, listenConn.QueryRow(context.Background(),
		`SELECT count(*) FROM environments WHERE name='rollback-me'`).Scan(&n))
	require.Equal(t, int64(0), n)
}

var assertErr = pgx.ErrTxClosed // any non-nil error; chosen to be unambiguous in logs

func testLogger(t *testing.T) interface{} { /* existing helper in package */ return nil }
```

- [ ] **Step 2: Run to verify they fail**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: FAIL — `WithTxNotify` does not exist.

- [ ] **Step 3: Implement the helper**

`internal/db/with_tx_notify.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithTxNotify runs fn inside a single Postgres transaction and, if fn returns
// nil, issues pg_notify(channel, payload) in the same transaction before
// COMMIT. Postgres queues notifications inside a transaction and only fires
// them on commit, after the transaction's writes are visible to other
// backends. This closes the visibility race the bare db.Emit has when paired
// with a separate Upsert: a consumer that wakes on the NOTIFY can safely
// SELECT and will see the projection write.
//
// If fn returns an error, the transaction rolls back and no NOTIFY is
// delivered — the consumer's 5-minute periodic refresh is the safety net
// that catches any silently-dropped write.
//
// The channel name is validated against validChannel; payload is passed as
// a $-parameter to pg_notify(text, text).
func WithTxNotify(ctx context.Context, pool *pgxpool.Pool, channel, payload string, fn func(pgx.Tx) error) error {
	if !validChannel(channel) {
		return fmt.Errorf("db.WithTxNotify: invalid channel name %q", channel)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db.WithTxNotify: Begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if err := fn(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, payload); err != nil {
		return fmt.Errorf("db.WithTxNotify(%s): pg_notify: %w", channel, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db.WithTxNotify(%s): Commit: %w", channel, err)
	}
	return nil
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: PASS.

- [ ] **Step 5: Refactor every existing `UpsertX` to expose a tx-taking variant**

Every projection-writing helper in `internal/db/` (`UpsertEnvironment`, `UpsertPlugin`, `UpsertPrompt`, `UpsertArtifact`, `UpsertMarketplacePlugin`, `UpsertExternalRef`) gets a sibling that takes `pgx.Tx` instead of `*pgxpool.Pool`. Naming pattern: `upsertEnvironmentTx(ctx, tx, row) error`. The existing pool-form becomes a thin wrapper for callers that don't have a tx yet (tests, etc.).

```go
// existing pool form:
func UpsertEnvironment(ctx context.Context, pool *pgxpool.Pool, row EnvironmentRow) error {
	tx, err := pool.Begin(ctx)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()
	if err := upsertEnvironmentTx(ctx, tx, row); err != nil { return err }
	return tx.Commit(ctx)
}

// new tx form — what controllers call via WithTxNotify:
func upsertEnvironmentTx(ctx context.Context, tx pgx.Tx, row EnvironmentRow) error {
	var ns string
	err := tx.QueryRow(ctx, upsertEnvironmentSQL, /* args */).Scan(&ns)
	if errors.Is(err, pgx.ErrNoRows) { return ErrOriginConflict }
	return err
}
```

The same split applies to `SoftDeleteX` and `DeleteX` — controllers that want to fire NOTIFY on delete (BIP, LiteLLMConnection, etc.) need the `Tx`-form too.

- [ ] **Step 6: Commit**

```bash
git add internal/db/with_tx_notify.go internal/db/with_tx_notify_test.go \
        internal/db/environments.go internal/db/plugins.go internal/db/prompts.go \
        internal/db/artifacts.go internal/db/marketplace_plugins.go internal/db/external_refs.go
git commit -m "feat(db): add WithTxNotify for transaction-coupled NOTIFY + tx-form helpers"
```

---

### Task A3: Postgres LISTEN runnable with reconnect

**Files:**
- Create: `internal/db/listen.go`
- Create: `internal/db/listen_test.go`

- [ ] **Step 1: Write the failing test**

`internal/db/listen_test.go`:

```go
package db

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestListener_DeliversToRegisteredHandler(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lis := NewListener(pool, testLogger(t))
	var got atomic.Value // string
	lis.Subscribe("ach_t_listen", func(payload string) { got.Store(payload) })

	go func() { _ = lis.Run(ctx) }()
	time.Sleep(100 * time.Millisecond) // let LISTEN register

	if err := Emit(ctx, pool, "ach_t_listen", "hello"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := got.Load().(string); ok && v == "hello" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("handler never fired; got=%v", got.Load())
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: FAIL — `NewListener` does not exist.

- [ ] **Step 3: Implement the Listener**

> **Revision 1:** The Listener opens a **dedicated `pgx.Conn`** via `pgx.Connect` rather than acquiring from the pgxpool. A LISTEN connection lives for the process lifetime, and parking it in the pool starves other queries (pool default size is small; one listener per consumer plus normal queries can exhaust the pool). The conn string is taken from `pool.Config().ConnString()` so callers don't need to plumb a separate DSN.

`internal/db/listen.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// Listener subscribes to one or more Postgres NOTIFY channels using a
// dedicated, long-lived pgx.Conn (NOT acquired from the pool — see
// revision-1 note in the plan; a parked pool conn starves other queries).
// It runs until ctx is cancelled and auto-reconnects on transient errors
// with capped exponential backoff.
//
// Consumers call Subscribe(channel, handler) BEFORE Run(ctx). Subscriptions
// added after Run() are picked up on the next reconnect; the Listener does
// NOT replay missed events — consumers must pair Listener with a periodic
// full-refresh (5m ticker) to recover from dropped LISTEN sessions.
package db

import (
	"context"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler func(payload string)

type Listener struct {
	connString string
	log        logr.Logger

	mu   sync.RWMutex
	subs map[string]Handler // channel → handler
}

// NewListener takes a pgxpool.Pool to read the conn string from; it does NOT
// hold a reference to the pool. The Listener opens its own dedicated conn
// via pgx.Connect in runOnce.
func NewListener(pool *pgxpool.Pool, log logr.Logger) *Listener {
	return &Listener{
		connString: pool.Config().ConnString(),
		log:        log,
		subs:       map[string]Handler{},
	}
}

func (l *Listener) Subscribe(channel string, h Handler) {
	l.mu.Lock()
	l.subs[channel] = h
	l.mu.Unlock()
}

func (l *Listener) Run(ctx context.Context) error {
	backoff := 100 * time.Millisecond
	const backoffMax = 30 * time.Second
	for {
		if err := l.runOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			l.log.Error(err, "listen session ended; reconnecting", "backoff", backoff.String())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			if backoff < backoffMax {
				backoff *= 2
			}
			continue
		}
		return nil
	}
}

func (l *Listener) runOnce(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, l.connString)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()

	l.mu.RLock()
	channels := make([]string, 0, len(l.subs))
	for c := range l.subs {
		channels = append(channels, c)
	}
	l.mu.RUnlock()

	for _, c := range channels {
		if !validChannel(c) {
			return errInvalidChannel(c)
		}
		if _, err := conn.Exec(ctx, "LISTEN "+c); err != nil {
			return err
		}
	}

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		l.mu.RLock()
		h := l.subs[n.Channel]
		l.mu.RUnlock()
		if h != nil {
			h(n.Payload)
		}
	}
}

func errInvalidChannel(s string) error {
	return &invalidChannelErr{name: s}
}

type invalidChannelErr struct{ name string }

func (e *invalidChannelErr) Error() string { return "db.Listener: invalid channel name: " + e.name }
```

- [ ] **Step 4: Run to verify it passes**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/listen.go internal/db/listen_test.go
git commit -m "feat(db): add Listener for pgx LISTEN/NOTIFY with auto-reconnect"
```

- [ ] **Step 6 (revision 1): Document the missed-NOTIFY contract**

The Listener does NOT replay events that arrived while disconnected. PostgreSQL drops queued NOTIFYs when the LISTEN session ends. Consumers MUST pair Listener with a periodic full-refresh (the 5-minute ticker in `bipcache` C1 and `envstore` C2) to recover the dropped events.

Add a contract test to `internal/db/listen_test.go`:

```go
// TestListener_DoesNotReplayMissed asserts the documented contract: events
// fired while the Listener is between reconnect attempts are lost. The 5m
// periodic refresh in C1/C2 is the recovery mechanism, not the Listener.
func TestListener_DoesNotReplayMissed(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lis := NewListener(pool, testLogger(t))
	var hits atomic.Int32
	lis.Subscribe("ach_t_replay", func(_ string) { hits.Add(1) })

	innerCtx, innerCancel := context.WithCancel(ctx)
	go func() { _ = lis.Run(innerCtx) }()
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, Emit(ctx, pool, "ach_t_replay", "first"))
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int32(1), hits.Load())

	// Stop the listener; emit while disconnected.
	innerCancel()
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, Emit(ctx, pool, "ach_t_replay", "missed"))

	// Restart; assert the "missed" event was NOT delivered.
	go func() { _ = lis.Run(ctx) }()
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int32(1), hits.Load(),
		"Listener replayed an event it should have dropped — contract broken")
}
```

Commit this test as part of the A3 commit above (amend it in if you've already committed; the test is a contract assertion on existing behavior).

---

### Task A4: New table `litellm_connections` + db helpers

**Files:**
- Create: `db/migrations/000006_litellm_connections.up.sql`
- Create: `db/migrations/000006_litellm_connections.down.sql`
- Create: `internal/db/litellm_connections.go`
- Create: `internal/db/litellm_connections_test.go`

- [ ] **Step 1: Write the failing test**

`internal/db/litellm_connections_test.go`:

```go
func TestUpsertLiteLLMConnection_InsertThenUpdate(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	row := LiteLLMConnectionRow{
		Namespace: "ach-system", Name: "default",
		Endpoint: "http://litellm.litellm-system.svc:4000",
		MasterKeySecretNamespace: "litellm-system",
		MasterKeySecretName:      "litellm-master-key",
		MasterKeySecretKey:       "master_key",
		ResourceVersion:          "1",
	}
	if err := UpsertLiteLLMConnection(ctx, pool, row); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := GetDefaultLiteLLMConnection(ctx, pool, "ach-system")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", err, got)
	}
	if got.Endpoint != row.Endpoint {
		t.Fatalf("got endpoint=%q want %q", got.Endpoint, row.Endpoint)
	}

	row.Endpoint = "http://litellm.litellm-system.svc:5000"
	row.ResourceVersion = "2"
	if err := UpsertLiteLLMConnection(ctx, pool, row); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = GetDefaultLiteLLMConnection(ctx, pool, "ach-system")
	if got.Endpoint != row.Endpoint {
		t.Fatalf("not updated: got %q", got.Endpoint)
	}
}

func TestGetDefaultLiteLLMConnection_Absent(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	got, err := GetDefaultLiteLLMConnection(context.Background(), pool, "ach-system")
	if err != nil { t.Fatalf("err: %v", err) }
	if got != nil { t.Fatalf("want nil, got %+v", got) }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: FAIL — table doesn't exist; helpers don't exist.

- [ ] **Step 3: Write the migration**

`db/migrations/000006_litellm_connections.up.sql`:

```sql
CREATE TABLE litellm_connections (
    namespace                    TEXT        NOT NULL,
    name                         TEXT        NOT NULL DEFAULT 'default',
    endpoint                     TEXT        NOT NULL,
    master_key_secret_namespace  TEXT        NOT NULL,
    master_key_secret_name       TEXT        NOT NULL,
    master_key_secret_key        TEXT        NOT NULL DEFAULT 'master_key',
    deletion_timestamp           TIMESTAMPTZ,
    resource_version             TEXT        NOT NULL,
    updated_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin                       TEXT        NOT NULL DEFAULT 'cr'
        CHECK (origin IN ('cr','ui')),
    locked                       BOOLEAN     NOT NULL DEFAULT FALSE,
    PRIMARY KEY (namespace, name),
    CONSTRAINT litellm_connections_cr_locked_chk CHECK (origin <> 'cr' OR locked = TRUE)
);
```

`db/migrations/000006_litellm_connections.down.sql`:

```sql
DROP TABLE IF EXISTS litellm_connections;
```

- [ ] **Step 4: Implement db helpers**

`internal/db/litellm_connections.go` mirrors `internal/db/environments.go` structure. Define `LiteLLMConnectionRow` struct with the eleven fields above; implement:

```go
func UpsertLiteLLMConnection(ctx context.Context, pool *pgxpool.Pool, row LiteLLMConnectionRow) error
func GetDefaultLiteLLMConnection(ctx context.Context, pool *pgxpool.Pool, ns string) (*LiteLLMConnectionRow, error)
func SoftDeleteLiteLLMConnection(ctx context.Context, pool *pgxpool.Pool, ns, name string) error
func DeleteLiteLLMConnection(ctx context.Context, pool *pgxpool.Pool, ns, name string) error
```

`GetDefaultLiteLLMConnection` returns `(nil, nil)` on absence (Hub §8.3 ergonomic convention). `UpsertLiteLLMConnection` follows the origin-gate pattern from Task A1.

- [ ] **Step 5: Run to verify it passes**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add db/migrations/000006_litellm_connections.up.sql \
        db/migrations/000006_litellm_connections.down.sql \
        internal/db/litellm_connections.go internal/db/litellm_connections_test.go
git commit -m "feat(db): add litellm_connections projection table + helpers"
```

---

### Task A5: New table `backend_identity_policies` + db helpers

**Files:**
- Create: `db/migrations/000007_backend_identity_policies.up.sql`
- Create: `db/migrations/000007_backend_identity_policies.down.sql`
- Create: `internal/db/backend_identity_policies.go`
- Create: `internal/db/backend_identity_policies_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/db/backend_identity_policies_test.go`:

```go
func TestUpsertBIP_InsertThenUpdate(t *testing.T) { /* mirrors environments_test */ }

func TestListBIPsByTarget_AlphaLastWinner(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	// Same target, three CRs — alpha LAST wins per BIP read-side semantics.
	for _, name := range []string{"aaa-bip", "mmm-bip", "zzz-bip"} {
		row := BIPRow{
			Namespace: "ach-system", Name: name,
			TargetKind: "MCPServer", TargetName: "github-mcp",
			ForwardIdentityJWT: name == "zzz-bip", // make the winner the true one
			ResourceVersion:    "1",
		}
		if err := UpsertBIP(ctx, pool, row); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}
	winner, err := ListBIPsByTarget(ctx, pool, "ach-system", "MCPServer", "github-mcp")
	if err != nil { t.Fatalf("list: %v", err) }
	if len(winner) != 3 { t.Fatalf("got %d, want 3", len(winner)) }
	// rows ordered by name ASC; caller picks last
	if winner[len(winner)-1].Name != "zzz-bip" {
		t.Fatalf("last = %q, want zzz-bip", winner[len(winner)-1].Name)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: FAIL — table + helpers absent.

- [ ] **Step 3: Write the migration**

`db/migrations/000007_backend_identity_policies.up.sql`:

```sql
CREATE TABLE backend_identity_policies (
    namespace             TEXT        NOT NULL,
    name                  TEXT        NOT NULL,
    target_kind           TEXT        NOT NULL CHECK (target_kind IN ('MCPServer','A2AAgent')),
    target_name           TEXT        NOT NULL,
    forward_identity_jwt  BOOLEAN     NOT NULL,
    observed_generation   BIGINT      NOT NULL DEFAULT 0,
    deletion_timestamp    TIMESTAMPTZ,
    resource_version      TEXT        NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin                TEXT        NOT NULL DEFAULT 'cr'
        CHECK (origin IN ('cr','ui')),
    locked                BOOLEAN     NOT NULL DEFAULT FALSE,
    PRIMARY KEY (namespace, name),
    CONSTRAINT backend_identity_policies_cr_locked_chk CHECK (origin <> 'cr' OR locked = TRUE)
);

CREATE INDEX backend_identity_policies_target_idx
    ON backend_identity_policies (namespace, target_kind, target_name);
```

`db/migrations/000007_backend_identity_policies.down.sql`:

```sql
DROP TABLE IF EXISTS backend_identity_policies;
```

- [ ] **Step 4: Implement db helpers**

`internal/db/backend_identity_policies.go`:

```go
type BIPRow struct {
	Namespace          string
	Name               string
	TargetKind         string
	TargetName         string
	ForwardIdentityJWT bool
	ObservedGeneration int64
	DeletionTimestamp  *time.Time
	ResourceVersion    string
}

func UpsertBIP(ctx context.Context, pool *pgxpool.Pool, row BIPRow) error { /* origin gate, mirrors A1 */ }
func GetBIPByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*BIPRow, error) { /* nil-on-absent */ }
func ListBIPsByTarget(ctx context.Context, pool *pgxpool.Pool, ns, targetKind, targetName string) ([]BIPRow, error) {
	// SELECT ... WHERE namespace=$1 AND target_kind=$2 AND target_name=$3 AND deletion_timestamp IS NULL
	// ORDER BY name ASC
}
func ListAllBIPs(ctx context.Context, pool *pgxpool.Pool, ns string) ([]BIPRow, error) {
	// Used by forwarder bipcache 5m periodic refresh.
}
func SoftDeleteBIP(ctx context.Context, pool *pgxpool.Pool, ns, name string) error
func DeleteBIP(ctx context.Context, pool *pgxpool.Pool, ns, name string) error
```

- [ ] **Step 5: Run to verify they pass**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add db/migrations/000007_backend_identity_policies.up.sql \
        db/migrations/000007_backend_identity_policies.down.sql \
        internal/db/backend_identity_policies.go \
        internal/db/backend_identity_policies_test.go
git commit -m "feat(db): add backend_identity_policies projection table + helpers"
```

---

### Task A6: Add `ListEnvironments` helper + tests

**Files:**
- Modify: `internal/db/environments.go`
- Modify: `internal/db/environments_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestListEnvironments_OrderedByName(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	for _, n := range []string{"zzz", "aaa", "mmm"} {
		row := EnvironmentRow{Namespace: "ach-system", Name: n, ResourceVersion: "1"}
		if err := UpsertEnvironment(ctx, pool, row); err != nil { t.Fatalf("upsert %s: %v", n, err) }
	}
	envs, err := ListEnvironments(ctx, pool, "ach-system")
	if err != nil { t.Fatalf("list: %v", err) }
	if len(envs) != 3 { t.Fatalf("got %d, want 3", len(envs)) }
	if envs[0].Name != "aaa" || envs[2].Name != "zzz" {
		t.Fatalf("ordering: %+v", envs)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: FAIL — `ListEnvironments` undefined.

- [ ] **Step 3: Implement**

In `internal/db/environments.go`:

```go
func ListEnvironments(ctx context.Context, pool *pgxpool.Pool, ns string) ([]EnvironmentRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT namespace, name, authorized_teams, context_prompts, context_plugins,
		       context_artifacts, runtime_models, runtime_mcp_servers, runtime_a2a_agents,
		       available_condition, access_group_synced_condition,
		       execution_resources_resolved_condition, deletion_timestamp,
		       resource_version, origin
		  FROM environments
		 WHERE namespace = $1
		   AND deletion_timestamp IS NULL
		 ORDER BY name ASC`, ns)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []EnvironmentRow
	for rows.Next() {
		var r EnvironmentRow
		if err := rows.Scan(/* ... */); err != nil { return nil, err }
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `./scripts/dev.sh make unit-pkg PKG=./internal/db/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/environments.go internal/db/environments_test.go
git commit -m "feat(db): add ListEnvironments helper for forwarder precheck"
```

---

### Task A7: Wire LiteLLMConnection controller to write to Postgres + emit NOTIFY

**Files:**
- Modify: `internal/controller/ach/litellmconnection_controller.go`
- Modify: `internal/controller/ach/litellmconnection_controller_test.go`
- Modify: `cmd/ach/cmd/operator.go` (inject `pgxpool.Pool` into the reconciler struct)

- [ ] **Step 1: Write the failing envtest**

In `internal/controller/ach/litellmconnection_controller_test.go`, add:

```go
func TestLiteLLMConnectionReconciler_ProjectsToPostgres(t *testing.T) {
	// envtest already wired; pool already set on r.Pool by test harness.
	r, cleanup := newLiteLLMConnReconciler(t)
	defer cleanup()

	cr := &achv1alpha1.LiteLLMConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: r.Namespace},
		Spec: achv1alpha1.LiteLLMConnectionSpec{
			Endpoint: "http://litellm.litellm-system.svc:4000",
			MasterKeySecretRef: achv1alpha1.SecretKeyRef{
				Namespace: "litellm-system", Name: "litellm-master-key", Key: "master_key",
			},
		},
	}
	require.NoError(t, r.Client.Create(ctx, cr))

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: r.Namespace, Name: "default",
	}})
	require.NoError(t, err)

	got, err := db.GetDefaultLiteLLMConnection(ctx, r.Pool, r.Namespace)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "http://litellm.litellm-system.svc:4000", got.Endpoint)
	require.Equal(t, "litellm-master-key", got.MasterKeySecretName)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestLiteLLMConnectionReconciler_ProjectsToPostgres`
Expected: FAIL — reconciler doesn't write to Postgres yet.

- [ ] **Step 3: Add Pool field + projection write**

In `internal/controller/ach/litellmconnection_controller.go`:

```go
type LiteLLMConnectionReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
	Pool      *pgxpool.Pool   // NEW
	// ... existing fields
}

// ... inside Reconcile, after the existing probe/snapshot logic, add:
row := db.LiteLLMConnectionRow{
	Namespace:                cr.Namespace,
	Name:                     cr.Name,
	Endpoint:                 cr.Spec.Endpoint,
	MasterKeySecretNamespace: cr.Spec.MasterKeySecretRef.Namespace,
	MasterKeySecretName:      cr.Spec.MasterKeySecretRef.Name,
	MasterKeySecretKey:       cr.Spec.MasterKeySecretRef.Key,
	ResourceVersion:          cr.ResourceVersion,
}
err := db.WithTxNotify(ctx, r.Pool,
	"ach_litellm_connections_changed",
	fmt.Sprintf("%s/%s", cr.Namespace, cr.Name),
	func(tx pgx.Tx) error { return db.UpsertLiteLLMConnectionTx(ctx, tx, row) },
)
if err != nil {
	if errors.Is(err, db.ErrOriginConflict) {
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:               "Synced",
			Status:             metav1.ConditionFalse,
			Reason:             "ConflictWithUIRow",
			Message:            "a UI-managed litellm_connections row blocks this CR's projection",
			ObservedGeneration: cr.Generation,
		})
		return ctrl.Result{RequeueAfter: time.Minute}, r.Status().Update(ctx, &cr)
	}
	return ctrl.Result{}, fmt.Errorf("UpsertLiteLLMConnection: %w", err)
}

// On finalizer drain, before removing finalizer — also tx-coupled so
// the delete NOTIFY only fires after the soft-delete commits.
err = db.WithTxNotify(ctx, r.Pool,
	"ach_litellm_connections_changed",
	fmt.Sprintf("%s/%s", cr.Namespace, cr.Name),
	func(tx pgx.Tx) error { return db.SoftDeleteLiteLLMConnectionTx(ctx, tx, cr.Namespace, cr.Name) },
)
if err != nil {
	return ctrl.Result{}, fmt.Errorf("SoftDeleteLiteLLMConnection: %w", err)
}
```

> **Revision 1 — `ConflictWithUIRow` condition test.** Augment the envtest with a second case: pre-seed a `(origin='ui', locked=false)` row directly via `pool.Exec`, then `Create` the CR with the same `name`, run `Reconcile`, and assert the CR's `Synced` condition is `False/ConflictWithUIRow`. Mirror this assertion in the BIP envtest (A8). Without this, the only test of the conflict path is the DB-error path in `internal/db/`, which doesn't exercise the status-condition write.

In `cmd/ach/cmd/operator.go` where `LiteLLMConnectionReconciler` is constructed, add `Pool: pool` to the struct literal.

- [ ] **Step 4: Run to verify it passes**

Run: `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestLiteLLMConnectionReconciler_ProjectsToPostgres`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/ach/litellmconnection_controller.go \
        internal/controller/ach/litellmconnection_controller_test.go \
        cmd/ach/cmd/operator.go
git commit -m "feat(controller): project LiteLLMConnection to Postgres + emit NOTIFY"
```

---

### Task A8: Wire BackendIdentityPolicy controller to write to Postgres + emit NOTIFY

**Files:**
- Modify: `internal/controller/ach/backendidentitypolicy_controller.go`
- Modify: `internal/controller/ach/backendidentitypolicy_finalizer_test.go`
- Modify: `cmd/ach/cmd/operator.go`

Pattern is identical to A7 — same `db.WithTxNotify` coupling. The reconciler currently does finalizer-only work — this task extends it to also Upsert/SoftDelete the projection row. The `Reconcile` body becomes (after the finalizer add path):

```go
row := db.BIPRow{
	Namespace:          cr.Namespace,
	Name:               cr.Name,
	TargetKind:         cr.Spec.Target.Kind,
	TargetName:         cr.Spec.Target.Name,
	ForwardIdentityJWT: cr.Spec.ForwardIdentityJWT,
	ObservedGeneration: cr.Generation,
	ResourceVersion:    cr.ResourceVersion,
}
err := db.WithTxNotify(ctx, r.Pool,
	"ach_backend_identity_policies_changed",
	fmt.Sprintf("%s/%s", cr.Namespace, cr.Name),
	func(tx pgx.Tx) error { return db.UpsertBIPTx(ctx, tx, row) },
)
if err != nil {
	if errors.Is(err, db.ErrOriginConflict) { /* same condition write as A7 */ }
	return ctrl.Result{}, fmt.Errorf("UpsertBIP: %w", err)
}
```

The same `WithTxNotify(... SoftDeleteBIPTx ...)` pattern runs on the finalizer drain path.

> **Revision 1 — `ConflictWithUIRow` condition test.** Mirror the A7 envtest addition: pre-seed a `(origin='ui')` row in `backend_identity_policies` with the same name, create the BIP CR, reconcile, assert `Synced=False/ConflictWithUIRow`.

- [ ] **Step 1: Add failing envtest** mirroring A7 (assert row appears in `backend_identity_policies` after reconcile; assert SoftDelete on finalizer drain; assert ConflictWithUIRow on origin clash).

- [ ] **Step 2: Run** `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestBackendIdentityPolicyReconciler_ProjectsToPostgres` → FAIL.

- [ ] **Step 3: Implement** as above + add `Pool *pgxpool.Pool` to the struct + wire in `operator.go`.

- [ ] **Step 4: Run** the same focus → PASS.

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(controller): project BackendIdentityPolicy to Postgres + emit NOTIFY"
```

---

### Task A9: Emit NOTIFY from the five existing projection writers

**Files:**
- Modify: `internal/controller/ach/environment_controller.go`
- Modify: `internal/controller/ach/plugin_controller.go`
- Modify: `internal/controller/ach/prompt_controller.go`
- Modify: `internal/controller/ach/artifact_controller.go`
- Modify: `internal/controller/ach/pluginmarketplace_controller.go`

> **Revision 1:** Each existing `UpsertXxx(ctx, r.Pool, row)` call site refactors to `db.WithTxNotify(ctx, r.Pool, channel, payload, func(tx pgx.Tx) error { return db.UpsertXxxTx(ctx, tx, row) })`. Same pattern on every `SoftDeleteXxx` path. No bare `db.Emit` is added — the helper couples them in a single transaction.

```go
err := db.WithTxNotify(ctx, r.Pool,
	"ach_environments_changed",
	fmt.Sprintf("%s/%s", cr.Namespace, cr.Name),
	func(tx pgx.Tx) error { return db.UpsertEnvironmentTx(ctx, tx, row) },
)
if err != nil { /* origin-conflict / wrap as before */ }
```

Channel names per table (snake_case + `_changed`):
- `ach_environments_changed`
- `ach_plugins_changed`
- `ach_prompts_changed`
- `ach_artifacts_changed`
- `ach_marketplace_plugins_changed`

- [ ] **Step 1: Write the failing test** (one suffices — pick `environment_controller`):

In `environment_projection_test.go`, add an assertion that runs a `LISTEN ach_environments_changed` on a sidecar conn before reconcile and verifies the notification arrives.

- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Add the five `db.Emit` calls** (one per reconciler).
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit**

```bash
git commit -am "feat(controller): emit NOTIFY ach_<table>_changed after every projection write"
```

---

### Task A10: Operator periodic full-resync runnable (5-minute safety net)

**Files:**
- Create: `internal/operator/resync/runnable.go`
- Create: `internal/operator/resync/runnable_test.go`
- Modify: `cmd/ach/cmd/operator.go`

The runnable lists every CR Kind every 5 minutes and pushes each `reconcile.Request` into the corresponding controller's work queue. This catches divergence after operator restart, transient DB errors swallowed by a single reconcile, etc. Built on `manager.Runnable`.

- [ ] **Step 1: Write the failing test**

Envtest: create 3 Environments, manually delete 2 of the projection rows from Postgres, start the runnable, advance time 5m, assert all 3 projection rows are re-created.

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement the runnable** (revision 1: source.Channel-based)

> **Revision 1:** Original plan grabbed per-controller `workqueue.RateLimitingInterface` after registration; that's not a public controller-runtime API. The supported mechanism is `source.Channel{Source: <-chan event.GenericEvent}` plumbed into each controller's `Watches(...)`. The Resync pushes `event.GenericEvent` into a per-Kind channel; controller-runtime's machinery enqueues a `reconcile.Request` from it.

`internal/operator/resync/runnable.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// Resync is a manager.Runnable that lists every ACH CR Kind every Interval
// and pushes a GenericEvent per item into the per-Kind source.Channel that
// the matching controller registered via Watches(...). Safety net for missed
// events, operator restarts, and Postgres write failures swallowed by a
// single reconcile. Default Interval is 5 minutes.
package resync

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// Channels maps each Kind name to the GenericEvent channel that its
// controller registered via builder.Watches(source.Channel{Source: ch}, ...).
type Channels struct {
	Environment      chan<- event.GenericEvent
	Plugin           chan<- event.GenericEvent
	Prompt           chan<- event.GenericEvent
	Artifact         chan<- event.GenericEvent
	Marketplace      chan<- event.GenericEvent
	BIP              chan<- event.GenericEvent
	LiteLLMConnection chan<- event.GenericEvent
}

type Resync struct {
	Client    client.Client
	Namespace string
	Interval  time.Duration
	Log       logr.Logger
	Channels  Channels
}

func (r *Resync) Start(ctx context.Context) error {
	if r.Interval == 0 {
		r.Interval = 5 * time.Minute
	}
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			r.resyncAll(ctx)
		}
	}
}

func (r *Resync) resyncAll(ctx context.Context) {
	r.resync(ctx, r.Channels.Environment, &achv1alpha1.EnvironmentList{})
	r.resync(ctx, r.Channels.Plugin, &achv1alpha1.PluginList{})
	r.resync(ctx, r.Channels.Prompt, &achv1alpha1.PromptList{})
	r.resync(ctx, r.Channels.Artifact, &achv1alpha1.ArtifactList{})
	r.resync(ctx, r.Channels.Marketplace, &achv1alpha1.PluginMarketplaceList{})
	r.resync(ctx, r.Channels.BIP, &achv1alpha1.BackendIdentityPolicyList{})
	r.resync(ctx, r.Channels.LiteLLMConnection, &achv1alpha1.LiteLLMConnectionList{})
}

func (r *Resync) resync(ctx context.Context, ch chan<- event.GenericEvent, list client.ObjectList) {
	if ch == nil {
		return
	}
	if err := r.Client.List(ctx, list, client.InNamespace(r.Namespace)); err != nil {
		r.Log.Error(err, "resync list", "kind", listKind(list))
		return
	}
	items, err := listItems(list)
	if err != nil {
		r.Log.Error(err, "resync items", "kind", listKind(list))
		return
	}
	for i := range items {
		ch <- event.GenericEvent{Object: items[i]}
	}
}

// listKind / listItems use runtime.Object reflection helpers; implementation
// in internal/operator/resync/list_helpers.go (a few lines of meta.ExtractList).
var _ = runtime.Object(nil)
```

In `cmd/ach/cmd/operator.go`, for every reconciler create a `chan event.GenericEvent` (buffered, e.g. capacity 64), pass the receive-end into the builder via `builder.WatchesRawSource(source.Channel{Source: ch}, &handler.EnqueueRequestForObject{})`, and pass the send-end to `Resync.Channels`. Then `mgr.Add(&resync.Resync{...})`.

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(operator): add 5m periodic full-resync runnable for projection convergence"
```

---

### Task A11: Operator LISTEN session for force-refresh signal

**Files:**
- Create: `internal/operator/refreshsignal/listener.go`
- Create: `internal/operator/refreshsignal/listener_test.go`
- Create: `internal/operator/refreshsignal/doc.go`
- Modify: `cmd/ach/cmd/operator.go`

The listener subscribes to `ach_refresh` (single channel). Payload format: `<kind>/<name>` where `kind ∈ {plugin, prompt, artifact, pluginmarketplace}`. Handler enqueues the matching CR into the matching controller's workqueue. This replaces the annotation-patching path of the old `/admin/refresh` handler.

> **Revision 1:** Like A10, this listener uses `source.Channel` rather than reaching into per-controller workqueues. The same per-Kind `chan event.GenericEvent` map plumbed from `cmd/ach/cmd/operator.go` is shared with the Resync runnable.

- [ ] **Step 1: Write the failing test**

```go
func TestRefreshSignal_EnqueuesPluginOnNOTIFY(t *testing.T) {
	pool := newTestPool(t)
	ch := make(chan event.GenericEvent, 4)
	lis := &Listener{
		Pool: pool, Namespace: "ach-system", Log: testLogger(t),
		Channels: map[string]chan<- event.GenericEvent{"plugin": ch},
		// k8s lookup so the GenericEvent carries an Object — use a fake client.
		Client: fakeClientWith(t, &achv1alpha1.Plugin{
			ObjectMeta: metav1.ObjectMeta{Name: "caveman", Namespace: "ach-system"},
		}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = lis.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, db.Emit(ctx, pool, "ach_refresh", "plugin/caveman"))

	select {
	case ev := <-ch:
		require.Equal(t, "caveman", ev.Object.GetName())
	case <-time.After(2 * time.Second):
		t.Fatal("no GenericEvent received")
	}
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement the Listener**

`internal/operator/refreshsignal/listener.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// Package refreshsignal subscribes to the `ach_refresh` Postgres channel and
// pushes a GenericEvent for the named CR into the per-Kind source.Channel
// the matching controller registered. Replaces the annotation-patching path
// the platform-api used to fire when /admin/refresh was called.
package refreshsignal

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/db"
)

type Listener struct {
	Pool      *pgxpool.Pool
	Namespace string
	Log       logr.Logger
	Client    client.Client // for Get-ing the CR before emitting the GenericEvent
	// Channels is keyed by lowercase kind: "plugin", "prompt", "artifact", "pluginmarketplace".
	Channels map[string]chan<- event.GenericEvent
}

func (l *Listener) Run(ctx context.Context) error {
	dbLis := db.NewListener(l.Pool, l.Log)
	dbLis.Subscribe("ach_refresh", func(payload string) { l.handle(ctx, payload) })
	return dbLis.Run(ctx)
}

func (l *Listener) handle(ctx context.Context, payload string) {
	kind, name, ok := strings.Cut(payload, "/")
	if !ok {
		l.Log.Info("ach_refresh: bad payload (expected '<kind>/<name>')", "payload", payload)
		return
	}
	ch, ok := l.Channels[kind]
	if !ok {
		l.Log.Info("ach_refresh: unknown kind", "kind", kind)
		return
	}
	obj, err := l.fetchObject(ctx, kind, name)
	if err != nil {
		l.Log.Error(err, "ach_refresh: fetch CR", "kind", kind, "name", name)
		return
	}
	select {
	case ch <- event.GenericEvent{Object: obj}:
	case <-ctx.Done():
	}
}

func (l *Listener) fetchObject(ctx context.Context, kind, name string) (client.Object, error) {
	var obj client.Object
	switch kind {
	case "plugin":           obj = &achv1alpha1.Plugin{}
	case "prompt":           obj = &achv1alpha1.Prompt{}
	case "artifact":         obj = &achv1alpha1.Artifact{}
	case "pluginmarketplace": obj = &achv1alpha1.PluginMarketplace{}
	default:
		return nil, &unknownKindErr{kind}
	}
	return obj, l.Client.Get(ctx, types.NamespacedName{Namespace: l.Namespace, Name: name}, obj)
}

type unknownKindErr struct{ kind string }
func (e *unknownKindErr) Error() string { return "refreshsignal: unknown kind " + e.kind }
```

In `cmd/ach/cmd/operator.go`, share the per-Kind `chan event.GenericEvent` map between `Resync` (A10) and `refreshsignal.Listener` (A11); both write the send-end, the controller builder consumes the receive-end via `source.Channel`.

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(operator): listen on ach_refresh for force-refresh signal"
```

---

## Phase B — Platform-API Port (k8s → Postgres)

### Task B0: EnvironmentRow → EnvironmentView adapter (revision 1)

**Why this task exists:** Today `internal/platformapi/hydrate/handler.go` consumes `achv1alpha1.RuntimeBlock` / `ContextBlock` (nested CR types) when building the `/platform/hydrate` response. `db.EnvironmentRow` is flat (string slices + JSONB conditions). B1 cannot just swap types — the hydrate handler would lose shape. Introduce an adapter that converts row → view, then B1 and downstream tasks all consume the view.

**Files:**
- Create: `internal/platformapi/store/adapter.go`
- Create: `internal/platformapi/store/adapter_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestRowToView_PopulatesContextAndRuntime(t *testing.T) {
	row := db.EnvironmentRow{
		Namespace:           "ach-system",
		Name:                "demo",
		AuthorizedTeams:     []string{"a", "b"},
		ContextPrompts:      []string{"p1"},
		ContextPlugins:      []string{"pl1"},
		ContextArtifacts:    []string{"ar1"},
		RuntimeModels:       []string{"gpt-4o"},
		RuntimeMCPServers:   []string{"github-mcp"},
		RuntimeA2AAgents:    []string{"plan-agent"},
		ResourceVersion:     "rv-1",
	}
	v := RowToView(row)
	require.Equal(t, "demo", v.Name)
	require.Equal(t, []string{"a", "b"}, v.AuthorizedTeams)
	require.Equal(t, []string{"p1"}, v.Context.Prompts)
	require.Equal(t, []string{"github-mcp"}, v.Runtime.MCPServers)
	require.Equal(t, "rv-1", v.ResourceVersion)
}
```

- [ ] **Step 2: Run** → FAIL — `RowToView` doesn't exist.

- [ ] **Step 3: Implement adapter**

`internal/platformapi/store/adapter.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package store

import (
	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/db"
)

// EnvironmentView is the platform-api-internal shape that handler code
// (hydrate, environments, envkeys) consumes. It mirrors the CR-shaped
// nested structs the handlers were built for, but is populated from a
// flat db.EnvironmentRow. Origin/locked are surfaced so handler code can
// choose to render edit-disabled UX, but they do NOT gate reads.
type EnvironmentView struct {
	Namespace        string
	Name             string
	AuthorizedTeams  []string
	Context          achv1alpha1.ContextBlock
	Runtime          achv1alpha1.RuntimeBlock
	Conditions       Conditions
	ResourceVersion  string
	DeletionPending  bool
	Origin           string // "cr" | "ui"
	Locked           bool
}

type Conditions struct {
	Available                    db.ConditionJSONB
	AccessGroupSynced            db.ConditionJSONB
	ExecutionResourcesResolved   db.ConditionJSONB
}

func RowToView(r db.EnvironmentRow) EnvironmentView {
	return EnvironmentView{
		Namespace:       r.Namespace,
		Name:            r.Name,
		AuthorizedTeams: r.AuthorizedTeams,
		Context: achv1alpha1.ContextBlock{
			Prompts:   r.ContextPrompts,
			Plugins:   r.ContextPlugins,
			Artifacts: r.ContextArtifacts,
		},
		Runtime: achv1alpha1.RuntimeBlock{
			Models:     r.RuntimeModels,
			MCPServers: r.RuntimeMCPServers,
			A2AAgents:  r.RuntimeA2AAgents,
		},
		Conditions: Conditions{
			Available:                  r.AvailableCondition,
			AccessGroupSynced:          r.AccessGroupSyncedCondition,
			ExecutionResourcesResolved: r.ExecutionResourcesResolvedCondition,
		},
		ResourceVersion: r.ResourceVersion,
		DeletionPending: r.DeletionTimestamp != nil,
		Origin:          r.Origin,
		Locked:          r.Locked,
	}
}
```

Update handler signatures in `internal/platformapi/hydrate/handler.go`, `internal/platformapi/environments/handler.go`, `internal/platformapi/envkeys/handler.go` to consume `*EnvironmentView` instead of `*achv1alpha1.Environment`. Field paths change one-to-one: `env.Spec.AuthorizedTeams` → `view.AuthorizedTeams`, `env.Spec.Runtime.MCPServers` → `view.Runtime.MCPServers`, etc.

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/platformapi/store/adapter.go internal/platformapi/store/adapter_test.go \
        internal/platformapi/hydrate/handler.go internal/platformapi/environments/handler.go \
        internal/platformapi/envkeys/handler.go
git commit -m "feat(platform-api): add EnvironmentRow→View adapter and re-shape handlers to consume it"
```

---

### Task B1: Port `internal/platformapi/store/store.go` to Postgres

**Files:**
- Rewrite: `internal/platformapi/store/store.go`
- Rewrite: `internal/platformapi/store/store_test.go`

The current Store wraps a `client.Client`. The new Store wraps a `*pgxpool.Pool`. All four methods (`GetEnvironment`, `EnvironmentTerminating`, `EnvironmentAccessGroupSynced`, `ListAuthorizedEnvironments`) become thin wrappers around `db.GetEnvironmentByName` / `db.ListEnvironments` and read condition status from the JSONB columns.

- [ ] **Step 1: Write failing tests** that use a pgxpool harness instead of an envtest client.

```go
func TestStore_GetEnvironment_HappyAndAbsent(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	// Seed.
	_ = db.UpsertEnvironment(ctx, pool, db.EnvironmentRow{
		Namespace: "ach-system", Name: "demo", ResourceVersion: "1",
	})
	s := New(pool, "ach-system", testLogger(t))

	env, err := s.GetEnvironment(ctx, "demo")
	require.NoError(t, err); require.NotNil(t, env); require.Equal(t, "demo", env.Name)

	gone, err := s.GetEnvironment(ctx, "nope")
	require.NoError(t, err); require.Nil(t, gone)
}

func TestStore_EnvironmentAccessGroupSynced_TrueFalseAbsent(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	_ = db.UpsertEnvironment(ctx, pool, db.EnvironmentRow{
		Namespace: "ach-system", Name: "demo", ResourceVersion: "1",
		AccessGroupSyncedCondition: db.ConditionJSONB{Type: "AccessGroupSynced", Status: "True"},
	})
	s := New(pool, "ach-system", testLogger(t))
	ok, err := s.EnvironmentAccessGroupSynced(ctx, "demo")
	require.NoError(t, err); require.True(t, ok)
}
```

- [ ] **Step 2: Run** → FAIL (current Store doesn't have a pool-based constructor).

- [ ] **Step 3: Rewrite the Store**

`internal/platformapi/store/store.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

const ConditionTypeAccessGroupSynced = "AccessGroupSynced"

type Store struct {
	pool *pgxpool.Pool
	ns   string
	log  logr.Logger
}

func New(pool *pgxpool.Pool, ns string, log logr.Logger) *Store {
	return &Store{pool: pool, ns: ns, log: log}
}

func (s *Store) GetEnvironment(ctx context.Context, name string) (*db.EnvironmentRow, error) {
	row, err := db.GetEnvironmentByName(ctx, s.pool, s.ns, name)
	if err != nil { return nil, fmt.Errorf("store: GetEnvironment(%s): %w", name, err) }
	return row, nil
}

func (s *Store) EnvironmentTerminating(ctx context.Context, name string) (bool, error) {
	row, err := s.GetEnvironment(ctx, name)
	if err != nil { return false, err }
	if row == nil { return false, nil }
	return row.DeletionTimestamp != nil, nil
}

func (s *Store) EnvironmentAccessGroupSynced(ctx context.Context, name string) (bool, error) {
	row, err := s.GetEnvironment(ctx, name)
	if err != nil { return false, err }
	if row == nil { return false, nil }
	return row.AccessGroupSyncedCondition.Status == "True", nil
}

func (s *Store) ListAuthorizedEnvironments(ctx context.Context, callerTeams []string, isAdmin bool) ([]db.EnvironmentRow, error) {
	rows, err := db.ListEnvironments(ctx, s.pool, s.ns)
	if err != nil { return nil, fmt.Errorf("store: ListAuthorizedEnvironments: %w", err) }
	if isAdmin { return rows, nil }
	out := make([]db.EnvironmentRow, 0, len(rows))
	for _, r := range rows {
		if hasIntersect(r.AuthorizedTeams, callerTeams) {
			out = append(out, r)
		}
	}
	return out, nil
}

func hasIntersect(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 { return false }
	set := make(map[string]struct{}, len(a))
	for _, s := range a { set[s] = struct{}{} }
	for _, s := range b { if _, ok := set[s]; ok { return true } }
	return false
}
```

Update every callsite that used `*achv1alpha1.Environment` to use `*db.EnvironmentRow` instead — affects `internal/platformapi/hydrate/handler.go`, `internal/platformapi/environments/handler.go`, `internal/platformapi/envkeys/handler.go`. Fields move from `env.Spec.AuthorizedTeams` → `row.AuthorizedTeams`, from `env.Spec.Runtime.Models` → `row.RuntimeModels`, etc. (The `EnvironmentRow` already has the projected fields.)

- [ ] **Step 4: Run** → PASS (store tests + handler tests both).

- [ ] **Step 5: Commit**

```bash
git commit -am "refactor(platform-api): port Store from informer-cached k8s to Postgres"
```

---

### Task B2: Port `/admin/refresh` from CR patch to Postgres column write + NOTIFY

**Files:**
- Modify: `internal/platformapi/admin/handler.go`
- Modify: `internal/platformapi/admin/handler_test.go`
- Create: `internal/db/refresh_signal.go`
- Create: `internal/db/refresh_signal_test.go`

- [ ] **Step 1: Add failing helper test**

`internal/db/refresh_signal_test.go`:

```go
func TestSetForceRefresh_SetsColumnAndEmitsNotify(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	// Seed an external_refs row.
	_ = UpsertExternalRef(ctx, pool, ExternalRef{Kind: "plugin", Name: "caveman", StorageLocation: "/p/caveman.tar.gz"})

	// Subscribe.
	conn, _ := pool.Acquire(ctx)
	defer conn.Release()
	_, _ = conn.Exec(ctx, "LISTEN ach_refresh")

	require.NoError(t, SetForceRefresh(ctx, pool, "ach-system", "plugin", "caveman"))

	// Column was set.
	var nonNull bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT force_refresh_requested_at IS NOT NULL FROM external_refs WHERE kind=$1 AND name=$2`,
		"plugin", "caveman").Scan(&nonNull))
	require.True(t, nonNull)

	// NOTIFY delivered.
	n, err := conn.Conn().WaitForNotification(ctx)
	require.NoError(t, err)
	require.Equal(t, "ach_refresh", n.Channel)
	require.Equal(t, "plugin/caveman", n.Payload)
}

func TestSetForceRefresh_BlockedForUIOriginRow(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	// Seed a UI-origin plugin row directly.
	_, _ = pool.Exec(ctx, `
	  INSERT INTO plugins (namespace, name, storage_location, max_staleness_seconds,
	                       last_successful_refresh, resource_version, origin)
	  VALUES ('ach-system','my-ui-plugin','/p/my.tar.gz', 0, now(), 'rv-ui', 'ui')`)
	// external_refs.force_refresh is only meaningful for CR-origin rows; the
	// helper checks plugins.origin before writing.
	err := SetForceRefresh(ctx, pool, "ach-system", "plugin", "my-ui-plugin")
	require.ErrorIs(t, err, ErrUIOriginRefreshUnsupported)
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement the helper**

`internal/db/refresh_signal.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUIOriginRefreshUnsupported is returned when SetForceRefresh is called for
// a row whose owning projection table has origin='ui'. UI-managed rows have
// no upstream to refresh; the UI is expected to re-upload the content directly.
var ErrUIOriginRefreshUnsupported = errors.New("db: cannot force-refresh a UI-managed row")

// SetForceRefresh marks the named external_refs / marketplace_plugins row
// for refresh and fires NOTIFY ach_refresh '<kind>/<name>'. Operator picks
// up the signal via internal/operator/refreshsignal.
//
// Allowed kinds: "plugin", "prompt", "artifact", "pluginmarketplace".
// The caller threads ns (the platform-api/operator namespace) so the
// origin guard can resolve the projection row.
func SetForceRefresh(ctx context.Context, pool *pgxpool.Pool, ns, kind, name string) error {
	// Guard: refuse if the projection row exists with origin='ui'.
	if tbl := projectionTable(kind); tbl != "" {
		var origin string
		err := pool.QueryRow(ctx,
			fmt.Sprintf(`SELECT origin FROM %s WHERE namespace=$1 AND name=$2`, tbl),
			ns, name).Scan(&origin)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("SetForceRefresh: origin lookup: %w", err)
		}
		if origin == "ui" {
			return ErrUIOriginRefreshUnsupported
		}
	}

	tag, err := pool.Exec(ctx,
		`UPDATE external_refs SET force_refresh_requested_at = now()
		  WHERE kind=$1 AND name=$2 AND origin='cr'`, kind, name)
	if err != nil { return fmt.Errorf("SetForceRefresh: %w", err) }
	if tag.RowsAffected() == 0 && kind == "pluginmarketplace" {
		_, err = pool.Exec(ctx,
			`UPDATE marketplace_plugins SET force_refresh_requested_at = now()
			  WHERE marketplace_name=$1 AND origin='cr'`, name)
		if err != nil { return fmt.Errorf("SetForceRefresh(marketplace): %w", err) }
	}
	return Emit(ctx, pool, "ach_refresh", kind+"/"+name)
}

func projectionTable(kind string) string {
	switch kind {
	case "plugin": return "plugins"
	case "prompt": return "prompts"
	case "artifact": return "artifacts"
	}
	return ""
}
```

- [ ] **Step 4: Port the handler**

In `internal/platformapi/admin/handler.go`, replace the body of `ForceRefreshHandler` after the request-validation block:

```go
err := db.SetForceRefresh(ctx, deps.Pool, deps.Namespace, req.Kind, req.Name)
switch {
case err == nil:
	// fallthrough to success
case errors.Is(err, db.ErrUIOriginRefreshUnsupported):
	render.Error(w, http.StatusBadRequest, audit.OutcomeInvalidKeyFormat,
		"UI-managed resource has no upstream to refresh", reqID)
	return
default:
	if deps.Logger != nil {
		deps.Logger.Error("admin.refresh: SetForceRefresh failed", "kind", req.Kind, "name", req.Name, "err", err)
	}
	if deps.Audit != nil { /* unchanged audit emit */ }
	render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
	return
}
// audit + 202 unchanged
```

Drop `deps.K8sClient` use entirely. Drop the `newACHObject` helper.

- [ ] **Step 5: Run** → PASS.

- [ ] **Step 6: Commit**

```bash
git commit -am "refactor(platform-api): /admin/refresh writes Postgres column + NOTIFY (no more CR patch)"
```

---

### Task B3: Remove controller-runtime manager from platform-api

**Files:**
- Modify: `cmd/ach/cmd/platform_api.go`

- [ ] **Step 1: Identify and remove**

Delete:
- Lines 31-40 (k8s imports: `corev1`, `runtime`, `scheme`, `cache`, `client`, `manager`, `log/zap`, `metrics/server`).
- Lines 234-244 (manager construction).
- Lines 250-264 (informer registrations for Secret + 6 CRD Kinds).
- Line 286: replace `K8sClient: mgr.GetClient()` in deps wiring with `Pool: pool` (already present).

Keep: every Postgres/Redis/Dex wiring path.

- [ ] **Step 2: Run** `./scripts/dev.sh make unit-pkg PKG=./...`
Expected: build green; any handler that still referenced `deps.K8sClient` should have been ported in B1/B2 — surfaces a compile error if missed.

- [ ] **Step 3: Run** `./scripts/dev.sh make envtest-run` to confirm controllers still wire up (they're operator-side and unaffected).

- [ ] **Step 4: Commit**

```bash
git commit -am "refactor(platform-api): drop controller-runtime manager and CRD informers"
```

---

### Task B4: Trim Helm RBAC for platform-api ServiceAccount

**Files:**
- Modify: `deploy/helm/ach/templates/platform-api-rbac.yaml` (or the file that defines it — verify path)

- [ ] **Step 1: Identify and strip**

Drop every `apiGroups: ["ach.ackstorm.ai"]` rule from the ClusterRole / Role bound to the platform-api ServiceAccount. Keep any `secrets` rules (filtered by resourceNames if used) only if any non-CRD Secret access remains; in MVP, platform-api does NOT read Secrets (verify by grepping).

- [ ] **Step 2: Run** `./scripts/dev.sh make e2e-focus FOCUS="TestPlatformAPI"` (existing e2e suite) — must still pass with the trimmed RBAC.

- [ ] **Step 3: Commit**

```bash
git commit -am "chore(helm): remove ach CRD RBAC from platform-api ServiceAccount"
```

---

## Phase C — Forwarder Port (k8s → Postgres)

### Task C1: New `internal/forwarder/bipcache` — Postgres-backed cache with NOTIFY + 5m resync

**Files:**
- Create: `internal/forwarder/bipcache/cache.go`
- Create: `internal/forwarder/bipcache/cache_test.go`
- Create: `internal/forwarder/bipcache/doc.go`

Design: in-memory map `(targetKind, targetName) → []BIPRow ordered by name ASC`, swapped atomically via `atomic.Pointer`. `Resolve(kind, name)` returns the alphabetically-LAST entry (matches existing `bip.ResolveWinner` semantics). Background loop subscribes to `ach_backend_identity_policies_changed` (event-driven) and also `time.Tick(5*time.Minute)` (safety net). Both paths call `db.ListAllBIPs` and rebuild the index.

- [ ] **Step 1: Write failing tests**

```go
func TestBIPCache_ResolveAlphaLast(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	for _, n := range []string{"aaa", "zzz", "mmm"} {
		_ = db.UpsertBIP(ctx, pool, db.BIPRow{
			Namespace: "ach-system", Name: n, TargetKind: "MCPServer",
			TargetName: "github-mcp", ForwardIdentityJWT: n == "zzz",
			ResourceVersion: "1",
		})
	}
	c := New(pool, "ach-system", testLogger(t))
	require.NoError(t, c.Refresh(ctx))
	w := c.Resolve("MCPServer", "github-mcp")
	require.NotNil(t, w)
	require.Equal(t, "zzz", w.Name)
	require.True(t, w.ForwardIdentityJWT)
}

// Revision 1: ported B4/B6 cases from internal/forwarder/bip/index_test.go to
// preserve the explicit-opt-out-LAST-means-nil semantic.

// B4 — Single opt-out BIP → Resolve returns nil.
func TestBIPCache_SingleOptOut_ResolveNil(t *testing.T) {
	pool := newTestPool(t); defer pool.Close()
	_ = db.UpsertBIP(ctx, pool, db.BIPRow{
		Namespace: "ach-system", Name: "only", TargetKind: "MCPServer",
		TargetName: "github-mcp", ForwardIdentityJWT: false, ResourceVersion: "1",
	})
	c := New(pool, "ach-system", testLogger(t))
	require.NoError(t, c.Refresh(ctx))
	require.Nil(t, c.Resolve("MCPServer", "github-mcp"))
}

// B6 — {a:opt-in, b:opt-out} → Resolve returns nil (LAST is b/opt-out).
func TestBIPCache_OptInThenOptOut_LastOptOutWins(t *testing.T) {
	pool := newTestPool(t); defer pool.Close()
	for _, e := range []struct{ name string; on bool }{
		{"aaa", true}, {"zzz", false},
	} {
		_ = db.UpsertBIP(ctx, pool, db.BIPRow{
			Namespace: "ach-system", Name: e.name, TargetKind: "MCPServer",
			TargetName: "github-mcp", ForwardIdentityJWT: e.on, ResourceVersion: "1",
		})
	}
	c := New(pool, "ach-system", testLogger(t))
	require.NoError(t, c.Refresh(ctx))
	require.Nil(t, c.Resolve("MCPServer", "github-mcp"),
		"alpha-LAST is opt-out — Resolve must return nil per ResolveWinner semantics")
}

func TestBIPCache_NOTIFYInvalidates(t *testing.T) {
	pool := newTestPool(t); defer pool.Close()
	c := New(pool, "ach-system", testLogger(t))
	ctx, cancel := context.WithCancel(context.Background()); defer cancel()
	go func() { _ = c.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)

	_ = db.UpsertBIP(ctx, pool, db.BIPRow{
		Namespace: "ach-system", Name: "aaa", TargetKind: "MCPServer",
		TargetName: "github-mcp", ForwardIdentityJWT: true, ResourceVersion: "1",
	})
	_ = db.Emit(ctx, pool, "ach_backend_identity_policies_changed", "ach-system/aaa")

	require.Eventually(t, func() bool {
		w := c.Resolve("MCPServer", "github-mcp")
		return w != nil && w.Name == "aaa"
	}, 2*time.Second, 20*time.Millisecond)
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement the cache**

`internal/forwarder/bipcache/cache.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package bipcache

import (
	"context"
	"sort"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

const (
	channelChanged    = "ach_backend_identity_policies_changed"
	periodicInterval  = 5 * time.Minute
)

type Cache struct {
	pool *pgxpool.Pool
	ns   string
	log  logr.Logger

	index atomic.Pointer[map[targetKey][]db.BIPRow]
}

type targetKey struct{ Kind, Name string }

func New(pool *pgxpool.Pool, ns string, log logr.Logger) *Cache {
	c := &Cache{pool: pool, ns: ns, log: log}
	empty := map[targetKey][]db.BIPRow{}
	c.index.Store(&empty)
	return c
}

// Resolve mirrors internal/forwarder/bip.ResolveWinner semantics 1:1:
//
//   1. Take the alphabetically-LAST BIP row matching (targetKind, targetName).
//   2. If that row's ForwardIdentityJWT is FALSE, it is an explicit opt-out —
//      return nil (the caller does NOT mint a per-target JWT).
//   3. Otherwise return the row (opt-in winner).
//
// The opt-out semantics are NOT just a bool check; they are the contract
// today's forwarder relies on, and B4/B6 in bip/index_test.go fix the
// behavior. Do not "simplify" by always returning the last row.
func (c *Cache) Resolve(targetKind, targetName string) *db.BIPRow {
	idx := *c.index.Load()
	rows := idx[targetKey{Kind: targetKind, Name: targetName}]
	if len(rows) == 0 {
		return nil
	}
	w := rows[len(rows)-1] // alpha-last winner
	if !w.ForwardIdentityJWT {
		return nil // explicit opt-out, no JWT mint
	}
	return &w
}

func (c *Cache) Refresh(ctx context.Context) error {
	rows, err := db.ListAllBIPs(ctx, c.pool, c.ns)
	if err != nil { return err }
	idx := make(map[targetKey][]db.BIPRow)
	for _, r := range rows {
		if r.DeletionTimestamp != nil { continue }
		k := targetKey{Kind: r.TargetKind, Name: r.TargetName}
		idx[k] = append(idx[k], r)
	}
	for k := range idx {
		sort.Slice(idx[k], func(i, j int) bool { return idx[k][i].Name < idx[k][j].Name })
	}
	c.index.Store(&idx)
	return nil
}

func (c *Cache) Run(ctx context.Context) error {
	if err := c.Refresh(ctx); err != nil {
		c.log.Error(err, "initial bipcache refresh; will retry")
	}
	dbLis := db.NewListener(c.pool, c.log)
	dbLis.Subscribe(channelChanged, func(_ string) {
		if err := c.Refresh(ctx); err != nil {
			c.log.Error(err, "bipcache event-driven refresh")
		}
	})
	go func() { _ = dbLis.Run(ctx) }()

	t := time.NewTicker(periodicInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done(): return nil
		case <-t.C:
			if err := c.Refresh(ctx); err != nil {
				c.log.Error(err, "bipcache periodic refresh")
			}
		}
	}
}
```

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/forwarder/bipcache/
git commit -m "feat(forwarder): Postgres-backed BIP cache with NOTIFY + 5m periodic refresh"
```

---

### Task C2: New `internal/forwarder/envstore` — Postgres-backed environment cache

**Files:**
- Create: `internal/forwarder/envstore/store.go`
- Create: `internal/forwarder/envstore/store_test.go`
- Create: `internal/forwarder/envstore/doc.go`

Same pattern as C1 — `atomic.Pointer[map[string]db.EnvironmentRow]` keyed by `name`; events on `ach_environments_changed`; 5m periodic. Surface:

```go
func (s *Store) Get(name string) (*db.EnvironmentRow, bool)
func (s *Store) List() []db.EnvironmentRow
func (s *Store) Run(ctx context.Context) error
func (s *Store) Refresh(ctx context.Context) error
```

- [ ] **Steps 1-5**: identical TDD structure to C1.

- [ ] **Commit**:

```bash
git commit -am "feat(forwarder): Postgres-backed Environment store with NOTIFY + 5m periodic refresh"
```

---

### Task C3: Port forwarder boot LiteLLMConnection resolve from k8s APIReader to Postgres

**Files:**
- Modify: `internal/forwarder/litellmconn/resolver.go`
- Modify: `internal/forwarder/litellmconn/resolver_test.go`
- Modify: `cmd/ach/cmd/forwarder.go`

The current `litellmconn.Resolve(ctx, mgr.GetAPIReader(), ns, log)` does:
1. `Get LiteLLMConnection/default` via APIReader
2. `Get MasterKeySecret` via APIReader
3. Return `{Endpoint, MasterKey}`

The new path does step 1 against Postgres (`db.GetDefaultLiteLLMConnection`) and step 2 against k8s (Secret is not a CRD; it stays in k8s). The function signature changes — callers now pass `*pgxpool.Pool` and `corev1Reader` (still a `mgr.GetAPIReader()`).

- [ ] **Step 1: Write failing tests** (httptest-style — k8s side mocked via fake client; pool seeded with `db.UpsertLiteLLMConnection`).

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Rewrite Resolve**

```go
func Resolve(ctx context.Context, pool *pgxpool.Pool, k8sReader client.Reader, ns string, log logr.Logger) (*Resolved, error) {
	row, err := db.GetDefaultLiteLLMConnection(ctx, pool, ns)
	if err != nil { return nil, fmt.Errorf("db.GetDefaultLiteLLMConnection: %w", err) }
	if row == nil {
		// Same retry-until-present behaviour as before. Cluster hydration is
		// async; the Connection CR + its operator projection may not exist yet.
		return nil, ErrLiteLLMConnectionNotReady
	}
	sec := &corev1.Secret{}
	err = k8sReader.Get(ctx, client.ObjectKey{
		Namespace: row.MasterKeySecretNamespace, Name: row.MasterKeySecretName,
	}, sec)
	if err != nil { return nil, fmt.Errorf("get master-key Secret: %w", err) }
	key, ok := sec.Data[row.MasterKeySecretKey]
	if !ok || len(key) == 0 {
		return nil, fmt.Errorf("master-key Secret %s/%s missing key %q",
			row.MasterKeySecretNamespace, row.MasterKeySecretName, row.MasterKeySecretKey)
	}
	return &Resolved{Endpoint: row.Endpoint, MasterKey: string(key)}, nil
}
```

In `cmd/ach/cmd/forwarder.go`, replace the call:

```go
- llmRes, err := resolveLiteLLMWithRetry(ctx, mgr.GetAPIReader(), cfg.Namespace, logger)
+ llmRes, err := resolveLiteLLMWithRetry(ctx, pool, mgr.GetAPIReader(), cfg.Namespace, logger)
```

`resolveLiteLLMWithRetry` keeps its 60s retry loop; it just polls both Postgres and k8s instead of k8s alone.

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git commit -am "refactor(forwarder): resolve LiteLLMConnection endpoint from Postgres (Secret stays in k8s)"
```

---

### Task C4: Port `proxy/handlers.go` ResolveWinner → bipcache

**Files:**
- Modify: `internal/forwarder/proxy/handlers.go`
- Modify: `cmd/ach/cmd/forwarder.go` (inject bipcache.Cache into Deps)
- Modify: `internal/forwarder/server.go` (Deps struct)

- [ ] **Step 1: Identify and replace**

In `internal/forwarder/proxy/handlers.go:109`, replace:

```go
- winner := bip.ResolveWinner(ctx, deps.K8sClient, ns, targetKind, targetName)
+ winner := deps.BIPCache.Resolve(targetKind, targetName)
```

Add `BIPCache *bipcache.Cache` to `forwarder.Deps`. Wire it in `cmd/ach/cmd/forwarder.go` after the cache is constructed; `mgr.Add(cacheRunnable{cache})` so its Run method participates in manager lifecycle.

- [ ] **Step 2: Run** `./scripts/dev.sh make unit-pkg PKG=./internal/forwarder/...` → existing handler tests need fixture updates (Deps construction). Adjust them — use a fake BIPCache (table-driven map) for unit tests.

- [ ] **Step 3: Run** → PASS.

- [ ] **Step 4: Commit**

```bash
git commit -am "refactor(forwarder): use bipcache for JWT policy decisions (no more informer ResolveWinner)"
```

---

### Task C5: Port `precheck/check.go` (Get + List Environment) → envstore

**Files:**
- Modify: `internal/forwarder/precheck/check.go`
- Modify: `internal/forwarder/server.go` (Deps struct)
- Modify: `cmd/ach/cmd/forwarder.go`

- [ ] **Step 1: Identify and replace**

In `internal/forwarder/precheck/check.go:89` (checkEk branch), replace `deps.K8sClient.Get(...)` with `deps.EnvStore.Get(name)`.

In `internal/forwarder/precheck/check.go:129` (checkPk branch), replace `deps.K8sClient.List(...)` with `deps.EnvStore.List()`.

Field access switches from CR (`env.Spec.Runtime.MCPServers`, `env.DeletionTimestamp`, `env.Spec.AuthorizedTeams`) to row (`row.RuntimeMCPServers`, `row.DeletionTimestamp`, `row.AuthorizedTeams`).

- [ ] **Step 2: Run** → handler tests adjust; use a fake EnvStore.
- [ ] **Step 3: Run** → PASS.
- [ ] **Step 4: Commit**

```bash
git commit -am "refactor(forwarder): use envstore for precheck (no more informer Get/List)"
```

---

### Task C6: Trim controller-runtime manager wiring in forwarder

**Files:**
- Modify: `cmd/ach/cmd/forwarder.go`

The forwarder's controller-runtime manager STAYS — but only because it owns the `ach-jwt-signing-keys` Secret informer (hot-reload of the JWT signer seed). Everything else goes.

Delete:
- The `bip.RegisterIndex(ctx, mgr)` call (Task C1's cache replaces the field-indexer use).
- The `for _, obj := range []client.Object{ &achv1alpha1.BackendIdentityPolicy{}, &achv1alpha1.Environment{}, &corev1.Secret{} }` loop — keep ONLY the Secret entry:

```go
if _, err := mgr.GetCache().GetInformer(ctx, &corev1.Secret{}); err != nil {
	return out, fmt.Errorf("informer Secret: %w", err)
}
```

- The `K8sClient: mgr.GetClient()` field assignment in `forwarder.Deps` literal — replace with `BIPCache: bipCache, EnvStore: envStore`.

Add: construction of `bipCache := bipcache.New(pool, cfg.Namespace, logger.WithName("bipcache"))` + `envStore := envstore.New(pool, cfg.Namespace, logger.WithName("envstore"))`; `mgr.Add(asRunnable(bipCache.Run))` and same for envStore.

- [ ] **Step 1: Run** `./scripts/dev.sh make unit-pkg PKG=./cmd/...` and `./scripts/dev.sh make envtest-run`.
- [ ] **Step 2: Commit**:

```bash
git commit -am "refactor(forwarder): drop BIP+Environment informers; keep Secret informer for JWT hot-reload"
```

---

### Task C7: Trim Helm RBAC for forwarder ServiceAccount (revision 1: cross-namespace)

**Why this needed an update (revision 1):** The forwarder SA lives in the chart's namespace (default `ach-system`). `LiteLLMConnection.spec.masterKeySecretRef` can point at a Secret in a different namespace (`litellm-system/litellm-master-key` in dev fixtures). A namespace-scoped `Role` in `ach-system` with `resourceNames: [litellm-master-key]` cannot grant access to a Secret in `litellm-system` — the binding has to land in the Secret's namespace. We split this into two Roles + two RoleBindings.

**Files:**
- Modify: `deploy/helm/ach/templates/forwarder-rbac.yaml`
- Modify: `deploy/helm/ach/values.yaml` (expose `forwarder.litellmMasterKey.{namespace,name,key}` so the RBAC template knows where to land)

- [ ] **Step 1: Identify and replace**

Drop every `apiGroups: ["ach.ackstorm.ai"]` rule. Replace with:

```yaml
# Role 1: in the forwarder's own namespace — for the JWT signing seed.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ include "ach.fullname" . }}-forwarder-secrets
  namespace: {{ .Release.Namespace }}
rules:
- apiGroups: [""]
  resources: ["secrets"]
  resourceNames: ["ach-jwt-signing-keys"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ include "ach.fullname" . }}-forwarder-secrets
  namespace: {{ .Release.Namespace }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ include "ach.fullname" . }}-forwarder-secrets
subjects:
- kind: ServiceAccount
  name: {{ include "ach.serviceAccountName" . }}-forwarder
  namespace: {{ .Release.Namespace }}
---
# Role 2: in the LiteLLM master-key Secret's namespace — for the upstream key.
# If .Values.forwarder.litellmMasterKey.namespace == .Release.Namespace, the
# template can collapse this with Role 1; otherwise we need a separate Role
# in that namespace plus a cross-namespace RoleBinding.
{{- if ne .Values.forwarder.litellmMasterKey.namespace .Release.Namespace }}
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ include "ach.fullname" . }}-forwarder-litellm-key
  namespace: {{ .Values.forwarder.litellmMasterKey.namespace }}
rules:
- apiGroups: [""]
  resources: ["secrets"]
  resourceNames: [{{ .Values.forwarder.litellmMasterKey.name | quote }}]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ include "ach.fullname" . }}-forwarder-litellm-key
  namespace: {{ .Values.forwarder.litellmMasterKey.namespace }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ include "ach.fullname" . }}-forwarder-litellm-key
subjects:
- kind: ServiceAccount
  name: {{ include "ach.serviceAccountName" . }}-forwarder
  namespace: {{ .Release.Namespace }}   # subject is in ACH ns; RoleBinding lives in Secret's ns
{{- end }}
```

Add to `deploy/helm/ach/values.yaml`:

```yaml
forwarder:
  litellmMasterKey:
    namespace: litellm-system          # MUST match LiteLLMConnection.spec.masterKeySecretRef.namespace
    name: litellm-master-key
    key: master_key
```

> **Validation note:** if the user changes `litellmMasterKey.namespace` after install, they must `helm upgrade` to land the cross-namespace RoleBinding in the new Secret's namespace. Document this in the chart README.

- [ ] **Step 2: Run** `make e2e-focus FOCUS="forwarder"` — must still pass.
- [ ] **Step 3: Run** a multi-namespace smoke: provision `litellm-master-key` in `litellm-system`, scale forwarder, and verify `kubectl auth can-i get secret/litellm-master-key -n litellm-system --as=system:serviceaccount:ach-system:ach-forwarder` returns `yes`.
- [ ] **Step 4: Commit**:

```bash
git commit -am "chore(helm): split forwarder RBAC across ach-system + LiteLLM-key namespace"
```

---

## Phase D — Cleanup & Verification

### Task D1: Remove dead `ctrl` import from content-service

**Files:**
- Modify: `cmd/ach/cmd/content_service.go`

- [ ] **Step 1: Delete the unused `ctrl "sigs.k8s.io/controller-runtime"` import** (the exploration agent confirmed no in-file use).
- [ ] **Step 2: Run** `./scripts/dev.sh go build ./...` → green.
- [ ] **Step 3: Commit**:

```bash
git commit -am "chore(content-service): drop dead controller-runtime import"
```

---

### Task D2: Delete obsolete `internal/forwarder/bip` package

**Files:**
- Delete: `internal/forwarder/bip/index.go`
- Delete: `internal/forwarder/bip/resolver.go`
- Delete: `internal/forwarder/bip/*_test.go`

- [ ] **Step 1: Confirm zero importers**

Run: `./scripts/dev.sh grep -rn "internal/forwarder/bip" cmd internal | grep -v "internal/forwarder/bipcache"`
Expected: empty.

- [ ] **Step 2: `rm -rf internal/forwarder/bip`**.
- [ ] **Step 3: Run** `./scripts/dev.sh go build ./...` → green.
- [ ] **Step 4: Commit**:

```bash
git commit -am "chore(forwarder): remove obsolete bip package (replaced by bipcache)"
```

---

### Task D3: Update CLAUDE.md architecture surfaces

**Files:**
- Modify: `CLAUDE.md`

Update:
1. The architecture diagram — show Postgres at the center with operator writing in and platform-api/forwarder/content-service reading from it. CRs flow only into the operator.
2. The "Repository-specific patterns" section — strike the "BIPs are owned by the operator but consumed by the forwarder via an informer-backed cache" line and replace with a one-paragraph description of the new Postgres + NOTIFY/LISTEN flow.
3. Add a "Common failure modes" entry for origin conflicts:

```markdown
### ❌ Operator condition: `Synced=False reason=ConflictWithUIRow`
A CR's projection collides with a row created by the UI (origin='ui'). The
operator refuses to clobber the UI-managed row.
✅ Rename the CR, or delete the UI row from Postgres before letting the
operator reconcile. UI and CR row names must be disjoint within a (namespace).
```

- [ ] **Step 1: Edit CLAUDE.md**.
- [ ] **Step 2: Commit**:

```bash
git commit -am "docs(claude): update arch diagram + common failure modes for Postgres-as-SoT"
```

---

### Task D4: Full e2e green from clean

- [ ] **Step 1**: `make cluster-down`
- [ ] **Step 2**: `./scripts/dev.sh make e2e-full`
Expected: PASS — full suite green from clean.

- [ ] **Step 3: Smoke test cross-process subscription path**

Manual: bring cluster up, apply `examples/09-backendidentitypolicy-context7.yaml`, then:

```bash
./scripts/dev.sh kubectl -n ach-system exec deploy/postgres -- \
  psql -U ach -c "SELECT name, target_kind, target_name, forward_identity_jwt FROM backend_identity_policies"
```

Expected: row appears within ~1s. Edit the CR (`kubectl edit bip context7-bip`); the row updates within ~1s of the operator reconcile.

- [ ] **Step 4**: Smoke test platform-api operator-down survival

```bash
./scripts/dev.sh kubectl -n ach-system scale deploy/ach-operator --replicas=0
./scripts/dev.sh kubectl -n ach-system exec deploy/ach-platform-api -- \
  curl -s localhost:8080/platform/environments -H "Authorization: Bearer $PK"
```

Expected: still returns the cached environments (operator down doesn't break reads). Restore operator: `kubectl scale deploy/ach-operator --replicas=1`.

- [ ] **Step 5 (revision 1): Cold-restart smoke — platform-api boots Postgres-only**

The version in Step 4 can pass via the platform-api's existing in-memory state and still miss the actual goal. Sharper test: restart platform-api WITH operator at 0, then curl. If the chart's RBAC trim landed correctly, platform-api has no CRD verbs — it MUST boot purely from Postgres.

```bash
./scripts/dev.sh kubectl -n ach-system scale deploy/ach-operator --replicas=0
./scripts/dev.sh kubectl -n ach-system rollout restart deploy/ach-platform-api
./scripts/dev.sh kubectl -n ach-system rollout status  deploy/ach-platform-api --timeout=2m

# Confirm zero CRD permissions remain on the platform-api SA:
./scripts/dev.sh kubectl -n ach-system auth can-i list environments.ach.ackstorm.ai \
  --as=system:serviceaccount:ach-system:ach-platform-api
# Expected: "no"

# And the API still serves:
PK=$(grep -oP '"personal_key":\s*"\K[^"]+' /tmp/pk.json)
./scripts/dev.sh kubectl -n ach-system exec deploy/ach-platform-api -- \
  curl -sf localhost:8080/platform/environments -H "Authorization: Bearer $PK" | jq '.items | length'
# Expected: matches `kubectl get environments -n ach-system | wc -l` (less the header)
```

Restore operator at end: `kubectl -n ach-system scale deploy/ach-operator --replicas=1`.

This step is the actual proof of the architecture goal: **no CR access from platform-api, ever**, including cold boot.

- [ ] **Step 6 (revision 1): Cold-restart smoke for forwarder**

Same idea for the forwarder:

```bash
./scripts/dev.sh kubectl -n ach-system rollout restart deploy/ach-forwarder
./scripts/dev.sh kubectl -n ach-system rollout status  deploy/ach-forwarder --timeout=2m
./scripts/dev.sh kubectl -n ach-system auth can-i list backendidentitypolicies.ach.ackstorm.ai \
  --as=system:serviceaccount:ach-system:ach-forwarder
# Expected: "no"

# JWT trust path still works (forwarder reachable at http://localhost:8080 via
# port-forward or the local ingress that exposes it on that port):
curl -sf -H "Authorization: Bearer $PK" \
  http://localhost:8080/v1/chat/completions -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}]}'
# Expected: 200 (or upstream-defined response); not 500/403 from forwarder
```

---

## Self-Review Checklist

Run through this list after the plan is saved:

1. **Spec coverage**: Every k8s touchpoint mapped in the analysis section is closed by a task (Environment store→B1; admin/refresh→B2; BIP ResolveWinner→C4; Env Get/List→C5; LiteLLMConnection resolve→C3; jwt Secret informer→preserved in C6). ✓
2. **Placeholder scan**: No "TBD" steps; every code step has a code block. ✓
3. **Type consistency**: `EnvironmentRow.AuthorizedTeams`, `BIPRow.TargetKind`, `LiteLLMConnectionRow.Endpoint` used identically across tasks. ✓
4. **Channel names**: `ach_<table>_changed` form, lowercase, table-name-suffixed. Listed once in Task A9; referenced in C1/C2 by the same constants. ✓
5. **Migration numbering**: `000005` (origin + locked), `000006` (litellm_connections), `000007` (backend_identity_policies). Sequential; no gaps with existing `000001-000004`. ✓
6. **TDD discipline**: every task has Step 1 (failing test), Step 2 (run/see fail), Step 3+ (implement), step (run/see pass), commit. ✓
7. **Locked/origin invariant**: CHECK constraint `(origin <> 'cr' OR locked = TRUE)` makes a CR-origin row that's NOT locked impossible. Belt-and-suspenders against accidental UPDATE statements that touch only `locked`. ✓
8. **(revision 1) NOTIFY/write atomicity**: every projection-mutating call site (A1 SQL, A7, A8, A9 reconcilers, A8 finalizer, A7 finalizer) uses `db.WithTxNotify`. No bare `pool.Exec("SELECT pg_notify")` outside the helper except the platform-api `/admin/refresh` handler (B2), which writes its column and `pg_notify` in the same tx via the same helper. ✓
9. **(revision 1) BIP opt-out semantics preserved**: `bipcache.Resolve` returns nil for alpha-LAST opt-out rows. B4/B6 tests ported from `internal/forwarder/bip/index_test.go`. ✓
10. **(revision 1) controller-runtime API correctness**: A10 + A11 use `source.Channel{Source: chan event.GenericEvent}` plumbed through `builder.WatchesRawSource` — no private-API workqueue access. ✓
11. **(revision 1) Cross-namespace master-key RBAC**: C7 lands two Role+RoleBinding pairs (forwarder ns + master-key ns) when those namespaces differ. Helm values expose the master-key namespace. ✓
12. **(revision 1) Listener uses dedicated conn**: A3 opens `pgx.Connect`, not `pool.Acquire`, so LISTEN sessions don't park pool conns. ✓
13. **(revision 1) Cold-restart smoke proves the architectural goal**: D4 Step 5 restarts platform-api with operator at 0 AND verifies the SA has no CRD verbs. D4 Step 6 same for forwarder. ✓

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-28-postgres-source-of-truth.md`.**

Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Each phase (A→B→C→D) ships independently as ~4 commits, so we can pause for review at the phase boundaries.

2. **Inline Execution** — Execute tasks in this session using `superpowers:executing-plans`, batch execution with checkpoints at each phase boundary.

**Which approach?**
