// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
)

// DefaultRefreshInterval is the §6.4 cadence at which the Snapshotter
// rebuilds its LiteLLM resource cache. The same value drives the
// EnvironmentReconciler's RequeueAfter so the slowest case where an
// Environment misses an event is still bounded to one refresh window.
const DefaultRefreshInterval = 5 * time.Minute

// initialRetryBackoff is the first sleep after a refresh failure. Each
// subsequent failure doubles the sleep, capped at DefaultRefreshInterval.
// Sized for the typical post-operator-restart race where the
// LiteLLMConnection reconciler completes its first probe within a few
// hundred milliseconds — issue #30.
const initialRetryBackoff = 1 * time.Second

// LiteLLMSnapshot is the immutable refresh result. Reads via
// Snapshotter.Snapshot are atomic.Pointer.Load + value-copy — lock-free.
//
// The map fields are NEVER mutated after the snapshot is published. Each
// refresh allocates fresh maps and stores a new *LiteLLMSnapshot via
// atomic.Pointer.Store. Concurrent readers that captured the prior
// pointer continue to read its (now frozen) maps without coordination.
type LiteLLMSnapshot struct {
	// Models is the set of LiteLLM model_name strings registered at
	// the time of the snapshot. Nil before any refresh succeeds.
	Models map[string]struct{}

	// MCPServers is the set of LiteLLM server_name strings (the
	// MCPServerEntry.ServerName field — D-13).
	MCPServers map[string]struct{}

	// A2AAgents is the set of LiteLLM agent_name strings (the
	// AgentEntry.AgentName field — D-13).
	A2AAgents map[string]struct{}

	// Teams is the set of LiteLLM team_alias strings (the
	// TeamListEntry.TeamAlias field). Empty-alias entries are skipped.
	Teams map[string]struct{}

	// RefreshedAt is the wall-clock instant at which the most recent
	// successful refresh (or stale-marker-only update) completed.
	RefreshedAt time.Time

	// Stale is true when the most recent refresh attempt failed and
	// this snapshot is the preserved prior value with the flag flipped.
	// Hub §6.4 callers prepend "snapshot stale (LiteLLM unreachable); "
	// to the ExecutionResourcesResolved.message when this is true.
	Stale bool
}

// Snapshotter is the controller-runtime manager.Runnable that refreshes
// a LiteLLMSnapshot on a fixed cadence, storing it in an atomic.Pointer
// for lock-free reads from every EnvironmentReconciler.Reconcile call.
//
// Field semantics:
//
//   - client: the widened Plan 02-01 litellm.Client interface. Phase 1
//     unit tests pass a NoopClient; Plan 02-09 wires the real REST client.
//   - interval: defaults to DefaultRefreshInterval. Test code mutates
//     before Start to drive tighter cadences.
//   - snap: atomic.Pointer guarantees publication-safe handoff between
//     the single writer (refresh) and many readers (Snapshot).
//   - log: scoped logger used for ticker / refresh diagnostics. Snapshot
//     payloads themselves contain only LiteLLM-registered names which
//     are operationally-visible per Hub §6.4 — not secret material.
//   - litellmUnreachableCount: atomic.Int64 counter Phase 5 will export
//     as litellm_unreachable_total{caller="operator"} per §18.5.
type Snapshotter struct {
	client                  litellm.Client
	interval                time.Duration
	snap                    atomic.Pointer[LiteLLMSnapshot]
	log                     logr.Logger
	litellmUnreachableCount atomic.Int64

	// Catalog persistence (runtime catalog). When pool is nil the
	// Snapshotter does not persist — single-binary unit tests stay inert.
	pool          *pgxpool.Pool
	catalogNS     string
	connectorName string
}

// NewSnapshotter constructs a Snapshotter wired to a litellm.Client.
// The default refresh interval is DefaultRefreshInterval (5m). Tests
// may mutate (s).interval after construction to accelerate the loop;
// production code paths never override the default.
func NewSnapshotter(c litellm.Client, log logr.Logger) *Snapshotter {
	return &Snapshotter{
		client:   c,
		interval: DefaultRefreshInterval,
		log:      log,
	}
}

// EnableCatalog wires Postgres persistence of each successful refresh into
// runtime_catalog_entries, keyed by (namespace, connector). Chainable. A nil
// pool leaves persistence disabled.
func (s *Snapshotter) EnableCatalog(pool *pgxpool.Pool, namespace, connector string) *Snapshotter {
	s.pool = pool
	s.catalogNS = namespace
	s.connectorName = connector
	return s
}

