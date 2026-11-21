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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
)

// instructionsChannel is the NOTIFY channel emitted on every Instruction projection
// write/delete (issue #34). The external_refs upsert for instructions
// fires on this same channel.
const instructionsChannel = "ach_instructions_changed"

// InstructionReconciler reconciles an Instruction object. Phase 2 implements the
// §10.3 steady-state refresh via materializeExternalRef. Instructions have
// NO size cap (Hub §13/§14) — the helper's SizeCapBytes is left at 0.
//
// CacheRoot mirrors PluginReconciler. Plan 02-09 injects DB and Fetchers
// from cmd/operator/main.go at SetupWithManager time.
type InstructionReconciler struct {
	FetchedObjectReconciler
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=instructions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=instructions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=instructions/finalizers,verbs=update

// Reconcile mirrors PluginReconciler.Reconcile with kind="instruction" and
// no size cap. Cache path is instruction/<name>.tar.gz.
func (r *InstructionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileExternalRefCR[achv1alpha1.Instruction](ctx, req, externalRefDriverConfig[achv1alpha1.Instruction, *achv1alpha1.Instruction]{
		client:    r.Client,
		db:        r.DB,
		cacheRoot: r.CacheRoot,
		namespace: r.Namespace,
		fetchers:  r.Fetchers,
		metrics:   r.Metrics,
		kind:      "instruction",
		finalizer: instructionFinalizer,
		specView: func(cr *achv1alpha1.Instruction) externalRefSpecView {
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
		writeProjection: r.writeInstructionProjection,
	})
}

// handleDeletion runs the §10.3 cleanup + finalizer-removal sequence
// extracted from Reconcile so the latter stays under the gocyclo
// threshold after Phase 5 (projection writes) + within-interval gate
// additions stacked into the steady-state branch.
func (r *InstructionReconciler) handleDeletion(ctx context.Context, cr *achv1alpha1.Instruction, logger logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(cr, instructionFinalizer) {
		return ctrl.Result{}, nil
	}
	// §10.3 cache layout: instruction/<name>.tar.gz (uniform context format —
	// single upstream file wrapped into a 1-entry gzip-tar at ingestion).
	if err := os.Remove(filepath.Join(r.CacheRoot, "instruction", cr.Name+".tar.gz")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ctrl.Result{}, err
	}
	if r.DB != nil {
		if err := achdb.DeleteExternalRef(ctx, r.DB, "instruction", cr.Name); err != nil {
			return ctrl.Result{}, fmt.Errorf("db delete external_ref: %w", err)
		}
		// Issue #34: delete + NOTIFY in one tx.
		payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
		if err := achdb.WithTxNotify(ctx, r.DB, instructionsChannel, payload, func(tx pgx.Tx) error {
			return achdb.DeleteInstructionTx(ctx, tx, cr.Namespace, cr.Name)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("db delete instruction projection: %w", err)
		}
	}
	controllerutil.RemoveFinalizer(cr, instructionFinalizer)
	if err := r.Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("§10.3 cleanup complete; finalizer removed", "name", cr.Name)
	return ctrl.Result{}, nil
}

// writeInstructionProjection projects the instructions row + NOTIFY atomically.
// Returns achdb.ErrOriginConflict raw on UI-row conflict.
func (r *InstructionReconciler) writeInstructionProjection(
	ctx context.Context,
	cr *achv1alpha1.Instruction,
	lastRefresh time.Time,
	maxStaleness time.Duration,
) error {
	row := achdb.InstructionRow{
		Namespace:             cr.Namespace,
		Name:                  cr.Name,
		StorageLocation:       cr.Status.StorageLocation,
		LastSuccessfulRefresh: &lastRefresh,
		MaxStalenessSeconds:   int64(maxStaleness.Seconds()),
		ResourceVersion:       cr.ResourceVersion,
	}
	return projectRow(ctx, r.DB, instructionsChannel, cr.Namespace, cr.Name, "instruction", func(tx pgx.Tx) error {
		return achdb.UpsertInstructionTx(ctx, tx, row)
	})
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *InstructionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return setupWithResync(mgr, r, &achv1alpha1.Instruction{}, "ach-instruction", r.ResyncSource)
}
