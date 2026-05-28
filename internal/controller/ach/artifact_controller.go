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
	"github.com/jackc/pgx/v5/pgxpool"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// ArtifactReconciler reconciles an Artifact object. Phase 2 implements
// the §10.3 steady-state refresh via materializeExternalRef. Cache path
// depends on spec.scope: object → artifact/<name>; directory →
// artifact/<name>.tar.gz. No size cap.
//
// Phase 2 directory-scope note: the fetcher returns whatever the upstream
// produces — Artifact spec.scope=directory currently relies on the source
// returning a pre-archived .tar.gz; on-the-fly tarball materialization
// from a directory prefix is a v1beta1 enhancement. (See 02-CONTEXT
// "Deferred" item set.)
//
// Deletion-path narrowing: when cr.Status.StorageLocation has been
// populated by a prior successful Phase 2 reconcile, we remove exactly
// that path. Otherwise, we fall back to the Phase 1 "try both object +
// directory paths" sweep for backwards compatibility with CRs that
// existed before Phase 2 ran.
type ArtifactReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
	CacheRoot string

	// Phase 2:
	DB       *pgxpool.Pool
	Fetchers FetcherFactory
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=artifacts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=artifacts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=artifacts/finalizers,verbs=update

// Reconcile mirrors PluginReconciler.Reconcile with kind="artifact",
// no size cap, and spec.Scope feeding computeFinalPath.
func (r *ArtifactReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("artifact", req.NamespacedName)

	var cr achv1alpha1.Artifact
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// ─── Deletion path: prefer status.StorageLocation when set. ───
	if !cr.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, &cr, logger)
	}

	// ─── Finalizer-add path. ───
	if !controllerutil.ContainsFinalizer(&cr, artifactFinalizer) {
		controllerutil.AddFinalizer(&cr, artifactFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// ─── Phase 2 steady state: §10.3 refresh. ───
	var priorRev string
	var lastRefresh time.Time
	if r.DB != nil {
		priorRow, err := achdb.GetExternalRef(ctx, r.DB, "artifact", cr.Name)
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
	// fresh and NotModified paths, making it the reliable source.
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
	finalPath := computeFinalPath(r.CacheRoot, "artifact", cr.Name, cr.Spec.Scope)

	deps := ExternalRefRefreshDeps{
		Client:        r.Client,
		Namespace:     r.Namespace,
		DB:            r.DB,
		CacheRoot:     r.CacheRoot,
		Kind:          "artifact",
		Name:          cr.Name,
		SourceSpec:    sourceSpec,
		AuthSecretRef: authRef,
		Refresh:       spec.Refresh,
		PriorRev:      priorRev,
		SizeCapBytes:  0, // no cap per spec §13
		FinalPath:     finalPath,
		Fetchers:      r.Fetchers,
		Log:           logger,
	}
	result := materializeExternalRef(ctx, deps)

	requeue := requeueDurationFromRefresh(spec.Refresh)

	if result.Err != nil {
		reason, message := classifyFetchError(result.Err, spec.Refresh, lastRefresh)
		applyReconcileConditions(&cr.Status.Conditions, reason, message, cr.Generation)
		cr.Status.ObservedGeneration = cr.Generation
		desiredStatus := cr.Status
		if statusErr := retryStatusUpdate(ctx, r.Client, &cr, func(fresh *achv1alpha1.Artifact) {
			fresh.Status = desiredStatus
		}); statusErr != nil {
			logger.Error(statusErr, "status update failed", "reason", reason)
		}
		switch reason {
		case ReasonPluginTooLarge, ReasonUnauthorized, ReasonNotFound, ReasonUpstreamInvalid:
			return ctrl.Result{RequeueAfter: requeue}, nil
		default:
			return ctrl.Result{}, result.Err
		}
	}

	applyReconcileConditions(&cr.Status.Conditions, ReasonSynced, sourceReachableMessage(sourceSpec), cr.Generation)
	cr.Status.UpstreamRev = result.UpstreamRev
	if result.NotModified && cr.Status.StorageLocation == "" {
		cr.Status.StorageLocation = finalPath
	} else if !result.NotModified {
		cr.Status.StorageLocation = finalPath
	}
	now := metav1.Now()
	cr.Status.LastSuccessfulRefresh = &now
	cr.Status.ObservedGeneration = cr.Generation

	// Spec v4 §5.2 / D-13 / D-15: dual-write the artifacts projection
	// row BEFORE the best-effort K8s Status update. DB is authoritative —
	// Plan 05-05 CS pipeline reads scope + max_staleness_seconds +
	// last_successful_refresh from this row on every artifact request.
	// cr.Spec.Scope is passed verbatim — kubebuilder enum validation
	// constrains it to {"object","directory"} at admission AND the DB
	// CHECK constraint scope IN ('object','directory') in migration
	// 000004 catches any drift.
	if err := r.writeArtifactProjection(ctx, &cr, now.Time, spec.Refresh.MaxStaleness.Duration); err != nil {
		return ctrl.Result{}, err
	}
	desiredStatus := cr.Status
	if err := retryStatusUpdate(ctx, r.Client, &cr, func(fresh *achv1alpha1.Artifact) {
		fresh.Status = desiredStatus
	}); err != nil {
		// WR-02: see plugin_controller.go for rationale.
		logger.Error(err, "status update failed; skipping annotation-clear")
		return ctrl.Result{RequeueAfter: requeue}, nil
	}

	if _, hasAnnotation := cr.Annotations["ach.ackstorm.ai/force-refresh"]; hasAnnotation {
		delete(cr.Annotations, "ach.ackstorm.ai/force-refresh")
		if err := r.Update(ctx, &cr); err != nil {
			logger.Error(err, "force-refresh annotation removal failed")
		}
	}

	return ctrl.Result{RequeueAfter: requeue}, nil
}

// reconcileDeletion runs the artifact deletion path: on-disk cache rm
// (using the recorded cr.Status.StorageLocation when set, else the
// Phase 1 carry-forward sweep over both object + directory paths) →
// external_refs DELETE (Phase 2 row removal) → projection-row soft-delete
// (Plan 05-04 — CS-09 grace window) → finalizer removal.
//
// Extracted from Reconcile to keep its cyclomatic complexity within the
// gocyclo budget after the spec v4 §5.2 projection-write extension landed.
// The body is purely sequential — every error path is a `return ctrl.Result{}, err`,
// no requeue cadence to compute here.
func (r *ArtifactReconciler) reconcileDeletion(
	ctx context.Context,
	cr *achv1alpha1.Artifact,
	logger logr.Logger,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(cr, artifactFinalizer) {
		return ctrl.Result{}, nil
	}
	if cr.Status.StorageLocation != "" {
		// Exact path recorded by Phase 2 — remove only that.
		if err := os.Remove(cr.Status.StorageLocation); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return ctrl.Result{}, err
		}
	} else {
		// Phase 1 carry-forward: status was never populated (CR
		// existed before Phase 2 reconcile ran). Attempt BOTH
		// paths; tolerate IsNotExist on either.
		if err := os.Remove(filepath.Join(r.CacheRoot, "artifact", cr.Name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return ctrl.Result{}, err
		}
		if err := os.Remove(filepath.Join(r.CacheRoot, "artifact", cr.Name+".tar.gz")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return ctrl.Result{}, err
		}
	}
	if r.DB != nil {
		if err := achdb.DeleteExternalRef(ctx, r.DB, "artifact", cr.Name); err != nil {
			return ctrl.Result{}, fmt.Errorf("db delete external_ref: %w", err)
		}
	}
	// Spec v4 §5.2 / CS-09 / D-15: soft-delete the artifacts projection
	// row AFTER the existing external_refs DELETE and BEFORE finalizer
	// removal. Two writes are intentional — see plugin_controller.go.
	if err := r.softDeleteArtifactProjection(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	controllerutil.RemoveFinalizer(cr, artifactFinalizer)
	if err := r.Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("§10.3 cleanup complete; finalizer removed", "name", cr.Name)
	return ctrl.Result{}, nil
}

// writeArtifactProjection wraps the spec v4 §5.2 dual-write to the
// artifacts projection table — encapsulating the nil-DB gate and the
// achdb.UpsertArtifact call so Reconcile's cyclomatic complexity stays
// within the gocyclo budget. lastRefresh is the metav1.Time the caller
// already stamped on cr.Status.LastSuccessfulRefresh; maxStaleness is
// pulled from spec.Refresh.MaxStaleness so callers don't reach back into
// cr.Spec from the helper.
func (r *ArtifactReconciler) writeArtifactProjection(
	ctx context.Context,
	cr *achv1alpha1.Artifact,
	lastRefresh time.Time,
	maxStaleness time.Duration,
) error {
	if r.DB == nil {
		return nil
	}
	row := achdb.ArtifactRow{
		Namespace:             cr.Namespace,
		Name:                  cr.Name,
		StorageLocation:       cr.Status.StorageLocation,
		Scope:                 cr.Spec.Scope,
		LastSuccessfulRefresh: &lastRefresh,
		MaxStalenessSeconds:   int64(maxStaleness.Seconds()),
		ResourceVersion:       cr.ResourceVersion,
	}
	if err := achdb.UpsertArtifact(ctx, r.DB, row); err != nil {
		return fmt.Errorf("db upsert artifact projection: %w", err)
	}
	return nil
}

// softDeleteArtifactProjection wraps the nil-DB gate + the
// achdb.SoftDeleteArtifact call. Caller is the deletion path between
// the external_refs DELETE and RemoveFinalizer.
func (r *ArtifactReconciler) softDeleteArtifactProjection(
	ctx context.Context,
	cr *achv1alpha1.Artifact,
) error {
	if r.DB == nil {
		return nil
	}
	if err := achdb.SoftDeleteArtifact(ctx, r.DB, cr.Namespace, cr.Name); err != nil {
		return fmt.Errorf("db soft-delete artifact projection: %w", err)
	}
	return nil
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *ArtifactReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Artifact{}, builder.WithPredicates()).
		Named("ach-artifact").
		Complete(r)
}
