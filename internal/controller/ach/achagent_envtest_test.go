// SPDX-License-Identifier: Apache-2.0

package ach

// ACHAgent reconcile envtest cases. Reuses the package envtest harness
// (suite_test.go: TestMain manager bootstrap, shared WatchNamespace-scoped
// cache, k8sClient direct client, Eventually poll helper). No per-test
// namespace — the manager cache is scoped to WatchNamespace, so every object
// is created there with a unique name.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func mustApply(t *testing.T, ctx context.Context, obj client.Object) {
	t.Helper()
	if err := k8sClient.Create(ctx, obj); err != nil {
		t.Fatalf("create %T %q: %v", obj, obj.GetName(), err)
	}
}

// waitAgentCond polls the named ACHAgent until condType reaches want (or ~10s).
func waitAgentCond(t *testing.T, ctx context.Context, name, condType string, want metav1.ConditionStatus) {
	t.Helper()
	var last metav1.ConditionStatus = "<none>"
	ok := Eventually(func() bool {
		var a achv1alpha1.ACHAgent
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: name}, &a); err != nil {
			return false
		}
		c := apimeta.FindStatusCondition(a.Status.Conditions, condType)
		if c == nil {
			return false
		}
		last = c.Status
		return c.Status == want
	}, 10*time.Second, 200*time.Millisecond)
	if !ok {
		t.Fatalf("ACHAgent %q condition %q = %v, want %v", name, condType, last, want)
	}
}

func assertConfigMapValid(t *testing.T, ctx context.Context, cmName string) {
	t.Helper()
	var cm corev1.ConfigMap
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: cmName}, &cm); err != nil {
		t.Fatalf("get configmap %q: %v", cmName, err)
	}
	raw, ok := cm.Data["config.json"]
	if !ok {
		t.Fatalf("configmap %q has no config.json", cmName)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("config.json is not valid JSON: %v", err)
	}
	if m["schemaVersion"] != "1" {
		t.Errorf("config.json schemaVersion = %v, want \"1\"", m["schemaVersion"])
	}
	capBlock, _ := m["capability"].(map[string]any)
	if capBlock["type"] != "ach" {
		t.Errorf("config.json capability.type = %v, want \"ach\"", capBlock["type"])
	}
}

func TestACHAgent_MissingProfile_ProfileResolvedFalse(t *testing.T) {
	ctx := context.Background()
	agent := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-no-profile", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-ghost"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "e"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	}
	if err := k8sClient.Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	waitAgentCond(t, ctx, "aa-no-profile", condProfileResolved, metav1.ConditionFalse)
	waitAgentCond(t, ctx, "aa-no-profile", condReady, metav1.ConditionFalse)
}

func TestACHAgent_HappyPath_AppliesConfigMapAndDeployment(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-happy", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	mustApply(t, ctx, &achv1alpha1.AgentProfile{ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-happy", Namespace: WatchNamespace}, Spec: achv1alpha1.AgentProfileSpec{Image: "img:test", Ach: achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}}})
	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-happy", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-happy"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-happy", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	})
	// No kubelet in envtest → WorkloadReady stays False; WorkloadApplied must go True.
	waitAgentCond(t, ctx, "aa-happy", condWorkloadApplied, metav1.ConditionTrue)
	assertConfigMapValid(t, ctx, agentResourceName("aa-happy"))
}

func TestACHAgent_ProfileDeletedAfterApplied_ReadyFlipsFalse(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-regress", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	prof := &achv1alpha1.AgentProfile{ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-regress", Namespace: WatchNamespace}, Spec: achv1alpha1.AgentProfileSpec{Image: "img:test", Ach: achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}}}
	mustApply(t, ctx, prof)
	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-regress", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-regress"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-regress", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	})
	waitAgentCond(t, ctx, "aa-regress", condWorkloadApplied, metav1.ConditionTrue)
	// Delete the profile → next reconcile must flip ProfileResolved AND Ready to False (R-B1).
	if err := k8sClient.Delete(ctx, prof); err != nil {
		t.Fatalf("delete profile: %v", err)
	}
	waitAgentCond(t, ctx, "aa-regress", condProfileResolved, metav1.ConditionFalse)
	waitAgentCond(t, ctx, "aa-regress", condReady, metav1.ConditionFalse)
}
