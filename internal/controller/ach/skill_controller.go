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
)

// skillsChannel is the NOTIFY channel emitted on every Skill projection
// write/soft-delete. The external_refs upsert (the fetcher-state side of the
// same logical resource) also fires on this channel under "skill/<name>".
const skillsChannel = "ach_skills_changed"

// SkillReconciler reconciles a Skill object via the shared external-ref
// driver (fetch → validate SKILL.md → stage → fsync → rename(2) → DB UPSERT).
// A Skill mirrors Plugin: a directory tar.gz stored as skill/<name>.tar.gz,
// but no pluginpack content filter is applied (the SKILL.md validation gate in
// materializeExternalRef is the only skill-specific logic).
//
// CacheRoot is the PVC mount root from ACH_CACHE_ROOT (default
// /var/cache/ach). DB nil-tolerance preserved for the envtest finalizer path.
type SkillReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
	CacheRoot string

	// DB is the Postgres pool used for external_refs UPSERT/GET/DELETE.
	// Nil in envtest (finalizer test); steady-state branch skips DB
	// reads/writes when nil so the existing test stays green.
	DB *pgxpool.Pool

	// SkillMaxSizeMiB is the per-skill size cap. When 0, the cap is treated
	// as "infinite" — materializeExternalRef receives SizeCapBytes = 0 and
	// skips the LimitReader wrap.
	SkillMaxSizeMiB int

	// Fetchers is the FetcherFactory; nil → defaults to registry.For.
	Fetchers FetcherFactory

	// ResyncSource is the external source.Channel feed used by the resync
	// runnable (periodic full re-list) and the refreshsignal listener.
	ResyncSource chan event.GenericEvent
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=skills,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=skills/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=skills/finalizers,verbs=update

// Reconcile implements the Skill lifecycle:
//
//   - Fetch + NotFound → no-op.
//   - Deletion path: remove <CacheRoot>/skill/<name>.tar.gz (IsNotExist
//     tolerated) → DeleteExternalRef → soft-delete projection → RemoveFinalizer.
//   - Finalizer-add path: AddFinalizer + Update.
//   - Steady-state: materializeExternalRef → status + RequeueAfter.
func (r *SkillReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileExternalRefCR[achv1alpha1.Skill](ctx, req, externalRefDriverConfig[achv1alpha1.Skill, *achv1alpha1.Skill]{
		client:    r.Client,
		db:        r.DB,
		cacheRoot: r.CacheRoot,
		namespace: r.Namespace,
		fetchers:  r.Fetchers,
		kind:      "skill",
		finalizer: skillFinalizer,
		specView: func(cr *achv1alpha1.Skill) externalRefSpecView {
			s := cr.Spec
			return externalRefSpecView{
				SourceSpec:    buildSourceSpec(s.Type, s.GitHub, s.GitLab, s.Bitbucket, s.S3, s.GCS, s.HTTP),
				AuthSecretRef: extractAuthSecretRef(s.Type, s.GitHub, s.GitLab, s.Bitbucket, s.S3, s.GCS, s.HTTP),
				Refresh:       s.Refresh,
				Scope:         "",
				SizeCapBytes:  int64(r.SkillMaxSizeMiB) << 20,
			}
		},
		handleDeletion:  r.reconcileDeletion,
		writeProjection: r.writeSkillProjection,
	})
}

// reconcileDeletion is the finalizer drain: remove the cached file, drop the
// external_refs row, soft-delete the skills projection row (in one tx with
// NOTIFY ach_skills_changed), then remove the finalizer. Nil DB paths skip the
// DB writes for envtest mode.
func (r *SkillReconciler) reconcileDeletion(ctx context.Context, cr *achv1alpha1.Skill, logger logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(cr, skillFinalizer) {
		return ctrl.Result{}, nil
	}
	if err := os.Remove(filepath.Join(r.CacheRoot, "skill", cr.Name+".tar.gz")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ctrl.Result{}, err
	}
	if r.DB != nil {
		if err := achdb.DeleteExternalRef(ctx, r.DB, "skill", cr.Name); err != nil {
			return ctrl.Result{}, fmt.Errorf("db delete external_ref: %w", err)
		}
		payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
		if err := achdb.WithTxNotify(ctx, r.DB, skillsChannel, payload, func(tx pgx.Tx) error {
			return achdb.SoftDeleteSkillTx(ctx, tx, cr.Namespace, cr.Name)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("db soft-delete skill projection: %w", err)
		}
	}
	controllerutil.RemoveFinalizer(cr, skillFinalizer)
	if err := r.Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("skill cleanup complete; finalizer removed", "name", cr.Name)
	return ctrl.Result{}, nil
}

// writeSkillProjection projects the skills row + NOTIFY atomically. Returns
// achdb.ErrOriginConflict raw so the caller can flip to
// Synced=False/ConflictWithUIRow.
func (r *SkillReconciler) writeSkillProjection(
	ctx context.Context,
	cr *achv1alpha1.Skill,
	lastRefresh time.Time,
	maxStaleness time.Duration,
) error {
	if r.DB == nil {
		return nil
	}
	row := achdb.SkillRow{
		Namespace:             cr.Namespace,
		Name:                  cr.Name,
		StorageLocation:       cr.Status.StorageLocation,
		LastSuccessfulRefresh: &lastRefresh,
		MaxStalenessSeconds:   int64(maxStaleness.Seconds()),
		ResourceVersion:       cr.ResourceVersion,
	}
	payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
	if err := achdb.WithTxNotify(ctx, r.DB, skillsChannel, payload, func(tx pgx.Tx) error {
		return achdb.UpsertSkillTx(ctx, tx, row)
	}); err != nil {
		if errors.Is(err, achdb.ErrOriginConflict) {
			return err
		}
		return fmt.Errorf("db upsert skill projection: %w", err)
	}
	return nil
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *SkillReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Skill{}, builder.WithPredicates()).
		Named("ach-skill")
	if r.ResyncSource != nil {
		b = b.WatchesRawSource(
			source.Channel(r.ResyncSource, &handler.EnqueueRequestForObject{}),
		)
	}
	return b.Complete(r)
}
