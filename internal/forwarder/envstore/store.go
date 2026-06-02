// SPDX-License-Identifier: Apache-2.0

package envstore

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

// Channel is the Postgres NOTIFY channel name the projection writers use
// after committing an Environment projection write.
const Channel = "ach_environments_changed"

// refreshInterval bounds the staleness of cached rows when the NOTIFY
// path is broken (Listener conn dropped + reconnected, missed events).
const refreshInterval = 5 * time.Minute

// Store is a Postgres-backed Environment lookup table keyed by name.
//
// Zero-value Store is invalid — use New.
type Store struct {
	pool *pgxpool.Pool
	ns   string
	log  logr.Logger

	rows atomic.Pointer[map[string]db.EnvironmentRow]
}

// New constructs a Store for the given namespace. The atomic pointer
// holds an empty map until Refresh runs at least once.
func New(pool *pgxpool.Pool, ns string, log logr.Logger) *Store {
	s := &Store{pool: pool, ns: ns, log: log}
	empty := map[string]db.EnvironmentRow{}
	s.rows.Store(&empty)
	return s
}

// Get returns the row for name and true on hit, (zero, false) on miss.
// The returned pointer addresses a copy of the cached row; callers may
// retain it past the next Refresh.
func (s *Store) Get(name string) (*db.EnvironmentRow, bool) {
	m := s.rows.Load()
	r, ok := (*m)[name]
	if !ok {
		return nil, false
	}
	return &r, true
}

// List returns a copy of every cached row. Order is not specified — the
// precheck path performs a linear scan with intersection semantics, so
// ordering does not affect outcome.
func (s *Store) List() []db.EnvironmentRow {
	m := s.rows.Load()
	out := make([]db.EnvironmentRow, 0, len(*m))
	for _, r := range *m {
		out = append(out, r)
	}
	return out
}

// Refresh reads the full Environment set for the namespace from
// Postgres and swaps the atomic pointer. db.ListEnvironments excludes
// drain-mode rows at the SQL layer per the precheck contract (FWD-03
// rejects terminating envs from granting access).
func (s *Store) Refresh(ctx context.Context) error {
	rows, err := db.ListEnvironments(ctx, s.pool, s.ns)
	if err != nil {
		return err
	}
	next := make(map[string]db.EnvironmentRow, len(rows))
	for _, r := range rows {
		next[r.Name] = r
	}
	s.rows.Store(&next)
	return nil
}

// Run implements manager.Runnable: initial refresh + LISTEN on Channel +
// 5-minute ticker safety net. Blocks until ctx is cancelled.
func (s *Store) Run(ctx context.Context) error {
	return db.RunRefreshLoop(ctx, s.pool, Channel, refreshInterval, s.log, s.Refresh)
}
