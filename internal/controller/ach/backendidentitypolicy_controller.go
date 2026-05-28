// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"fmt"
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

// backendIdentityPoliciesChannel is the NOTIFY channel fired on every BIP
// projection write/soft-delete (issue #34). Forwarder bipcache subscribes
// to this channel to keep its read-side index converged.
const backendIdentityPoliciesChannel = "ach_backend_identity_policies_changed"

// BackendIdentityPolicyReconciler reconciles a BackendIdentityPolicy
// object per CRD-06 (finalizer add/remove) and the issue-#34 projection
// extension (write the row to backend_identity_policies + emit NOTIFY).
//
// DESIGN DECISION (TODO.md §6, feedback_bip_no_shadow_logic.md, 2026-05-26):
// the Operator stays dumb on BIP duplicates. No Synced=DuplicateTarget
// reason is ever emitted; no shadow flip; no Operator-side resolution of
// (spec.target.kind, spec.target.name) duplicates. The Forwarder resolves
// duplicates at READ time by selecting the alphabetically-LAST
// metadata.name as the winner. Operators flip precedence by renaming
// CRs (e.g. a "zz-" prefix). See internal/forwarder/bip/index.go
// for the read-side resolver.
//
// CacheRoot is intentionally absent from the struct: this reconciler
// has no filesystem cleanup body.
//
// Pool is nil-tolerant: existing envtests that don't need projection
// leave it unset, and the upsert/soft-delete branches are skipped.
type BackendIdentityPolicyReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
	// Pool projects every CR mutation into the backend_identity_policies
	// table inside a single transaction with the
	// ach_backend_identity_policies_changed NOTIFY (issue #34). Nil-tolerant.
	Pool *pgxpool.Pool
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies/finalizers,verbs=update

// Reconcile implements the BackendIdentityPolicy lifecycle: finalizer
// add/remove + projection write/soft-delete via the issue-#34 NOTIFY
// helper. Per TODO.md §6 + OP-16 the Operator emits NO Synced=Duplicate*
// reason; duplicates coexist and the Forwarder resolves alphabetically-
// LAST at read time. The only Synced=False reason that can land here is
// ConflictWithUIRow (a UI-owned row already holds the PK), and it is
// requeued after a minute so the operator does not hot-loop.
func (r *BackendIdentityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("backendidentitypolicy", req.NamespacedName)

	var cr achv1alpha1.BackendIdentityPolicy
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !cr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&cr, backendIdentityPolicyFinalizer) {
			// Issue #34: soft-delete the projection row inside the same
			// transaction as the NOTIFY so any forwarder waking up on
			// ach_backend_identity_policies_changed SELECTs a snapshot
			// that already reflects the soft-delete. Skipped when
			// r.Pool is nil (existing envtests).
			if r.Pool != nil {
				ns, name := cr.Namespace, cr.Name
				payload := fmt.Sprintf("%s/%s", ns, name)
				if err := achdb.WithTxNotify(ctx, r.Pool, backendIdentityPoliciesChannel, payload, func(tx pgx.Tx) error {
					return achdb.SoftDeleteBIPTx(ctx, tx, ns, name)
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("db soft-delete backend_identity_policies projection: %w", err)
				}
			}
			controllerutil.RemoveFinalizer(&cr, backendIdentityPolicyFinalizer)
			if err := r.Update(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("finalizer removed; projection soft-deleted")
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&cr, backendIdentityPolicyFinalizer) {
		controllerutil.AddFinalizer(&cr, backendIdentityPolicyFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Issue #34: project the CR spec to the backend_identity_policies
	// table inside a single tx with the ach_backend_identity_policies_changed
	// NOTIFY so the forwarder bipcache can converge from Postgres alone.
	// ErrOriginConflict (a UI row holds the same PK) flips
	// Synced=False/ConflictWithUIRow and requeues in 1 minute. r.Pool is
	// nil-tolerant for existing envtests that exercise only the finalizer
	// surface.
	if r.Pool != nil {
		row := achdb.BIPRow{
			Namespace:          cr.Namespace,
			Name:               cr.Name,
			TargetKind:         cr.Spec.Target.Kind,
			TargetName:         cr.Spec.Target.Name,
			ForwardIdentityJWT: cr.Spec.ForwardIdentityJWT,
			ObservedGeneration: cr.Generation,
			ResourceVersion:    cr.ResourceVersion,
		}
		payload := fmt.Sprintf("%s/%s", cr.Namespace, cr.Name)
		err := achdb.WithTxNotify(ctx, r.Pool, backendIdentityPoliciesChannel, payload, func(tx pgx.Tx) error {
			return achdb.UpsertBIPTx(ctx, tx, row)
		})
		if err != nil {
			if errors.Is(err, achdb.ErrOriginConflict) {
				if werr := r.writeConflictStatus(ctx, &cr); werr != nil {
					logger.Error(werr, "status update failed", "reason", "ConflictWithUIRow")
				}
				return ctrl.Result{RequeueAfter: time.Minute}, nil
			}
			return ctrl.Result{}, fmt.Errorf("db upsert backend_identity_policies projection: %w", err)
		}
	}

	// Steady state — no positive Synced condition is emitted (TODO.md §6 +
	// OP-16 keep the closed condition set minimal). The bump of
	// ObservedGeneration tells operators the reconciler has seen the
	// current spec; the absence of conditions is itself the "happy path"
	// signal.
	if cr.Status.ObservedGeneration != cr.Generation {
		cr.Status.ObservedGeneration = cr.Generation
		desiredStatus := cr.Status
		if err := retryStatusUpdate(ctx, r.Client, &cr, func(fresh *achv1alpha1.BackendIdentityPolicy) {
			fresh.Status = desiredStatus
		}); err != nil {
			logger.Error(err, "status update failed")
		}
	}

	return ctrl.Result{}, nil
}

// writeConflictStatus emits the only Synced=False reason this reconciler
// ever writes — ConflictWithUIRow — when a UI-origin row holds the same
// (namespace, name) PK. The forwarder ignores the operator's CR until
// the conflict is resolved (UI-row drops its lock, or operator-side CR
// is renamed).
func (r *BackendIdentityPolicyReconciler) writeConflictStatus(
	ctx context.Context,
	cr *achv1alpha1.BackendIdentityPolicy,
) error {
	apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               "Synced",
		Status:             metav1.ConditionFalse,
		Reason:             "ConflictWithUIRow",
		Message:            "projection row owned by UI; operator declines to overwrite",
		ObservedGeneration: cr.Generation,
		LastTransitionTime: metav1.Now(),
	})
	cr.Status.ObservedGeneration = cr.Generation
	desiredStatus := cr.Status
	return retryStatusUpdate(ctx, r.Client, cr, func(fresh *achv1alpha1.BackendIdentityPolicy) {
		fresh.Status = desiredStatus
	})
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *BackendIdentityPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.BackendIdentityPolicy{}, builder.WithPredicates()).
		Named("ach-backendidentitypolicy").
		Complete(r)
}
