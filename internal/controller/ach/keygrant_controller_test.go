// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
)

// keyGrantStoreFake is the in-memory KeyGrantStore the suite reconciler writes.
type keyGrantStoreFake struct {
	mu     sync.Mutex
	grants map[string]achdb.KeyGrantRow
	keys   map[string]achdb.EkKeyInfo
}

func newKeyGrantStoreFake() *keyGrantStoreFake {
	return &keyGrantStoreFake{grants: map[string]achdb.KeyGrantRow{}, keys: map[string]achdb.EkKeyInfo{}}
}

func (s *keyGrantStoreFake) UpsertKeyGrant(_ context.Context, row achdb.KeyGrantRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.grants[row.Namespace+"/"+row.Name] = row
	return nil
}

func (s *keyGrantStoreFake) DeleteKeyGrant(_ context.Context, ns, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.grants, ns+"/"+name)
	return nil
}

func (s *keyGrantStoreFake) ListKeyGrants(_ context.Context, ns string) ([]achdb.KeyGrantRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []achdb.KeyGrantRow
	for _, g := range s.grants {
		if g.Namespace == ns {
			out = append(out, g)
		}
	}
	return out, nil
}

func (s *keyGrantStoreFake) ListExchangeKeys(context.Context) ([]achdb.EkKeyInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []achdb.EkKeyInfo
	for _, k := range s.keys {
		if k.Status != "revoked" {
			out = append(out, k)
		}
	}
	return out, nil
}

func (s *keyGrantStoreFake) RevokeEnvironmentKey(_ context.Context, keyID string) (*achdb.EkKeyInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[keyID]
	if !ok || k.Status == "revoked" {
		return nil, nil
	}
	k.Status = "revoked"
	s.keys[keyID] = k
	return &k, nil
}

func (s *keyGrantStoreFake) grant(ns, name string) (achdb.KeyGrantRow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.grants[ns+"/"+name]
	return g, ok
}

func (s *keyGrantStoreFake) addKey(k achdb.EkKeyInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[k.KeyID] = k
}

func (s *keyGrantStoreFake) status(keyID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys[keyID].Status
}

// keyGrantLLMFake answers the identity lookup and records revokes and budgets.
type keyGrantLLMFake struct {
	*litellm.NoopClient
	mu      sync.Mutex
	users   map[string]bool
	revoked []string
	budgets map[string]litellm.TagBudget
}

func newKeyGrantLLMFake() *keyGrantLLMFake {
	return &keyGrantLLMFake{NoopClient: &litellm.NoopClient{Log: logr.Discard()},
		users: map[string]bool{}, budgets: map[string]litellm.TagBudget{}}
}

func (f *keyGrantLLMFake) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.users[email] {
		return &litellm.UserInfo{UserID: email, UserEmail: email}, nil
	}
	return nil, litellm.ErrNotFound
}

func (f *keyGrantLLMFake) RevokeKey(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, token)
	return nil
}

func (f *keyGrantLLMFake) UpsertTagBudget(_ context.Context, name string, b litellm.TagBudget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.budgets[name] = b
	return nil
}

func (f *keyGrantLLMFake) knowUser(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[email] = true
}

func (f *keyGrantLLMFake) budget(tag string) (litellm.TagBudget, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.budgets[tag]
	return b, ok
}

func (f *keyGrantLLMFake) wasRevoked(token string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.revoked, token)
}

func newKeyGrant(name, identity string, envs, sas []string, budget *achv1alpha1.BudgetBlock) *achv1alpha1.KeyGrant {
	return &achv1alpha1.KeyGrant{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: WatchNamespace},
		Spec: achv1alpha1.KeyGrantSpec{Identity: identity, Environments: envs,
			Requesters: achv1alpha1.KeyGrantRequesters{ServiceAccounts: sas}, Budget: budget},
	}
}

