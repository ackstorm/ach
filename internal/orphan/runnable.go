// SPDX-License-Identifier: Apache-2.0

package orphan

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
)

// OrphanAgeFloor is the Hub §18.4 race defender — a LiteLLM key counts
// as orphan only when it is ≥10 minutes old at the moment the tick
// inspects it. Defends against the natural race where a PK_ / EK_
// INSERT (ACH side) commits in the same second as the orphan loop
// reads the active-key set, which would otherwise mis-classify the
// freshly-issued key as orphan.
const OrphanAgeFloor = 10 * time.Minute

// D-18 outcome enum — the third field of every audit event emitted by
// this package. Keeping the values as exported constants prevents call
// sites from drifting on the string literals. Phase 02.2 Plan 1
// renamed the success-path constant to OutcomeRevoked (value
// "revoked") in a single one-shot rename per greenfield/no-compat-shims
// discipline; no prior constant remains in the enum.
const (
	OutcomeRevoked            = "revoked"
	OutcomeLiteLLMUnreachable = "litellm_unreachable"
	OutcomeRevokeFailed       = "revoke_failed"
)

// listUsersFn / listKeyIDsFn are the function-typed test seams that
// stand in for db.ListACHManagedLitellmUsers / db.ListActiveACHKeyIDs.
// Production NewRunnable wires the real helpers; unit tests override
// the fields directly on the constructed Runnable to avoid spinning a
// real Postgres container.
type listUsersFn func(context.Context, *pgxpool.Pool) ([]string, error)
type listKeyIDsFn func(context.Context, *pgxpool.Pool) ([]string, error)

// Runnable is the controller-runtime manager.Runnable that ticks every
// Interval and processes one orphan-cleanup pass per tick. See the
// package docstring for the full per-tick procedure.
//
// Lifecycle:
//
//   - Start(ctx) is the production entry — controller-runtime invokes
//     it from mgr.Add. It ticks indefinitely on the provided Interval,
//     calling TickOnce on every tick. Cancellation of ctx (manager
//     shutdown) returns nil cleanly; the function never returns a
//     non-nil error (controller-runtime treats a non-nil Runnable
//     error as fatal to the manager, and LiteLLM-unreachable is
//     definitionally not fatal — D-18).
//
//   - TickOnce(ctx) is the test-friendly single-pass entry; tests call
//     it directly without spinning the ticker. Start invokes TickOnce
//     on every t.C; there is no initial-tick on Start (unlike the
//     Snapshotter — orphan cleanup runs on the schedule from t+Interval).
//
// Concurrency: TickOnce is single-writer; Start is the only caller in
// production. Tests that invoke TickOnce concurrently with a Start
// goroutine MUST coordinate externally — the package does not lock.
type Runnable struct {
	Client   litellm.Client
	DB       *pgxpool.Pool
	Audit    *slog.Logger
	Interval time.Duration
	Log      logr.Logger

	// Test seams — production code never touches these after
	// NewRunnable wires the defaults.
	ListUsers  listUsersFn
	ListKeyIDs listKeyIDsFn
}

// NewRunnable constructs a Runnable wired against the live db helpers.
// interval is accepted pre-validated; the caller (Plan 02-09
// cmd/operator/main.go via MustEnvDurationAtLeast) is responsible for
// enforcing the Hub §18.4 / D-15 ≥5m floor. Passing a too-small interval
// is harmless to the loop itself — the OrphanAgeFloor=10m check inside
// TickOnce defers revocation regardless — but produces wasted ticks.
func NewRunnable(client litellm.Client, dbPool *pgxpool.Pool, audit *slog.Logger,
	interval time.Duration, log logr.Logger) *Runnable {
	return &Runnable{
		Client:     client,
		DB:         dbPool,
		Audit:      audit,
		Interval:   interval,
		Log:        log,
		ListUsers:  db.ListACHManagedLitellmUsers,
		ListKeyIDs: db.ListActiveACHKeyIDs,
	}
}

// Start implements controller-runtime's manager.Runnable. It ticks
// every r.Interval until ctx is canceled and ALWAYS returns nil on
// ctx cancellation — controller-runtime treats a non-nil error from
// a Runnable as fatal to the manager. A LiteLLM-unreachable refresh
// is NOT fatal; the loop records it via the audit event in TickOnce
// and the next tick retries.
//
// No initial tick on Start (unlike Snapshotter): the orphan loop runs
// ON the schedule; an immediate tick on startup would create a
// thundering-herd if multiple operators restart simultaneously
// (single-replica today; future-proofing). The first orphan cleanup
// happens at t+Interval.
func (r *Runnable) Start(ctx context.Context) error {
	r.Log.Info("orphan-cleanup Runnable starting", "interval", r.Interval)
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			r.Log.Info("orphan-cleanup Runnable stopping")
			return nil
		case <-t.C:
			r.TickOnce(ctx)
		}
	}
}

