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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

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
// DESIGN DECISION (TODO.md §6, feedback_bip_no_shadow_logic.md, 2026-05-26;
// revised G15 2026-06-16): the Operator does NOT resolve duplicates — runtime
// stays forwarder-resolved. The Forwarder (internal/forwarder/bipcache) picks
// the alphabetically-FIRST metadata.name as the winner at READ time; operators
// flip precedence by renaming CRs (e.g. an "aaa-" prefix). The Operator now
// emits one ADVISORY status on the loser(s): when ≥2 live BIPs name the same
// (spec.target.kind, spec.target.name), every CR that is not the alpha-FIRST
// winner gets Synced=False/NameConflict("shadowed by BackendIdentityPolicy/
// <winner>"). This is informational only — it never changes which row the
// forwarder mints from.
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

	// Issue #34 (A10/A11): see PluginReconciler.ResyncSource.
	ResyncSource chan event.GenericEvent
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies/finalizers,verbs=update

// Reconcile implements the BackendIdentityPolicy lifecycle: finalizer
// add/remove + projection write/soft-delete via the issue-#34 NOTIFY
// helper. Duplicates coexist and the Forwarder resolves alphabetically-
// FIRST at read time; the operator additionally writes one advisory
// Synced=False/NameConflict on the shadowed loser(s) (G15, see the type
// doc). The other Synced=False reason that can land here is
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

	// Duplicate-target advisory (G15): when ≥2 live BIPs name the same
	// (target.kind, target.name), the alpha-FIRST metadata.name wins the
	// forwarder tiebreak; the rest are advisory-flagged
	// Synced=False/NameConflict. Runtime stays forwarder-resolved — this
	// status is informational only and never changes the mint row.
	winnerName, err := r.duplicateWinner(ctx, &cr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("list BIPs for duplicate-target advisory: %w", err)
	}
	if winnerName != "" && winnerName != cr.Name {
		// Idempotent: only write when the advisory condition is not already
		// in its terminal shape. Without this guard the empty-predicate For()
		// watch + enqueueSiblings would hot-loop on every status round-trip.
		msg := "shadowed by BackendIdentityPolicy/" + winnerName
		cond := apimeta.FindStatusCondition(cr.Status.Conditions, "Synced")
		alreadySet := cond != nil &&
			cond.Status == metav1.ConditionFalse &&
			cond.Reason == ReasonNameConflict &&
			cond.Message == msg &&
			cr.Status.ObservedGeneration == cr.Generation
		if !alreadySet {
			if werr := r.writeNameConflictStatus(ctx, &cr, winnerName); werr != nil {
				logger.Error(werr, "status update failed", "reason", ReasonNameConflict)
			}
		}
		return ctrl.Result{}, nil
	}
	// Winner or singleton: clear any stale NameConflict left from a prior
	// reconcile where this CR lost the tiebreak (e.g. the previous winner
	// was deleted). Leave a ConflictWithUIRow Synced condition untouched.
	if cond := apimeta.FindStatusCondition(cr.Status.Conditions, "Synced"); cond != nil && cond.Reason == ReasonNameConflict {
		apimeta.RemoveStatusCondition(&cr.Status.Conditions, "Synced")
		cr.Status.ObservedGeneration = cr.Generation
		desiredStatus := cr.Status
		if err := retryStatusUpdate(ctx, r.Client, &cr, func(fresh *achv1alpha1.BackendIdentityPolicy) {
			fresh.Status = desiredStatus
		}); err != nil {
			logger.Error(err, "status update failed", "reason", "clear-NameConflict")
		}
		return ctrl.Result{}, nil
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
	setConflictWithUIRowCondition(&cr.Status.Conditions, "Synced", cr.Generation)
	cr.Status.ObservedGeneration = cr.Generation
	desiredStatus := cr.Status
	return retryStatusUpdate(ctx, r.Client, cr, func(fresh *achv1alpha1.BackendIdentityPolicy) {
		fresh.Status = desiredStatus
	})
}

// duplicateWinner returns the alpha-FIRST metadata.name among all live BIPs
// sharing this CR's (target.kind, target.name) when the group has >1 member,
// else "". Mirrors bipcache.Resolve's tiebreak (rows[0] in name-ASC order is
// the winner) but over the CR set rather than the projection rows — the
// reconciler is Pool-nil under envtest, and the CRs are the source the
// projection mirrors anyway. Soft-deleting CRs are excluded.
func (r *BackendIdentityPolicyReconciler) duplicateWinner(
	ctx context.Context,
	cr *achv1alpha1.BackendIdentityPolicy,
) (string, error) {
	var list achv1alpha1.BackendIdentityPolicyList
	if err := r.List(ctx, &list, client.InNamespace(cr.Namespace)); err != nil {
		return "", err
	}
	winner := ""
	count := 0
	for i := range list.Items {
		s := &list.Items[i]
		if !s.DeletionTimestamp.IsZero() {
			continue
		}
		if s.Spec.Target.Kind != cr.Spec.Target.Kind || s.Spec.Target.Name != cr.Spec.Target.Name {
			continue
		}
		count++
		if winner == "" || s.Name < winner {
			winner = s.Name
		}
	}
	if count <= 1 {
		return "", nil
	}
	return winner, nil
}

// writeNameConflictStatus emits the advisory Synced=False/NameConflict
// condition on a shadowed (non-winner) BIP. See the type doc (G15).
func (r *BackendIdentityPolicyReconciler) writeNameConflictStatus(
	ctx context.Context,
	cr *achv1alpha1.BackendIdentityPolicy,
	winnerName string,
) error {
	setExternalRefCondition(
		&cr.Status.Conditions, "Synced", metav1.ConditionFalse,
		ReasonNameConflict, "shadowed by BackendIdentityPolicy/"+winnerName, cr.Generation,
	)
	cr.Status.ObservedGeneration = cr.Generation
	desiredStatus := cr.Status
	return retryStatusUpdate(ctx, r.Client, cr, func(fresh *achv1alpha1.BackendIdentityPolicy) {
		fresh.Status = desiredStatus
	})
}

// enqueueSiblings maps a changed BIP to every other live BIP sharing its
// (target.kind, target.name) so a pre-existing loser/winner recomputes its
// advisory NameConflict when a sibling is created or deleted (the primary
// For() watch only enqueues the changed object itself).
func (r *BackendIdentityPolicyReconciler) enqueueSiblings(ctx context.Context, obj client.Object) []reconcile.Request {
	changed, ok := obj.(*achv1alpha1.BackendIdentityPolicy)
	if !ok {
		return nil
	}
	var list achv1alpha1.BackendIdentityPolicyList
	if err := r.List(ctx, &list, client.InNamespace(changed.Namespace)); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		s := &list.Items[i]
		if s.Name == changed.Name {
			continue
		}
		if s.Spec.Target.Kind == changed.Spec.Target.Kind && s.Spec.Target.Name == changed.Spec.Target.Name {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(s)})
		}
	}
	return reqs
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *BackendIdentityPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.BackendIdentityPolicy{}, builder.WithPredicates()).
		Named("ach-backendidentitypolicy").
		Watches(
			&achv1alpha1.BackendIdentityPolicy{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueSiblings),
		)
	if r.ResyncSource != nil {
		b = b.WatchesRawSource(
			source.Channel(r.ResyncSource, &handler.EnqueueRequestForObject{}),
		)
	}
	return b.Complete(r)
}
