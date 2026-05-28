// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
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
	achdb "github.com/ackstorm/ach/internal/db"
)

// pluginsChannel is the NOTIFY channel emitted on every Plugin projection
// write/soft-delete (issue #34). The external_refs upsert (the
// fetcher-state side of the same logical resource) also fires on this
// channel under the "plugin/<name>" payload.
const pluginsChannel = "ach_plugins_changed"

// PluginReconciler reconciles a Plugin object. Phase 2 implements the
// §10.3 steady-state refresh loop (fetch → stage → fsync → rename(2) →
// DB UPSERT) via the shared materializeExternalRef helper. Size cap is
// enforced per D-12.
//
// CacheRoot is the PVC mount root from ACH_CACHE_ROOT (default
// /var/cache/ach). Plan 02-09 injects DB, PluginMaxSizeMiB, and
// Fetchers from cmd/operator/main.go at SetupWithManager time. DB nil-
// tolerance preserved for the Phase 1 envtest path (the finalizer test
// runs without a Postgres pool).
type PluginReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
	CacheRoot string

	// Phase 2 (Plan 02-09 wires these from cmd/operator/main.go):

	// DB is the Postgres pool used for external_refs UPSERT/GET/DELETE.
	// Nil in envtest (Phase 1 finalizer test); steady-state branch skips
	// DB reads/writes when nil so the existing test stays green.
	DB *pgxpool.Pool

	// PluginMaxSizeMiB is the per-plugin size cap (D-12). When 0, the
	// cap is treated as "infinite" — materializeExternalRef receives
	// SizeCapBytes = 0 and skips the LimitReader wrap. Plan 02-09
	// validates ACH_PLUGIN_MAX_SIZE_MIB > 0 at startup; envtest leaves
	// it at zero and exercises the no-cap branch.
	PluginMaxSizeMiB int

	// Fetchers is the FetcherFactory; nil → defaults to registry.For.
	// Tests inject a fake fetcher to exercise the §10.3 staging /
	// rename(2) / UPSERT branches without live HTTPS traffic.
	Fetchers FetcherFactory
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=plugins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=plugins/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=plugins/finalizers,verbs=update