// Snapshot returns the most recent LiteLLMSnapshot value (a shallow
// copy of the underlying atomic.Pointer target). On cold start (no
// refresh completed yet) it returns the zero value LiteLLMSnapshot{}.
// The caller MUST treat the zero value as "every spec entry unresolved"
// — which is the correct first-reconcile behavior per Hub §6.4 (an
// Environment that races the snapshotter to the first reconcile sees
// all-unresolved until the first refresh tick lands).
func (s *Snapshotter) Snapshot() LiteLLMSnapshot {
	if p := s.snap.Load(); p != nil {
		return *p
	}
	return LiteLLMSnapshot{}
}

// LiteLLMUnreachableCount returns the cumulative number of refresh
// attempts that failed because LiteLLM was unreachable (i.e. any of
// the four list calls returned a non-ErrNotFound error). Phase 5
// wires this counter into the litellm_unreachable_total{caller="operator"}
// Prometheus counter per Hub §18.5.
func (s *Snapshotter) LiteLLMUnreachableCount() int64 {
	return s.litellmUnreachableCount.Load()
}

// Start implements controller-runtime's manager.Runnable. It performs
// an initial refresh (so the first Environment reconcile after manager
// boot has a populated snapshot if LiteLLM is reachable), then ticks
// every s.interval until ctx is canceled (manager shutdown / SIGTERM).
//
// The function ALWAYS returns nil on ctx cancellation — controller-runtime
// treats a non-nil error from a Runnable as fatal to the manager. A
// LiteLLM-unreachable refresh is NOT fatal; it is recorded via the
// litellmUnreachableCount counter and the prior snapshot is preserved
// with Stale=true per D-14.
func (s *Snapshotter) Start(ctx context.Context) error {
	s.log.Info("litellm-snapshot Runnable starting", "interval", s.interval)
	// Issue #30: refresh failures (typically connection.ErrNotReady at
	// operator boot, before the LiteLLMConnection reconciler has probed)
	// previously waited a full s.interval before retrying. We now drive
	// the next-tick interval from the most recent refresh result:
	// success → steady s.interval; failure → exponential backoff seeded
	// at initialRetryBackoff, doubling, capped at s.interval. Reset on
	// success so a single transient failure doesn't permanently shorten
	// the tick.
	next := s.refreshAndNextInterval(ctx, initialRetryBackoff)
	backoff := initialRetryBackoff
	for {
		select {
		case <-ctx.Done():
			s.log.Info("litellm-snapshot Runnable stopping")
			return nil
		case <-time.After(next):
			next = s.refreshAndNextInterval(ctx, backoff)
			if next == s.interval {
				backoff = initialRetryBackoff
			} else {
				backoff = nextBackoff(backoff, s.interval)
			}
		}
	}
}

// refreshAndNextInterval runs a refresh and returns how long the caller
// should sleep before the next tick. On success it returns s.interval;
// on failure it returns the supplied backoff (clamped to s.interval).
// Split out so Start can stay readable.
func (s *Snapshotter) refreshAndNextInterval(ctx context.Context, backoff time.Duration) time.Duration {
	if s.refresh(ctx) {
		return s.interval
	}
	if backoff > s.interval {
		return s.interval
	}
	return backoff
}

// nextBackoff doubles cur, capped at maxDur.
func nextBackoff(cur, maxDur time.Duration) time.Duration {
	return min(cur*2, maxDur)
}

// refresh issues the three LiteLLM list calls and, on full success,
// stores a fresh LiteLLMSnapshot via atomic.Pointer.Store. On any
// failure (D-14):
//
//   - ErrNotFound from any of the three calls is downgraded to an empty
//     slice — a LiteLLM with zero models / mcp servers / a2a agents is
//     a valid empty intersection, not an upstream error (per Plan 02-01
//     SUMMARY's documented snapshotter contract).
//   - A non-ErrNotFound error from ANY of the three calls treats the
//     whole tick as a refresh failure. The litellmUnreachableCount
//     counter increments; the prior snapshot (if any) is re-stored with
//     Stale=true; if no prior snapshot exists, an empty Stale snapshot
//     is published so cold-start callers don't oscillate between "zero
//     value" and "stale-empty" semantics.
//
// RefreshForTest invokes refresh synchronously. Exposed for envtest
// suites that need a populated snapshot before manager.Start without
// running the ticker loop. NOT for production callers — production
// invokes Start() which owns the ticker.
func (s *Snapshotter) RefreshForTest(ctx context.Context) {
	s.refresh(ctx)
}

