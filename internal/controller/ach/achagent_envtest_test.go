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
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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

// runtimeClassName needs no operator field: podTemplate is a PreserveUnknownFields
// raw strategic-merge overlay, so a PodSpec scalar merges user-wins. This test is
// the regression guard for that contract — sandboxed runtimes (gVisor/Kata) are an
// operator feature only because nothing in the overlay path filters the field.
func TestACHAgent_PodTemplateOverlay_SetsRuntimeClassName(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-rc", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	mustApply(t, ctx, &achv1alpha1.AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-rc", Namespace: WatchNamespace},
		Spec: achv1alpha1.AgentProfileSpec{
			Image:       "img:test",
			Ach:         achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"},
			Model:       &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
			PodTemplate: &apiextensionsv1.JSON{Raw: []byte(`{"spec":{"runtimeClassName":"gvisor"}}`)},
		},
	})
	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-rc", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-rc"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-rc", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	})
	waitAgentCond(t, ctx, "aa-rc", condWorkloadApplied, metav1.ConditionTrue)

	var dep appsv1.Deployment
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: agentResourceName("aa-rc")}, &dep); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	rc := dep.Spec.Template.Spec.RuntimeClassName
	if rc == nil || *rc != "gvisor" {
		t.Fatalf("runtimeClassName = %v, want \"gvisor\" (CRD pruning or overlay filtering regressed)", rc)
	}
	if sc := dep.Spec.Template.Spec.SecurityContext; sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
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

// CEL: expose.gateway requires expose.service (admission rejects gateway-only).
func TestACHAgent_ExposeGatewayRequiresService(t *testing.T) {
	ctx := context.Background()
	base := func(name string, expose *achv1alpha1.ExposeSpec) *achv1alpha1.ACHAgent {
		return &achv1alpha1.ACHAgent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: WatchNamespace},
			Spec: achv1alpha1.ACHAgentSpec{
				ProfileRef: achv1alpha1.LocalObjectRef{Name: "p"},
				Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
				Capability: achv1alpha1.CapabilitySpec{Environment: "e"},
				Expose:     expose,
				Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
			},
		}
	}
	// gateway without service → rejected
	if err := k8sClient.Create(ctx, base("aa-exp-nosvc", &achv1alpha1.ExposeSpec{Gateway: true})); err == nil {
		t.Fatal("expected rejection: expose.gateway requires expose.service")
	}
	// service + gateway → accepted
	if err := k8sClient.Create(ctx, base("aa-exp-ok", &achv1alpha1.ExposeSpec{Service: true, Gateway: true})); err != nil {
		t.Fatalf("service+gateway must be accepted: %v", err)
	}
}

// expose.service gates Service creation; expose.gateway gates status.gatewayURL.
func TestACHAgent_ExposeService_CreatesServiceAndGatewayURL(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-exp", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	mustApply(t, ctx, &achv1alpha1.AgentProfile{ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-exp", Namespace: WatchNamespace}, Spec: achv1alpha1.AgentProfileSpec{Image: "img:test", Ach: achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}}})

	webhookCh := func() achv1alpha1.ChannelSpec {
		return achv1alpha1.ChannelSpec{
			Name: "gh", Type: "webhook", Source: "github",
			Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "none"}},
		}
	}

	// Exposed agent: Service created + status.gatewayURL published (path-only in envtest).
	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-exp-pub", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-exp"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-exp", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Expose:     &achv1alpha1.ExposeSpec{Service: true, Gateway: true},
			Channels:   []achv1alpha1.ChannelSpec{webhookCh()},
		},
	})
	waitAgentCond(t, ctx, "aa-exp-pub", condWorkloadApplied, metav1.ConditionTrue)

	var svc corev1.Service
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: agentResourceName("aa-exp-pub")}, &svc); err != nil {
		t.Fatalf("exposed agent must have a Service: %v", err)
	}
	var pub achv1alpha1.ACHAgent
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: "aa-exp-pub"}, &pub); err != nil {
		t.Fatalf("get exposed agent: %v", err)
	}
	if want := "/agents/" + WatchNamespace + "/achagent-aa-exp-pub"; pub.Status.GatewayURL != want {
		t.Fatalf("status.gatewayURL = %q, want %q", pub.Status.GatewayURL, want)
	}

	// Private agent (no expose): same webhook channel, but no Service, no URL.
	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-exp-priv", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-exp"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-exp", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Channels:   []achv1alpha1.ChannelSpec{webhookCh()},
		},
	})
	waitAgentCond(t, ctx, "aa-exp-priv", condWorkloadApplied, metav1.ConditionTrue)

	var privSvc corev1.Service
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: agentResourceName("aa-exp-priv")}, &privSvc)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("private agent must have no Service, got err=%v", err)
	}
	var priv achv1alpha1.ACHAgent
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: "aa-exp-priv"}, &priv); err != nil {
		t.Fatalf("get private agent: %v", err)
	}
	if priv.Status.GatewayURL != "" {
		t.Fatalf("private agent status.gatewayURL = %q, want empty", priv.Status.GatewayURL)
	}
}

