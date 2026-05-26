// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/connection"
	"github.com/ackstorm/ach/internal/litellm"
)

const litellmConnectionFinalizer = "litellmconnections.ach.ackstorm.ai/finalizer"

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=litellmconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=litellmconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=litellmconnections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// LiteLLMConnectionReconciler probes LiteLLMConnection/default and publishes a
// ready snapshot used by the rest of the operator.
type LiteLLMConnectionReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Cache     connection.CacheReader
	Namespace string
	Log       logr.Logger
}

// Reconcile reconciles the singleton LiteLLMConnection/default.
func (r *LiteLLMConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var conn achv1alpha1.LiteLLMConnection
	if err := r.Get(ctx, req.NamespacedName, &conn); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !conn.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&conn, litellmConnectionFinalizer) {
			controllerutil.RemoveFinalizer(&conn, litellmConnectionFinalizer)
			if err := r.Update(ctx, &conn); err != nil {
				return ctrl.Result{}, err
			}
			r.Cache.Rebuild(connection.Snapshot{
				Ready:      false,
				Reason:     "Absent",
				Generation: conn.Generation,
			})
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&conn, litellmConnectionFinalizer) {
		controllerutil.AddFinalizer(&conn, litellmConnectionFinalizer)
		if err := r.Update(ctx, &conn); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	curReady := apimeta.FindStatusCondition(conn.Status.Conditions, "Ready")
	if curReady == nil || conn.Status.ObservedGeneration != conn.Generation {
		if curReady == nil || curReady.Reason != "Connecting" {
			if err := r.writeStatus(ctx, &conn, metav1.ConditionFalse, "Connecting", "probing endpoint"); err != nil {
				r.Log.Error(err, "status update failed", "reason", "Connecting")
			}
		}
	}

	secretKey := types.NamespacedName{
		Namespace: req.Namespace,
		Name:      conn.Spec.MasterKeySecretRef.Name,
	}
	var secret corev1.Secret
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			msg := "Secret " + req.Namespace + "/" + conn.Spec.MasterKeySecretRef.Name + " not found"
			if werr := r.writeStatus(ctx, &conn, metav1.ConditionFalse, "SecretNotFound", msg); werr != nil {
				r.Log.Error(werr, "status update failed", "reason", "SecretNotFound")
			}
			r.Cache.Rebuild(connection.Snapshot{
				Ready:      false,
				Reason:     "SecretNotFound",
				Generation: conn.Generation,
			})
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	masterKey, ok := secret.Data[conn.Spec.MasterKeySecretRef.Key]
	if !ok {
		msg := "Secret " + req.Namespace + "/" + conn.Spec.MasterKeySecretRef.Name +
			":" + conn.Spec.MasterKeySecretRef.Key + " key not found"
		if err := r.writeStatus(ctx, &conn, metav1.ConditionFalse, "SecretNotFound", msg); err != nil {
			r.Log.Error(err, "status update failed", "reason", "SecretNotFound")
		}
		r.Cache.Rebuild(connection.Snapshot{
			Ready:      false,
			Reason:     "SecretNotFound",
			Generation: conn.Generation,
		})
		return ctrl.Result{}, nil
	}

	client := litellm.NewRESTClient(conn.Spec.Endpoint, string(masterKey), r.Log.WithName("probe"))
	if err := client.ProbeConnection(ctx); err != nil {
		var auth401 *litellm.Auth401Error
		if errors.As(err, &auth401) {
			if werr := r.writeStatus(ctx, &conn, metav1.ConditionFalse, "BadMasterKey", "401 from "+auth401.Path); werr != nil {
				r.Log.Error(werr, "status update failed", "reason", "BadMasterKey")
			}
			r.Cache.Rebuild(connection.Snapshot{
				Ready:      false,
				Reason:     "BadMasterKey",
				Generation: conn.Generation,
			})
			return ctrl.Result{}, nil
		}
		if werr := r.writeStatus(ctx, &conn, metav1.ConditionFalse, "Unreachable", "probe failed: "+err.Error()); werr != nil {
			r.Log.Error(werr, "status update failed", "reason", "Unreachable")
		}
		r.Cache.Rebuild(connection.Snapshot{
			Ready:      false,
			Reason:     "Unreachable",
			Generation: conn.Generation,
		})
		return ctrl.Result{}, err
	}

	if err := r.writeStatus(ctx, &conn, metav1.ConditionTrue, "Synced", "probe ok"); err != nil {
		r.Log.Error(err, "status update failed", "reason", "Synced")
	}
	r.Cache.Rebuild(connection.Snapshot{
		Ready:      true,
		Reason:     "Synced",
		Client:     client,
		Generation: conn.Generation,
	})

	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *LiteLLMConnectionReconciler) writeStatus(
	ctx context.Context,
	conn *achv1alpha1.LiteLLMConnection,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	apimeta.SetStatusCondition(&conn.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: conn.Generation,
		LastTransitionTime: metav1.Now(),
	})
	conn.Status.ObservedGeneration = conn.Generation
	return r.Status().Update(ctx, conn)
}

func (r *LiteLLMConnectionReconciler) secretToConnection(ctx context.Context, obj client.Object) []ctrl.Request {
	var list achv1alpha1.LiteLLMConnectionList
	if err := r.List(ctx, &list); err != nil {
		r.Log.V(1).Info("secretToConnection: list failed", "error", err)
		return nil
	}
	out := make([]ctrl.Request, 0, len(list.Items))
	for i := range list.Items {
		cr := &list.Items[i]
		if cr.Namespace == obj.GetNamespace() && cr.Spec.MasterKeySecretRef.Name == obj.GetName() {
			out = append(out, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cr)})
		}
	}
	return out
}

func (r *LiteLLMConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.LiteLLMConnection{}, builder.WithPredicates()).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.secretToConnection),
		).
		Named("litellmconnection").
		Complete(r)
}