// touchKeyGrant bumps an annotation so the reconciler runs again.
func touchKeyGrant(t *testing.T, kg *achv1alpha1.KeyGrant) {
	t.Helper()
	var cur achv1alpha1.KeyGrant
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kg), &cur); err != nil {
		t.Fatal(err)
	}
	patch := client.MergeFrom(cur.DeepCopy())
	if cur.Annotations == nil {
		cur.Annotations = map[string]string{}
	}
	cur.Annotations["ach.ackstorm.ai/test-touch"] = time.Now().Format(time.RFC3339Nano)
	if err := k8sClient.Patch(context.Background(), &cur, patch); err != nil {
		t.Fatal(err)
	}
}

func TestKeyGrantAdmission(t *testing.T) {
	ctx := context.Background()
	valid := func(name string) *achv1alpha1.KeyGrant {
		return newKeyGrant(name, "ai.run@ackstorm.com", []string{"k8s-doctor"}, []string{"ach-agent/agent-k8s-doctor"}, nil)
	}
	ok := valid("kg-admission-ok")
	if err := k8sClient.Create(ctx, ok); err != nil {
		t.Fatalf("valid KeyGrant refused: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), ok) })

	cases := []struct {
		name   string
		mutate func(*achv1alpha1.KeyGrant)
	}{
		{"upper-case identity", func(k *achv1alpha1.KeyGrant) { k.Spec.Identity = "AI.Run@ackstorm.com" }},
		{"glob identity", func(k *achv1alpha1.KeyGrant) { k.Spec.Identity = "*@ackstorm.com" }},
		{"no environments", func(k *achv1alpha1.KeyGrant) { k.Spec.Environments = nil }},
		{"glob environment", func(k *achv1alpha1.KeyGrant) { k.Spec.Environments = []string{"k8s-*"} }},
		{"no service accounts", func(k *achv1alpha1.KeyGrant) { k.Spec.Requesters.ServiceAccounts = nil }},
		{"bare service account", func(k *achv1alpha1.KeyGrant) { k.Spec.Requesters.ServiceAccounts = []string{"agent-k8s-doctor"} }},
		{"glob service account", func(k *achv1alpha1.KeyGrant) { k.Spec.Requesters.ServiceAccounts = []string{"ach-agent/*"} }},
		{"negative budget", func(k *achv1alpha1.KeyGrant) { k.Spec.Budget = &achv1alpha1.BudgetBlock{MaxBudget: -1} }},
	}
	for i, tc := range cases {
		kg := valid(fmt.Sprintf("kg-admission-bad-%d", i))
		tc.mutate(kg)
		if err := k8sClient.Create(ctx, kg); err == nil {
			_ = k8sClient.Delete(context.Background(), kg)
			t.Errorf("%s: admitted", tc.name)
		}
	}
}

func TestKeyGrantReconciler_ProjectionStatusFinalizer(t *testing.T) {
	ctx := context.Background()
	kg := newKeyGrant("kg-status", "kg-status@ackstorm.com", []string{"kg-status-env"}, []string{"agents/kg-status"}, nil)
	if err := k8sClient.Create(ctx, kg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), kg) })

	waitReady := func(status metav1.ConditionStatus, reason string) {
		t.Helper()
		if !Eventually(func() bool {
			var got achv1alpha1.KeyGrant
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(kg), &got); err != nil {
				return false
			}
			c := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
			return c != nil && c.Status == status && c.Reason == reason && got.Status.ObservedGeneration == got.Generation
		}, 30*time.Second, 250*time.Millisecond) {
			t.Fatalf("Ready never became %s/%s", status, reason)
		}
	}

	waitReady(metav1.ConditionFalse, "IdentityUnknown")
	if row, ok := keyGrantStore.grant(WatchNamespace, "kg-status"); !ok || row.Identity != "kg-status@ackstorm.com" ||
		!slices.Equal(row.ServiceAccounts, []string{"agents/kg-status"}) {
		t.Fatalf("projection row = %+v (present=%v)", row, ok)
	}

	keyGrantLLM.knowUser("kg-status@ackstorm.com")
	touchKeyGrant(t, kg)
	waitReady(metav1.ConditionFalse, "EnvironmentMissing")

	env := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "kg-status-env", Namespace: WatchNamespace},
		Spec:       achv1alpha1.EnvironmentSpec{AuthorizedTeams: []string{"default"}},
	}
	if err := k8sClient.Create(ctx, env); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), env) })
	touchKeyGrant(t, kg)
	waitReady(metav1.ConditionTrue, "Granted")

	if err := k8sClient.Delete(ctx, kg); err != nil {
		t.Fatal(err)
	}
	if !WaitForGone(ctx, &achv1alpha1.KeyGrant{ObjectMeta: kg.ObjectMeta}, 15*time.Second) {
		t.Fatal("KeyGrant not removed: finalizer stuck")
	}
	if _, ok := keyGrantStore.grant(WatchNamespace, "kg-status"); ok {
		t.Fatal("projection row survived the finalizer")
	}
}

