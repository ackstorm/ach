// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// BackendIdentityPolicyReconciler reconciles a BackendIdentityPolicy
// object. Scope is the CRD-06 finalizer add/remove only —
// BackendIdentityPolicy has no PVC-cached form (no Source*, no
// upstream content; the resource is consumed at runtime by the
// Forwarder from the informer cache, not via a streamed file). The
// finalizer exists for consistency with the other six kinds.
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
type BackendIdentityPolicyReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies/finalizers,verbs=update

// Reconcile implements the BackendIdentityPolicy lifecycle — finalizer
// add/remove only, no file cleanup, no status write. Per TODO.md §6 +
// OP-16, the Operator emits NO Synced reason for this kind: duplicates
// coexist and the Forwarder (internal/forwarder/bip) resolves
// alphabetically-LAST at read time. CRD-07's closed condition set for
// BIP is intentionally minimal — no "Initializing" reason, no
// "DuplicateTarget" reason, no churn.
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
			// No PVC file to clean. The Forwarder reads BIP spec via
			// informer cache (no Forwarder-side cache to invalidate;
			// controller-runtime handles eviction on delete).
			controllerutil.RemoveFinalizer(&cr, backendIdentityPolicyFinalizer)
			if err := r.Update(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("finalizer removed; no cached file to clean")
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

	// Steady state — no status write. Operator emits no Synced reason
	// for BackendIdentityPolicy (TODO.md §6 + OP-16); the Forwarder
	// resolves duplicates at READ time.
	return ctrl.Result{}, nil
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *BackendIdentityPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.BackendIdentityPolicy{}, builder.WithPredicates()).
		Named("ach-backendidentitypolicy").
		Complete(r)
}