// TickOnce executes one pass of the Hub §18.4 procedure. Exported for
// unit tests; Start calls it on every ticker fire.
//
// Failure handling:
//
//   - DB-list failure (ListUsers / ListKeyIDs error) → log and return.
//     The next tick retries; we do not emit an audit event for DB
//     errors because the audit channel is reserved for LiteLLM
//     interactions per D-18.
//
//   - LiteLLM ListUserKeys failure on any user → emit ONE audit event
//     with outcome="litellm_unreachable" and return. Sibling users are
//     NOT processed for this tick; the next tick retries from a clean
//     slate. This is the Hub §18.4 "abort-on-unreachable" rule.
//
//   - LiteLLM RevokeKey failure on a single orphan key → emit an audit
//     event with outcome="revoke_failed" and CONTINUE to the next key
//     (and the next user). Revoke failures are per-key and do not
//     indicate the upstream is unreachable; aborting on the first one
//     would starve sibling users whose orphans are revokable.
func (r *Runnable) TickOnce(ctx context.Context) {
	// Step 1: enumerate ACH-managed user_id set.
	userIDs, err := r.ListUsers(ctx, r.DB)
	if err != nil {
		r.Log.Error(err, "orphan-cleanup: ListACHManagedLitellmUsers failed; skipping tick")
		return
	}
	if len(userIDs) == 0 {
		r.Log.V(1).Info("orphan-cleanup: no ACH-managed users; tick is a no-op")
		return
	}

	// Step 2: enumerate active ACH key_id set (for the "absent" membership test).
	achKeyIDs, err := r.ListKeyIDs(ctx, r.DB)
	if err != nil {
		r.Log.Error(err, "orphan-cleanup: ListActiveACHKeyIDs failed; skipping tick")
		return
	}
	achKeySet := make(map[string]struct{}, len(achKeyIDs))
	for _, k := range achKeyIDs {
		achKeySet[k] = struct{}{}
	}

	// Step 3: per-user ListUserKeys → identify orphans → revoke.
	now := time.Now()
	cutoff := now.Add(-OrphanAgeFloor)
	for _, uid := range userIDs {
		keys, err := r.Client.ListUserKeys(ctx, uid)
		if err != nil {
			// Hub §18.4: LiteLLM-unreachable aborts the tick cleanly.
			// Emit ONE audit event characterizing the abort and return.
			// CR-03: err.Error() is NOT included on the audit event —
			// audit/doc.go's no-scrubbing contract makes raw error text
			// an audit-safety hazard (litellm.RESTClient.makeRequest
			// wraps with %w so the underlying net/http error string,
			// which is bounded but not guaranteed body-free across Go
			// runtime versions, would surface here). Diagnostic detail
			// belongs in the operational log (line below); the audit
			// channel carries only the closed-enum outcome + identifiers
			// per Hub §16.1 / §18.2.
			r.Audit.Info("operator.orphan-cleanup",
				"target.kind", "tick",
				"outcome", OutcomeLiteLLMUnreachable,
				"user_id", uid)
			r.Log.Info("orphan-cleanup: tick aborted on LiteLLM-unreachable",
				"user_id", uid, "err", err)
			return
		}
		for _, k := range keys {
			// Skip if too new (Hub §18.4 race defender).
			if !k.CreatedAt.Before(cutoff) {
				continue
			}
			// Ownership gate: ACH revokes ONLY keys it minted. An ACH key
			// carries ach_key_id in its LiteLLM metadata (set at mint in
			// sso.go / envkeys/handler.go); a key WITHOUT it is foreign
			// (manual dashboard / tf-* / token-factory) and is NEVER
			// touched — "ACH limpia sus mierdas; si no son suyas, dejarlas."
			achID, _ := k.Metadata["ach_key_id"].(string)
			if achID == "" {
				continue // FOREIGN key — ACH did not mint it; leave it
			}
			// Skip if ACH still tracks it as active. The membership join is
			// ach_key_id ↔ key_id (both pkid_*/ekid_*, ListActiveACHKeyIDs);
			// the opaque Token is the revoke handle only, never the
			// membership key — that namespace mismatch was the bug that
			// revoked ACH's own pk_/ek_ keys.
			if _, tracked := achKeySet[achID]; tracked {
				continue
			}
			// ACH minted it and no longer tracks it → true orphan.
			// Revoke by the opaque Token + emit audit reflecting the outcome.
			if err := r.Client.RevokeKey(ctx, k.Token); err != nil {
				// CR-03: err.Error() is NOT included on the audit event;
				// see the litellm_unreachable branch above for rationale.
				// Diagnostic detail goes to the operational log.
				r.Audit.Info("operator.orphan-cleanup",
					"target.kind", "litellm_key",
					"target.name", k.Token,
					"outcome", OutcomeRevokeFailed,
					"user_id", uid)
				r.Log.Info("orphan-cleanup: revoke failed; continuing tick",
					"token", k.Token, "err", err)
				continue // do NOT abort the tick — sibling users may still have revokable orphans
			}
			r.Audit.Info("operator.orphan-cleanup",
				"target.kind", "litellm_key",
				"target.name", k.Token,
				"outcome", OutcomeRevoked,
				"user_id", uid)
			r.Log.Info("orphan-cleanup: revoked",
				"token", k.Token, "ach_key_id", achID, "user_id", uid, "key_alias", k.KeyAlias)
		}
	}
}