func TestKeyGrantReconciler_RevokesUngrantedKeysAndAppliesLowestBudget(t *testing.T) {
	ctx := context.Background()
	const who = "sweep@ackstorm.com"
	keyGrantLLM.knowUser(who)
	g50 := newKeyGrant("kg-sweep-50", who, []string{"sweep-env"}, []string{"agents/sweep-a"},
		&achv1alpha1.BudgetBlock{MaxBudget: 50, BudgetDuration: "30d"})
	g20 := newKeyGrant("kg-sweep-20", who, []string{"sweep-env"}, []string{"agents/sweep-a"},
		&achv1alpha1.BudgetBlock{MaxBudget: 20, BudgetDuration: "7d"})
	for _, g := range []*achv1alpha1.KeyGrant{g50, g20} {
		if err := k8sClient.Create(ctx, g); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), g) })
	}
	// Seed keys only once both grants are projected, so no sweep sees key a uncovered.
	if !Eventually(func() bool {
		_, a := keyGrantStore.grant(WatchNamespace, "kg-sweep-50")
		_, b := keyGrantStore.grant(WatchNamespace, "kg-sweep-20")
		return a && b
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatal("grants never projected")
	}
	tokA, tokB := "lt-sweep-a", "lt-sweep-b"
	keyGrantStore.addKey(achdb.EkKeyInfo{KeyID: "ekid_sweepa", OwnerEmail: who, Environment: "sweep-env",
		Requester: "agents/sweep-a", LiteLLMToken: &tokA, Status: "active"})
	keyGrantStore.addKey(achdb.EkKeyInfo{KeyID: "ekid_sweepb", OwnerEmail: who, Environment: "sweep-env",
		Requester: "agents/sweep-b", LiteLLMToken: &tokB, Status: "active"})
	touchKeyGrant(t, g50)

	if !Eventually(func() bool {
		return keyGrantStore.status("ekid_sweepb") == "revoked" && keyGrantLLM.wasRevoked(tokB)
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatal("the key no grant allows was not revoked (LiteLLM first, then the row)")
	}
	if !Eventually(func() bool {
		b, ok := keyGrantLLM.budget(litellm.KeyBudgetTag("ekid_sweepa"))
		return ok && b.MaxBudget == 20 && b.BudgetDuration == "7d"
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatal("the covered key does not carry the lowest grant budget")
	}
	if keyGrantStore.status("ekid_sweepa") != "active" || keyGrantLLM.wasRevoked(tokA) {
		t.Fatal("a covered key was revoked")
	}

	if err := k8sClient.Delete(ctx, g20); err != nil {
		t.Fatal(err)
	}
	if !Eventually(func() bool {
		b, ok := keyGrantLLM.budget(litellm.KeyBudgetTag("ekid_sweepa"))
		return ok && b.MaxBudget == 50
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatal("removing the lower grant must fall back to the remaining grant's budget")
	}
	if err := k8sClient.Delete(ctx, g50); err != nil {
		t.Fatal(err)
	}
	if !Eventually(func() bool { return keyGrantStore.status("ekid_sweepa") == "revoked" }, 30*time.Second, 250*time.Millisecond) {
		t.Fatal("removing the last covering grant must revoke the key")
	}
}

func TestKeyGrantReconciler_ColdStartKeepsKeyOfUnprojectedGrant(t *testing.T) {
	ctx := context.Background()
	const who = "cold@ackstorm.com"
	a := newKeyGrant("kg-cold-a", who, []string{"cold-env"}, []string{"agents/cold-a"}, nil)
	b := newKeyGrant("kg-cold-b", who, []string{"cold-env"}, []string{"agents/cold-b"}, nil)
	for _, g := range []*achv1alpha1.KeyGrant{a, b} {
		if err := k8sClient.Create(ctx, g); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), g) })
	}
	// A reconciler with an empty key_grants table, as after a restored or wiped DB.
	store, llm := newKeyGrantStoreFake(), newKeyGrantLLMFake()
	tok := "lt-cold-b"
	store.addKey(achdb.EkKeyInfo{KeyID: "ekid_coldb", OwnerEmail: who, Environment: "cold-env",
		Requester: "agents/cold-b", LiteLLMToken: &tok, Status: "active"})
	r := &KeyGrantReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), LiteLLM: llm,
		Namespace: WatchNamespace, Log: logr.Discard(), Store: store}
	// The suite manager's own reconciler adds the finalizers; drive projection
	// only once both grants carry one, so the direct Reconcile never races an
	// Update with it.
	if !Eventually(func() bool {
		for _, g := range []*achv1alpha1.KeyGrant{a, b} {
			var cur achv1alpha1.KeyGrant
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(g), &cur); err != nil ||
				!controllerutil.ContainsFinalizer(&cur, keyGrantFinalizer) {
				return false
			}
		}
		return true
	}, 10*time.Second, 100*time.Millisecond) {
		t.Fatal("the manager did not add the finalizers")
	}
	if _, err := r.Reconcile(ctx, ctrlRequest(a)); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.grant(WatchNamespace, "kg-cold-a"); !ok {
		t.Fatal("grant A was not projected")
	}
	if _, ok := store.grant(WatchNamespace, "kg-cold-b"); ok {
		t.Fatal("test premise broken: grant B must not be projected yet")
	}
	if store.status("ekid_coldb") != "active" || llm.wasRevoked(tok) {
		t.Fatal("the key of a grant not projected yet was revoked")
	}
}

