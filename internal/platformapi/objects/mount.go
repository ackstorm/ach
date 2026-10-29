// SPDX-License-Identifier: Apache-2.0

// Package objects implements the GitOps-wins UI Objects API (G2) — the
// platform-api write surface for authoring ACH objects from a UI/admin client.
// v1 scopes the UI-writable set to Environment ONLY (the external-ref kinds'
// projection stores operator-computed cache state, not the source spec, so they
// can neither be authored nor exported from the projection — see
// internal/db/ui_objects.go).
//
// The contract is GitOps-wins: a UI-created Environment is a DRAFT (origin='ui',
// status conditions NULL until the operator reconciles the equivalent CR). The
// DB layer (internal/db) gates every UI write WHERE origin='ui' so the UI path
// can never clobber an operator-owned row. The round-trip is: draft in UI →
// export YAML (export.go) → commit + kubectl apply → operator takeover.
package objects

import (
	"context"
	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

// Deps is the dependency bag server.go composes from the top-level platform-api
// Deps and hands to Mount. The shape mirrors admin.Deps so the wire-up stays
// uniform across handler packages.
type Deps struct {
	// Pool is the Postgres pool — the production Store (dbStore) is built from
	// it inside Mount.
	Pool *pgxpool.Pool
	// Namespace is POD_NAMESPACE — the namespace every UI-authored row is
	// keyed under (the projection is single-namespace per process).
	Namespace string
	// Audit is the audit slog logger.
	Audit *slog.Logger
	// Logger is the operational (NOT audit) logger.
	Logger *slog.Logger
	// DisableUIWrites mirrors ACH_DISABLE_UI_WRITES — when true, every write
	// handler short-circuits to 403 ui_writes_disabled before touching the DB.
	DisableUIWrites bool
}

// Store is the storage seam the handlers depend on so they can be unit-tested
// against an in-memory fake with no Postgres. dbStore is the production
// implementation; the namespace is baked in at construction so handlers only
// pass a name.
type Store interface {
	Get(ctx context.Context, name string) (*db.EnvironmentRow, error)
	List(ctx context.Context) ([]db.EnvironmentRow, error)
	Insert(ctx context.Context, row db.EnvironmentRow) error
	Update(ctx context.Context, row db.EnvironmentRow) error
	Delete(ctx context.Context, name string) error
}

// dbStore is the production Store, wrapping a pgx pool + the process namespace.
type dbStore struct {
	pool *pgxpool.Pool
	ns   string
}

func (s dbStore) Get(ctx context.Context, name string) (*db.EnvironmentRow, error) {
	return db.GetEnvironmentByName(ctx, s.pool, s.ns, name)
}

func (s dbStore) List(ctx context.Context) ([]db.EnvironmentRow, error) {
	return db.ListEnvironments(ctx, s.pool, s.ns)
}

func (s dbStore) Insert(ctx context.Context, row db.EnvironmentRow) error {
	return db.InsertUIEnvironment(ctx, s.pool, row)
}

func (s dbStore) Update(ctx context.Context, row db.EnvironmentRow) error {
	return db.UpdateUIEnvironment(ctx, s.pool, row)
}

func (s dbStore) Delete(ctx context.Context, name string) error {
	return db.DeleteUIEnvironment(ctx, s.pool, s.ns, name)
}

// validKind reports whether kind is a UI-writable object kind. v1 admits only
// "environments"; every other path segment is rejected 404 so the route subtree
// surfaces a uniform "unknown or non-UI-writable kind" error rather than 405.
func validKind(kind string) bool {
	return kind == "environments"
}

// Mount returns a chi.Router subtree configurator registering the UI Objects
// CRUD + YAML-export endpoints under /{kind}. The caller mounts it inside the
// authenticated, AdminOnly-gated chi.Group (server.go). The production dbStore
// is built from deps here; tests use mountWithStore directly to inject a fake.
//
// Routes (under the mount point, e.g. /platform/objects):
//
//	GET    /{kind}            — list
//	GET    /{kind}/{name}     — get
//	POST   /{kind}            — create
//	PATCH  /{kind}/{name}     — patch (JSON merge)
//	DELETE /{kind}/{name}     — delete
//	GET    /{kind}/{name}/yaml — canonical YAML export
func Mount(deps Deps) func(chi.Router) {
	store := dbStore{pool: deps.Pool, ns: deps.Namespace}
	return mountWithStore(deps, store)
}

// mountWithStore is the testable core of Mount: it registers the routes against
// an injected Store. Mount wires the production dbStore; unit tests pass a fake.
func mountWithStore(deps Deps, s Store) func(chi.Router) {
	return func(r chi.Router) {
		r.Get("/{kind}", listHandler(deps, s))
		r.Get("/{kind}/{name}", getHandler(deps, s))
		r.Post("/{kind}", createHandler(deps, s))
		r.Patch("/{kind}/{name}", patchHandler(deps, s))
		r.Delete("/{kind}/{name}", deleteHandler(deps, s))
		r.Get("/{kind}/{name}/yaml", exportHandler(deps, s))
	}
}
