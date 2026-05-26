// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/snapshot"
)

// ekDrainMaxIterations bounds the §6.5 step-4 drain loop. Phase 1 has
// zero ek_ rows, so the first listing returns 0 and the loop exits on
// iteration 0; the cap exists to document the W3-concrete contract for
// Phase 3+ when ek_ rows start appearing.
const ekDrainMaxIterations = 10

// ekDrainSleep is the pause between drain iterations when active rows
// remain. The value is small because the loop is per-CR-deletion and
// only spins while a concurrent ek_ creation is committing.
const ekDrainSleep = 100 * time.Millisecond

// EnvironmentReconciler reconciles a Environment object per Hub §6.5
// (deletion drain) plus the standard finalizer add/remove lifecycle
// (CRD-06).
//
// Field semantics:
//
//   - client.Client (embedded): K8s API access (Get/Update/Status).
//   - Scheme: runtime scheme — required by controller-runtime.
//   - LiteLLM: D-11 interface. Phase 1 wires *litellm.NoopClient;
//     Phase 2 wires the real REST client. Reconcile MUST type this as
//     the interface so the swap is wiring-only.
//   - Namespace: the WATCH_NAMESPACE the reconciler is scoped to
//     (MULTI-01). cmd/operator/main.go (Plan 06) injects watchNS.
//   - Log: structured logger, scoped with .WithName("Environment").
//   - DB: optional pgxpool.Pool. Nil in Phase 1 envtest/unit tests
//     (the ek_ drain trivially skips); Plan 06 injects the real pool
//     for production. The drain code path is exercised whenever DB is
//     non-nil — Phase 1 cluster smoke runs against a DB-backed pool.
//   - Snapshotter: D-13. Plan 02-09 wires *snapshot.Snapshotter (an
//     atomic.Pointer-backed LiteLLM resource cache refreshed every
//     5 minutes). Reconcile reads via Snapshot() — lock-free. Nil in
//     Phase 1 unit tests; the steady-state branch emits the back-compat
//     AccessGroupSynced=Unknown reason="Initializing" condition in that
//     case so existing envtests stay green.
type EnvironmentReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	LiteLLM   litellm.Client
	Namespace string
	Log       logr.Logger
	DB        *pgxpool.Pool
	// Phase 2 (Plan 02-09 wires from cmd/operator/main.go):
	Snapshotter *snapshot.Snapshotter
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=environments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=environments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=environments/finalizers,verbs=update

