// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/pluginref"
	"github.com/ackstorm/ach/internal/skillref"
	"github.com/ackstorm/ach/internal/snapshot"
)

// environmentsChannel is the NOTIFY channel emitted on every Environment
// projection write/soft-delete (issue #34). Consumers waking on this
// channel select the matching row to drive their read-side caches.
const environmentsChannel = "ach_environments_changed"

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
	// Issue #34 (A10/A11): external source.Channel feed used by the
	// resync runnable (periodic full re-list) and the refreshsignal
	// listener (NOTIFY ach_refresh). When non-nil the SetupWithManager
	// wires WatchesRawSource on the corresponding builder.
	ResyncSource chan event.GenericEvent
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
		return r.reconcileDeletion(ctx, &env, logger)
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
		// Phase 1 unit-test back-compat: emit AccessGroupSynced=Unknown
		// (J.6 placeholder), then run the §9 rollup over the resulting
		// conditions slice so envtest assertions on Available are
		// stable even without a wired Snapshotter.
		apimeta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:               "AccessGroupSynced",
			Status:             metav1.ConditionUnknown,
			Reason:             ReasonInitializing,
			Message:            "snapshotter not wired (unit-test mode)",
			ObservedGeneration: env.Generation,
			LastTransitionTime: metav1.Now(),
		})
		available := computeAvailable(env.Status.Conditions)
		available.ObservedGeneration = env.Generation
		available.LastTransitionTime = metav1.Now()
		apimeta.SetStatusCondition(&env.Status.Conditions, available)
		env.Status.ObservedGeneration = env.Generation
		// Spec v4 §5.2 / D-15: dual-write the projection row BEFORE the
		// best-effort K8s Status update. The back-compat branch ships
		// placeholder Unknown conditions for AccessGroupSynced /
		// ExecutionResourcesResolved — the JSON marshal still works and
		// envtest mode gets the projection row Plan 05-05 will read.
		if err := r.writeEnvironmentProjection(ctx, &env, available); err != nil {
			if errors.Is(err, achdb.ErrOriginConflict) {
				return r.writeConflictWithUIRow(ctx, &env, logger)
			}
			return ctrl.Result{}, err
		}
		desiredStatus := env.Status
		if err := retryStatusUpdate(ctx, r.Client, &env, func(fresh *achv1alpha1.Environment) {
			fresh.Status = desiredStatus
		}); err != nil {
			logger.Error(err, "status update failed")
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

	// Context content closed-set (handoff item 4 / Task B9): a listed plugin or
	// skill must resolve AND have content synced — see contextContentUnresolved.
	unresolvedContextPlugins, unresolvedContextSkills, err := r.contextContentUnresolved(ctx, &env)
	if err != nil {
		return ctrl.Result{}, err
	}
	totalUnresolved += len(unresolvedContextPlugins) + len(unresolvedContextSkills)

	// Hub §6.6 closed set for ExecutionResourcesResolved:
	//   True  + reason=Resolved          — every spec.runtime.* found
	//   False + reason=ResourceUnresolved — at least one name absent
	condStatus := metav1.ConditionTrue
	reason := "Resolved"
	var message string
	if totalUnresolved > 0 {
		condStatus = metav1.ConditionFalse
		reason = "ResourceUnresolved"
		message = fmt.Sprintf("%d unresolved (models=%d, mcp=%d, a2a=%d, context_plugins=%d, context_skills=%d)",
			totalUnresolved,
			len(unresolved.Models),
			len(unresolved.MCPServers),
			len(unresolved.A2AAgents),
			len(unresolvedContextPlugins),
			len(unresolvedContextSkills),
		)
		message = appendContentUnresolvedMsg(message, unresolvedContextPlugins, unresolvedContextSkills)
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

	// Patch the unresolved fields on env.Status, then emit all three §6.6
	// conditions in memory and issue a SINGLE Status().Update so the
	// conditions slice + the UnresolvedRuntime/UnresolvedContextPlugins
	// fields land atomically.
	//
	// Three conditions are emitted in one Status().Update:
	//
	//   - ExecutionResourcesResolved: computed above from the
	//     Snapshotter set-difference (Resolved / ResourceUnresolved)
	//     PLUS the plugin content-present gate (nil last_successful_refresh
	//     forces False regardless of runtime resolution).
	//   - AccessGroupSynced: §7 reconcileAccessGroup helper produces
	//     the real True/False per Hub §6.6 (Synced / PartialBind /
	//     AccessGroupCreateFailed).
	//   - Available: §9 composite rollup. computeAvailable reads the
	//     two REQUIRED sub-conditions (AccessGroupSynced,
	//     ExecutionResourcesResolved) from the in-memory conditions
	//     slice and produces True/False/Unknown per the documented
	//     precedence. True iff both required sub-conditions are True;
	//     False iff any required sub-condition is False; Unknown if
	//     any required sub-condition is Unknown or missing.
	env.Status.UnresolvedRuntime = &unresolved
	env.Status.UnresolvedContextPlugins = unresolvedContextPlugins
	env.Status.UnresolvedContextSkills = unresolvedContextSkills
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
	// §9: composite Available rollup. Called AFTER the two writes above
	// (ExecutionResourcesResolved + the AccessGroupSynced reconciler) so
	// the helper reads the freshly-set sub-conditions. The previous J.6
	// placeholder Unknown is now superseded — apimeta.SetStatusCondition
	// replaces the in-memory entry regardless of prior status/reason
	// because the reason changes between calls.
	available := computeAvailable(env.Status.Conditions)
	available.ObservedGeneration = env.Generation
	available.LastTransitionTime = metav1.Now()
	apimeta.SetStatusCondition(&env.Status.Conditions, available)
	env.Status.ObservedGeneration = env.Generation

	// Spec v4 §5.2 / D-15: DB-first, K8s best-effort. Write the
	// projection row BEFORE the K8s Status subresource so a transient
	// DB failure retries the whole reconcile and the row + status land
	// together. writeEnvironmentProjection builds the row (marshalling
	// the three §6.6 closed-set conditions Available, AccessGroupSynced,
	// ExecutionResourcesResolved into jsonb bytes) and calls
	// achdb.UpsertEnvironment under the nil-DB gate.
	if err := r.writeEnvironmentProjection(ctx, &env, available); err != nil {
		if errors.Is(err, achdb.ErrOriginConflict) {
			return r.writeConflictWithUIRow(ctx, &env, logger)
		}
		return ctrl.Result{}, err
	}
	desiredStatus := env.Status
	if err := retryStatusUpdate(ctx, r.Client, &env, func(fresh *achv1alpha1.Environment) {
		fresh.Status = desiredStatus
	}); err != nil {
		logger.Error(err, "status update failed")
	}

	// D-08: Hub §6.4 requeue cadence — keeps Environments converging
	// on snapshot drift even when no spec change triggers an event-
	// driven reconcile.
	//
	// Issue #30: when the snapshot is stale (LiteLLM unreachable at the
	// moment this reconcile observed it), shorten the requeue so the
	// Environment converges to its terminal state on a timescale matched
	// to the Snapshotter's adaptive-backoff retry (seconds), not the
	// 5min steady-state interval. Otherwise an operator restart would
	// leave Available=False for ~5min even after the Snapshotter has
	// already recovered.
	if snap.Stale {
		return ctrl.Result{RequeueAfter: staleRequeueAfter}, nil
	}
	if len(unresolvedContextPlugins) > 0 || len(unresolvedContextSkills) > 0 {
		// Plugin / skill / marketplace content writes NOTIFY their own
		// controllers, not Environment, so no event re-enqueues this
		// Environment when a referenced plugin's / skill's content lands.
		// Without a shorter requeue the Environment would sit
		// ExecutionResourcesResolved=False until the 5-min steady-state tick.
		// Converge on a content-wait cadence.
		return ctrl.Result{RequeueAfter: pluginUnresolvedRequeueAfter}, nil
	}
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// staleRequeueAfter is the requeue cadence when the LiteLLM snapshot
// is stale. Picked to converge within ~30s of the Snapshotter's
// adaptive-backoff recovery (issue #30) without hammering the apiserver.
const staleRequeueAfter = 15 * time.Second

// pluginUnresolvedRequeueAfter is the requeue cadence when one or more
// context plugins are referenced but not yet content-present. Plugin /
// marketplace projection writes do not enqueue Environments, so this poll
// is how the Environment converges to Available once content lands.
const pluginUnresolvedRequeueAfter = 30 * time.Second

// contextPluginsUnresolved returns the spec.context.plugins refs that are
// not yet content-present (handoff item 4 / Task B9): a listed plugin must
// resolve AND have its content synced (last_successful_refresh non-null),
// not merely exist by name — this prevents an ExecutionResourcesResolved
// false-green when a plugin is referenced but its artifact was never
// fetched. A bare ref resolves a Plugin CRD row; name@marketplace resolves
// the marketplace_plugins row. A malformed ref (e.g. "name@") is reported
// unresolved rather than silently degrading to a bare lookup.
//
// Guarded on r.DB != nil so nil-DB unit/envtest paths are unaffected;
// production reconciles always have DB wired.
func (r *EnvironmentReconciler) contextPluginsUnresolved(ctx context.Context, env *achv1alpha1.Environment) ([]string, error) {
	if r.DB == nil {
		return nil, nil
	}
	var unresolved []string
	for _, ref := range env.Spec.Context.Plugins {
		if !pluginref.Valid(ref) {
			unresolved = append(unresolved, ref)
			continue
		}
		pname, mkt, _ := pluginref.Parse(ref)
		res, err := achdb.ResolvePluginByName(ctx, r.DB, r.Namespace, pname, mkt)
		if err != nil {
			return nil, fmt.Errorf("resolve context plugin %q: %w", ref, err)
		}
		if res == nil || res.LastSuccessfulRefresh == nil {
			unresolved = append(unresolved, ref)
		}
	}
	return unresolved, nil
}

// contextSkillsUnresolved returns the spec.context.skills refs that are not
// yet content-present: a listed skill must resolve to a Skill projection row
// AND have its content synced (last_successful_refresh non-null), not merely
// exist by name — same content-gating as contextPluginsUnresolved. Guarded on
// r.DB != nil so nil-DB unit/envtest paths are unaffected.
func (r *EnvironmentReconciler) contextSkillsUnresolved(ctx context.Context, env *achv1alpha1.Environment) ([]string, error) {
	if r.DB == nil {
		return nil, nil
	}
	var unresolved []string
	for _, sref := range env.Spec.Context.Skills {
		if !skillref.Valid(sref) {
			unresolved = append(unresolved, sref) // malformed ref → unresolved
			continue
		}
		sname, mkt, _ := skillref.Parse(sref)
		res, err := achdb.ResolveSkillByName(ctx, r.DB, r.Namespace, sname, mkt)
		if err != nil {
			return nil, fmt.Errorf("resolve context skill %q: %w", sref, err)
		}
		if res == nil || res.LastSuccessfulRefresh == nil {
			unresolved = append(unresolved, sref)
		}
	}
	return unresolved, nil
}

// contextContentUnresolved bundles the plugin + skill content-present checks so
// Reconcile carries a single error branch (keeps its cyclomatic budget). Both
// arms return the refs that resolve by name but are not yet content-synced.
func (r *EnvironmentReconciler) contextContentUnresolved(ctx context.Context, env *achv1alpha1.Environment) (plugins, skills []string, err error) {
	plugins, err = r.contextPluginsUnresolved(ctx, env)
	if err != nil {
		return nil, nil, err
	}
	skills, err = r.contextSkillsUnresolved(ctx, env)
	if err != nil {
		return nil, nil, err
	}
	return plugins, skills, nil
}

// appendContentUnresolvedMsg appends the per-kind "not content-present" suffix
// to the ExecutionResourcesResolved message for each non-empty bucket.
func appendContentUnresolvedMsg(message string, unresolvedPlugins, unresolvedSkills []string) string {
	if len(unresolvedPlugins) > 0 {
		message += fmt.Sprintf("; plugins not content-present: %v", unresolvedPlugins)
	}
	if len(unresolvedSkills) > 0 {
		message += fmt.Sprintf("; skills not content-present: %v", unresolvedSkills)
	}
	return message
}

// writeEnvironmentProjection performs the spec v4 §5.2 dual-write to
// the environments projection table — wrapping the nil-DB gate, the
// condition marshalling, the row assembly, and the achdb.UpsertEnvironment
// call into one helper to keep Reconcile's cyclomatic complexity in check.
//
// The three §6.6 closed-set conditions (Available, AccessGroupSynced,
// ExecutionResourcesResolved) are JSON-marshalled into the row's jsonb
// columns so Content Service / Platform API can render kubectl-describe
// parity without round-tripping K8s.
//
// AccessGroupSynced and ExecutionResourcesResolved are read back from
// env.Status.Conditions via apimeta.FindStatusCondition: the steady-state
// caller has just set both via SetStatusCondition; the back-compat caller
// has just set AccessGroupSynced=Unknown/Initializing and leaves
// ExecutionResourcesResolved unset — FindStatusCondition returns nil →
// the corresponding column marshals to NULL via pgx's []byte/jsonb default.
//
// Per D-15 the DB write (achdb.UpsertEnvironment) is authoritative: a
// reconcileDeletion is the §6.5 finalizer drain — extracted from Reconcile
// to keep the main method's cyclomatic complexity within golangci-lint's
// gocyclo budget. Runs delete-side-effects on LiteLLM, drains ek_ rows,
// soft-deletes the projection row, then removes the finalizer.
func (r *EnvironmentReconciler) reconcileDeletion(ctx context.Context, env *achv1alpha1.Environment, logger logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(env, environmentFinalizer) {
		return ctrl.Result{}, nil
	}
	if err := r.LiteLLM.DeleteAccessGroup(ctx, env.Name); err != nil {
		return ctrl.Result{}, fmt.Errorf("§6.5 step 2 DeleteAccessGroup: %w", err)
	}
	if err := r.LiteLLM.DeleteTag(ctx, env.Name); err != nil {
		return ctrl.Result{}, fmt.Errorf("§6.5 step 3 DeleteTag: %w", err)
	}
	if err := r.drainEkRows(ctx, env); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.softDeleteEnvironmentProjection(ctx, env); err != nil {
		return ctrl.Result{}, err
	}
	controllerutil.RemoveFinalizer(env, environmentFinalizer)
	if err := r.Update(ctx, env); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("§6.5 drain complete; finalizer removed", "env", env.Name)
	return ctrl.Result{}, nil
}

// transient error wraps with fmt.Errorf so controller-runtime requeues
// the whole reconcile (K8s status + projection row land together or not
// at all). Nil DB returns nil — the per-call-site `if r.DB != nil` gate
// is encapsulated here.
func (r *EnvironmentReconciler) writeEnvironmentProjection(
	ctx context.Context,
	env *achv1alpha1.Environment,
	available metav1.Condition,
) error {
	if r.DB == nil {
		return nil
	}
	availBytes, err := json.Marshal(available)
	if err != nil {
		return fmt.Errorf("marshal Available condition: %w", err)
	}
	var agSyncedBytes []byte
	if c := apimeta.FindStatusCondition(env.Status.Conditions, "AccessGroupSynced"); c != nil {
		b, mErr := json.Marshal(c)
		if mErr != nil {
			return fmt.Errorf("marshal AccessGroupSynced condition: %w", mErr)
		}
		agSyncedBytes = b
	}
	var execResolvedBytes []byte
	if c := apimeta.FindStatusCondition(env.Status.Conditions, "ExecutionResourcesResolved"); c != nil {
		b, mErr := json.Marshal(c)
		if mErr != nil {
			return fmt.Errorf("marshal ExecutionResourcesResolved condition: %w", mErr)
		}
		execResolvedBytes = b
	}
	row := achdb.EnvironmentRow{
		Namespace:                           env.Namespace,
		Name:                                env.Name,
		AuthorizedTeams:                     env.Spec.AuthorizedTeams,
		ContextPrompts:                      env.Spec.Context.Prompts,
		ContextPlugins:                      env.Spec.Context.Plugins,
		ContextArtifacts:                    env.Spec.Context.Artifacts,
		ContextSkills:                       env.Spec.Context.Skills,
		RuntimeModels:                       env.Spec.Runtime.Models,
		RuntimeMCPServers:                   env.Spec.Runtime.MCPServers,
		RuntimeA2AAgents:                    env.Spec.Runtime.A2AAgents,
		AvailableCondition:                  availBytes,
		AccessGroupSyncedCondition:          agSyncedBytes,
		ExecutionResourcesResolvedCondition: execResolvedBytes,
		ResourceVersion:                     env.ResourceVersion,
	}
	// Issue #34: project + NOTIFY atomically so any consumer waking on
	// ach_environments_changed SELECTs a snapshot that already reflects
	// the upsert. ErrOriginConflict (UI-owned row) is mapped to the
	// closed-set Synced=False/ConflictWithUIRow condition above by the
	// reconciler, so we return it raw here and let the caller handle.
	payload := fmt.Sprintf("%s/%s", env.Namespace, env.Name)
	if err := achdb.WithTxNotify(ctx, r.DB, environmentsChannel, payload, func(tx pgx.Tx) error {
		return achdb.UpsertEnvironmentTx(ctx, tx, row)
	}); err != nil {
		if errors.Is(err, achdb.ErrOriginConflict) {
			return err
		}
		return fmt.Errorf("db upsert environment projection: %w", err)
	}
	return nil
}

// softDeleteEnvironmentProjection wraps the nil-DB gate + the
// achdb.SoftDeleteEnvironment call. Caller is the deletion path between
// drainEkRows and RemoveFinalizer; the soft-delete preserves the
// projection row's deletion_timestamp so Plan 05-05's CS pipeline can
// keep serving until hard-delete (CS-09).
func (r *EnvironmentReconciler) softDeleteEnvironmentProjection(
	ctx context.Context,
	env *achv1alpha1.Environment,
) error {
	if r.DB == nil {
		return nil
	}
	payload := fmt.Sprintf("%s/%s", env.Namespace, env.Name)
	if err := achdb.WithTxNotify(ctx, r.DB, environmentsChannel, payload, func(tx pgx.Tx) error {
		return achdb.SoftDeleteEnvironmentTx(ctx, tx, env.Namespace, env.Name)
	}); err != nil {
		return fmt.Errorf("db soft-delete environment projection: %w", err)
	}
	return nil
}

// reconcileAccessGroup is the §7 implementation step: ensure the LiteLLM
// access group <env.Name> exists with the correct desired-state bindings
// for env.Spec.Runtime.Models / .MCPServers / .A2AAgents and
// env.Spec.AuthorizedTeams. Returns the metav1.Condition that the caller
// should publish on env.Status.Conditions.
//
// Migration (issue #17): rewrites the legacy POST /access_group/new flow
// (which required non-empty model_names per LiteLLM 1.83.x's hidden
// validator, and bound teams via the magic team.models[] entry
// "access_group/<name>") onto the /v1/access_group endpoints. Resolution
// of MCP / A2A / Team names → LiteLLM IDs happens on demand each
// reconcile (no Snapshotter changes per issue #17 plan §1); the access
// group UUID is resolved by name each reconcile (no CRD status field
// per issue #17 plan §2).
//
// Closed-set conditions emitted (Hub §6.6, updated for issue #17):
//   - True/Synced                  — desired state matches observed
//   - False/UnresolvedReferences   — one or more env.Spec names had no
//     matching LiteLLM ID. Distinct from
//     ExecutionResourcesResolved=False because that condition is about
//     the Snapshotter (cached, may be stale); this one is about the
//     fresh-fetched resolver maps.
//   - False/AccessGroupCreateFailed — POST /v1/access_group failed
//   - False/AccessGroupUpdateFailed — PUT /v1/access_group/{id} failed
//   - False/ResolveFailed          — one of ListMCPServers /
//     ListA2AAgents / ListTeamsByAlias errored (LiteLLM unreachable
//     mid-reconcile)
func (r *EnvironmentReconciler) reconcileAccessGroup(
	ctx context.Context,
	env *achv1alpha1.Environment,
) metav1.Condition {
	logger := log.FromContext(ctx).WithValues("environment", env.Name)

	// Step 1: resolve names → IDs (on-demand each reconcile per #17 §1).
	//
	// ListMCPServers / ListA2AAgents return litellm.ErrNotFound when the
	// LiteLLM list endpoint responds 200 with an empty array (REL-05
	// length-check). Per the client contract (internal/litellm/client.go),
	// callers MUST translate ErrNotFound → empty slice: a LiteLLM with zero
	// registered MCP servers / A2A agents is a valid empty closed-set, not a
	// resolve failure. Without this an Environment referencing no MCP/A2A
	// entries can never reach Available on a fresh LiteLLM. Mirrors the
	// Snapshotter's handling (internal/snapshot/snapshot.go).
	mcpEntries, mcpErr := r.LiteLLM.ListMCPServers(ctx)
	if errors.Is(mcpErr, litellm.ErrNotFound) {
		mcpEntries, mcpErr = nil, nil
	}
	if mcpErr != nil {
		return resolveFailed(env, "ListMCPServers", mcpErr)
	}
	agentEntries, agentErr := r.LiteLLM.ListA2AAgents(ctx)
	if errors.Is(agentErr, litellm.ErrNotFound) {
		agentEntries, agentErr = nil, nil
	}
	if agentErr != nil {
		return resolveFailed(env, "ListA2AAgents", agentErr)
	}

	mcpMap := make(map[string]string, len(mcpEntries))
	for _, e := range mcpEntries {
		if e.ServerName != "" {
			mcpMap[e.ServerName] = e.ServerID
		}
	}
	agentMap := make(map[string]string, len(agentEntries))
	for _, e := range agentEntries {
		if e.AgentName != "" {
			agentMap[e.AgentName] = e.AgentID
		}
	}

	mcpIDs, mcpUnresolved := mapResolve(env.Spec.Runtime.MCPServers, mcpMap)
	agentIDs, agentUnresolved := mapResolve(env.Spec.Runtime.A2AAgents, agentMap)
	// mapResolve returns nil for empty input; normalize to a non-nil []
	// so the PUT body serializes the dimension as `[]` (clear) rather
	// than `null` — AccessGroupUpdateRequest no longer uses omitempty on
	// the managed lists and only `[]` is a proven LiteLLM clear.
	mcpIDs = nonNilStrings(mcpIDs)
	agentIDs = nonNilStrings(agentIDs)

	// Teams use the existing per-alias filtered endpoint. N small (1-3
	// authorized teams per env) so per-alias round-trips are fine.
	teamIDs := make([]string, 0, len(env.Spec.AuthorizedTeams))
	var teamUnresolved []string
	for _, alias := range env.Spec.AuthorizedTeams {
		entries, terr := r.LiteLLM.ListTeamsByAlias(ctx, alias)
		if terr != nil {
			return resolveFailed(env, "ListTeamsByAlias", terr)
		}
		if len(entries) == 0 || entries[0].TeamID == "" {
			teamUnresolved = append(teamUnresolved, alias)
			continue
		}
		teamIDs = append(teamIDs, entries[0].TeamID)
	}

	if len(mcpUnresolved)+len(agentUnresolved)+len(teamUnresolved) > 0 {
		return metav1.Condition{
			Type:   "AccessGroupSynced",
			Status: metav1.ConditionFalse,
			Reason: "UnresolvedReferences",
			Message: fmt.Sprintf(
				"unresolved: mcpServers=%v a2aAgents=%v authorizedTeams=%v",
				mcpUnresolved, agentUnresolved, teamUnresolved,
			),
			ObservedGeneration: env.Generation,
			LastTransitionTime: metav1.Now(),
		}
	}

	// Step 2: discover whether the access group already exists.
	existing, gerr := r.LiteLLM.GetAccessGroupByName(ctx, env.Name)
	if gerr != nil {
		return resolveFailed(env, "GetAccessGroupByName", gerr)
	}

	desiredModels := env.Spec.Runtime.Models
	if desiredModels == nil {
		desiredModels = []string{}
	}

	// Step 3a: POST when absent.
	if existing == nil {
		created, cerr := r.LiteLLM.CreateAccessGroup(ctx, litellm.AccessGroupCreateRequest{
			AccessGroupName:    env.Name,
			AccessModelNames:   desiredModels,
			AccessMCPServerIDs: mcpIDs,
			AccessAgentIDs:     agentIDs,
			AssignedTeamIDs:    teamIDs,
		})
		if cerr != nil {
			logger.Error(cerr, "POST /v1/access_group failed")
			return metav1.Condition{
				Type:               "AccessGroupSynced",
				Status:             metav1.ConditionFalse,
				Reason:             "AccessGroupCreateFailed",
				Message:            fmt.Sprintf("LiteLLM CreateAccessGroup(%s) failed: %v", env.Name, cerr),
				ObservedGeneration: env.Generation,
				LastTransitionTime: metav1.Now(),
			}
		}
		logger.Info("created access group", "name", env.Name, "id", created.AccessGroupID)
		return accessGroupSyncedCondition(env, created)
	}

	// Step 3b: PUT when drifted.
	if drift := computeAccessGroupDrift(existing, desiredModels, mcpIDs, agentIDs, teamIDs); drift {
		updated, uerr := r.LiteLLM.UpdateAccessGroup(ctx, existing.AccessGroupID, litellm.AccessGroupUpdateRequest{
			AccessModelNames:   desiredModels,
			AccessMCPServerIDs: mcpIDs,
			AccessAgentIDs:     agentIDs,
			AssignedTeamIDs:    teamIDs,
		})
		if uerr != nil {
			logger.Error(uerr, "PUT /v1/access_group/{id} failed", "id", existing.AccessGroupID)
			return metav1.Condition{
				Type:               "AccessGroupSynced",
				Status:             metav1.ConditionFalse,
				Reason:             "AccessGroupUpdateFailed",
				Message:            fmt.Sprintf("LiteLLM UpdateAccessGroup(%s, id=%s) failed: %v", env.Name, existing.AccessGroupID, uerr),
				ObservedGeneration: env.Generation,
				LastTransitionTime: metav1.Now(),
			}
		}
		logger.Info("updated access group", "name", env.Name, "id", updated.AccessGroupID)
		return accessGroupSyncedCondition(env, updated)
	}

	return accessGroupSyncedCondition(env, existing)
}

// resolveFailed packages a LiteLLM-unreachable failure during the
// reconcileAccessGroup resolver phase into the closed-set condition.
func resolveFailed(env *achv1alpha1.Environment, op string, err error) metav1.Condition {
	return metav1.Condition{
		Type:               "AccessGroupSynced",
		Status:             metav1.ConditionFalse,
		Reason:             "ResolveFailed",
		Message:            fmt.Sprintf("LiteLLM %s failed: %v", op, err),
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
}

// accessGroupSyncedCondition is the True/Synced terminal condition shared by the
// create and update branches.
func accessGroupSyncedCondition(env *achv1alpha1.Environment, ag *litellm.AccessGroupResponse) metav1.Condition {
	return metav1.Condition{
		Type:               "AccessGroupSynced",
		Status:             metav1.ConditionTrue,
		Reason:             "Synced",
		Message:            fmt.Sprintf("access group %q (id=%s) bound to %d team(s), %d model(s), %d mcp, %d agent", ag.AccessGroupName, ag.AccessGroupID, len(ag.AssignedTeamIDs), len(ag.AccessModelNames), len(ag.AccessMCPServerIDs), len(ag.AccessAgentIDs)),
		ObservedGeneration: env.Generation,
		LastTransitionTime: metav1.Now(),
	}
}

// nonNilStrings normalizes a nil slice to an empty (non-nil) slice so it
// marshals to JSON `[]` (clear) rather than `null`. Required because
// AccessGroupUpdateRequest no longer uses omitempty on the managed lists
// and only `[]` (not `null`) is a proven LiteLLM clear (PUT partial-update:
// absent=keep, `[]`=clear).
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// mapResolve splits `names` into (resolvedIDs, unresolvedNames) by
// looking each up in `m`. Empty names are skipped (defensive: should
// not appear since the CRD validators reject empty list members).
func mapResolve(names []string, m map[string]string) (ids []string, unresolved []string) {
	for _, n := range names {
		if n == "" {
			continue
		}
		if id, ok := m[n]; ok {
			ids = append(ids, id)
		} else {
			unresolved = append(unresolved, n)
		}
	}
	return ids, unresolved
}

// computeAccessGroupDrift returns true iff the existing access group's
// stored bindings diverge from the desired state. Each dimension is
// compared as a set (order-independent).
func computeAccessGroupDrift(existing *litellm.AccessGroupResponse, models, mcps, agents, teams []string) bool {
	return !sameSet(existing.AccessModelNames, models) ||
		!sameSet(existing.AccessMCPServerIDs, mcps) ||
		!sameSet(existing.AccessAgentIDs, agents) ||
		!sameSet(existing.AssignedTeamIDs, teams)
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]struct{}, len(a))
	for _, x := range a {
		m[x] = struct{}{}
	}
	for _, x := range b {
		if _, ok := m[x]; !ok {
			return false
		}
	}
	return true
}

// requiredAvailableSubConditions is the closed set of condition types whose
// status drives the Environment.Available rollup. Per TODO §9 and TODO §16
// the contract is: Available=True IFF every required sub-condition is True.
// ContentReady is intentionally OMITTED — Hub §6.6 lists it in the closed
// set but no reconciler writes it in Phase 2; including it would pin the
// rollup at Unknown indefinitely. When a future plan wires ContentReady,
// add it to this slice in one line.
var requiredAvailableSubConditions = []string{
	"AccessGroupSynced",
	"ExecutionResourcesResolved",
}

// computeAvailable is the pure §9 rollup. Given the current conditions
// slice, return the Available condition the reconciler should write back.
// The helper is independent of env.Generation (the caller stamps that)
// and of LastTransitionTime (apimeta.SetStatusCondition preserves it
// when the (Type, Status, Reason) tuple is unchanged).
//
// Precedence:
//
//  1. Any REQUIRED sub-condition False → Available=False reason=SubConditionsNotReady
//  2. Else any REQUIRED sub-condition Unknown or absent → Available=Unknown reason=PendingSubConditions
//  3. All REQUIRED sub-conditions True → Available=True reason=AllSubConditionsTrue
//
// The pre-existing "Available" entry in conds (if any) is ignored — the
// helper recomputes from scratch every call, so a stale prior write does
// not influence the new outcome.
func computeAvailable(conds []metav1.Condition) metav1.Condition {
	// Build a quick lookup so we walk conds once.
	byType := make(map[string]metav1.ConditionStatus, len(conds))
	for _, c := range conds {
		byType[c.Type] = c.Status
	}

	var falseTypes, unknownOrMissing []string
	for _, t := range requiredAvailableSubConditions {
		switch byType[t] {
		case metav1.ConditionTrue:
			// happy path — accumulates implicitly
		case metav1.ConditionFalse:
			falseTypes = append(falseTypes, t)
		default:
			// metav1.ConditionUnknown OR map zero-value (missing entirely).
			unknownOrMissing = append(unknownOrMissing, t)
		}
	}

	switch {
	case len(falseTypes) > 0:
		return metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionFalse,
			Reason:  ReasonSubConditionsNotReady,
			Message: fmt.Sprintf("sub-conditions False: %v", falseTypes),
		}
	case len(unknownOrMissing) > 0:
		return metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionUnknown,
			Reason:  ReasonPendingSubConditions,
			Message: fmt.Sprintf("sub-conditions Unknown or not yet written: %v", unknownOrMissing),
		}
	default:
		return metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionTrue,
			Reason:  ReasonAllSubConditionsTrue,
			Message: fmt.Sprintf("all required sub-conditions True: %v", requiredAvailableSubConditions),
		}
	}
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