// Reconcile implements the Phase 2 Plugin lifecycle:
//
//   - Fetch + NotFound → no-op.
//   - Deletion path: remove <CacheRoot>/plugin/<name>.tar.gz (IsNotExist
//     tolerated) → DeleteExternalRef (DB-aligned to live CRs per OP-12)
//     → RemoveFinalizer.
//   - Finalizer-add path: AddFinalizer + Update.
//   - Steady-state: materializeExternalRef → status + force-refresh
//     annotation removal → RequeueAfter.
func (r *PluginReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("plugin", req.NamespacedName)

	var cr achv1alpha1.Plugin
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// ─── Deletion path: §10.3 cleanup + DB row drop + finalizer remove. ───
	if !cr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&cr, pluginFinalizer) {
			// §10.3 cache layout: plugin/<name>.tar.gz
			if err := os.Remove(filepath.Join(r.CacheRoot, "plugin", cr.Name+".tar.gz")); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return ctrl.Result{}, err
			}
			// OP-12: drop the DB row in sync with the cached file. Nil-DB
			// (Phase 1 envtest) skips the call so the finalizer test still
			// passes without a Postgres pool.
			if r.DB != nil {
				if err := achdb.DeleteExternalRef(ctx, r.DB, "plugin", cr.Name); err != nil {
					return ctrl.Result{}, fmt.Errorf("db delete external_ref: %w", err)
				}
			}
			// Spec v4 §5.2 / CS-09 / D-15: soft-delete the plugins
			// projection row AFTER the existing external_refs DELETE
			// and BEFORE finalizer removal. Two writes are intentional:
			// external_refs is the §10.3 cache-refresh row (no longer
			// needed once the file is gone); the plugins projection row
			// stays soft-deleted so CS-09 in-flight reads finish — Plan
			// 05-05 staleness check filters on deletion_timestamp.
			// Issue #34: soft-delete + NOTIFY in one tx so consumers
			// waking on ach_plugins_changed SELECT the soft-deleted row.
			if r.DB != nil {
				payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
				if err := achdb.WithTxNotify(ctx, r.DB, pluginsChannel, payload, func(tx pgx.Tx) error {
					return achdb.SoftDeletePluginTx(ctx, tx, cr.Namespace, cr.Name)
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("db soft-delete plugin projection: %w", err)
				}
			}
			controllerutil.RemoveFinalizer(&cr, pluginFinalizer)
			if err := r.Update(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("§10.3 cleanup complete; finalizer removed", "name", cr.Name)
		}
		return ctrl.Result{}, nil
	}

	// ─── Finalizer-add path. ───
	if !controllerutil.ContainsFinalizer(&cr, pluginFinalizer) {
		controllerutil.AddFinalizer(&cr, pluginFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// ─── Phase 2 steady state: §10.3 refresh. ───

	// Read PriorRev for conditional GET; absent DB → empty PriorRev forces
	// a full fetch on every reconcile (envtest mode).
	var priorRev string
	var lastRefresh time.Time
	if r.DB != nil {
		priorRow, err := achdb.GetExternalRef(ctx, r.DB, "plugin", cr.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("db get external_ref: %w", err)
		}
		if priorRow != nil {
			priorRev = priorRow.UpstreamRev
			lastRefresh = priorRow.LastSuccessfulRefresh
		}
	}

	// §10.3 within-interval gate: skip the upstream probe when the CR was
	// successfully refreshed within spec.refresh.interval and nothing
	// demands re-verification. Cuts steady-state GitHub API burn ~10x.
	// Reads lastRefresh from cr.Status.LastSuccessfulRefresh (not the DB
	// row) because the materializeExternalRef NotModified (304) path
	// returns before the external_refs DB upsert — so the DB row goes
	// stale on every 304, defeating the gate. Status is bumped on both
	// fresh and NotModified paths (see plugin_controller.go status block
	// below), so it is the reliable source for the gate decision.
	var gateLastRefresh time.Time
	if cr.Status.LastSuccessfulRefresh != nil {
		gateLastRefresh = cr.Status.LastSuccessfulRefresh.Time
	}
	if shouldSkipFetch(cr.Spec.Refresh, gateLastRefresh, cr.Status.ObservedGeneration, cr.Generation, cr.Annotations, time.Now()) {
		remaining := time.Until(gateLastRefresh.Add(requeueDurationFromRefresh(cr.Spec.Refresh)))
		if remaining < time.Second {
			remaining = time.Second
		}
		logger.V(1).Info("§10.3 within-interval gate: skipping fetch",
			"lastRefresh", gateLastRefresh, "requeueAfter", remaining)
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	spec := cr.Spec
	sourceSpec := buildSourceSpec(spec.Type, spec.GitHub, spec.GitLab, spec.Bitbucket, spec.S3, spec.GCS, spec.HTTP)
	authRef := extractAuthSecretRef(spec.Type, spec.GitHub, spec.GitLab, spec.Bitbucket, spec.S3, spec.GCS, spec.HTTP)
	finalPath := computeFinalPath(r.CacheRoot, "plugin", cr.Name, "")

	deps := ExternalRefRefreshDeps{
		Client:        r.Client,
		Namespace:     r.Namespace,
		DB:            r.DB,
		CacheRoot:     r.CacheRoot,
		Kind:          "plugin",
		Name:          cr.Name,
		SourceSpec:    sourceSpec,
		AuthSecretRef: authRef,
		Refresh:       spec.Refresh,
		PriorRev:      priorRev,
		SizeCapBytes:  int64(r.PluginMaxSizeMiB) << 20,
		FinalPath:     finalPath,
		Fetchers:      r.Fetchers,
		Log:           logger,
	}
	result := materializeExternalRef(ctx, deps)

	requeue := requeueDurationFromRefresh(spec.Refresh)

	// ─── Failure path. ───
	if result.Err != nil {
		reason, message := classifyFetchError(result.Err, spec.Refresh, lastRefresh)
		applyReconcileConditions(&cr.Status.Conditions, reason, message, cr.Generation)
		cr.Status.ObservedGeneration = cr.Generation
		desiredStatus := cr.Status
		if statusErr := retryStatusUpdate(ctx, r.Client, &cr, func(fresh *achv1alpha1.Plugin) {
			fresh.Status = desiredStatus
		}); statusErr != nil {
			logger.Error(statusErr, "status update failed", "reason", reason)
		}
		// Configuration-derived and terminal-upstream reasons won't change
		// by retrying immediately — return nil + RequeueAfter so the
		// reconciler does not hot-loop. Transient (Unreachable +
		// StaleCacheExpired) returns the err so controller-runtime's
		// workqueue applies exponential backoff.
		switch reason {
		case ReasonPluginTooLarge, ReasonUnauthorized, ReasonNotFound, ReasonUpstreamInvalid:
			return ctrl.Result{RequeueAfter: requeue}, nil
		default:
			return ctrl.Result{}, result.Err
		}
	}

	// ─── Success / NotModified: status update, annotation clear, requeue. ───
	applyReconcileConditions(&cr.Status.Conditions, ReasonSynced, sourceReachableMessage(sourceSpec), cr.Generation)
	if result.NotModified {
		// Preserve the prior UpstreamRev (already equal to priorRev per
		// MaterializeResult contract) and StorageLocation; bump
		// LastSuccessfulRefresh so the staleness predicate stays accurate
		// even when the upstream didn't change.
		cr.Status.UpstreamRev = result.UpstreamRev
		if cr.Status.StorageLocation == "" {
			// First-reconcile-after-NotModified edge: no prior status was
			// written so populate StorageLocation from the computed final
			// path (the file already exists on disk per the NotModified
			// contract).
			cr.Status.StorageLocation = finalPath
		}
	} else {
		cr.Status.UpstreamRev = result.UpstreamRev
		cr.Status.StorageLocation = finalPath
	}
	now := metav1.Now()
	cr.Status.LastSuccessfulRefresh = &now
	cr.Status.ObservedGeneration = cr.Generation

	// Spec v4 §5.2 / D-13 / D-15: dual-write the plugins projection row
	// BEFORE the best-effort K8s Status update. DB is authoritative —
	// Plan 05-05 CS pipeline reads max_staleness_seconds + storage_location
	// + last_successful_refresh from this row on every plugin request.
	// The external_refs row drives §10.3 cache-refresh decisions for the
	// Operator side; this projection row is what CS reads.
	if r.DB != nil {
		lastRefresh := now.Time
		row := achdb.PluginRow{
			Namespace:             cr.Namespace,
			Name:                  cr.Name,
			StorageLocation:       cr.Status.StorageLocation,
			LastSuccessfulRefresh: &lastRefresh,
			MaxStalenessSeconds:   int64(spec.Refresh.MaxStaleness.Duration.Seconds()),
			ResourceVersion:       cr.ResourceVersion,
		}
		// Issue #34: project + NOTIFY atomically so any consumer waking
		// on ach_plugins_changed SELECTs a snapshot that already reflects
		// the upsert. ErrOriginConflict (UI-owned row) flips to
		// SourceReachable=False/ConflictWithUIRow and requeues in a
		// minute so the operator does not hot-loop.
		payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
		err := achdb.WithTxNotify(ctx, r.DB, pluginsChannel, payload, func(tx pgx.Tx) error {
			return achdb.UpsertPluginTx(ctx, tx, row)
		})
		if err != nil {
			if errors.Is(err, achdb.ErrOriginConflict) {
				return r.writePluginConflictStatus(ctx, &cr, logger)
			}
			return ctrl.Result{}, fmt.Errorf("db upsert plugin projection: %w", err)
		}
	}
	desiredStatus := cr.Status
	if err := retryStatusUpdate(ctx, r.Client, &cr, func(fresh *achv1alpha1.Plugin) {
		fresh.Status = desiredStatus
	}); err != nil {
		// WR-02: retry-on-conflict eliminates the 409-driven flake, but
		// a non-409 Get/Update error (apiserver unreachable, NotFound)
		// still surfaces here. A follow-up r.Update on the annotation
		// would also fail; skip and let the next reconcile retry from
		// a fresh Get.
		logger.Error(err, "status update failed; skipping annotation-clear")
		return ctrl.Result{RequeueAfter: requeue}, nil
	}

	// D-07: clear force-refresh annotation if present (UpsertExternalRef
	// in materializeExternalRef already cleared the DB column).
	if _, hasAnnotation := cr.Annotations["ach.ackstorm.ai/force-refresh"]; hasAnnotation {
		delete(cr.Annotations, "ach.ackstorm.ai/force-refresh")
		if err := r.Update(ctx, &cr); err != nil {
			logger.Error(err, "force-refresh annotation removal failed")
		}
	}

	return ctrl.Result{RequeueAfter: requeue}, nil
}

// writePluginConflictStatus flips Synced=False/ConflictWithUIRow when the
// projection upsert is blocked by a UI-origin row holding the same PK.
// Mirrors the back-off pattern used by the other reconcilers: 1-minute
// RequeueAfter so the operator does not hot-loop on a UI lock.
func (r *PluginReconciler) writePluginConflictStatus(
	ctx context.Context,
	cr *achv1alpha1.Plugin,
	logger logr.Logger,
) (ctrl.Result, error) {
	apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               ConditionSynced,
		Status:             metav1.ConditionFalse,
		Reason:             "ConflictWithUIRow",
		Message:            "projection row owned by UI; operator declines to overwrite",
		ObservedGeneration: cr.Generation,
		LastTransitionTime: metav1.Now(),
	})
	cr.Status.ObservedGeneration = cr.Generation
	desiredStatus := cr.Status
	if err := retryStatusUpdate(ctx, r.Client, cr, func(fresh *achv1alpha1.Plugin) {
		fresh.Status = desiredStatus
	}); err != nil {
		logger.Error(err, "status update failed", "reason", "ConflictWithUIRow")
	}
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *PluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Plugin{}, builder.WithPredicates()).
		Named("ach-plugin").
		Complete(r)
}