// Reconcile implements the Phase 1 Environment lifecycle:
//
//   - Step 1: fetch the CR. NotFound → no-op.
//   - Step 2a (deletion path, §6.5):
//     i.   Delete LiteLLM access group <name> (§6.5 step 2 — runtime barrier).
//     ii.  Delete LiteLLM tag <name>          (§6.5 step 3).
//     iii. Drain ek_ rows                     (§6.5 step 4 — Phase 1 real code per D-12).
//     iv.  RemoveFinalizer                    (§6.5 step 5).
//   - Step 2b (finalizer-add path): controllerutil.AddFinalizer + Update.
//   - Step 3 (steady state, Phase 2 — OP-13 / Hub §6.4):
//     read the LiteLLM snapshot (lock-free via r.Snapshotter.Snapshot),
//     compute spec.runtime.{Models,MCPServers,A2AAgents} \ snapshot
//     (set difference), record unresolved entries in
//     env.Status.UnresolvedRuntime, flip ExecutionResourcesResolved
//     condition per Hub §6.6 closed set (Resolved when empty,
//     ResourceUnresolved when non-empty), prepend "snapshot stale
//     (LiteLLM unreachable); " to the message when the snapshot's
//     Stale flag is set (D-14). Return ctrl.Result{RequeueAfter:
//     5*time.Minute} per Hub §6.4 — event-driven reconciles fire
//     immediately on spec change; the requeue is the fallback for
//     snapshot drift detection. When r.Snapshotter is nil (Phase 1
//     unit-test mode), emit AccessGroupSynced=Unknown reason=
//     "Initializing" for back-compat with existing envtests.
func (r *EnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("environment", req.NamespacedName)

	// ─── Step 1: Fetch the CR ──────────────────────────────────────
	var env achv1alpha1.Environment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		if apierrors.IsNotFound(err) {
			// CR already gone; finalizer flow either ran on a previous
			// reconcile or never installed. Nothing to do.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// ─── Step 2a: Deletion path — §6.5 drain ───────────────────────
	if !env.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&env, environmentFinalizer) {
			// §6.5 step 2: delete LiteLLM access group.
			if err := r.LiteLLM.DeleteAccessGroup(ctx, env.Name); err != nil {
				return ctrl.Result{}, fmt.Errorf("§6.5 step 2 DeleteAccessGroup: %w", err)
			}
			// §6.5 step 3: delete LiteLLM tag.
			if err := r.LiteLLM.DeleteTag(ctx, env.Name); err != nil {
				return ctrl.Result{}, fmt.Errorf("§6.5 step 3 DeleteTag: %w", err)
			}
			// §6.5 step 4: drain ek_ rows (REAL Phase 1 code per D-12;
			// trivially exits in Phase 1 because no rows exist).
			if err := r.drainEkRows(ctx, &env); err != nil {
				// Transient/wrapped DB errors propagate — controller-runtime
				// requeues with exponential backoff; the finalizer stays.
				return ctrl.Result{}, err
			}
			// §6.5 step 5: remove the finalizer.
			controllerutil.RemoveFinalizer(&env, environmentFinalizer)
			if err := r.Update(ctx, &env); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("§6.5 drain complete; finalizer removed", "env", env.Name)
		}
		return ctrl.Result{}, nil
	}

	// ─── Step 2b: Finalizer-add path ───────────────────────────────
	if !controllerutil.ContainsFinalizer(&env, environmentFinalizer) {
		controllerutil.AddFinalizer(&env, environmentFinalizer)
		if err := r.Update(ctx, &env); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// ─── Step 3: Steady state — ExecutionResourcesResolved (OP-13) ──
	//
	// When the Snapshotter is not wired (Phase 1 unit-test mode), emit
	// the back-compat AccessGroupSynced=Unknown reason="Initializing"
	// condition so the existing finalizer envtest stays green. Plan
	// 02-09 will wire r.Snapshotter from cmd/operator/main.go via
	// mgr.Add(snapshotter); production reconciles always take the full
	// branch below.
	if r.Snapshotter == nil {
		if err := r.writeStatus(
			ctx, &env,
			"AccessGroupSynced", metav1.ConditionUnknown, "Initializing",
			"snapshotter not wired (unit-test mode)",
		); err != nil {
			logger.Error(err, "status update failed", "type", "AccessGroupSynced")
		}
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Read the LiteLLM snapshot (lock-free) and compute set difference
	// spec.runtime.X \ snapshot.X for X ∈ {Models, MCPServers, A2AAgents}.
	// Lookup cost is O(n) in spec.runtime size, NOT in snapshot size.
	snap := r.Snapshotter.Snapshot()
	unresolved := achv1alpha1.UnresolvedRuntime{}
	for _, m := range env.Spec.Runtime.Models {
		if _, ok := snap.Models[m]; !ok {
			unresolved.Models = append(unresolved.Models, m)
		}
	}
	for _, mcp := range env.Spec.Runtime.MCPServers {
		if _, ok := snap.MCPServers[mcp]; !ok {
			unresolved.MCPServers = append(unresolved.MCPServers, mcp)
		}
	}
	for _, a := range env.Spec.Runtime.A2AAgents {
		if _, ok := snap.A2AAgents[a]; !ok {
			unresolved.A2AAgents = append(unresolved.A2AAgents, a)
		}
	}
	totalUnresolved := len(unresolved.Models) + len(unresolved.MCPServers) + len(unresolved.A2AAgents)

	// Hub §6.6 closed set for ExecutionResourcesResolved:
	//   True  + reason=Resolved          — every spec.runtime.* found
	//   False + reason=ResourceUnresolved — at least one name absent
	condStatus := metav1.ConditionTrue
	reason := "Resolved"
	var message string
	if totalUnresolved > 0 {
		condStatus = metav1.ConditionFalse
		reason = "ResourceUnresolved"
		message = fmt.Sprintf("%d unresolved (models=%d, mcp=%d, a2a=%d)",
			totalUnresolved,
			len(unresolved.Models),
			len(unresolved.MCPServers),
			len(unresolved.A2AAgents),
		)
	}
	// D-14: prefix stale marker so operators inspecting `kubectl
	// describe environment` see that the condition reflects cached data.
	if snap.Stale {
		if message == "" {
			message = "snapshot stale (LiteLLM unreachable)"
		} else {
			message = "snapshot stale (LiteLLM unreachable); " + message
		}
	}

	// Patch the unresolved field on env.Status, then emit all three §6.6
	// conditions in memory and issue a SINGLE Status().Update so the
	// conditions slice + the UnresolvedRuntime field land atomically.
	//
	// ExecutionResourcesResolved is the only condition this reconciler
	// computes today. AccessGroupSynced and Available are written as
	// placeholder Unknown values so the kubebuilder printcolumns surface
	// SOMETHING (operators reading `kubectl get environment` see
	// "Unknown" instead of an empty cell). When TODO §7 lands, the
	// access-group binding step overrides AccessGroupSynced to True /
	// False with the real reason. When TODO §9 lands, the composite
	// rollup overrides Available.
	env.Status.UnresolvedRuntime = &unresolved
	apimeta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:               "ExecutionResourcesResolved",
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	})
	// §7: real AccessGroupSynced reconciliation. The helper owns the
	// closed-set Type/Reason mapping per Hub §6.6 and returns the
	// metav1.Condition to publish.
	agCond := r.reconcileAccessGroup(ctx, &env)
	// Surface snapshot-stale prefix so operators see when the binding
	// decision was made against cached LiteLLM data (Hub §6.4 / D-14).
	if snap.Stale && agCond.Status == metav1.ConditionTrue {
		agCond.Message = "snapshot stale (LiteLLM unreachable); " + agCond.Message
	}
	apimeta.SetStatusCondition(&env.Status.Conditions, agCond)
	// Echo the synced access group name (CRD-02 status field). Only set
	// when AccessGroupSynced is True so a stale Status doesn't lie.
	if agCond.Status == metav1.ConditionTrue {
		env.Status.LitellmAccessGroup = env.Name
	}
	// Placeholder: TODO §9 owns the composite rollup.
	if !hasCondition(env.Status.Conditions, "Available") {
		apimeta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:               "Available",
			Status:             metav1.ConditionUnknown,
			Reason:             "PendingSubConditions",
			Message:            "composite Ready rollup not yet implemented (see TODO §9)",
			ObservedGeneration: env.Generation,
			LastTransitionTime: metav1.Now(),
		})
	}
	env.Status.ObservedGeneration = env.Generation
	if err := r.Status().Update(ctx, &env); err != nil {
		logger.Error(err, "status update failed")
	}

	// D-08: Hub §6.4 requeue cadence — keeps Environments converging
	// on snapshot drift even when no spec change triggers an event-
	// driven reconcile.
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// reconcileAccessGroup is the §7 implementation step: ensure the LiteLLM
// access group <env.Name> exists AND every spec.authorizedTeams[i] team
// is bound to it. Returns the metav1.Condition that the caller should
// publish on env.Status.Conditions.
//
// Wire steps (Hub §6.4 / TODO §7):
//
//  1. CreateAccessGroup(env.Name, nil). ErrAlreadyExists swallowed as
//     idempotent success.
//  2. ListAccessGroupBindings(env.Name) — drift baseline. Failure is
//     non-fatal (fall back to empty current set; binds are idempotent).
//  3. For each team in env.Spec.AuthorizedTeams: skip if already in
//     CURRENT set; otherwise BindTeamToAccessGroup(env.Name, team).
//     Collect failures into a partial-bind set.
//  4. Orphan detection: log (do not auto-remove) bindings present in
//     CURRENT but absent from spec.authorizedTeams. Auto-removal is
//     §10 / TODO §10 scope.
//  5. Closed-set condition emit:
//     - True/Synced on full success
//     - False/PartialBind on any bind failure (offending teams listed)
//     - False/AccessGroupCreateFailed on create error (other than
//     ErrAlreadyExists)
func (r *EnvironmentReconciler) reconcileAccessGroup(
	ctx context.Context,
	env *achv1alpha1.Environment,
) metav1.Condition {
	logger := log.FromContext(ctx).WithValues("environment", env.Name)

	// Step 1: ensure the access group exists.
	if err := r.LiteLLM.CreateAccessGroup(ctx, env.Name, nil); err != nil && !errors.Is(err, litellm.ErrAlreadyExists) {
		logger.Error(err, "CreateAccessGroup failed")
		return metav1.Condition{
			Type:               "AccessGroupSynced",
			Status:             metav1.ConditionFalse,
			Reason:             "AccessGroupCreateFailed",
			Message:            fmt.Sprintf("LiteLLM CreateAccessGroup(%s) failed: %v", env.Name, err),
			ObservedGeneration: env.Generation,
			LastTransitionTime: metav1.Now(),
		}
	}

	// Step 2: discover CURRENT bindings (drift baseline).
	current, err := r.LiteLLM.ListAccessGroupBindings(ctx, env.Name)
	if err != nil {
		logger.Info("ListAccessGroupBindings failed; proceeding without drift baseline", "err", err)
		current = nil
	}
	currentSet := make(map[string]struct{}, len(current))
	for _, t := range current {
		currentSet[t] = struct{}{}
	}

	// Step 3: bind every spec.authorizedTeams[i]. Skip teams already
	// observed in the CURRENT set.
	var failed []string
	var lastErr error
	for _, team := range env.Spec.AuthorizedTeams {
		if _, ok := currentSet[team]; ok {
			continue
		}
		if berr := r.LiteLLM.BindTeamToAccessGroup(ctx, env.Name, team); berr != nil {
			logger.Error(berr, "BindTeamToAccessGroup failed", "team", team)
			failed = append(failed, team)
			lastErr = berr
			continue
		}
	}

	// Step 4: orphan detection (log only).
	specSet := make(map[string]struct{}, len(env.Spec.AuthorizedTeams))
	for _, t := range env.Spec.AuthorizedTeams {
		specSet[t] = struct{}{}
	}
	for _, t := range current {
		if _, ok := specSet[t]; !ok {
			logger.Info("orphan team binding detected (not auto-removed; see TODO §10)",
				"env", env.Name, "team", t)
		}
	}

	// Step 5: closed-set condition emit.
	if len(failed) > 0 {
		return metav1.Condition{
			Type:               "AccessGroupSynced",
			Status:             metav1.ConditionFalse,
			Reason:             "PartialBind",
			Message:            fmt.Sprintf("LiteLLM BindTeamToAccessGroup failed for %d team(s): %v (last err: %v)", len(failed), failed, lastErr),
			ObservedGeneration: env.Generation,
			LastTransitionTime: metav1.Now(),
		}
	}
	return metav1.Condition{
		Type:               "AccessGroupSynced",
		Status:             metav1.ConditionTrue,
		Reason:             "Synced",
		Message:            fmt.Sprintf("LiteLLM access group %q bound to %d team(s)", env.Name, len(env.Spec.AuthorizedTeams)),
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
}

// hasCondition reports whether conds carries an entry of the given type.
// Used to gate placeholder writes so future reconcilers (TODO §7/§9) that
// emit a real True/False are not clobbered by the Unknown placeholder.
func hasCondition(conds []metav1.Condition, t string) bool {
	for _, c := range conds {
		if c.Type == t {
			return true
		}
	}
	return false
}

// drainEkRows implements §6.5 step 4 with the W3-concrete revision:
// bounded loop (cap 10), 100ms inter-iteration sleep, transient-DB-error
// awareness via pgconn error class inspection, cap-exhausted slog.Warn
// continuation. The loop body executes two SQL statements per iteration:
//
//  1. UPDATE environment_keys SET status='revoked', revoked_at=now()
//     WHERE environment=$1 AND status='active'
//     — idempotent revocation of any active ek_ bound to this Environment.
//  2. SELECT count(*) FROM environment_keys
//     WHERE environment=$1 AND status='active'
//     — fresh check so a concurrent INSERT after the UPDATE is detected.
//
// Phase 1 invariant: drainEkRows is called with r.DB possibly nil
// (envtest / unit tests without a real Postgres). Nil DB → log + return
// nil (the drain trivially has nothing to revoke; finalizer removal
// proceeds). Plan 06 injects the real pool from cmd/operator/main.go.
//
// Transient-classification rule (W3): pgconn.PgError with Code class
// "08" (connection exception) or "57" (operator intervention) → return
// the raw error so controller-runtime treats it as transient and applies
// exponential backoff. Any other error returns wrapped via fmt.Errorf
// for visibility in logs; controller-runtime still requeues.
//
// Cap-exhausted path (W3): if 10 iterations elapse with active rows
// remaining, log slog.Warn("ek_ drain cap exhausted", ...) and return
// nil so the caller can RemoveFinalizer. This is UNREACHABLE in
// Phase 1 (zero rows exist); the contract documents Phase 3+ behavior
// where a sustained INSERT stream could keep the loop alive.
func (r *EnvironmentReconciler) drainEkRows(ctx context.Context, env *achv1alpha1.Environment) error {
	if r.DB == nil {
		slog.Info("ek_ drain skipped: DB pool not wired", "env", env.Name)
		return nil
	}

	var lastCount int64
	for i := 0; i < ekDrainMaxIterations; i++ {
		// Step (a): revoke any active rows.
		if _, err := r.DB.Exec(ctx,
			`UPDATE environment_keys SET status='revoked', revoked_at=now() WHERE environment=$1 AND status='active'`,
			env.Name,
		); err != nil {
			return classifyDrainErr("ek_ drain UPDATE", err)
		}

		// Step (b): fresh count of any rows that may have committed
		// between the UPDATE and now.
		row := r.DB.QueryRow(ctx,
			`SELECT count(*) FROM environment_keys WHERE environment=$1 AND status='active'`,
			env.Name,
		)
		var n int64
		if err := row.Scan(&n); err != nil {
			return classifyDrainErr("ek_ drain SELECT", err)
		}
		lastCount = n
		if n == 0 {
			// Drain converged; finalizer removal can proceed.
			return nil
		}

		// Active rows remain — sleep and try again.
		time.Sleep(ekDrainSleep)
	}

	// Cap exhausted. Unreachable in Phase 1; Phase 3+ contract is "log
	// loud and proceed" so the Environment doesn't hang in Terminating
	// indefinitely on a pathological INSERT stream.
	slog.Warn("ek_ drain cap exhausted",
		"env", env.Name,
		"remaining", lastCount,
		"iterations", ekDrainMaxIterations,
	)
	return nil
}

// classifyDrainErr maps a Postgres error into either:
//   - the raw error (transient — controller-runtime exponential backoff),
//   - a wrapped error including context (visible in logs; still requeued).
//
// Per W3 the transient classes are "08" (connection exception) and "57"
// (operator intervention — admin shutdown, immediate shutdown, etc.).
// All other class codes are treated as "wrap + return" so they surface
// in operator logs but still trigger requeue; nothing here is treated
// as a fatal swallow.
func classifyDrainErr(label string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// SQLSTATE class is the first two characters of pgErr.Code.
		if len(pgErr.Code) >= 2 {
			class := pgErr.Code[:2]
			if class == "08" || class == "57" {
				// Transient: return raw err so the requeue path is
				// the default exponential-backoff rate limiter.
				return err
			}
		}
	}
	// Wrap so the operator log carries the label and the underlying
	// error string; controller-runtime still requeues.
	return fmt.Errorf("%s: %w", label, err)
}

