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

// Channel is the Postgres NOTIFY channel name the projection writers use
// after committing a BackendIdentityPolicy projection write. The cache
// LISTENs on it for event-driven refresh.
const Channel = "ach_backend_identity_policies_changed"

// refreshInterval bounds the staleness of cached rows when the NOTIFY
// path is broken (Listener conn dropped + reconnected, missed events).
const refreshInterval = 5 * time.Minute

// targetKey is the cache's lookup key. Mirrors the (kind, name) tuple
// of the BIP projection's spec.target.
type targetKey struct {
	Kind string
	Name string
}

// Cache is a Postgres-backed BackendIdentityPolicy lookup table. The
// underlying state lives in an atomic.Pointer to a map keyed by
// (kind, name) → []BIPRow sorted by Name ASC; Resolve picks the
// alpha-FIRST winner per the ResolveWinner contract.
//
// Zero-value Cache is invalid — use New.
type Cache struct {
	pool *pgxpool.Pool
	ns   string
	log  logr.Logger

	rows atomic.Pointer[map[targetKey][]db.BIPRow]
}

// New constructs a Cache for the given namespace. The atomic pointer
// holds an empty map until Refresh runs at least once.
func New(pool *pgxpool.Pool, ns string, log logr.Logger) *Cache {
	c := &Cache{pool: pool, ns: ns, log: log}
	empty := map[targetKey][]db.BIPRow{}
	c.rows.Store(&empty)
	return c
}

// Resolve returns the BIPRow that wins the (targetKind, targetName)
// lookup or nil when no policy applies. The contract mirrors the legacy
// bip.ResolveWinner:
//
//  1. zero matches               → nil
//  2. alpha-FIRST.ForwardIdentityJWT == false (explicit opt-out) → nil
//  3. otherwise the alpha-FIRST row
//
// The returned pointer addresses a copy of the cached row; callers may
// retain it past the next Refresh.
func (c *Cache) Resolve(targetKind, targetName string) *db.BIPRow {
	m := c.rows.Load()
	rows := (*m)[targetKey{Kind: targetKind, Name: targetName}]
	if len(rows) == 0 {
		return nil
	}
	first := rows[0] // alpha-FIRST winner (frozen §9.3 + house convention; G15)
	if !first.ForwardIdentityJWT {
		return nil
	}
	return &first
}

// Refresh reads the full BIP set for the namespace from Postgres and
// swaps the atomic pointer. Errors propagate so the Run safety-net
// ticker can log + retry on the next tick.
func (c *Cache) Refresh(ctx context.Context) error {
	rows, err := db.ListAllBIPs(ctx, c.pool, c.ns)
	if err != nil {
		return err
	}
	next := map[targetKey][]db.BIPRow{}
	for _, r := range rows {
		k := targetKey{Kind: r.TargetKind, Name: r.TargetName}
		next[k] = append(next[k], r)
	}
	// db.ListAllBIPs already orders by name ASC at the SQL layer, but
	// guard against future writers by sorting per-group as well. Rows are
	// name-ASC so rows[0] is the alpha-FIRST winner Resolve returns.
	for k := range next {
		sort.SliceStable(next[k], func(i, j int) bool {
			return next[k][i].Name < next[k][j].Name
		})
	}
	c.rows.Store(&next)
	return nil
}

// Run implements manager.Runnable: initial refresh + LISTEN on Channel +
// 5-minute ticker safety net. Blocks until ctx is cancelled.
func (c *Cache) Run(ctx context.Context) error {
	return db.RunRefreshLoop(ctx, c.pool, Channel, refreshInterval, c.log, c.Refresh)
}
