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

	// Issue #34 (A10/A11): external source.Channel feed used by the
	// resync runnable (periodic full re-list) and the refreshsignal
	// listener (NOTIFY ach_refresh).
	ResyncSource chan event.GenericEvent

	// Metrics is the operator collector set (G7). Nil-tolerant (envtest
	// leaves it unset); wired from cmd/ach/cmd/operator.go.
	Metrics *achmetrics.OperatorCollectors
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
	return reconcileExternalRefCR[achv1alpha1.Plugin](ctx, req, externalRefDriverConfig[achv1alpha1.Plugin, *achv1alpha1.Plugin]{
		client:    r.Client,
		db:        r.DB,
		cacheRoot: r.CacheRoot,
		namespace: r.Namespace,
		fetchers:  r.Fetchers,
		metrics:   r.Metrics,
		kind:      "plugin",
		finalizer: pluginFinalizer,
		specView: func(cr *achv1alpha1.Plugin) externalRefSpecView {
			s := cr.Spec
			return externalRefSpecView{
				SourceSpec:    buildSourceSpec(s.Type, s.GitHub, s.GitLab, s.Bitbucket, s.S3, s.GCS, s.HTTP),
				AuthSecretRef: extractAuthSecretRef(s.Type, s.GitHub, s.GitLab, s.Bitbucket, s.S3, s.GCS, s.HTTP),
				Refresh:       s.Refresh,
				Scope:         "",
				SizeCapBytes:  int64(r.PluginMaxSizeMiB) << 20,
			}
		},
		handleDeletion:  r.reconcileDeletion,
		writeProjection: r.writePluginProjection,
	})
}

// reconcileDeletion is the §10.3 finalizer drain extracted from Reconcile
// to keep cyclomatic complexity within the gocyclo budget. Removes the
// cached file, drops the external_refs row, soft-deletes the plugins
// projection row (in one tx with NOTIFY ach_plugins_changed), then removes
// the finalizer. Nil DB paths skip the DB writes for Phase 1 envtest mode.
func (r *PluginReconciler) reconcileDeletion(ctx context.Context, cr *achv1alpha1.Plugin, logger logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(cr, pluginFinalizer) {
		return ctrl.Result{}, nil
	}
	if err := os.Remove(filepath.Join(r.CacheRoot, "plugin", cr.Name+".tar.gz")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ctrl.Result{}, err
	}
	if r.DB != nil {
		if err := achdb.DeleteExternalRef(ctx, r.DB, "plugin", cr.Name); err != nil {
			return ctrl.Result{}, fmt.Errorf("db delete external_ref: %w", err)
		}
		payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
		if err := achdb.WithTxNotify(ctx, r.DB, pluginsChannel, payload, func(tx pgx.Tx) error {
			return achdb.SoftDeletePluginTx(ctx, tx, cr.Namespace, cr.Name)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("db soft-delete plugin projection: %w", err)
		}
	}
	controllerutil.RemoveFinalizer(cr, pluginFinalizer)
	if err := r.Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("§10.3 cleanup complete; finalizer removed", "name", cr.Name)
	return ctrl.Result{}, nil
}

// writePluginProjection projects the plugins row + NOTIFY atomically.
// Returns achdb.ErrOriginConflict raw so the caller can flip to
// Synced=False/ConflictWithUIRow.
func (r *PluginReconciler) writePluginProjection(
	ctx context.Context,
	cr *achv1alpha1.Plugin,
	lastRefresh time.Time,
	maxStaleness time.Duration,
) error {
	if r.DB == nil {
		return nil
	}
	row := achdb.PluginRow{
		Namespace:             cr.Namespace,
		Name:                  cr.Name,
		StorageLocation:       cr.Status.StorageLocation,
		LastSuccessfulRefresh: &lastRefresh,
		MaxStalenessSeconds:   int64(maxStaleness.Seconds()),
		ResourceVersion:       cr.ResourceVersion,
	}
	payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
	if err := achdb.WithTxNotify(ctx, r.DB, pluginsChannel, payload, func(tx pgx.Tx) error {
		return achdb.UpsertPluginTx(ctx, tx, row)
	}); err != nil {
		if errors.Is(err, achdb.ErrOriginConflict) {
			return err
		}
		return fmt.Errorf("db upsert plugin projection: %w", err)
	}
	return nil
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *PluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Plugin{}, builder.WithPredicates()).
		Named("ach-plugin")
	if r.ResyncSource != nil {
		b = b.WatchesRawSource(
			source.Channel(r.ResyncSource, &handler.EnqueueRequestForObject{}),
		)
	}
	return b.Complete(r)
}