func ctrlRequest(kg *achv1alpha1.KeyGrant) ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKeyFromObject(kg)}
}

// failingRevokeLLM refuses to revoke one token.
type failingRevokeLLM struct {
	*keyGrantLLMFake
	failToken string
}

func (f *failingRevokeLLM) RevokeKey(ctx context.Context, token string) error {
	if token == f.failToken {
		return errors.New("litellm down")
	}
	return f.keyGrantLLMFake.RevokeKey(ctx, token)
}

func fakeKeyGrantClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(k8sClient.Scheme()).WithObjects(objs...).
		WithStatusSubresource(&achv1alpha1.KeyGrant{}).Build()
}

func liveKeyGrant(name, who, env, sa string) *achv1alpha1.KeyGrant {
	kg := newKeyGrant(name, who, []string{env}, []string{sa}, nil)
	kg.Namespace = WatchNamespace
	kg.Finalizers = []string{keyGrantFinalizer}
	return kg
}

func TestKeyGrantReconciler_SweepPrunesRowWithoutKeyGrant(t *testing.T) {
	ctx := context.Background()
	const who = "prune@ackstorm.com"
	live := liveKeyGrant("kg-live", who, "prune-env", "agents/live")
	store, llm := newKeyGrantStoreFake(), newKeyGrantLLMFake()
	llm.knowUser(who)
	_ = store.UpsertKeyGrant(ctx, keyGrantRow(live))
	ghost := keyGrantRow(liveKeyGrant("kg-ghost", who, "prune-env", "agents/ghost"))
	_ = store.UpsertKeyGrant(ctx, ghost)
	tokLive, tokGhost := "lt-live", "lt-ghost"
	store.addKey(achdb.EkKeyInfo{KeyID: "ekid_live", OwnerEmail: who, Environment: "prune-env",
		Requester: "agents/live", LiteLLMToken: &tokLive, Status: "active"})
	store.addKey(achdb.EkKeyInfo{KeyID: "ekid_ghost", OwnerEmail: who, Environment: "prune-env",
		Requester: "agents/ghost", LiteLLMToken: &tokGhost, Status: "active"})
	r := &KeyGrantReconciler{Client: fakeKeyGrantClient(t, live), Scheme: k8sClient.Scheme(), LiteLLM: llm,
		Namespace: WatchNamespace, Log: logr.Discard(), Store: store}

	if _, err := r.Reconcile(ctx, ctrlRequest(live)); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.grant(WatchNamespace, "kg-ghost"); ok {
		t.Fatal("a row with no KeyGrant survived the sweep")
	}
	if _, ok := store.grant(WatchNamespace, "kg-live"); !ok {
		t.Fatal("the row of a live KeyGrant was pruned")
	}
	if store.status("ekid_ghost") != "revoked" || !llm.wasRevoked(tokGhost) {
		t.Fatal("the key only the pruned row allowed was not revoked")
	}
	if store.status("ekid_live") != "active" || llm.wasRevoked(tokLive) {
		t.Fatal("the key of a live grant was revoked")
	}
}

