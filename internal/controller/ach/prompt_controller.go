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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	achmetrics "github.com/ackstorm/ach/internal/metrics"
)

// promptsChannel is the NOTIFY channel emitted on every Prompt projection
// write/soft-delete (issue #34). The external_refs upsert for prompts
// fires on this same channel.
const promptsChannel = "ach_prompts_changed"

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

	// Issue #34 (A10/A11): see PluginReconciler.ResyncSource.
	ResyncSource chan event.GenericEvent

	// Metrics is the operator collector set (G7). Nil-tolerant.
	Metrics *achmetrics.OperatorCollectors
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=prompts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=prompts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=prompts/finalizers,verbs=update

// Reconcile mirrors PluginReconciler.Reconcile with kind="prompt" and
// no size cap. Cache path is prompt/<name> (raw bytes, no .tar.gz suffix).
func (r *PromptReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileExternalRefCR[achv1alpha1.Prompt](ctx, req, externalRefDriverConfig[achv1alpha1.Prompt, *achv1alpha1.Prompt]{
		client:    r.Client,
		db:        r.DB,
		cacheRoot: r.CacheRoot,
		namespace: r.Namespace,
		fetchers:  r.Fetchers,
		metrics:   r.Metrics,
		kind:      "prompt",
		finalizer: promptFinalizer,
		specView: func(cr *achv1alpha1.Prompt) externalRefSpecView {
			s := cr.Spec
			return externalRefSpecView{
				SourceSpec:    buildSourceSpec(s.Type, s.GitHub, s.GitLab, s.Bitbucket, s.S3, s.GCS, s.HTTP),
				AuthSecretRef: extractAuthSecretRef(s.Type, s.GitHub, s.GitLab, s.Bitbucket, s.S3, s.GCS, s.HTTP),
				Refresh:       s.Refresh,
				Scope:         "",
				SizeCapBytes:  0,
			}
		},
		handleDeletion:  r.handleDeletion,
		writeProjection: r.writePromptProjection,
	})
}

// handleDeletion runs the §10.3 cleanup + finalizer-removal sequence
// extracted from Reconcile so the latter stays under the gocyclo
// threshold after Phase 5 (projection writes) + within-interval gate
// additions stacked into the steady-state branch.
func (r *PromptReconciler) handleDeletion(ctx context.Context, cr *achv1alpha1.Prompt, logger logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(cr, promptFinalizer) {
		return ctrl.Result{}, nil
	}
	// §10.3 cache layout: prompt/<name>.tar.gz (uniform context format —
	// single upstream file wrapped into a 1-entry gzip-tar at ingestion).
	if err := os.Remove(filepath.Join(r.CacheRoot, "prompt", cr.Name+".tar.gz")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ctrl.Result{}, err
	}
	// Legacy pre-uniform bare file (best-effort cleanup; tolerate absence).
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
		// Issue #34: soft-delete + NOTIFY in one tx.
		payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
		if err := achdb.WithTxNotify(ctx, r.DB, promptsChannel, payload, func(tx pgx.Tx) error {
			return achdb.SoftDeletePromptTx(ctx, tx, cr.Namespace, cr.Name)
		}); err != nil {
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

// writePromptProjection projects the prompts row + NOTIFY atomically.
// ContentType is optional (CS-06 falls back to application/octet-stream
// when NULL); only populated when spec.contentType is set. Returns
// achdb.ErrOriginConflict raw on UI-row conflict.
func (r *PromptReconciler) writePromptProjection(
	ctx context.Context,
	cr *achv1alpha1.Prompt,
	lastRefresh time.Time,
	maxStaleness time.Duration,
) error {
	if r.DB == nil {
		return nil
	}
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
		MaxStalenessSeconds:   int64(maxStaleness.Seconds()),
		ResourceVersion:       cr.ResourceVersion,
	}
	payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
	if err := achdb.WithTxNotify(ctx, r.DB, promptsChannel, payload, func(tx pgx.Tx) error {
		return achdb.UpsertPromptTx(ctx, tx, row)
	}); err != nil {
		if errors.Is(err, achdb.ErrOriginConflict) {
			return err
		}
		return fmt.Errorf("db upsert prompt projection: %w", err)
	}
	return nil
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *PromptReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Prompt{}).
		Named("ach-prompt")
	if r.ResyncSource != nil {
		b = b.WatchesRawSource(
			source.Channel(r.ResyncSource, &handler.EnqueueRequestForObject{}),
		)
	}
	return b.Complete(r)
}
