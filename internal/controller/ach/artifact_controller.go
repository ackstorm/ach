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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	achmetrics "github.com/ackstorm/ach/internal/metrics"
)

// artifactsChannel is the NOTIFY channel emitted on every Artifact
// projection write/soft-delete (issue #34). The external_refs upsert for
// artifacts also fires on this channel.
const artifactsChannel = "ach_artifacts_changed"

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

	// Issue #34 (A10/A11): see PluginReconciler.ResyncSource.
	ResyncSource chan event.GenericEvent

	// Metrics is the operator collector set (G7). Nil-tolerant.
	Metrics *achmetrics.OperatorCollectors
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=artifacts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=artifacts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=artifacts/finalizers,verbs=update

// Reconcile mirrors PluginReconciler.Reconcile with kind="artifact",
// no size cap, and spec.Scope feeding computeFinalPath.
func (r *ArtifactReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileExternalRefCR[achv1alpha1.Artifact](ctx, req, externalRefDriverConfig[achv1alpha1.Artifact, *achv1alpha1.Artifact]{
		client:    r.Client,
		db:        r.DB,
		cacheRoot: r.CacheRoot,
		namespace: r.Namespace,
		fetchers:  r.Fetchers,
		metrics:   r.Metrics,
		kind:      "artifact",
		finalizer: artifactFinalizer,
		specView: func(cr *achv1alpha1.Artifact) externalRefSpecView {
			s := cr.Spec
			return externalRefSpecView{
				SourceSpec:    buildSourceSpec(s.Type, s.GitHub, s.GitLab, s.Bitbucket, s.S3, s.GCS, s.HTTP),
				AuthSecretRef: extractAuthSecretRef(s.Type, s.GitHub, s.GitLab, s.Bitbucket, s.S3, s.GCS, s.HTTP),
				Refresh:       s.Refresh,
				Scope:         cr.Spec.Scope,
				SizeCapBytes:  0,
			}
		},
		handleDeletion:  r.reconcileDeletion,
		writeProjection: r.writeArtifactProjection,
	})
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
	// Issue #34: project + NOTIFY atomically on ach_artifacts_changed.
	// ErrOriginConflict surfaces raw so the caller flips to
	// Synced=False/ConflictWithUIRow.
	payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
	if err := achdb.WithTxNotify(ctx, r.DB, artifactsChannel, payload, func(tx pgx.Tx) error {
		return achdb.UpsertArtifactTx(ctx, tx, row)
	}); err != nil {
		if errors.Is(err, achdb.ErrOriginConflict) {
			return err
		}
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
	payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
	if err := achdb.WithTxNotify(ctx, r.DB, artifactsChannel, payload, func(tx pgx.Tx) error {
		return achdb.SoftDeleteArtifactTx(ctx, tx, cr.Namespace, cr.Name)
	}); err != nil {
		return fmt.Errorf("db soft-delete artifact projection: %w", err)
	}
	return nil
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *ArtifactReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Artifact{}, builder.WithPredicates()).
		Named("ach-artifact")
	if r.ResyncSource != nil {
		b = b.WatchesRawSource(
			source.Channel(r.ResyncSource, &handler.EnqueueRequestForObject{}),
		)
	}
	return b.Complete(r)
}
