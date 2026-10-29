// SPDX-License-Identifier: Apache-2.0

// Package envcache is the content-service's in-memory Environment projection
// cache. It mirrors the forwarder's internal/forwarder/envstore: an atomic
// snapshot of every Environment row in the namespace, refreshed on the
// `ach_environments_changed` Postgres NOTIFY channel plus a 5-minute periodic
// safety net (LISTEN is at-most-once on session loss). The prior Redis-backed
// 60s-TTL cache (D-07) is gone — Redis is retained only for the key/teams
// caches in cmd/ach/cmd/content_service.go.
//
// CS-09: the snapshot includes drain-mode rows (deletion_timestamp set) via
// db.ListEnvironmentsIncludingDraining, so the content-service keeps serving
// environments under finalizer drain — see authz.go resolveEnv.
package envcache

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

// Channel is the Postgres NOTIFY channel the operator emits on environment writes.
const Channel = "ach_environments_changed"

// refreshInterval bounds staleness when the NOTIFY path is broken (Listener
// conn dropped + reconnected, missed events).
const refreshInterval = 5 * time.Minute

// EnvRow is the authz-relevant subset of db.EnvironmentRow consumed by the
// content-service pipeline (authz.go). Only the fields the gates read are
// projected; the projection PK and staleness columns gate on the contentRow,
// not the EnvRow.
type EnvRow struct {
	AuthorizedTeams  []string
	ContextPrompts   []string
	ContextPlugins   []string
	ContextArtifacts []string
	ContextSkills    []string
}

// Cache is a Postgres-backed Environment lookup table keyed by name, refreshed
// via LISTEN/NOTIFY. Zero-value Cache is invalid — use New (or the white-box
// test seam in cache_unit_test.go).
type Cache struct {
	pool *pgxpool.Pool
	ns   string
	log  logr.Logger
	rows atomic.Pointer[map[string]EnvRow]
}

// New constructs a Cache for the given namespace. The atomic pointer holds an
// empty map until Refresh runs at least once.
func New(pool *pgxpool.Pool, ns string, log logr.Logger) *Cache {
	c := &Cache{pool: pool, ns: ns, log: log}
	empty := map[string]EnvRow{}
	c.rows.Store(&empty)
	return c
}

// Get returns the cached row for name and true on hit, (nil, false) on miss.
// ns is accepted for call-site symmetry with the prior interface; the cache is
// single-namespace (fixed at New). The returned pointer addresses a copy of
// the cached row.
func (c *Cache) Get(ns, name string) (*EnvRow, bool) {
	_ = ns
	m := c.rows.Load()
	r, ok := (*m)[name]
	if !ok {
		return nil, false
	}
	return &r, true
}

// NewWithRows builds a Cache pre-populated with rows and no pool — a test
// seam for callers in OTHER packages (e.g. the content-service authz tests)
// that need a populated cache without a Postgres backend. Refresh/Run would
// nil-panic on the absent pool, so it is only safe for Get-only assertions.
func NewWithRows(rows map[string]EnvRow) *Cache {
	c := &Cache{}
	cp := make(map[string]EnvRow, len(rows))
	for k, v := range rows {
		cp[k] = v
	}
	c.rows.Store(&cp)
	return c
}

// Refresh loads ALL environments including drain-mode rows (CS-09) and swaps
// the snapshot atomically.
func (c *Cache) Refresh(ctx context.Context) error {
	rows, err := db.ListEnvironmentsIncludingDraining(ctx, c.pool, c.ns)
	if err != nil {
		return err
	}
	next := make(map[string]EnvRow, len(rows))
	for _, r := range rows {
		next[r.Name] = EnvRow{
			AuthorizedTeams:  r.AuthorizedTeams,
			ContextPrompts:   r.ContextPrompts,
			ContextPlugins:   r.ContextPlugins,
			ContextArtifacts: r.ContextArtifacts,
			ContextSkills:    r.ContextSkills,
		}
	}
	c.rows.Store(&next)
	return nil
}

// Run implements the standard cache-freshness lifecycle: initial refresh +
// LISTEN on Channel + 5-minute ticker safety net. Blocks until ctx is
// cancelled.
func (c *Cache) Run(ctx context.Context) error {
	return db.RunRefreshLoop(ctx, c.pool, Channel, refreshInterval, c.log, c.Refresh)
}
