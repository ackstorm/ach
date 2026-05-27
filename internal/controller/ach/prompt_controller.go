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

// PromptReconciler reconciles a Prompt object. Phase 2 implements the
// §10.3 steady-state refresh via materializeExternalRef. Prompts have
// NO size cap (Hub §13/§14) — the helper's SizeCapBytes is left at 0.
//
// CacheRoot mirrors PluginReconciler. Plan 02-09 injects DB and Fetchers
// from cmd/operator/main.go at SetupWithManager time.
type PromptReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
	CacheRoot string

	// Phase 2:
	DB       *pgxpool.Pool
	Fetchers FetcherFactory
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=prompts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=prompts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=prompts/finalizers,verbs=update

// Reconcile mirrors PluginReconciler.Reconcile with kind="prompt" and
// no size cap. Cache path is prompt/<name> (raw bytes, no .tar.gz suffix).
func (r *PromptReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("prompt", req.NamespacedName)

	var cr achv1alpha1.Prompt
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// ─── Deletion path. ───
	if !cr.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &cr, logger)
	}

	// ─── Finalizer-add path. ───
	if !controllerutil.ContainsFinalizer(&cr, promptFinalizer) {
		controllerutil.AddFinalizer(&cr, promptFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// ─── Phase 2 steady state: §10.3 refresh. ───
	var priorRev string
	var lastRefresh time.Time
	if r.DB != nil {
		priorRow, err := achdb.GetExternalRef(ctx, r.DB, "prompt", cr.Name)
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
	finalPath := computeFinalPath(r.CacheRoot, "prompt", cr.Name, "")

	deps := ExternalRefRefreshDeps{
		Client:        r.Client,
		Namespace:     r.Namespace,
		DB:            r.DB,
		CacheRoot:     r.CacheRoot,
		Kind:          "prompt",
		Name:          cr.Name,
		SourceSpec:    sourceSpec,
		AuthSecretRef: authRef,
		Refresh:       spec.Refresh,
		PriorRev:      priorRev,
		SizeCapBytes:  0, // no cap per spec §13/§14
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

	setExternalRefCondition(&cr.Status.Conditions, "SourceReachable", metav1.ConditionTrue, ReasonSynced, sourceReachableMessage(sourceSpec), cr.Generation)
	cr.Status.UpstreamRev = result.UpstreamRev
	if result.NotModified && cr.Status.StorageLocation == "" {
		cr.Status.StorageLocation = finalPath
	} else if !result.NotModified {
		cr.Status.StorageLocation = finalPath
	}
	now := metav1.Now()
	cr.Status.LastSuccessfulRefresh = &now
	cr.Status.ObservedGeneration = cr.Generation

	// Spec v4 §5.2 / D-13 / D-15: dual-write the prompts projection row
	// BEFORE the best-effort K8s Status update. DB is authoritative —
	// Plan 05-05 CS pipeline reads content_type + max_staleness_seconds +
	// last_successful_refresh from this row. ContentType is optional
	// (CS-06 falls back to application/octet-stream when NULL); only
	// populate it when spec.contentType is set.
	if r.DB != nil {
		lastRefresh := now.Time
		var contentType *string
		if cr.Spec.ContentType != "" {
			ct := cr.Spec.ContentType
			contentType = &ct
		}
		row := achdb.PromptRow{
			Namespace:             cr.Namespace,
			Name:                  cr.Name,
			StorageLocation:       cr.Status.StorageLocation,
			ContentType:           contentType,
			LastSuccessfulRefresh: &lastRefresh,
			MaxStalenessSeconds:   int64(spec.Refresh.MaxStaleness.Duration.Seconds()),
			ResourceVersion:       cr.ResourceVersion,
		}
		if err := achdb.UpsertPrompt(ctx, r.DB, row); err != nil {
			return ctrl.Result{}, fmt.Errorf("db upsert prompt projection: %w", err)
		}
	}
	if err := r.Status().Update(ctx, &cr); err != nil {
		// WR-02: see plugin_controller.go for rationale — stale
		// ResourceVersion after a failed Status().Update would make
		// the subsequent r.Update on the annotation also 409. Skip
		// and let the next reconcile retry from a fresh Get.
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

// handleDeletion runs the §10.3 cleanup + finalizer-removal sequence
// extracted from Reconcile so the latter stays under the gocyclo
// threshold after Phase 5 (projection writes) + within-interval gate
// additions stacked into the steady-state branch.
func (r *PromptReconciler) handleDeletion(ctx context.Context, cr *achv1alpha1.Prompt, logger logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(cr, promptFinalizer) {
		return ctrl.Result{}, nil
	}
	// §10.3 cache layout: prompt/<name>
	if err := os.Remove(filepath.Join(r.CacheRoot, "prompt", cr.Name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ctrl.Result{}, err
	}
	if r.DB != nil {
		if err := achdb.DeleteExternalRef(ctx, r.DB, "prompt", cr.Name); err != nil {
			return ctrl.Result{}, fmt.Errorf("db delete external_ref: %w", err)
		}
		// Spec v4 §5.2 / CS-09 / D-15: soft-delete the prompts
		// projection row AFTER the existing external_refs DELETE
		// and BEFORE finalizer removal. Two writes are intentional —
		// see plugin_controller.go for rationale.
		if err := achdb.SoftDeletePrompt(ctx, r.DB, cr.Namespace, cr.Name); err != nil {
			return ctrl.Result{}, fmt.Errorf("db soft-delete prompt projection: %w", err)
		}
	}
	controllerutil.RemoveFinalizer(cr, promptFinalizer)
	if err := r.Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("§10.3 cleanup complete; finalizer removed", "name", cr.Name)
	return ctrl.Result{}, nil
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *PromptReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Prompt{}, builder.WithPredicates()).
		Named("ach-prompt").
		Complete(r)
}
