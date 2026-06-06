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
	OutcomeRevoked               = "revoked"
	OutcomeLiteLLMUnreachable    = "litellm_unreachable"
	OutcomeRevokeFailed          = "revoke_failed"
	OutcomeSkippedEmptyActiveSet = "skipped_empty_active_set"
	OutcomeSkippedCircuitBreaker = "skipped_circuit_breaker"
)

// DefaultMaxRevoke is the B2 circuit-breaker cap applied when
// Runnable.MaxRevoke is unset (≤0). A correct steady state revokes 0–few
// keys per tick; a double-digit batch is itself the alarm, so the tick
// aborts revocation rather than executing a suspiciously large purge.
const DefaultMaxRevoke = 10

// orphanCandidate is one true orphan identified in pass 1 of TickOnce —
// ACH-minted (achID present), older than the floor, not in the active
// set. token is the opaque LiteLLM revoke handle; achID is the
// pkid_*/ekid_* identity used only for logging/traceability.
type orphanCandidate struct {
	userID string
	token  string
	achID  string
}

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

	// DryRun (ACH_ORPHAN_CLEANUP_DRY_RUN), when true, makes every tick
	// log "WOULD revoke" + count the candidate under skipped{dry_run}
	// but NEVER call RevokeKey. A reversible, image-level neutralize
	// cleaner than the year-long-interval emergency knob.
	DryRun bool
	// MaxRevoke (ACH_ORPHAN_CLEANUP_MAX_REVOKE) is the B2 circuit-breaker
	// cap; ≤0 means DefaultMaxRevoke. A single tick with more candidates
	// than this aborts revocation entirely (the batch is the alarm).
	MaxRevoke int
	// Metrics holds the Prometheus collectors; nil disables
	// instrumentation (the m* helpers are nil-guarded).
	Metrics *Metrics

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
	interval time.Duration, dryRun bool, maxRevoke int, log logr.Logger) *Runnable {
	return &Runnable{
		Client:     client,
		DB:         dbPool,
		Audit:      audit,
		Interval:   interval,
		DryRun:     dryRun,
		MaxRevoke:  maxRevoke,
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

	// Step 3 (pass 1): per-user ListUserKeys → collect true-orphan
	// candidates. Revocation is DEFERRED to pass 2 so the B1 empty-set
	// fail-safe and B2 circuit-breaker can inspect the FULL candidate set
	// across all users before anything is revoked — a fail-open whole-
	// fleet revoke is exactly the incident this defends against.
	now := time.Now()
	cutoff := now.Add(-OrphanAgeFloor)
	candidates := make([]orphanCandidate, 0)
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
			r.mSkipped(SkipReasonLiteLLMUnreachable, 1)
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
			candidates = append(candidates, orphanCandidate{userID: uid, token: k.Token, achID: achID})
		}
	}
	r.mCandidates(len(candidates))
	if len(candidates) == 0 {
		return // steady state — nothing to revoke
	}

	// B1 fail-safe: an empty active set with ≥1 ACH-owned candidate is the
	// mis-wire signature (the active-key lookup returned nothing while ACH
	// keys still exist upstream — the shape of the original incident). Skip
	// ALL revocation this tick rather than risk revoking ACH's own keys on a
	// bad active-set read.
	//
	// KNOWN LIMITATION (Codex review, PR #119): the orphan loop only
	// enumerates users with ACTIVE ACH rows (db.ListACHManagedLitellmUsers
	// filters status='active'). A genuinely-orphaned key whose owner's LAST
	// active row was revoked — e.g. a DB-side revoke whose LiteLLM-side delete
	// failed — drops out of future ticks and is NOT backstopped here. Closing
	// that needs a widened enumeration (active OR recently-revoked users) with
	// a per-tick cost bound — a design change tracked as a follow-up, not part
	// of this fix. The empty-set branch itself is near-unreachable in
	// practice: achKeySet and the user set read the same active rows, so an
	// empty achKeySet implies an empty user set (early return above) except
	// across a sub-tick read race.
	if len(achKeySet) == 0 {
		if r.DryRun {
			r.previewDryRun(candidates) // still surface the batch this guard would abort
		}
		r.mSkipped(SkipReasonEmptyActiveSet, len(candidates))
		r.Audit.Info("operator.orphan-cleanup",
			"target.kind", "tick",
			"outcome", OutcomeSkippedEmptyActiveSet,
			"candidate_count", len(candidates))
		r.Log.Info("orphan-cleanup: WARNING empty active key set with ACH-owned candidates present; skipping revocation (possible mis-wire)",
			"candidate_count", len(candidates))
		return
	}

	// B2 circuit-breaker: a correct steady state revokes 0–few keys per
	// tick; a batch over the cap is itself the alarm. Abort revocation
	// this tick and surface it loudly rather than execute a large purge.
	maxRevoke := r.MaxRevoke
	if maxRevoke <= 0 {
		maxRevoke = DefaultMaxRevoke
	}
	if len(candidates) > maxRevoke {
		if r.DryRun {
			r.previewDryRun(candidates) // still surface the batch this guard would abort
		}
		r.mSkipped(SkipReasonCircuitBreaker, len(candidates))
		r.Audit.Info("operator.orphan-cleanup",
			"target.kind", "tick",
			"outcome", OutcomeSkippedCircuitBreaker,
			"candidate_count", len(candidates))
		r.Log.Info("orphan-cleanup: WARNING revoke candidates exceed circuit-breaker cap; skipping revocation",
			"candidate_count", len(candidates), "max_revoke", maxRevoke)
		return
	}

	// Step 4 (pass 2). In dry-run (B3) surface every candidate as a
	// WOULD-revoke preview and stop — RevokeKey is never called. This is also
	// reached only for the un-guarded path; the B1/B2 branches above run their
	// own previewDryRun so a dry-run operator sees the guarded batches too.
	if r.DryRun {
		r.previewDryRun(candidates)
		return
	}
	// Real revocation: revoke each candidate by its opaque Token. A per-key
	// RevokeKey failure is logged + audited and the tick CONTINUES — sibling
	// candidates may still be revokable (it does not indicate the upstream is
	// unreachable).
	for _, c := range candidates {
		if err := r.Client.RevokeKey(ctx, c.token); err != nil {
			// CR-03: err.Error() is NOT included on the audit event; see
			// the litellm_unreachable branch above for rationale.
			r.mSkipped(SkipReasonRevokeFailed, 1)
			r.Audit.Info("operator.orphan-cleanup",
				"target.kind", "litellm_key",
				"target.name", c.token,
				"outcome", OutcomeRevokeFailed,
				"user_id", c.userID)
			r.Log.Info("orphan-cleanup: revoke failed; continuing tick",
				"token", c.token, "err", err)
			continue
		}
		r.mRevoked()
		r.Audit.Info("operator.orphan-cleanup",
			"target.kind", "litellm_key",
			"target.name", c.token,
			"outcome", OutcomeRevoked,
			"user_id", c.userID)
		r.Log.Info("orphan-cleanup: revoked",
			"token", c.token, "ach_key_id", c.achID, "user_id", c.userID)
	}
}

// previewDryRun (B3) logs every candidate as a WOULD-revoke line and counts it
// under skipped{dry_run} WITHOUT calling RevokeKey. It is invoked from the
// un-guarded pass-2 path AND from the B1/B2 guard branches, so a dry-run
// operator can inspect exactly the batches those guards would abort — the
// suspicious batches dry-run exists to diagnose.
func (r *Runnable) previewDryRun(candidates []orphanCandidate) {
	for _, c := range candidates {
		r.mSkipped(SkipReasonDryRun, 1)
		r.Log.Info("orphan-cleanup: WOULD revoke (dry-run)",
			"token", c.token, "ach_key_id", c.achID, "user_id", c.userID)
	}
}