// writeStatus is the idempotent status-condition helper (sister pattern
// at ach_litellm/internal/controller/litellmconnection_controller.go
// lines 387-413). apimeta.SetStatusCondition handles last-transition-time
// preservation when the condition is unchanged. ObservedGeneration is
// always bumped to env.Generation so consumers can detect "the
// reconciler has seen this revision."
//
// CRD-07 closed sets (§6.6): the Type must be one of Available,
// ContentReady, ExecutionResourcesResolved, AccessGroupSynced; reason
// must be drawn from the matching column. Phase 1 emitted
// AccessGroupSynced=Unknown reason="Initializing"; Phase 2 / OP-13
// (Plan 02-07) emits ExecutionResourcesResolved with reason ∈
// {"Resolved", "ResourceUnresolved"}. The Phase 1 stub is retained
// only as a back-compat branch when r.Snapshotter is nil (unit tests).
func (r *EnvironmentReconciler) writeStatus(
	ctx context.Context,
	env *achv1alpha1.Environment,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	cond := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
	apimeta.SetStatusCondition(&env.Status.Conditions, cond)
	env.Status.ObservedGeneration = env.Generation
	return r.Status().Update(ctx, env)
}

// SetupWithManager registers the reconciler with controller-runtime.
// Single watch on Environment — Phase 2 will add Secret + LiteLLM
// fast-path watches when they exist.
func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Environment{}, builder.WithPredicates()).
		Named("ach-environment").
		Complete(r)
}
