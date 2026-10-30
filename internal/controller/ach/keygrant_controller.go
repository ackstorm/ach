// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keygrant"
	"github.com/ackstorm/ach/internal/litellm"
)

// KeyGrantStore is the reconciler's Postgres seam: the key_grants projection
// and the exchange keys grants govern.
type KeyGrantStore interface {
	UpsertKeyGrant(ctx context.Context, row achdb.KeyGrantRow) error
	DeleteKeyGrant(ctx context.Context, ns, name string) error
	ListExchangeKeys(ctx context.Context) ([]achdb.EkKeyInfo, error)
	RevokeEnvironmentKey(ctx context.Context, keyID string) (*achdb.EkKeyInfo, error)
}

type poolKeyGrantStore struct{ pool *pgxpool.Pool }

// NewKeyGrantStore is the production KeyGrantStore.
func NewKeyGrantStore(pool *pgxpool.Pool) KeyGrantStore { return poolKeyGrantStore{pool: pool} }

func (s poolKeyGrantStore) UpsertKeyGrant(ctx context.Context, row achdb.KeyGrantRow) error {
	return achdb.UpsertKeyGrant(ctx, s.pool, row)
}
func (s poolKeyGrantStore) DeleteKeyGrant(ctx context.Context, ns, name string) error {
	return achdb.DeleteKeyGrant(ctx, s.pool, ns, name)
}
func (s poolKeyGrantStore) ListExchangeKeys(ctx context.Context) ([]achdb.EkKeyInfo, error) {
	return achdb.ListExchangeKeys(ctx, s.pool)
}
func (s poolKeyGrantStore) RevokeEnvironmentKey(ctx context.Context, keyID string) (*achdb.EkKeyInfo, error) {
	return achdb.RevokeEnvironmentKey(ctx, s.pool, keyID)
}

// KeyGrantReconciler projects KeyGrant CRs into key_grants and owns exchange
// key cleanup: after every projection change it plans from the KeyGrant CRs
// themselves (the table fills one CR at a time and may start empty), revokes the exchange keys no
// grant allows any more (LiteLLM first) and writes the lowest covering
// spec.budget onto every other one's key:<id> tag. The platform API never
// revokes on grant removal: its resolvers stop accepting the JWT within the
// cache window, and this sweep — single leader, LiteLLM master key — revokes.
// A key minted by an exchange that raced a grant's removal is caught by the
// next sweep (the 5-minute resync re-runs every grant).
type KeyGrantReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	LiteLLM   litellm.Client
	Namespace string
	Log       logr.Logger
	// Store nil skips the projection and the sweep.
	Store        KeyGrantStore
	ResyncSource chan event.GenericEvent
}

// update on keygrants is what adding and removing the finalizer takes.
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=keygrants,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=keygrants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=keygrants/finalizers,verbs=update

func (r *KeyGrantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var kg achv1alpha1.KeyGrant
	if err := r.Get(ctx, req.NamespacedName, &kg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !kg.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&kg, keyGrantFinalizer) {
			return ctrl.Result{}, nil
		}
		if r.Store != nil {
			if err := r.Store.DeleteKeyGrant(ctx, kg.Namespace, kg.Name); err != nil {
				return ctrl.Result{}, fmt.Errorf("delete key_grants row: %w", err)
			}
			if err := r.sweep(ctx); err != nil {
				return ctrl.Result{}, err
			}
		}
		controllerutil.RemoveFinalizer(&kg, keyGrantFinalizer)
		return ctrl.Result{}, r.Update(ctx, &kg)
	}
	if controllerutil.AddFinalizer(&kg, keyGrantFinalizer) {
		return ctrl.Result{}, r.Update(ctx, &kg)
	}

	if r.Store != nil {
		if err := r.Store.UpsertKeyGrant(ctx, keyGrantRow(&kg)); err != nil {
			return ctrl.Result{}, fmt.Errorf("upsert key_grants row: %w", err)
		}
		if err := r.sweep(ctx); err != nil {
			return ctrl.Result{}, err
		}
	}

	cond, err := r.readyCondition(ctx, &kg)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cur := apimeta.FindStatusCondition(kg.Status.Conditions, "Ready"); cur != nil &&
		cur.Status == cond.Status && cur.Reason == cond.Reason && cur.Message == cond.Message &&
		kg.Status.ObservedGeneration == kg.Generation {
		return ctrl.Result{}, nil
	}
	setExternalRefCondition(&kg.Status.Conditions, "Ready", cond.Status, cond.Reason, cond.Message, kg.Generation)
	kg.Status.ObservedGeneration = kg.Generation
	desired := kg.Status
	return ctrl.Result{}, retryStatusUpdate(ctx, r.Client, &kg, func(fresh *achv1alpha1.KeyGrant) {
		fresh.Status = desired
	})
}

