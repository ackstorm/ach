// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// legacyCleanupFixture builds a minimal AgentProfile + ACHAgent pair that reaches
// WorkloadApplied=True on a successful Reconcile (every workspace-v1-required block
// populated, no storage/persistence enabled so RuntimeStorageOptions{} is sufficient, a
// single cron channel so no channel secret is required) plus the identity Secret it
// references. Shares the envtest fixture helpers (testLimitsSpec etc.) — their shape
// requirements are identical for a fake-client Reconcile.
func legacyCleanupFixture(agentName string, agentUID types.UID) (*achv1alpha1.AgentProfile, *achv1alpha1.ACHAgent, *corev1.Secret) {
	ns := "ns"
	profile := &achv1alpha1.AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: agentName + "-prof", Namespace: ns},
		Spec: achv1alpha1.AgentProfileSpec{
			Achagent: achv1alpha1.AgentDefaults{
				Image: "img:test", Ach: &achv1alpha1.AchSpec{BaseURL: "https://ach"},
				Model:  &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
				Limits: testLimitsSpec(10), Workspace: testWorkspaceSpec(), Artifacts: testArtifactsSpec(), Engine: testEngineSpec(),
			},
			Execution: testExecutionSpec(),
		},
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: agentName + "-ek", Namespace: ns}, Data: map[string][]byte{"ek": []byte("ek_test")}}
	agent := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: ns, UID: agentUID},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef:    achv1alpha1.LocalObjectRef{Name: profile.Name},
			AgentDefaults: achv1alpha1.AgentDefaults{Ach: envtestIdentity(secret.Name, "prod")},
			Channels:      []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	}
	return profile, agent, secret
}

// legacyDeploymentFor builds the pre-workspace-v1 legacy Deployment pruneLegacyDeployment
// targets, owned by the given ACHAgent.
func legacyDeploymentFor(a *achv1alpha1.ACHAgent) *appsv1.Deployment {
	trueVal := true
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: agentResourceName(a.Name), Namespace: a.Namespace, UID: "legacy-dep-uid",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: achv1alpha1.GroupVersion.String(), Kind: "ACHAgent", Name: a.Name, UID: a.UID, Controller: &trueVal,
		}},
	}}
}