// writeConflictWithUIRow flips AccessGroupSynced=False/ConflictWithUIRow
// when the projection upsert is blocked by a UI-origin row holding the
// same PK. Requeues in 1 minute so the operator does not hot-loop. The
// CR status surfaces enough detail for an operator to investigate (the
// row's UI lock will be released elsewhere; the next reconcile retries).
func (r *EnvironmentReconciler) writeConflictWithUIRow(
	ctx context.Context,
	env *achv1alpha1.Environment,
	logger logr.Logger,
) (ctrl.Result, error) {
	setConflictWithUIRowCondition(&env.Status.Conditions, "AccessGroupSynced", env.Generation)
	env.Status.ObservedGeneration = env.Generation
	desiredStatus := env.Status
	if err := retryStatusUpdate(ctx, r.Client, env, func(fresh *achv1alpha1.Environment) {
		fresh.Status = desiredStatus
	}); err != nil {
		logger.Error(err, "status update failed", "reason", ReasonConflictWithUIRow)
	}
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// SetupWithManager registers the reconciler with controller-runtime.
// Single watch on Environment — Phase 2 will add Secret + LiteLLM
// fast-path watches when they exist.
func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Environment{}, builder.WithPredicates()).
		Named("ach-environment")
	if r.ResyncSource != nil {
		b = b.WatchesRawSource(
			source.Channel(r.ResyncSource, &handler.EnqueueRequestForObject{}),
		)
	}
	return b.Complete(r)
}