// Disabling expose.service (true→false) prunes the now-orphaned Service. Owner-ref
// GC only fires on ACHAgent delete, so without this the Service would leak — the
// same convergence that cleans agents predating the expose feature.
func TestACHAgent_ExposeServiceDisabled_PrunesService(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-prune", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	mustApply(t, ctx, &achv1alpha1.AgentProfile{ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-prune", Namespace: WatchNamespace}, Spec: achv1alpha1.AgentProfileSpec{Image: "img:test", Ach: achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}}})

	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-prune", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-prune"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-prune", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Expose:     &achv1alpha1.ExposeSpec{Service: true},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "gh", Type: "webhook", Source: "github", Webhook: &achv1alpha1.WebhookSpec{Auth: achv1alpha1.WebhookAuthSpec{Type: "none"}}}},
		},
	})
	waitAgentCond(t, ctx, "aa-prune", condWorkloadApplied, metav1.ConditionTrue)

	svcKey := types.NamespacedName{Namespace: WatchNamespace, Name: agentResourceName("aa-prune")}
	if err := k8sClient.Get(ctx, svcKey, &corev1.Service{}); err != nil {
		t.Fatalf("exposed agent must have a Service before disable: %v", err)
	}

	// Flip expose off — mirrors a pre-feat agent: Service on disk, spec no longer wants it.
	var a achv1alpha1.ACHAgent
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: "aa-prune"}, &a); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	a.Spec.Expose = nil
	if err := k8sClient.Update(ctx, &a); err != nil {
		t.Fatalf("disable expose: %v", err)
	}

	if !Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, svcKey, &corev1.Service{}))
	}, 10*time.Second, 200*time.Millisecond) {
		t.Fatal("Service must be pruned after expose.service disabled")
	}
}

// TestACHAgent_MemoryAuth_WiresConfigAndSecretKeyRef proves the hindsight admin
// secret round-trips: the ConfigMap renders auth.env = the operator-generated
// name, and the Deployment carries a matching secretKeyRef env var (never inline,
// never a file). A missing referenced key drives ChannelSecretsResolved=False via
// the shared ReferencedSecrets/checkChannelSecrets path.
func TestACHAgent_MemoryAuth_WiresConfigAndSecretKeyRef(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-mem", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hs-admin", Namespace: WatchNamespace}, Data: map[string][]byte{"token": []byte("bearer")}})
	mustApply(t, ctx, &achv1alpha1.AgentProfile{ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-mem", Namespace: WatchNamespace}, Spec: achv1alpha1.AgentProfileSpec{Image: "img:test", Ach: achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}}})

	memAgent := func(name, secretName string) *achv1alpha1.ACHAgent {
		return &achv1alpha1.ACHAgent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: WatchNamespace},
			Spec: achv1alpha1.ACHAgentSpec{
				ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-mem"},
				Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-mem", Key: "ek"}},
				Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
				Memory: &achv1alpha1.MemorySpec{Type: "hindsight", Hindsight: &achv1alpha1.HindsightSpec{
					Endpoint: "http://h", Mission: "reviewer",
					Auth:         &achv1alpha1.SecretKeyRef{Name: secretName, Key: "token"},
					MentalModels: []achv1alpha1.MentalModelSpec{{ID: "arch", Name: "Arch", SourceQuery: "what arch?"}},
				}},
				Channels: []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
			},
		}
	}

	// Auth secret present → applied, config + Deployment wired.
	mustApply(t, ctx, memAgent("aa-mem", "hs-admin"))
	waitAgentCond(t, ctx, "aa-mem", condWorkloadApplied, metav1.ConditionTrue)
	waitAgentCond(t, ctx, "aa-mem", condChannelSecretsResolved, metav1.ConditionTrue)

	var cm corev1.ConfigMap
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: agentResourceName("aa-mem")}, &cm); err != nil {
		t.Fatalf("get configmap: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &cfg); err != nil {
		t.Fatalf("config.json invalid: %v", err)
	}
	auth := cfg["memory"].(map[string]any)["hindsight"].(map[string]any)["auth"].(map[string]any)
	if auth["env"] != "ACH_SECRET_MEMORY_HINDSIGHT" {
		t.Errorf("config memory.hindsight.auth.env = %v, want ACH_SECRET_MEMORY_HINDSIGHT", auth["env"])
	}

	var dep appsv1.Deployment
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: agentResourceName("aa-mem")}, &dep); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	var found bool
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		if e.Name != "ACH_SECRET_MEMORY_HINDSIGHT" {
			continue
		}
		found = true
		if e.Value != "" || e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil ||
			e.ValueFrom.SecretKeyRef.Name != "hs-admin" || e.ValueFrom.SecretKeyRef.Key != "token" {
			t.Errorf("deployment env ACH_SECRET_MEMORY_HINDSIGHT must be secretKeyRef hs-admin/token, got %+v", e)
		}
	}
	if !found {
		t.Error("deployment missing ACH_SECRET_MEMORY_HINDSIGHT env var")
	}

	// Referenced key missing from the secret → ChannelSecretsResolved=False.
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hs-nokey", Namespace: WatchNamespace}, Data: map[string][]byte{"other": []byte("x")}})
	mustApply(t, ctx, memAgent("aa-mem-nokey", "hs-nokey"))
	waitAgentCond(t, ctx, "aa-mem-nokey", condChannelSecretsResolved, metav1.ConditionFalse)
}

