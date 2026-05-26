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
// object. Phase 1 scope is the CRD-06 finalizer add/remove only —
// BackendIdentityPolicy has no PVC-cached form (no Source*, no
// upstream content; the resource is consumed at runtime by the
// Forwarder from the informer cache, not via a streamed file). The
// finalizer exists for consistency with the other six kinds and so
// Phase 4 can layer real Synced=DuplicateTarget reconciliation on
// top without a CRD migration.
//
// CacheRoot is intentionally absent from the struct: this reconciler
// has no filesystem cleanup body. Plan 06's main.go will inject the
// other fields only.
type BackendIdentityPolicyReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=backendidentitypolicies/finalizers,verbs=update

// Reconcile implements the Phase 1 BackendIdentityPolicy lifecycle —
// finalizer add/remove only, no file cleanup. No status write either:
// the §6.6 BackendIdentityPolicy-specific Synced=DuplicateTarget
// reason is a Phase 4 reconciliation outcome (OP-14/OP-16); writing a
// stub reason here would conflict with the CRD-07 closed set.
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
			// No PVC file to clean. Phase 4 may need to invalidate a
			// Forwarder cache entry here; Phase 1 just removes the
			// finalizer so K8s deletion can complete.
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

	// Steady state — no status write in Phase 1 (Synced=DuplicateTarget
	// is Phase 4's owner; CRD-07 doesn't admit an "Initializing" reason
	// for this kind).
	return ctrl.Result{}, nil
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *BackendIdentityPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.BackendIdentityPolicy{}, builder.WithPredicates()).
		Named("ach-backendidentitypolicy").
		Complete(r)
}
