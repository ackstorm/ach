// SPDX-License-Identifier: Apache-2.0

// Package agentstore is the gateway's Postgres-backed exposed-agent route cache.
// It mirrors internal/forwarder/bipcache: an atomic.Pointer map refreshed on
// LISTEN ach_achagents_changed plus a 5-minute safety-net ticker (the shared
// db.RunRefreshLoop). Keyed "namespace/serviceName" → upstream base URL (the
// public /agents/{ns}/{service} URL carries the Service name, e.g.
// achagent-gh, not the ACHAgent CR name).
package agentstore

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

// refreshInterval bounds staleness when the NOTIFY path is broken (dropped
// Listener conn, missed events).
const refreshInterval = 5 * time.Minute

// Store maps (namespace, serviceName) → upstream base URL for exposed agents.
// Zero-value is invalid — use New.
type Store struct {
	pool *pgxpool.Pool
	log  logr.Logger
	rows atomic.Pointer[map[string]string]
}

// New constructs a Store. The atomic pointer holds an empty map until the
// first Refresh, so Upstream is safe to call immediately.
func New(pool *pgxpool.Pool, log logr.Logger) *Store {
	s := &Store{pool: pool, log: log}
	empty := map[string]string{}
	s.rows.Store(&empty)
	return s
}

// Upstream returns the upstream base URL (no path) for an exposed agent, or
// ("", false) when the (ns, serviceName) pair is not a routable exposed agent.
func (s *Store) Upstream(ns, serviceName string) (string, bool) {
	m := s.rows.Load()
	u, ok := (*m)[ns+"/"+serviceName]
	return u, ok
}

// Refresh reloads the full webhook-agent route set from Postgres and swaps the
// atomic pointer.
func (s *Store) Refresh(ctx context.Context) error {
	rows, err := db.ListExposedAgents(ctx, s.pool)
	if err != nil {
		return err
	}
	next := make(map[string]string, len(rows))
	for _, r := range rows {
		next[r.Namespace+"/"+r.ServiceName] = fmt.Sprintf(
			"http://%s.%s.svc.cluster.local:%d", r.ServiceName, r.Namespace, r.ServicePort)
	}
	s.rows.Store(&next)
	return nil
}

// Run implements the standard cache lifecycle: initial refresh + LISTEN on
// AgentsChannel + 5-minute ticker. Blocks until ctx is cancelled.
func (s *Store) Run(ctx context.Context) error {
	return db.RunRefreshLoop(ctx, s.pool, db.AgentsChannel, refreshInterval, s.log, s.Refresh)
}