func TestKeyGrantReconciler_DeleteFinalizerIgnoresUnrelatedKeyFailure(t *testing.T) {
	ctx := context.Background()
	const who = "fin@ackstorm.com"
	now := metav1.Now()
	dying := liveKeyGrant("kg-dying", who, "fin-env", "agents/dying")
	dying.DeletionTimestamp = &now
	other := liveKeyGrant("kg-other", "other@ackstorm.com", "fin-env", "agents/other")
	store := newKeyGrantStoreFake()
	llm := &failingRevokeLLM{keyGrantLLMFake: newKeyGrantLLMFake(), failToken: "lt-unrelated"}
	_ = store.UpsertKeyGrant(ctx, keyGrantRow(dying))
	_ = store.UpsertKeyGrant(ctx, keyGrantRow(other))
	tokDying, tokUnrelated := "lt-dying", "lt-unrelated"
	store.addKey(achdb.EkKeyInfo{KeyID: "ekid_dying", OwnerEmail: who, Environment: "fin-env",
		Requester: "agents/dying", LiteLLMToken: &tokDying, Status: "active"})
	store.addKey(achdb.EkKeyInfo{KeyID: "ekid_unrelated", OwnerEmail: who, Environment: "fin-env",
		Requester: "agents/unrelated", LiteLLMToken: &tokUnrelated, Status: "active"})
	c := fakeKeyGrantClient(t, dying, other)
	r := &KeyGrantReconciler{Client: c, Scheme: k8sClient.Scheme(), LiteLLM: llm,
		Namespace: WatchNamespace, Log: logr.Discard(), Store: store}

	if _, err := r.Reconcile(ctx, ctrlRequest(dying)); err != nil {
		t.Fatalf("an unrelated key's failed revoke blocked the finalizer: %v", err)
	}
	if store.status("ekid_dying") != "revoked" || !llm.wasRevoked(tokDying) {
		t.Fatal("the key only the deleting grant allowed was not revoked")
	}
	if store.status("ekid_unrelated") != "active" {
		t.Fatal("an unrelated key was touched by the deletion path")
	}
	var got achv1alpha1.KeyGrant
	if err := c.Get(ctx, client.ObjectKeyFromObject(dying), &got); !apierrors.IsNotFound(err) {
		t.Fatalf("finalizer not removed: %v", err)
	}
}
