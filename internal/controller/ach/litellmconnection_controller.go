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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/connection"
	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
)

// litellmConnectionsChannel is the NOTIFY channel emitted on every
// LiteLLMConnection projection write/soft-delete. Forwarder boots and
// hot-reloads off this channel (issue #34).
const litellmConnectionsChannel = "ach_litellm_connections_changed"

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
	// Pool projects the LiteLLMConnection CR spec to the
	// litellm_connections table inside a single transaction with the
	// ach_litellm_connections_changed NOTIFY (issue #34). Nil-tolerant for
	// existing envtests that do not need projection.
	Pool *pgxpool.Pool

	// Issue #34 (A10/A11): see PluginReconciler.ResyncSource.
	ResyncSource chan event.GenericEvent
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
			// Issue #34: soft-delete the projection row inside the same
			// transaction as the NOTIFY so any consumer waking up on
			// ach_litellm_connections_changed SELECTs a snapshot that
			// already reflects the soft-delete. Skipped when r.Pool is
			// nil (existing envtests).
			if r.Pool != nil {
				ns, name := conn.Namespace, conn.Name
				payload := fmt.Sprintf("%s/%s", ns, name)
				if err := achdb.WithTxNotify(ctx, r.Pool, litellmConnectionsChannel, payload, func(tx pgx.Tx) error {
					return achdb.SoftDeleteLiteLLMConnectionTx(ctx, tx, ns, name)
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("db soft-delete litellm_connections projection: %w", err)
				}
			}
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

	// Issue #34: project the CR spec to the litellm_connections table
	// inside a single transaction with the ach_litellm_connections_changed
	// NOTIFY so the forwarder can boot from Postgres alone. ErrOriginConflict
	// (a UI row holds the same PK) flips Synced=False/ConflictWithUIRow and
	// requeues in 1 minute. r.Pool is nil-tolerant for existing envtests.
	if r.Pool != nil {
		row := achdb.LiteLLMConnectionRow{
			Namespace:                conn.Namespace,
			Name:                     conn.Name,
			Endpoint:                 conn.Spec.Endpoint,
			MasterKeySecretNamespace: conn.Namespace,
			MasterKeySecretName:      conn.Spec.MasterKeySecretRef.Name,
			MasterKeySecretKey:       conn.Spec.MasterKeySecretRef.Key,
			ResourceVersion:          conn.ResourceVersion,
		}
		payload := fmt.Sprintf("%s/%s", conn.Namespace, conn.Name)
		err := achdb.WithTxNotify(ctx, r.Pool, litellmConnectionsChannel, payload, func(tx pgx.Tx) error {
			return achdb.UpsertLiteLLMConnectionTx(ctx, tx, row)
		})
		if err != nil {
			if errors.Is(err, achdb.ErrOriginConflict) {
				if werr := r.writeStatus(ctx, &conn, metav1.ConditionFalse, ReasonConflictWithUIRow,
					ConflictWithUIRowMessage); werr != nil {
					r.Log.Error(werr, "status update failed", "reason", ReasonConflictWithUIRow)
				}
				return ctrl.Result{RequeueAfter: time.Minute}, nil
			}
			return ctrl.Result{}, fmt.Errorf("db upsert litellm_connections projection: %w", err)
		}
	}

	// Operator-side bootstrap: guarantee LiteLLM has the canonical
	// `default` team before any SSO callback fires. Idempotent —
	// list-first, create only on empty. Failure is logged + tolerated;
	// the next reconcile (5 minutes) retries. We deliberately do not
	// fail the reconcile on this: the LiteLLMConnection itself is
	// Synced=True (probe succeeded); only the team-seed side effect
	// failed, which a transient LiteLLM hiccup might cause.
	if err := client.EnsureDefaultTeam(ctx); err != nil {
		r.Log.Info("EnsureDefaultTeam failed; will retry on next reconcile",
			"err", err.Error())
	}

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
	desiredStatus := conn.Status
	return retryStatusUpdate(ctx, r.Client, conn, func(fresh *achv1alpha1.LiteLLMConnection) {
		fresh.Status = desiredStatus
	})
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
	b := ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.LiteLLMConnection{}, builder.WithPredicates()).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.secretToConnection),
		).
		Named("litellmconnection")
	if r.ResyncSource != nil {
		b = b.WatchesRawSource(
			source.Channel(r.ResyncSource, &handler.EnqueueRequestForObject{}),
		)
	}
	return b.Complete(r)
}