// TestACHAgent_LegacyCleanupAfterControlApply is the Reconcile-level cutover-ordering
// regression: the legacy Deployment (and, by the same codepath, legacy Sandbox objects) must
// never be pruned before the new control StatefulSet is confirmed applied, so a cutover never
// has a workload gap — a failing StatefulSet apply must leave the legacy Deployment in place,
// and a successful one must prune it in the same pass.
func TestACHAgent_LegacyCleanupAfterControlApply(t *testing.T) {
	ctx := context.Background()

	t.Run("failing control StatefulSet apply prevents the legacy delete", func(t *testing.T) {
		scheme := pruneTestScheme(t)
		profile, agent, secret := legacyCleanupFixture("cut-fail", "a0000000-0000-0000-0000-000000000001")
		dep := legacyDeploymentFor(agent)
		fake := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(profile, agent, secret, dep).
			WithStatusSubresource(&achv1alpha1.ACHAgent{}).Build()
		wantErr := errors.New("injected statefulset apply failure")
		spy := interceptor.NewClient(fake, interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*appsv1.StatefulSet); ok {
					return wantErr
				}
				return c.Create(ctx, obj, opts...)
			},
		})
		r := &ACHAgentReconciler{Client: spy, APIReader: spy, Scheme: scheme}
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: agent.Namespace, Name: agent.Name}}); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		var remaining appsv1.Deployment
		if err := fake.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: agentResourceName(agent.Name)}, &remaining); err != nil {
			t.Fatalf("legacy Deployment must survive a failed control apply, Get err: %v", err)
		}
		var fresh achv1alpha1.ACHAgent
		if err := fake.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: agent.Name}, &fresh); err != nil {
			t.Fatal(err)
		}
		if c := meta.FindStatusCondition(fresh.Status.Conditions, condWorkloadApplied); c == nil || c.Status != metav1.ConditionFalse {
			t.Errorf("WorkloadApplied = %+v, want False after the injected apply failure", c)
		}
	})

	t.Run("successful control StatefulSet apply precedes the legacy delete", func(t *testing.T) {
		scheme := pruneTestScheme(t)
		profile, agent, secret := legacyCleanupFixture("cut-ok", "a0000000-0000-0000-0000-000000000002")
		dep := legacyDeploymentFor(agent)
		fake := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(profile, agent, secret, dep).
			WithStatusSubresource(&achv1alpha1.ACHAgent{}).Build()
		r := &ACHAgentReconciler{Client: fake, APIReader: fake, Scheme: scheme}
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: agent.Namespace, Name: agent.Name}}); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		var remaining appsv1.Deployment
		err := fake.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: agentResourceName(agent.Name)}, &remaining)
		if !apierrors.IsNotFound(err) {
			t.Fatalf("legacy Deployment must be pruned once the control StatefulSet applies, Get err: %v", err)
		}
		var sts appsv1.StatefulSet
		if err := fake.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: controlServiceName(string(agent.UID))}, &sts); err != nil {
			t.Fatalf("control StatefulSet must be applied: %v", err)
		}
		var fresh achv1alpha1.ACHAgent
		if err := fake.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: agent.Name}, &fresh); err != nil {
			t.Fatal(err)
		}
		if c := meta.FindStatusCondition(fresh.Status.Conditions, condWorkloadApplied); c == nil || c.Status != metav1.ConditionTrue {
			t.Errorf("WorkloadApplied = %+v, want True", c)
		}
	})
}