// Renders on presence, prunes on removal. The flip edits the AgentProfile, so this also
// exercises the profile→agents reverse-enqueue watch.
func TestACHAgent_NetworkPolicy_RendersAndPrunes(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-np", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt(443)
	mustApply(t, ctx, &achv1alpha1.AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-np", Namespace: WatchNamespace},
		Spec: achv1alpha1.AgentProfileSpec{
			Image: "img:test",
			Ach:   achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"},
			Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
			NetworkPolicy: &achv1alpha1.NetworkPolicySpec{
				Egress: []networkingv1.NetworkPolicyEgressRule{{
					To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/8"}}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
				}},
			},
		},
	})
	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-np", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-np"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-np", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	})
	waitAgentCond(t, ctx, "aa-np", condWorkloadApplied, metav1.ConditionTrue)

	npKey := types.NamespacedName{Namespace: WatchNamespace, Name: agentResourceName("aa-np")}
	var np networkingv1.NetworkPolicy
	if !Eventually(func() bool { return k8sClient.Get(ctx, npKey, &np) == nil }, 10*time.Second, 200*time.Millisecond) {
		t.Fatal("NetworkPolicy must exist when the profile declares networkPolicy")
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("policyTypes = %v, want [Egress] only", np.Spec.PolicyTypes)
	}
	if np.Spec.PodSelector.MatchLabels[agentLabelKey] != "aa-np" {
		t.Errorf("podSelector = %v, want %s=aa-np", np.Spec.PodSelector.MatchLabels, agentLabelKey)
	}
	if len(np.Spec.Egress) != 2 {
		t.Fatalf("egress rules = %d, want 2 (dns + profile rule)", len(np.Spec.Egress))
	}
	if len(np.OwnerReferences) == 0 {
		t.Error("NetworkPolicy must carry an owner ref (GC on ACHAgent delete)")
	} else if or := np.OwnerReferences[0]; or.Name != "aa-np" || or.Kind != "ACHAgent" {
		t.Errorf("owner ref = %s/%s, want ACHAgent/aa-np", or.Kind, or.Name)
	}

	// Remove the block from the profile — the policy must be pruned (owner-ref GC only
	// fires on ACHAgent delete, not when the owner stops desiring the child).
	var prof achv1alpha1.AgentProfile
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: "aa-prof-np"}, &prof); err != nil {
		t.Fatalf("get profile: %v", err)
	}
	prof.Spec.NetworkPolicy = nil
	if err := k8sClient.Update(ctx, &prof); err != nil {
		t.Fatalf("remove networkPolicy: %v", err)
	}

	if !Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, npKey, &networkingv1.NetworkPolicy{}))
	}, 10*time.Second, 200*time.Millisecond) {
		t.Fatal("NetworkPolicy must be pruned once the profile drops the block")
	}
}