func keyGrantRow(kg *achv1alpha1.KeyGrant) achdb.KeyGrantRow {
	row := achdb.KeyGrantRow{
		Namespace: kg.Namespace, Name: kg.Name, Identity: kg.Spec.Identity,
		Environments: kg.Spec.Environments, ServiceAccounts: kg.Spec.Requesters.ServiceAccounts,
	}
	if b := kg.Spec.Budget; b != nil {
		maxBudget := b.MaxBudget
		row.MaxBudget = &maxBudget
		row.BudgetDuration = b.BudgetDuration
	}
	return row
}

// readyCondition reports whether an exchange for this grant can succeed: the
// identity must be known to LiteLLM (teams are enrolled at its first ACH
// login) and every named Environment must exist.
func (r *KeyGrantReconciler) readyCondition(ctx context.Context, kg *achv1alpha1.KeyGrant) (metav1.Condition, error) {
	info, err := r.LiteLLM.UserInfoByEmail(ctx, kg.Spec.Identity)
	if err != nil && !litellm.IsNotFound(err) {
		return metav1.Condition{}, fmt.Errorf("LiteLLM user lookup: %w", err)
	}
	if info == nil {
		return metav1.Condition{Status: metav1.ConditionFalse, Reason: "IdentityUnknown",
			Message: kg.Spec.Identity + " has never logged in to ACH"}, nil
	}
	var missing []string
	for _, name := range kg.Spec.Environments {
		var env achv1alpha1.Environment
		err := r.Get(ctx, types.NamespacedName{Namespace: kg.Namespace, Name: name}, &env)
		if apierrors.IsNotFound(err) {
			missing = append(missing, name)
			continue
		}
		if err != nil {
			return metav1.Condition{}, err
		}
	}
	if len(missing) > 0 {
		return metav1.Condition{Status: metav1.ConditionFalse, Reason: "EnvironmentMissing",
			Message: "no Environment named " + strings.Join(missing, ", ")}, nil
	}
	return metav1.Condition{Status: metav1.ConditionTrue, Reason: "Granted",
		Message: "identity known; every Environment exists"}, nil
}

// sweep revokes every exchange key no grant allows any more, LiteLLM first
// (a 404 means the key is already gone), and writes the lowest covering
// spec.budget onto every other one. It plans from the KeyGrant CRs, not the
// key_grants table: the table is filled one CR at a time, so on a cold start
// it holds only the grants reconciled so far and would revoke keys a not yet
// projected grant still allows. A CR being deleted covers nothing. One failing
// key never blocks the others; the errors are joined.
// ponytail: one UpsertTagBudget per covered key per KeyGrant reconcile; skip
// unchanged ceilings if LiteLLM load ever shows it.
func (r *KeyGrantReconciler) sweep(ctx context.Context) error {
	var list achv1alpha1.KeyGrantList
	if err := r.List(ctx, &list, client.InNamespace(r.Namespace)); err != nil {
		return fmt.Errorf("list KeyGrants: %w", err)
	}
	grants := make([]achdb.KeyGrantRow, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].DeletionTimestamp.IsZero() {
			grants = append(grants, keyGrantRow(&list.Items[i]))
		}
	}
	keys, err := r.Store.ListExchangeKeys(ctx)
	if err != nil {
		return fmt.Errorf("list exchange keys: %w", err)
	}
	revoke, budgets := keygrant.Plan(grants, keys)
	var errs []error
	for _, k := range revoke {
		if err := r.revokeKey(ctx, k); err != nil {
			errs = append(errs, err)
			continue
		}
		r.Log.Info("revoked exchange key no grant allows", "key_id", k.KeyID,
			"environment", k.Environment, "requester", k.Requester)
	}
	for keyID, b := range budgets {
		if err := r.LiteLLM.UpsertTagBudget(ctx, litellm.KeyBudgetTag(keyID), b); err != nil {
			errs = append(errs, fmt.Errorf("budget for exchange key %s: %w", keyID, err))
		}
	}
	return errors.Join(errs...)
}

// revokeKey kills the LiteLLM key, then its budget tag, then the row: the row
// stays listed until every earlier step succeeded, so a failure is retried.
func (r *KeyGrantReconciler) revokeKey(ctx context.Context, k achdb.EkKeyInfo) error {
	if k.LiteLLMToken != nil {
		if err := r.LiteLLM.RevokeKey(ctx, *k.LiteLLMToken); err != nil && !litellm.IsHTTPNotFound(err) {
			return fmt.Errorf("revoke exchange key %s: %w", k.KeyID, err)
		}
	}
	if err := litellm.DeleteKeyBudget(ctx, r.LiteLLM, k.KeyID); err != nil && !litellm.IsHTTPNotFound(err) {
		return fmt.Errorf("delete budget of exchange key %s: %w", k.KeyID, err)
	}
	if _, err := r.Store.RevokeEnvironmentKey(ctx, k.KeyID); err != nil {
		return fmt.Errorf("revoke exchange key row %s: %w", k.KeyID, err)
	}
	return nil
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *KeyGrantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Namespace == "" {
		return errors.New("KeyGrantReconciler: Namespace is required")
	}
	return setupWithResync(mgr, r, &achv1alpha1.KeyGrant{}, "ach-keygrant", r.ResyncSource)
}