// TestACHAgent_LegacyIdentityBlocksWithoutMutatingChildren is the Important-review regression
// (task-1-brief "old nil legacy identity"): an ACHAgent stored before admission started
// requiring spec.ach.identity (object-level CEL only validates NEW/changed objects, never
// retroactively) must block with IdentityResolved=False/IdentityMissing — never panic on the
// nil dereference, and never create or delete any child object. Built directly against the
// fake client (bypassing CEL, which a real apiserver would now enforce on create) to simulate
// exactly that pre-existing stored shape.
func TestACHAgent_LegacyIdentityBlocksWithoutMutatingChildren(t *testing.T) {
	ctx := context.Background()
	scheme := pruneTestScheme(t)
	profile, agent, secret := legacyCleanupFixture("legacy-identity", "a0000000-0000-0000-0000-000000000003")
	agent.Spec.Ach = nil // the pre-CEL stored shape this regression targets
	fake := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(profile, agent, secret).
		WithStatusSubresource(&achv1alpha1.ACHAgent{}).Build()
	r := &ACHAgentReconciler{Client: fake, APIReader: fake, Scheme: scheme}

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: agent.Namespace, Name: agent.Name}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var fresh achv1alpha1.ACHAgent
	if err := fake.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: agent.Name}, &fresh); err != nil {
		t.Fatal(err)
	}
	if c := meta.FindStatusCondition(fresh.Status.Conditions, condIdentityResolved); c == nil || c.Status != metav1.ConditionFalse || c.Reason != "IdentityMissing" {
		t.Fatalf("IdentityResolved = %+v, want False/IdentityMissing", c)
	}

	var stsList appsv1.StatefulSetList
	if err := fake.List(ctx, &stsList, client.InNamespace(agent.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(stsList.Items) != 0 {
		t.Errorf("no control StatefulSet must be created when identity is missing, got %d", len(stsList.Items))
	}
	var cmList corev1.ConfigMapList
	if err := fake.List(ctx, &cmList, client.InNamespace(agent.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(cmList.Items) != 0 {
		t.Errorf("no ConfigMap must be created when identity is missing, got %d", len(cmList.Items))
	}
}

// servedSandboxRESTMapper reports both legacy agent-sandbox kinds as served, the
// "CRDs installed" case TestPruneLegacySandbox needs for its delete-path assertions.
func servedSandboxRESTMapper() meta.RESTMapper {
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{sandboxTemplateGVK.GroupVersion(), sandboxWarmPoolGVK.GroupVersion()})
	m.Add(sandboxTemplateGVK, meta.RESTScopeNamespace)
	m.Add(sandboxWarmPoolGVK, meta.RESTScopeNamespace)
	return m
}

func unstructuredSandboxObj(gvk schema.GroupVersionKind, ns, name string, uid types.UID, owners []metav1.OwnerReference) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetUID(uid)
	u.SetOwnerReferences(owners)
	return u
}

// TestPruneLegacySandbox is the Sandbox counterpart of TestPruneLegacyDeployment, for the
// agent-sandbox legacy SandboxWarmPool/SandboxTemplate objects a pre-workspace-v1 sandboxed
// ACHAgent owned. Same UID-owned-only contract: current-owner deletes, prior-UID/foreign/
// unowned objects survive, an absent object or an unserved GVK (agent-sandbox CRDs not
// installed) is a no-op, any other error propagates, and a Secret is never touched.
func TestPruneLegacySandbox(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	legacyName := agentResourceName(a.Name)
	trueVal := true
	matchingOwner := []metav1.OwnerReference{{
		APIVersion: achv1alpha1.GroupVersion.String(), Kind: "ACHAgent",
		Name: a.Name, UID: a.UID, Controller: &trueVal,
	}}

	t.Run("matching owner deletes pool before template with UID preconditions, no secret touched", func(t *testing.T) {
		scheme := pruneTestScheme(t)
		pool := unstructuredSandboxObj(sandboxWarmPoolGVK, a.Namespace, legacyName, "pool-uid", matchingOwner)
		tmpl := unstructuredSandboxObj(sandboxTemplateGVK, a.Namespace, legacyName, "tmpl-uid", matchingOwner)
		sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: legacyName + "-sandbox-key", Namespace: a.Namespace}}
		fake := clientfake.NewClientBuilder().WithScheme(scheme).WithRESTMapper(servedSandboxRESTMapper()).
			WithObjects(sec).WithRuntimeObjects(pool, tmpl).Build()

		var deletedOrder []string
		var preconds []*types.UID
		var secretDeleted bool
		spy := interceptor.NewClient(fake, interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*corev1.Secret); ok {
					secretDeleted = true
				}
				if u, ok := obj.(*unstructured.Unstructured); ok {
					deletedOrder = append(deletedOrder, u.GetKind())
				}
				do := &client.DeleteOptions{}
				do.ApplyOptions(opts)
				preconds = append(preconds, do.Preconditions.UID)
				return c.Delete(ctx, obj, opts...)
			},
		})
		r := &ACHAgentReconciler{Client: spy, APIReader: spy}

		if err := r.pruneLegacySandbox(context.Background(), a); err != nil {
			t.Fatalf("pruneLegacySandbox: %v", err)
		}
		if secretDeleted {
			t.Error("sandbox-key Secret must never be deleted by this cleanup")
		}
		if len(deletedOrder) != 2 || deletedOrder[0] != "SandboxWarmPool" || deletedOrder[1] != "SandboxTemplate" {
			t.Fatalf("delete order = %v, want [SandboxWarmPool SandboxTemplate]", deletedOrder)
		}
		if len(preconds) != 2 || preconds[0] == nil || *preconds[0] != pool.GetUID() || preconds[1] == nil || *preconds[1] != tmpl.GetUID() {
			t.Errorf("delete preconditions = %v, want observed object UIDs [%v %v]", preconds, pool.GetUID(), tmpl.GetUID())
		}
	})

	for _, kind := range []struct {
		name string
		gvk  schema.GroupVersionKind
	}{{"SandboxWarmPool", sandboxWarmPoolGVK}, {"SandboxTemplate", sandboxTemplateGVK}} {
		cases := map[string]struct {
			present    bool
			owners     []metav1.OwnerReference
			wantDelete bool
		}{
			"absent":  {present: false, wantDelete: false},
			"unowned": {present: true, wantDelete: false},
			"foreign owner kind": {present: true, owners: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "some-rs", UID: "11111111-1111-1111-1111-111111111111", Controller: &trueVal,
			}}, wantDelete: false},
			"stale same-name ACHAgent UID": {present: true, owners: []metav1.OwnerReference{{
				APIVersion: achv1alpha1.GroupVersion.String(), Kind: "ACHAgent", Name: a.Name, UID: "99999999-9999-9999-9999-999999999999", Controller: &trueVal,
			}}, wantDelete: false},
			"matching controller owner": {present: true, owners: matchingOwner, wantDelete: true},
		}
		for caseName, tc := range cases {
			t.Run(kind.name+"/"+caseName, func(t *testing.T) {
				scheme := pruneTestScheme(t)
				b := clientfake.NewClientBuilder().WithScheme(scheme).WithRESTMapper(servedSandboxRESTMapper())
				if tc.present {
					b = b.WithRuntimeObjects(unstructuredSandboxObj(kind.gvk, a.Namespace, legacyName, "obj-uid", tc.owners))
				}
				fake := b.Build()
				var deleted bool
				spy := interceptor.NewClient(fake, interceptor.Funcs{
					Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						deleted = true
						return c.Delete(ctx, obj, opts...)
					},
				})
				r := &ACHAgentReconciler{Client: spy, APIReader: spy}
				if err := r.pruneLegacySandbox(context.Background(), a); err != nil {
					t.Fatalf("pruneLegacySandbox: %v", err)
				}
				if deleted != tc.wantDelete {
					t.Fatalf("%s deleted = %v, want %v", kind.name, deleted, tc.wantDelete)
				}
			})
		}
	}

	t.Run("unserved GVK (agent-sandbox CRDs not installed) is a no-op", func(t *testing.T) {
		scheme := pruneTestScheme(t)
		fake := clientfake.NewClientBuilder().WithScheme(scheme).WithRESTMapper(meta.NewDefaultRESTMapper(nil)).Build()
		var called bool
		spy := interceptor.NewClient(fake, interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				called = true
				return c.Get(ctx, key, obj, opts...)
			},
		})
		r := &ACHAgentReconciler{Client: spy, APIReader: spy}
		if err := r.pruneLegacySandbox(context.Background(), a); err != nil {
			t.Fatalf("pruneLegacySandbox: %v", err)
		}
		if called {
			t.Error("Get must never be attempted for a GVK the RESTMapper does not serve")
		}
	})

	t.Run("a non-NotFound Get error propagates", func(t *testing.T) {
		scheme := pruneTestScheme(t)
		fake := clientfake.NewClientBuilder().WithScheme(scheme).WithRESTMapper(servedSandboxRESTMapper()).Build()
		wantErr := errors.New("transient apiserver error")
		spy := interceptor.NewClient(fake, interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return wantErr
			},
		})
		r := &ACHAgentReconciler{Client: spy, APIReader: spy}
		if err := r.pruneLegacySandbox(context.Background(), a); !errors.Is(err, wantErr) {
			t.Fatalf("pruneLegacySandbox error = %v, want wrapping %v", err, wantErr)
		}
	})

	t.Run("a Delete error propagates", func(t *testing.T) {
		scheme := pruneTestScheme(t)
		pool := unstructuredSandboxObj(sandboxWarmPoolGVK, a.Namespace, legacyName, "pool-uid", matchingOwner)
		fake := clientfake.NewClientBuilder().WithScheme(scheme).WithRESTMapper(servedSandboxRESTMapper()).WithRuntimeObjects(pool).Build()
		wantErr := errors.New("transient delete error")
		spy := interceptor.NewClient(fake, interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				return wantErr
			},
		})
		r := &ACHAgentReconciler{Client: spy, APIReader: spy}
		if err := r.pruneLegacySandbox(context.Background(), a); !errors.Is(err, wantErr) {
			t.Fatalf("pruneLegacySandbox error = %v, want wrapping %v", err, wantErr)
		}
	})
}

func pruneTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := achv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme achv1alpha1: %v", err)
	}
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme clientgoscheme: %v", err)
	}
	return scheme
}

// TestPruneLegacyDeployment is the review-mandated regression for the cutover cleanup path
// (task-4-review.md finding 2): a successful reconcile must delete ONLY the legacy Deployment
// this exact ACHAgent controls — a same-named Deployment with no owner, a foreign owner, or a
// stale ACHAgent UID (e.g. a prior agent that reused the name) must be retained unchanged —
// and the delete itself must carry the observed object's UID as a precondition, so a
// replacement object created between the Get and the Delete is never collected by name alone.
func TestPruneLegacyDeployment(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	a.UID = "3fa0b3b2-9c7a-4e1d-8a2f-6d1c0e9b4a77"
	legacyName := agentResourceName(a.Name)
	trueVal := true

	cases := map[string]struct {
		owners     []metav1.OwnerReference
		present    bool
		wantDelete bool
	}{
		"absent": {present: false, wantDelete: false},
		"matching controller owner": {
			present: true,
			owners: []metav1.OwnerReference{{
				APIVersion: achv1alpha1.GroupVersion.String(), Kind: "ACHAgent",
				Name: a.Name, UID: a.UID, Controller: &trueVal,
			}},
			wantDelete: true,
		},
		"unowned": {present: true, wantDelete: false},
		"foreign owner kind": {
			present: true,
			owners: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet",
				Name: "some-rs", UID: "11111111-1111-1111-1111-111111111111", Controller: &trueVal,
			}},
			wantDelete: false,
		},
		"stale same-name ACHAgent UID": {
			present: true,
			owners: []metav1.OwnerReference{{
				APIVersion: achv1alpha1.GroupVersion.String(), Kind: "ACHAgent",
				Name: a.Name, UID: "99999999-9999-9999-9999-999999999999", Controller: &trueVal,
			}},
			wantDelete: false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			scheme := pruneTestScheme(t)
			var objs []client.Object
			var depUID types.UID
			if tc.present {
				dep := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:            legacyName,
						Namespace:       a.Namespace,
						UID:             types.UID("dep-uid-" + name),
						OwnerReferences: tc.owners,
					},
				}
				depUID = dep.UID
				objs = append(objs, dep)
			}

			var gotDeleted bool
			var gotPrecondUID *types.UID
			fake := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			spy := interceptor.NewClient(fake, interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					gotDeleted = true
					do := &client.DeleteOptions{}
					do.ApplyOptions(opts)
					if do.Preconditions != nil {
						gotPrecondUID = do.Preconditions.UID
					}
					return c.Delete(ctx, obj, opts...)
				},
			})
			r := &ACHAgentReconciler{Client: spy}

			if err := r.pruneLegacyDeployment(context.Background(), a); err != nil {
				t.Fatalf("pruneLegacyDeployment: %v", err)
			}

			if gotDeleted != tc.wantDelete {
				t.Fatalf("deleted = %v, want %v", gotDeleted, tc.wantDelete)
			}
			if tc.wantDelete {
				if gotPrecondUID == nil || *gotPrecondUID != depUID {
					t.Errorf("delete precondition UID = %v, want observed Deployment UID %v", gotPrecondUID, depUID)
				}
			}

			if tc.present && !tc.wantDelete {
				var remaining appsv1.Deployment
				if err := fake.Get(context.Background(), types.NamespacedName{Namespace: a.Namespace, Name: legacyName}, &remaining); err != nil {
					t.Errorf("retained Deployment must still exist, Get err: %v", err)
				}
			}
		})
	}
}