// refresh is invoked only from Start's single-writer goroutine, so no
// internal lock is required against itself. The atomic.Pointer.Store
// guarantees publication-safety against concurrent Snapshot() readers.
// Returns true on a fully successful refresh (all four list calls OK
// or ErrNotFound), false otherwise. Start uses the return value to
// pick the next-tick interval (issue #30).
func (s *Snapshotter) refresh(ctx context.Context) bool {
	models, errM := s.client.ListModels(ctx)
	mcps, errC := s.client.ListMCPServers(ctx)
	agents, errA := s.client.ListA2AAgents(ctx)
	teams, errT := s.client.ListAllTeams(ctx)

	// ErrNotFound → empty set, NOT an error (D-13 / Plan 02-01 contract).
	if errors.Is(errM, litellm.ErrNotFound) {
		models, errM = nil, nil
	}
	if errors.Is(errC, litellm.ErrNotFound) {
		mcps, errC = nil, nil
	}
	if errors.Is(errA, litellm.ErrNotFound) {
		agents, errA = nil, nil
	}
	if errors.Is(errT, litellm.ErrNotFound) {
		teams, errT = nil, nil
	}

	if errM != nil || errC != nil || errA != nil || errT != nil {
		s.litellmUnreachableCount.Add(1)
		if cur := s.snap.Load(); cur != nil {
			// Preserve prior snapshot with Stale flipped.
			stale := *cur
			stale.Stale = true
			s.snap.Store(&stale)
			s.log.Info("litellm snapshot: upstream unreachable; preserving prior snapshot",
				"modelsErr", errM,
				"mcpErr", errC,
				"a2aErr", errA,
				"teamsErr", errT,
				"priorRefreshedAt", cur.RefreshedAt,
			)
			return false
		}
		// First refresh ever failed — store an empty stale snapshot so
		// callers don't oscillate between "cold start" and "stale"
		// semantics across subsequent ticks.
		s.snap.Store(&LiteLLMSnapshot{
			Stale:       true,
			RefreshedAt: time.Now(),
		})
		s.log.Info("litellm snapshot: initial refresh failed; emitting empty stale snapshot",
			"modelsErr", errM,
			"mcpErr", errC,
			"a2aErr", errA,
			"teamsErr", errT,
		)
		return false
	}

	next := &LiteLLMSnapshot{
		Models:      toSet(models, func(m litellm.ModelInfoResponse) string { return m.ModelName }),
		MCPServers:  toSet(mcps, func(m litellm.MCPServerEntry) string { return m.ServerName }),
		A2AAgents:   toSet(agents, func(a litellm.AgentEntry) string { return a.AgentName }),
		Teams:       toSet(teams, func(t litellm.TeamListEntry) string { return t.TeamAlias }),
		RefreshedAt: time.Now(),
		Stale:       false,
	}
	s.snap.Store(next)
	if s.pool != nil {
		if err := achdb.ReplaceRuntimeCatalog(ctx, s.pool, s.catalogNS, s.connectorName,
			next.Models, next.MCPServers, next.A2AAgents, next.Teams, next.RefreshedAt); err != nil {
			// Non-fatal: the in-memory snapshot is already published and the
			// EnvironmentReconciler reads that, not the table. Log and move on;
			// the next refresh retries the projection.
			s.log.Error(err, "litellm snapshot: catalog persistence failed",
				"connector", s.connectorName)
		}
	}
	s.log.Info("litellm snapshot refreshed",
		"models", len(next.Models),
		"mcpServers", len(next.MCPServers),
		"a2aAgents", len(next.A2AAgents),
		"teams", len(next.Teams),
	)
	return true
}

// toSet projects items into a name set via key. Empty keys are skipped
// (identifiers are never empty; for teams this drops alias-less entries,
// which cannot be referenced in Environment.spec.authorizedTeams).
func toSet[T any](items []T, key func(T) string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, it := range items {
		if k := key(it); k != "" {
			out[k] = struct{}{}
		}
	}
	return out
}
