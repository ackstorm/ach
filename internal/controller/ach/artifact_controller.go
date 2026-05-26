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
		if controllerutil.ContainsFinalizer(&cr, artifactFinalizer) {
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
			controllerutil.RemoveFinalizer(&cr, artifactFinalizer)
			if err := r.Update(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("§10.3 cleanup complete; finalizer removed", "name", cr.Name)
		}
		return ctrl.Result{}, nil
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
		setExternalRefCondition(&cr.Status.Conditions, "SourceReachable", metav1.ConditionFalse, reason, message, cr.Generation)
		cr.Status.ObservedGeneration = cr.Generation
		if statusErr := r.Status().Update(ctx, &cr); statusErr != nil {
			logger.Error(statusErr, "status update failed", "reason", reason)
		}
		switch reason {
		case ReasonPluginTooLarge, ReasonUnauthorized, ReasonNotFound, ReasonUpstreamInvalid:
			return ctrl.Result{RequeueAfter: requeue}, nil
		default:
			return ctrl.Result{}, result.Err
		}
	}

	setExternalRefCondition(&cr.Status.Conditions, "SourceReachable", metav1.ConditionTrue, ReasonSynced, "", cr.Generation)
	cr.Status.UpstreamRev = result.UpstreamRev
	if result.NotModified && cr.Status.StorageLocation == "" {
		cr.Status.StorageLocation = finalPath
	} else if !result.NotModified {
		cr.Status.StorageLocation = finalPath
	}
	now := metav1.Now()
	cr.Status.LastSuccessfulRefresh = &now
	cr.Status.ObservedGeneration = cr.Generation
	if err := r.Status().Update(ctx, &cr); err != nil {
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

// SetupWithManager registers the reconciler with controller-runtime.
func (r *ArtifactReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Artifact{}, builder.WithPredicates()).
		Named("ach-artifact").
		Complete(r)
}
