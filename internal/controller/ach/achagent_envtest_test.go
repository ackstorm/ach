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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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

func TestACHAgent_SessionCustomRequiresKey(t *testing.T) {
	ctx := context.Background()
	base := func(name string, sess *achv1alpha1.SessionSpec) *achv1alpha1.ACHAgent {
		return &achv1alpha1.ACHAgent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: WatchNamespace},
			Spec: achv1alpha1.ACHAgentSpec{
				ProfileRef: achv1alpha1.LocalObjectRef{Name: "p"},
				Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
				Capability: achv1alpha1.CapabilitySpec{Environment: "e"},
				Channels: []achv1alpha1.ChannelSpec{{
					Name: "c", Type: "cron", Session: sess,
					Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"},
				}},
			},
		}
	}
	key := "{{ payload.thread }}"
	// custom without key → rejected
	if err := k8sClient.Create(ctx, base("aa-sess-nokey", &achv1alpha1.SessionSpec{Type: "custom"})); err == nil {
		t.Fatal("expected rejection: type=custom requires key")
	}
	// none WITH key → rejected
	if err := k8sClient.Create(ctx, base("aa-sess-badkey", &achv1alpha1.SessionSpec{Type: "none", Key: &key})); err == nil {
		t.Fatal("expected rejection: key forbidden unless type=custom")
	}
	// custom WITH key → accepted
	if err := k8sClient.Create(ctx, base("aa-sess-ok", &achv1alpha1.SessionSpec{Type: "custom", Key: &key})); err != nil {
		t.Fatalf("custom+key must be accepted: %v", err)
	}
}

func TestACHAgent_PodTemplateOverlay_MergesIntoDeployment(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-pt", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	mustApply(t, ctx, &achv1alpha1.AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-pt", Namespace: WatchNamespace},
		Spec: achv1alpha1.AgentProfileSpec{
			Image:       "img:test",
			Ach:         achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"},
			Model:       &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
			PodTemplate: &apiextensionsv1.JSON{Raw: []byte(`{"spec":{"securityContext":{"fsGroup":1000,"fsGroupChangePolicy":"OnRootMismatch"}}}`)},
		},
	})
	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-pt", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-pt"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-pt", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	})
	waitAgentCond(t, ctx, "aa-pt", condWorkloadApplied, metav1.ConditionTrue)

	var dep appsv1.Deployment
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: agentResourceName("aa-pt")}, &dep); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	sc := dep.Spec.Template.Spec.SecurityContext
	if sc == nil || sc.FSGroup == nil || *sc.FSGroup != 1000 {
		t.Fatalf("pod securityContext = %+v, want fsGroup 1000 merged", sc)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("operator runAsNonRoot lost after overlay round-trip")
	}
}

func TestACHAgent_PodTemplateInvalid_WorkloadAppliedFalse(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-ptbad", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	mustApply(t, ctx, &achv1alpha1.AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-ptbad", Namespace: WatchNamespace},
		Spec: achv1alpha1.AgentProfileSpec{
			Image: "img:test",
			Ach:   achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"},
			Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
			// valid JSON (the API server accepts it) but a strategic-merge type mismatch
			PodTemplate: &apiextensionsv1.JSON{Raw: []byte(`{"spec":{"containers":{"not":"a-list"}}}`)},
		},
	})
	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-ptbad", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-ptbad"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-ptbad", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	})
	waitAgentCond(t, ctx, "aa-ptbad", condWorkloadApplied, metav1.ConditionFalse)
	waitAgentCond(t, ctx, "aa-ptbad", condReady, metav1.ConditionFalse)

	var a achv1alpha1.ACHAgent
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: "aa-ptbad"}, &a); err != nil {
		t.Fatalf("get achagent: %v", err)
	}
	if c := apimeta.FindStatusCondition(a.Status.Conditions, condWorkloadApplied); c == nil || c.Reason != "PodTemplateInvalid" {
		t.Fatalf("WorkloadApplied reason = %v, want PodTemplateInvalid", c)
	}
}
