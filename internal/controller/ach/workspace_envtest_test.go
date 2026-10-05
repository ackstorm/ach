// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWorkspaceAdmission(t *testing.T) {
	ctx := context.Background()
	base := func(spec map[string]any) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
		u.SetGroupVersionKind(achv1alpha1.GroupVersion.WithKind("Workspace"))
		u.SetNamespace(WatchNamespace)
		return u
	}
	validSpec := func() map[string]any {
		return map[string]any{
			"agentRef":     map[string]any{"name": "demo", "uid": testWorkspaceAgentUID},
			"workspaceRef": testWorkspaceRef,
			"replicas":     int64(0),
		}
	}
	create := func(t *testing.T, name string, spec map[string]any) (*unstructured.Unstructured, error) {
		t.Helper()
		u := base(spec)
		u.SetName("ws-admit-" + name)
		return u, k8sClient.Create(ctx, u)
	}
	invalidCreate := func(name, field string, spec map[string]any) func(*testing.T) {
		return func(t *testing.T) {
			_, err := create(t, name, spec)
			if !apierrors.IsInvalid(err) {
				t.Fatalf("%s: want Invalid, got %v", name, err)
			}
			if field != "" && !strings.Contains(err.Error(), field) {
				t.Fatalf("%s: missing field diagnostic %q: %v", name, field, err)
			}
		}
	}
	validCreate := func(name string, replicas int64) func(*testing.T) {
		return func(t *testing.T) {
			spec := validSpec()
			spec["replicas"] = replicas
			u, err := create(t, name, spec)
			if err != nil {
				t.Fatal(err)
			}
			var got achv1alpha1.Workspace
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: u.GetNamespace(), Name: u.GetName()}, &got); err != nil {
				t.Fatal(err)
			}
			if got.Spec.Replicas != int32(replicas) {
				t.Fatalf("replicas=%d, want %d", got.Spec.Replicas, replicas)
			}
			if err := k8sClient.Delete(ctx, u); err != nil {
				t.Fatal(err)
			}
		}
	}

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "valid_zero", run: validCreate("valid-zero", 0)},
		{name: "valid_one", run: validCreate("valid-one", 1)},
		{name: "missing_spec", run: invalidCreate("missing-spec", "spec", nil)},
		{name: "missing_agent_ref", run: invalidCreate("missing-agent-ref", "agentRef", map[string]any{"workspaceRef": testWorkspaceRef, "replicas": int64(0)})},
		{name: "missing_agent_name", run: invalidCreate("missing-agent-name", "name", map[string]any{"agentRef": map[string]any{"uid": testWorkspaceAgentUID}, "workspaceRef": testWorkspaceRef, "replicas": int64(0)})},
		{name: "missing_agent_uid", run: invalidCreate("missing-agent-uid", "uid", map[string]any{"agentRef": map[string]any{"name": "demo"}, "workspaceRef": testWorkspaceRef, "replicas": int64(0)})},
		{name: "missing_workspace_ref", run: invalidCreate("missing-workspace-ref", "workspaceRef", map[string]any{"agentRef": map[string]any{"name": "demo", "uid": testWorkspaceAgentUID}, "replicas": int64(0)})},
		{name: "missing_replicas", run: invalidCreate("missing-replicas", "replicas", map[string]any{"agentRef": map[string]any{"name": "demo", "uid": testWorkspaceAgentUID}, "workspaceRef": testWorkspaceRef})},
		{name: "replicas_minus_one", run: func(t *testing.T) {
			s := validSpec()
			s["replicas"] = int64(-1)
			invalidCreate("minus-one", "replicas", s)(t)
		}},
		{name: "replicas_two", run: func(t *testing.T) {
			s := validSpec()
			s["replicas"] = int64(2)
			invalidCreate("replicas-two", "replicas", s)(t)
		}},
		{name: "replicas_fractional", run: func(t *testing.T) {
			s := validSpec()
			s["replicas"] = 0.5
			invalidCreate("fractional", "replicas", s)(t)
		}},
		{name: "uppercase_uid", run: func(t *testing.T) {
			s := validSpec()
			s["agentRef"].(map[string]any)["uid"] = strings.ToUpper(testWorkspaceAgentUID)
			invalidCreate("uppercase-uid", "uid", s)(t)
		}},
		{name: "malformed_uid", run: func(t *testing.T) {
			s := validSpec()
			s["agentRef"].(map[string]any)["uid"] = "not-a-uid"
			invalidCreate("malformed-uid", "uid", s)(t)
		}},
		{name: "uppercase_digest", run: func(t *testing.T) {
			s := validSpec()
			s["workspaceRef"] = strings.ToUpper(testWorkspaceRef)
			invalidCreate("uppercase-digest", "workspaceRef", s)(t)
		}},
		{name: "short_digest", run: func(t *testing.T) {
			s := validSpec()
			s["workspaceRef"] = testWorkspaceRef[:63]
			invalidCreate("short-digest", "workspaceRef", s)(t)
		}},
		{name: "immutable_agent_name", run: immutableWorkspaceUpdate("immutable-agent-name", func(spec map[string]any) { spec["agentRef"].(map[string]any)["name"] = "other" }, "agentRef")},
		{name: "immutable_agent_uid", run: immutableWorkspaceUpdate("immutable-agent-uid", func(spec map[string]any) {
			spec["agentRef"].(map[string]any)["uid"] = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		}, "agentRef")},
		{name: "immutable_workspace_ref", run: immutableWorkspaceUpdate("immutable-workspace-ref", func(spec map[string]any) { spec["workspaceRef"] = strings.Repeat("b", 64) }, "workspaceRef")},
		{name: "mutable_preconditions", run: func(t *testing.T) {
			u, err := create(t, "mutable-preconditions", validSpec())
			if err != nil {
				t.Fatal(err)
			}
			key := client.ObjectKeyFromObject(u)
			var original achv1alpha1.Workspace
			if err := k8sClient.Get(ctx, key, &original); err != nil {
				t.Fatal(err)
			}
			for _, values := range []struct{ sts, pod string }{{"sts-first", "pod-first"}, {"", ""}} {
				if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					var fresh achv1alpha1.Workspace
					if err := k8sClient.Get(ctx, key, &fresh); err != nil {
						return err
					}
					fresh.Spec.ExpectedStatefulSetUID, fresh.Spec.ExpectedPodUID = values.sts, values.pod
					return k8sClient.Update(ctx, &fresh)
				}); err != nil {
					t.Fatalf("set/clear preconditions: %v", err)
				}
			}
			var got achv1alpha1.Workspace
			if err := k8sClient.Get(ctx, key, &got); err != nil {
				t.Fatal(err)
			}
			if got.Spec.AgentRef != original.Spec.AgentRef || got.Spec.WorkspaceRef != original.Spec.WorkspaceRef || got.Spec.Replicas != original.Spec.Replicas || got.Spec.ExpectedStatefulSetUID != "" || got.Spec.ExpectedPodUID != "" {
				t.Fatalf("setting/clearing preconditions changed identity or left preconditions: %+v", got.Spec)
			}
			if err := k8sClient.Delete(ctx, u); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func createWorkspaceManagerFixture(t *testing.T, tag string, replicas int32) (*achv1alpha1.Workspace, *achv1alpha1.ACHAgent, *achv1alpha1.AgentProfile) {
	t.Helper()
	ctx := context.Background()
	profileName := "ws-profile-" + tag
	agentName := "ws-agent-" + tag
	secretName := "ws-ek-" + tag
	profile := &achv1alpha1.AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: profileName, Namespace: WatchNamespace},
		Spec: achv1alpha1.AgentProfileSpec{
			Achagent: achv1alpha1.AgentDefaults{
				Image: "registry.test/control:0.1.0", Ach: &achv1alpha1.AchSpec{BaseURL: "https://ach"},
				Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}, Limits: testLimitsSpec(10),
				Workspace: testWorkspaceSpec(), Artifacts: testArtifactsSpec(), Engine: testEngineSpec(),
			},
			Execution: testExecutionSpec(),
		},
	}
	mustApply(t, ctx, profile)
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	agent := &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef:    achv1alpha1.LocalObjectRef{Name: profileName},
			AgentDefaults: achv1alpha1.AgentDefaults{Ach: envtestIdentity(secretName, "prod")},
			Channels:      []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	}
	mustApply(t, ctx, agent)
	var currentAgent achv1alpha1.ACHAgent
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), &currentAgent); err != nil {
		t.Fatal(err)
	}
	key := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: WatchNamespace, Name: workspaceKeySecretName(string(currentAgent.UID))}}
	if !Eventually(func() bool { return k8sClient.Get(ctx, client.ObjectKeyFromObject(key), key) == nil }, 10*time.Second, 100*time.Millisecond) {
		t.Fatalf("ACHAgent workspace key scaffolding did not appear for %s", agentName)
	}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), &currentAgent); err != nil {
		t.Fatal(err)
	}
	ref := strings.Repeat("a", 64)
	w := &achv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: workspaceResourceName(currentAgent.Name, ref), Namespace: WatchNamespace},
		Spec:       achv1alpha1.WorkspaceResourceSpec{AgentRef: achv1alpha1.WorkspaceAgentReference{Name: currentAgent.Name, UID: string(currentAgent.UID)}, WorkspaceRef: ref, Replicas: replicas},
	}
	setWorkspaceControllerMetadata(w, &currentAgent)
	return w, &currentAgent, profile
}

func createManagerWorkspace(t *testing.T, w *achv1alpha1.Workspace) *achv1alpha1.Workspace {
	t.Helper()
	if err := k8sClient.Create(context.Background(), w); err != nil {
		t.Fatalf("create Workspace: %v", err)
	}
	return w
}

func waitWorkspaceCondition(t *testing.T, w *achv1alpha1.Workspace, typ string, status metav1.ConditionStatus, reason string) achv1alpha1.Workspace {
	t.Helper()
	ctx := context.Background()
	var got achv1alpha1.Workspace
	if !Eventually(func() bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(w), &got); err != nil {
			return false
		}
		condition := apimeta.FindStatusCondition(got.Status.Conditions, typ)
		return got.Status.ObservedGeneration == got.Generation && condition != nil && condition.ObservedGeneration == got.Generation && condition.Status == status && (reason == "" || condition.Reason == reason)
	}, 10*time.Second, 100*time.Millisecond) {
		condition := apimeta.FindStatusCondition(got.Status.Conditions, typ)
		t.Fatalf("Workspace condition %s did not reach %s/%s: got %+v", typ, status, reason, condition)
	}
	return got
}

func getWorkspaceChildren(t *testing.T, w *achv1alpha1.Workspace) (*appsv1.StatefulSet, *corev1.Service) {
	t.Helper()
	key := types.NamespacedName{Namespace: w.Namespace, Name: w.Name}
	var sts appsv1.StatefulSet
	if err := k8sClient.Get(context.Background(), key, &sts); err != nil {
		t.Fatalf("get Workspace StatefulSet: %v", err)
	}
	var svc corev1.Service
	if err := k8sClient.Get(context.Background(), key, &svc); err != nil {
		t.Fatalf("get Workspace Service: %v", err)
	}
	return &sts, &svc
}

func TestWorkspaceFreshCreate(t *testing.T) {
	for _, replicas := range []int32{0, 1} {
		t.Run(fmt.Sprintf("replicas_%d", replicas), func(t *testing.T) {
			w, agent, profile := createWorkspaceManagerFixture(t, fmt.Sprintf("fresh%d", replicas), replicas)
			createManagerWorkspace(t, w)
			got := waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
			if got.Status.ObservedGeneration != w.Generation {
				t.Fatalf("observedGeneration=%d, want %d", got.Status.ObservedGeneration, w.Generation)
			}
			sts, svc := getWorkspaceChildren(t, w)
			secret := &corev1.Secret{}
			if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: w.Namespace, Name: workspaceKeySecretName(string(agent.UID))}, secret); err != nil {
				t.Fatal(err)
			}
			expected, err := buildWorkspaceStatefulSet(w, agent, profile, workspaceEngineVerifyKey(secret.Data["key"]))
			if err != nil {
				t.Fatal(err)
			}
			if workspaceTemplateDrift(sts, expected) {
				liveNormalized, desiredNormalized := normalizeWorkspaceTemplate(sts.Spec.Template), normalizeWorkspaceTemplate(expected.Spec.Template)
				t.Fatalf("API-defaulted template drift remains: pod fields=%v; container fields=%v; volumes fields=%v", differingWorkspaceStructFields(liveNormalized.Spec, desiredNormalized.Spec), differingWorkspaceStructFields(liveNormalized.Spec.Containers[0], desiredNormalized.Spec.Containers[0]), differingWorkspaceStructFields(liveNormalized.Spec.Volumes, desiredNormalized.Spec.Volumes))
			}
			if apimeta.FindStatusCondition(got.Status.Conditions, "UpdatePending").Status != metav1.ConditionFalse {
				t.Fatal("standard API defaults were reported as profile drift")
			}
			stableVersion := sts.ResourceVersion
			time.Sleep(250 * time.Millisecond)
			stable, _ := getWorkspaceChildren(t, w)
			if stable.ResourceVersion != stableVersion {
				t.Fatalf("unchanged API-defaulted template received unnecessary updates: rv %s→%s", stableVersion, stable.ResourceVersion)
			}
			if sts.Annotations[workspaceUIDAnnotation] != string(w.UID) {
				t.Fatalf("CREATE provenance=%q, want %q", sts.Annotations[workspaceUIDAnnotation], w.UID)
			}
			if _, present := sts.Spec.Template.Annotations[workspaceUIDAnnotation]; present {
				t.Fatal("Workspace UID provenance leaked into Pod template")
			}
			if len(sts.OwnerReferences) != 1 || sts.OwnerReferences[0].Kind != "ACHAgent" || sts.OwnerReferences[0].UID != agent.UID {
				t.Fatalf("StatefulSet owner refs=%+v", sts.OwnerReferences)
			}
			if len(svc.OwnerReferences) != 1 || svc.OwnerReferences[0].Kind != "ACHAgent" || svc.OwnerReferences[0].UID != agent.UID {
				t.Fatalf("Service owner refs=%+v", svc.OwnerReferences)
			}
			if got.Status.StatefulSetUID != string(sts.UID) {
				t.Fatalf("status StatefulSet UID=%q, want %q", got.Status.StatefulSetUID, sts.UID)
			}
		})
	}
}

func TestWorkspaceSleepPreconditions(t *testing.T) {
	w, _, _ := createWorkspaceManagerFixture(t, "sleep", 1)
	createManagerWorkspace(t, w)
	waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	sts, _ := getWorkspaceChildren(t, w)
	before := sts.DeepCopy()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: w.Name + "-0", Namespace: w.Namespace, Labels: sts.Spec.Template.Labels, Annotations: sts.Spec.Template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: sts.Name, UID: sts.UID}}}, Spec: sts.Spec.Template.Spec}
	mustApply(t, context.Background(), pod)
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), pod); err != nil {
		t.Fatal(err)
	}
	var current achv1alpha1.Workspace
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(w), &current); err != nil {
		t.Fatal(err)
	}
	base := current.DeepCopy()
	current.Spec.ExpectedStatefulSetUID = string(sts.UID)
	current.Spec.ExpectedPodUID = string(pod.UID)
	current.Spec.Replicas = 0
	if err := k8sClient.Patch(context.Background(), &current, client.MergeFrom(base)); err != nil {
		t.Fatal(err)
	}
	waitWorkspaceCondition(t, &current, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	after, _ := getWorkspaceChildren(t, w)
	if after.Spec.Replicas == nil || *after.Spec.Replicas != 0 {
		t.Fatalf("replicas=%v, want 0", after.Spec.Replicas)
	}
	before.Spec.Replicas, after.Spec.Replicas = nil, nil
	if !reflect.DeepEqual(before.Spec, after.Spec) || before.UID != after.UID {
		t.Fatal("sleep changed template or child identity")
	}
}

func TestWorkspaceActiveAdoption(t *testing.T) {
	w, agent, profile := createWorkspaceManagerFixture(t, "active-adopt", 1)
	sts, err := buildWorkspaceStatefulSet(w, agent, profile, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	sts.UID = ""
	mustApply(t, context.Background(), sts)
	svc := buildWorkspaceService(w, agent)
	mustApply(t, context.Background(), svc)
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(sts), sts); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(svc), svc); err != nil {
		t.Fatal(err)
	}
	oldTemplate := sts.Spec.Template.DeepCopy()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: w.Name + "-0", Namespace: w.Namespace, Labels: sts.Spec.Template.Labels, Annotations: sts.Spec.Template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: sts.Name, UID: sts.UID}}}, Spec: sts.Spec.Template.Spec}
	mustApply(t, context.Background(), pod)
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), pod); err != nil {
		t.Fatal(err)
	}
	profile.Spec.Execution.Image = "registry.test/exec:new"
	if err := k8sClient.Update(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	w.Spec.ExpectedStatefulSetUID, w.Spec.ExpectedPodUID = string(sts.UID), string(pod.UID)
	createManagerWorkspace(t, w)
	got := waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	if apimeta.FindStatusCondition(got.Status.Conditions, "UpdatePending").Status != metav1.ConditionTrue {
		t.Fatal("active drift was not reported pending")
	}
	after, _ := getWorkspaceChildren(t, w)
	var afterPod corev1.Pod
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &afterPod); err != nil {
		t.Fatal(err)
	}
	if after.UID != sts.UID || afterPod.UID != pod.UID || !reflect.DeepEqual(after.Spec.Template, *oldTemplate) {
		t.Fatal("active legacy adoption changed workload or Pod identity")
	}
}

func TestWorkspaceIdleAdoption(t *testing.T) {
	w, agent, profile := createWorkspaceManagerFixture(t, "idle-adopt", 0)
	profile.Spec.Execution.Image = "registry.test/exec:old"
	if err := k8sClient.Update(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	sts, err := buildWorkspaceStatefulSet(w, agent, profile, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, context.Background(), sts)
	svc := buildWorkspaceService(w, agent)
	mustApply(t, context.Background(), svc)
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(sts), sts); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(svc), svc); err != nil {
		t.Fatal(err)
	}
	profile.Spec.Execution.Image = "registry.test/exec:new"
	if err := k8sClient.Update(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	w.Spec.ExpectedStatefulSetUID = string(sts.UID)
	createManagerWorkspace(t, w)
	waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	after, _ := getWorkspaceChildren(t, w)
	if *after.Spec.Replicas != 0 || after.Spec.Template.Spec.Containers[0].Image != "registry.test/exec:new" || after.UID != sts.UID {
		t.Fatal("idle adoption did not refresh in place while remaining asleep")
	}
}

func TestWorkspaceProfileDriftWhileLive(t *testing.T) {
	w, _, profile := createWorkspaceManagerFixture(t, "drift-live", 1)
	createManagerWorkspace(t, w)
	waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	before, _ := getWorkspaceChildren(t, w)
	profile.Spec.Execution.Image = "registry.test/exec:changed"
	if err := k8sClient.Update(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	got := waitWorkspaceCondition(t, w, "UpdatePending", metav1.ConditionTrue, "WorkloadDrift")
	after, _ := getWorkspaceChildren(t, w)
	if *after.Spec.Replicas != 1 || !reflect.DeepEqual(before.Spec.Template, after.Spec.Template) || after.UID != before.UID {
		t.Fatal("profile drift changed a live StatefulSet template or scale")
	}
	if apimeta.FindStatusCondition(got.Status.Conditions, "WorkloadApplied").Status != metav1.ConditionTrue {
		t.Fatal("live template drift invalidated replica application")
	}
}

func TestWorkspaceLingeringPodBlocksIdle(t *testing.T) {
	for caseIndex, tc := range []struct {
		name      string
		podName   string
		phase     corev1.PodPhase
		unlabeled bool
		orphan    bool
		deleting  bool
	}{
		{name: "pending", phase: corev1.PodPending},
		{name: "running", phase: corev1.PodRunning},
		{name: "succeeded", phase: corev1.PodSucceeded},
		{name: "failed", phase: corev1.PodFailed},
		{name: "deleting with finalizer", phase: corev1.PodRunning, deleting: true},
		{name: "orphan full-label match", phase: corev1.PodPending, orphan: true},
		{name: "unlabeled exact ordinal", phase: corev1.PodPending, unlabeled: true},
		{name: "differently named full-label match", podName: "workspace-extra", phase: corev1.PodRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, profile := createWorkspaceManagerFixture(t, fmt.Sprintf("lingering-%d", caseIndex), 0)
			createManagerWorkspace(t, w)
			waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
			sts, _ := getWorkspaceChildren(t, w)
			podName := tc.podName
			if podName == "" {
				podName = w.Name + "-0"
			} else if podName == "workspace-extra" {
				podName = w.Name + "-extra"
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: w.Namespace, Labels: sts.Spec.Template.Labels, Annotations: sts.Spec.Template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: sts.Name, UID: sts.UID}}}, Spec: sts.Spec.Template.Spec}
			if tc.unlabeled {
				pod.Labels = nil
			}
			if tc.orphan {
				pod.OwnerReferences = nil
			}
			if tc.deleting {
				pod.Finalizers = []string{"test.ach.ackstorm.ai/hold"}
			}
			mustApply(t, context.Background(), pod)
			if tc.phase != "" {
				if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), pod); err != nil {
					t.Fatal(err)
				}
				pod.Status.Phase = tc.phase
				if err := k8sClient.Status().Update(context.Background(), pod); err != nil {
					t.Fatal(err)
				}
			}
			if tc.deleting {
				if err := k8sClient.Delete(context.Background(), pod); err != nil {
					t.Fatal(err)
				}
				if !Eventually(func() bool {
					var deleting corev1.Pod
					return k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &deleting) == nil && deleting.DeletionTimestamp != nil
				}, 5*time.Second, 100*time.Millisecond) {
					t.Fatal("Pod finalizer did not retain a deleting Pod")
				}
			}
			before, _ := getWorkspaceChildren(t, w)
			profile.Spec.Execution.Image = "registry.test/exec:pending"
			if err := k8sClient.Update(context.Background(), profile); err != nil {
				t.Fatal(err)
			}
			waitWorkspaceCondition(t, w, "UpdatePending", metav1.ConditionTrue, "WorkloadDrift")
			current, _ := getWorkspaceChildren(t, w)
			if *current.Spec.Replicas != 0 || !reflect.DeepEqual(before.Spec.Template, current.Spec.Template) {
				t.Fatal("lingering Pod allowed idle template refresh or changed replicas")
			}
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				var request achv1alpha1.Workspace
				if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(w), &request); err != nil {
					return err
				}
				request.Spec.Replicas = 1
				request.Spec.ExpectedStatefulSetUID = string(sts.UID)
				return k8sClient.Update(context.Background(), &request)
			}); err != nil {
				t.Fatal(err)
			}
			waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionFalse, "WorkspaceConflict")
			current, _ = getWorkspaceChildren(t, w)
			if !reflect.DeepEqual(before, current) {
				t.Fatal("Pod phase, identity, or deletion did not block every StatefulSet write")
			}
			if tc.deleting {
				var deleting corev1.Pod
				if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &deleting); err != nil {
					t.Fatal(err)
				}
				deleting.Finalizers = nil
				if err := k8sClient.Update(context.Background(), &deleting); err != nil {
					t.Fatal(err)
				}
			}
			if err := k8sClient.Delete(context.Background(), pod); err != nil && !apierrors.IsNotFound(err) {
				t.Fatal(err)
			}
			if !Eventually(func() bool {
				var gone corev1.Pod
				return apierrors.IsNotFound(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &gone))
			}, 5*time.Second, 100*time.Millisecond) {
				t.Fatal("matching Pod remained after deletion")
			}
			waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
			activated, _ := getWorkspaceChildren(t, w)
			if activated.Spec.Replicas == nil || *activated.Spec.Replicas != 1 || activated.Spec.Template.Spec.Containers[0].Image != profile.Spec.Execution.Image {
				t.Fatal("activation did not proceed with the latest template after the last matching Pod disappeared")
			}
		})
	}
}

func TestWorkspaceIdleRefreshAfterLastPodDeletion(t *testing.T) {
	w, _, profile := createWorkspaceManagerFixture(t, "idle-delete-watch", 0)
	createManagerWorkspace(t, w)
	waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	sts, _ := getWorkspaceChildren(t, w)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: w.Name + "-0", Namespace: w.Namespace, Labels: sts.Spec.Template.Labels, Annotations: sts.Spec.Template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: sts.Name, UID: sts.UID}}}, Spec: sts.Spec.Template.Spec}
	mustApply(t, context.Background(), pod)
	profile.Spec.Execution.Image = "registry.test/exec:after-pod"
	if err := k8sClient.Update(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	waitWorkspaceCondition(t, w, "UpdatePending", metav1.ConditionTrue, "WorkloadDrift")
	before, _ := getWorkspaceChildren(t, w)
	if *before.Spec.Replicas != 0 || before.Spec.Template.Spec.Containers[0].Image == profile.Spec.Execution.Image {
		t.Fatal("Pod did not prevent idle refresh while drift was pending")
	}
	if err := k8sClient.Delete(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if !Eventually(func() bool {
		var live appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(sts), &live); err != nil {
			return false
		}
		return live.Spec.Replicas != nil && *live.Spec.Replicas == 0 && live.Spec.Template.Spec.Containers[0].Image == profile.Spec.Execution.Image
	}, 10*time.Second, 100*time.Millisecond) {
		t.Fatal("Pod deletion watch did not refresh the idle template without changing the Workspace")
	}
	got := waitWorkspaceCondition(t, w, "UpdatePending", metav1.ConditionFalse, "UpToDate")
	if got.Spec.Replicas != 0 || got.Generation != w.Generation {
		t.Fatal("Pod deletion watch changed Workspace desired replicas or generation")
	}
}

func TestWorkspaceIdleRefreshAndActivation(t *testing.T) {
	w, _, profile := createWorkspaceManagerFixture(t, "idle-refresh", 0)
	createManagerWorkspace(t, w)
	waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	sts, _ := getWorkspaceChildren(t, w)
	profile.Spec.Execution.Image = "registry.test/exec:refreshed"
	if err := k8sClient.Update(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	if !Eventually(func() bool {
		var live appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(sts), &live); err != nil {
			return false
		}
		return live.Spec.Template.Spec.Containers[0].Image == "registry.test/exec:refreshed" && *live.Spec.Replicas == 0
	}, 10*time.Second, 100*time.Millisecond) {
		t.Fatal("idle profile refresh did not apply while remaining asleep")
	}
	var current achv1alpha1.Workspace
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(w), &current); err != nil {
		t.Fatal(err)
	}
	base := current.DeepCopy()
	current.Spec.ExpectedStatefulSetUID = string(sts.UID)
	current.Spec.Replicas = 1
	if err := k8sClient.Patch(context.Background(), &current, client.MergeFrom(base)); err != nil {
		t.Fatal(err)
	}
	waitWorkspaceCondition(t, &current, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	activated, _ := getWorkspaceChildren(t, w)
	if *activated.Spec.Replicas != 1 || activated.Spec.Template.Spec.Containers[0].Image != "registry.test/exec:refreshed" {
		t.Fatal("activation did not apply the latest template and replica count together")
	}
}

func TestWorkspaceServiceRepairAndConflict(t *testing.T) {
	t.Run("repair absent bound Service", func(t *testing.T) {
		w, _, _ := createWorkspaceManagerFixture(t, "repair-svc", 0)
		createManagerWorkspace(t, w)
		waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
		sts, svc := getWorkspaceChildren(t, w)
		if err := k8sClient.Delete(context.Background(), svc); err != nil {
			t.Fatal(err)
		}
		if !Eventually(func() bool {
			var live corev1.Service
			return k8sClient.Get(context.Background(), client.ObjectKeyFromObject(svc), &live) == nil
		}, 10*time.Second, 100*time.Millisecond) {
			t.Fatal("bound missing Service was not repaired")
		}
		after, _ := getWorkspaceChildren(t, w)
		if after.UID != sts.UID {
			t.Fatal("service repair replaced StatefulSet")
		}
	})
	t.Run("foreign Service blocks scale", func(t *testing.T) {
		w, _, _ := createWorkspaceManagerFixture(t, "foreign-svc", 0)
		createManagerWorkspace(t, w)
		waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
		sts, svc := getWorkspaceChildren(t, w)
		svc.Spec.Selector = map[string]string{workspaceNameLabel: "foreign"}
		if err := k8sClient.Update(context.Background(), svc); err != nil {
			t.Fatal(err)
		}
		var current achv1alpha1.Workspace
		if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(w), &current); err != nil {
			t.Fatal(err)
		}
		current.Spec.ExpectedStatefulSetUID, current.Spec.Replicas = string(sts.UID), 1
		if err := k8sClient.Update(context.Background(), &current); err != nil {
			t.Fatal(err)
		}
		waitWorkspaceCondition(t, &current, "WorkloadApplied", metav1.ConditionFalse, "WorkspaceConflict")
		after, _ := getWorkspaceChildren(t, w)
		if *after.Spec.Replicas != 0 || after.UID != sts.UID {
			t.Fatal("foreign Service conflict allowed StatefulSet update")
		}
	})
}

func TestWorkspaceOwnerAndRenderFailures(t *testing.T) {
	t.Run("missing owner", func(t *testing.T) {
		ref := strings.Repeat("b", 64)
		w := &achv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Name: workspaceResourceName("ws-missing-owner", ref), Namespace: WatchNamespace}, Spec: achv1alpha1.WorkspaceResourceSpec{AgentRef: achv1alpha1.WorkspaceAgentReference{Name: "ws-missing-owner", UID: testWorkspaceAgentUID}, WorkspaceRef: ref}}
		createManagerWorkspace(t, w)
		got := waitWorkspaceCondition(t, w, "OwnerResolved", metav1.ConditionFalse, "AgentNotFound")
		if got.Status.ObservedGeneration != w.Generation || apimeta.FindStatusCondition(got.Status.Conditions, "WorkloadApplied").Status != metav1.ConditionFalse {
			t.Fatal("missing owner status was not current-generation failure")
		}
		key := types.NamespacedName{Namespace: w.Namespace, Name: w.Name}
		var sts appsv1.StatefulSet
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(), key, &sts); !apierrors.IsNotFound(err) {
			t.Fatalf("missing owner created or retained a StatefulSet: %v", err)
		}
		if err := k8sClient.Get(context.Background(), key, &svc); !apierrors.IsNotFound(err) {
			t.Fatalf("missing owner created or retained a Service: %v", err)
		}
	})
	t.Run("missing profile recovers on Profile event", func(t *testing.T) {
		w, _, profile := createWorkspaceManagerFixture(t, "missing-profile-recovery", 0)
		if err := k8sClient.Delete(context.Background(), profile); err != nil {
			t.Fatal(err)
		}
		if !Eventually(func() bool {
			var gone achv1alpha1.AgentProfile
			return apierrors.IsNotFound(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(profile), &gone))
		}, 5*time.Second, 100*time.Millisecond) {
			t.Fatal("AgentProfile was not deleted")
		}
		createManagerWorkspace(t, w)
		waitWorkspaceCondition(t, w, "OwnerResolved", metav1.ConditionFalse, "ProfileNotFound")
		key := types.NamespacedName{Namespace: w.Namespace, Name: w.Name}
		var sts appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(), key, &sts); !apierrors.IsNotFound(err) {
			t.Fatalf("missing Profile created a StatefulSet: %v", err)
		}
		recreated := profile.DeepCopy()
		recreated.ResourceVersion, recreated.UID, recreated.Generation = "", "", 0
		recreated.CreationTimestamp, recreated.ManagedFields = metav1.Time{}, nil
		mustApply(t, context.Background(), recreated)
		waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	})
	t.Run("invalid execution render", func(t *testing.T) {
		w, _, profile := createWorkspaceManagerFixture(t, "invalid-render", 0)
		createManagerWorkspace(t, w)
		waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
		beforeSTS, beforeService := getWorkspaceChildren(t, w)
		beforeSTS, beforeService = beforeSTS.DeepCopy(), beforeService.DeepCopy()
		profile.Spec.Execution.EphemeralStorage = "bad-quantity"
		if err := k8sClient.Update(context.Background(), profile); err != nil {
			t.Fatal(err)
		}
		got := waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionFalse, "ExecutionRenderFailed")
		if got.Status.ObservedGeneration != w.Generation {
			t.Fatal("invalid render was not reported against the current generation")
		}
		afterSTS, afterService := getWorkspaceChildren(t, w)
		if !reflect.DeepEqual(beforeSTS, afterSTS) || !reflect.DeepEqual(beforeService, afterService) {
			t.Fatal("invalid render changed a full existing StatefulSet or Service")
		}
		profile.Spec.Execution.EphemeralStorage = "3Gi"
		if err := k8sClient.Update(context.Background(), profile); err != nil {
			t.Fatal(err)
		}
		waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
		recoveredSTS, recoveredService := getWorkspaceChildren(t, w)
		if !reflect.DeepEqual(beforeSTS, recoveredSTS) || !reflect.DeepEqual(beforeService, recoveredService) {
			t.Fatal("valid render recovery needlessly changed existing child objects")
		}
	})
}

func TestWorkspaceRecreatedAgentUIDDoesNotRecoverOldWorkspace(t *testing.T) {
	ctx := context.Background()
	w, agent, _ := createWorkspaceManagerFixture(t, "recreated-agent", 0)
	oldUID := agent.UID
	createManagerWorkspace(t, w)
	waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	beforeSTS, beforeService := getWorkspaceChildren(t, w)
	beforeSTS, beforeService = beforeSTS.DeepCopy(), beforeService.DeepCopy()

	if err := k8sClient.Delete(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if !Eventually(func() bool {
		var gone achv1alpha1.ACHAgent
		return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), &gone))
	}, 10*time.Second, 100*time.Millisecond) {
		t.Fatal("original ACHAgent was not deleted")
	}
	recreated := agent.DeepCopy()
	recreated.UID, recreated.ResourceVersion, recreated.Generation = "", "", 0
	recreated.CreationTimestamp, recreated.ManagedFields = metav1.Time{}, nil
	if err := k8sClient.Create(ctx, recreated); err != nil {
		t.Fatalf("recreate ACHAgent: %v", err)
	}
	var replacement achv1alpha1.ACHAgent
	if !Eventually(func() bool {
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(recreated), &replacement) == nil && replacement.UID != oldUID
	}, 5*time.Second, 100*time.Millisecond) {
		t.Fatal("recreated ACHAgent did not receive a new immutable UID")
	}

	// A later Workspace request proves the old immutable reference stays fenced after recreation.
	var request achv1alpha1.Workspace
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(w), &request); err != nil {
			return err
		}
		request.Spec.ExpectedStatefulSetUID = string(beforeSTS.UID)
		request.Spec.Replicas = 1
		return k8sClient.Update(ctx, &request)
	}); err != nil {
		t.Fatal(err)
	}
	got := waitWorkspaceCondition(t, &request, "OwnerResolved", metav1.ConditionFalse, "AgentUIDMismatch")
	workload := apimeta.FindStatusCondition(got.Status.Conditions, "WorkloadApplied")
	if workload == nil || workload.Status != metav1.ConditionFalse || workload.Reason != "WorkspaceConflict" || workload.ObservedGeneration != request.Generation {
		t.Fatalf("recreated owner did not fence current Workspace generation: %+v", workload)
	}
	afterSTS, afterService := getWorkspaceChildren(t, w)
	if !reflect.DeepEqual(beforeSTS, afterSTS) || !reflect.DeepEqual(beforeService, afterService) {
		t.Fatal("recreated Agent UID mismatch mutated the old Workspace children")
	}
}

func TestWorkspaceKeyFailuresAndProfileEventRecovery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*corev1.Secret)
	}{
		{name: "missing", mutate: nil},
		{name: "empty", mutate: func(secret *corev1.Secret) { secret.Data["key"] = nil }},
		{name: "unowned", mutate: func(secret *corev1.Secret) { secret.OwnerReferences = nil }},
		{name: "prior UID", mutate: func(secret *corev1.Secret) { secret.OwnerReferences[0].UID = "prior-agent-uid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runWorkspaceKeyFailureProfileEventCase(t, tc.name, tc.mutate)
		})
	}
}

func runWorkspaceKeyFailureProfileEventCase(t *testing.T, name string, mutate func(*corev1.Secret)) {
	t.Helper()
	ctx := context.Background()
	tag := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	w, agent, profile := createWorkspaceManagerFixture(t, "key-event-"+tag, 0)
	createManagerWorkspace(t, w)
	waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	beforeSTS, beforeService := getWorkspaceChildren(t, w)
	beforeSTS, beforeService = beforeSTS.DeepCopy(), beforeService.DeepCopy()
	key := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: w.Namespace, Name: workspaceKeySecretName(string(agent.UID))}}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(key), key); err != nil {
		t.Fatal(err)
	}
	if mutate == nil {
		runWorkspaceMissingKeyEventRecovery(t, w, agent, profile, key, beforeSTS, beforeService)
		return
	}
	runWorkspaceInvalidKeyEventRecovery(t, w, agent, profile, key, mutate, beforeSTS, beforeService)
}

func runWorkspaceMissingKeyEventRecovery(t *testing.T, w *achv1alpha1.Workspace, agent *achv1alpha1.ACHAgent, profile *achv1alpha1.AgentProfile, key *corev1.Secret, beforeSTS *appsv1.StatefulSet, beforeService *corev1.Service) {
	t.Helper()
	ctx := context.Background()
	if err := k8sClient.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	// The existing ACHAgent Secret event recreates a missing key. Observe that
	// normal path instead of racing it to manufacture a transient failure.
	var repairedSecret corev1.Secret
	if !Eventually(func() bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(key), &repairedSecret); err != nil {
			return false
		}
		return len(repairedSecret.Data["key"]) > 0 && workspaceKeyOwnedBy(&repairedSecret, agent)
	}, 10*time.Second, 100*time.Millisecond) {
		t.Fatal("ACHAgent event did not recreate the missing owned workspace key")
	}
	if repairedSecret.UID == "" || repairedSecret.UID == key.UID || len(repairedSecret.Data["key"]) == 0 {
		t.Fatalf("recreated workspace key UID/data = %q/%d bytes, want a new UID and nonempty key", repairedSecret.UID, len(repairedSecret.Data["key"]))
	}
	touchWorkspaceProfile(t, profile)
	expectedSTS, err := buildWorkspaceStatefulSet(w, agent, profile, workspaceEngineVerifyKey(repairedSecret.Data["key"]))
	if err != nil {
		t.Fatalf("build repaired-key StatefulSet target: %v", err)
	}
	if !workspaceTemplateDrift(beforeSTS, expectedSTS) {
		t.Fatal("retained pre-repair template matches the repaired-key target")
	}
	if !Eventually(func() bool {
		var current appsv1.StatefulSet
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(beforeSTS), &current); err != nil {
			return false
		}
		return current.UID == beforeSTS.UID && !workspaceTemplateDrift(&current, expectedSTS)
	}, 10*time.Second, 100*time.Millisecond) {
		t.Fatal("Workspace did not apply the repaired-key template while retaining its StatefulSet UID")
	}
	recovered := waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	if recovered.Status.ObservedGeneration != w.Generation || recovered.Status.StatefulSetUID != string(beforeSTS.UID) || recovered.Spec.AgentRef.UID != string(agent.UID) {
		t.Fatalf("missing-key recovery lost current generation or original identity: %+v", recovered.Status)
	}
	afterSTS, afterService := getWorkspaceChildren(t, w)
	if afterSTS.UID != beforeSTS.UID || !reflect.DeepEqual(beforeService, afterService) || afterSTS.Spec.Replicas == nil || *afterSTS.Spec.Replicas != 0 {
		t.Fatal("Agent key recreation replaced child identity, changed Service, or woke the Workspace")
	}
	if len(afterSTS.OwnerReferences) != 1 || afterSTS.OwnerReferences[0].UID != agent.UID || afterSTS.OwnerReferences[0].Kind != "ACHAgent" {
		t.Fatalf("Agent key recreation changed the StatefulSet owner identity: %+v", afterSTS.OwnerReferences)
	}
}

func runWorkspaceInvalidKeyEventRecovery(t *testing.T, w *achv1alpha1.Workspace, agent *achv1alpha1.ACHAgent, profile *achv1alpha1.AgentProfile, key *corev1.Secret, mutate func(*corev1.Secret), beforeSTS *appsv1.StatefulSet, beforeService *corev1.Service) {
	t.Helper()
	ctx := context.Background()
	validKey := key.DeepCopy()
	mutate(key)
	if err := k8sClient.Update(ctx, key); err != nil {
		t.Fatal(err)
	}
	touchWorkspaceProfile(t, profile)
	failed := waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionFalse, "WorkspaceKeyUnavailable")
	if failed.Status.ObservedGeneration != w.Generation {
		t.Fatalf("key failure observed generation %d, want %d", failed.Status.ObservedGeneration, w.Generation)
	}
	afterFailureSTS, afterFailureService := getWorkspaceChildren(t, w)
	if !reflect.DeepEqual(beforeSTS, afterFailureSTS) || !reflect.DeepEqual(beforeService, afterFailureService) {
		t.Fatal("unavailable/invalid key changed a full child object")
	}
	var current corev1.Secret
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(key), &current); err != nil {
		t.Fatal(err)
	}
	current.Data, current.OwnerReferences = validKey.Data, validKey.OwnerReferences
	if err := k8sClient.Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
	touchWorkspaceProfile(t, profile)
	recovered := waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	if recovered.Status.ObservedGeneration != w.Generation || recovered.Status.StatefulSetUID != string(beforeSTS.UID) || recovered.Spec.AgentRef.UID != string(agent.UID) {
		t.Fatalf("valid key repair lost current generation or original identity: %+v", recovered.Status)
	}
	afterRecoverySTS, afterRecoveryService := getWorkspaceChildren(t, w)
	if !reflect.DeepEqual(beforeSTS, afterRecoverySTS) || !reflect.DeepEqual(beforeService, afterRecoveryService) {
		t.Fatal("valid key repair/profile event needlessly changed a full child object")
	}
}

func touchWorkspaceProfile(t *testing.T, profile *achv1alpha1.AgentProfile) {
	t.Helper()
	ctx := context.Background()
	var current achv1alpha1.AgentProfile
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(profile), &current); err != nil {
		t.Fatal(err)
	}
	if current.Annotations == nil {
		current.Annotations = make(map[string]string)
	}
	current.Annotations["runtime.ach.ackstorm.ai/workspace-test-event"] = fmt.Sprintf("%d", time.Now().UnixNano())
	if err := k8sClient.Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceEmptyImageAdmissionLeavesChildrenUnchanged(t *testing.T) {
	w, _, profile := createWorkspaceManagerFixture(t, "empty-image-admission", 0)
	createManagerWorkspace(t, w)
	waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	beforeSTS, beforeService := getWorkspaceChildren(t, w)
	beforeSTS, beforeService = beforeSTS.DeepCopy(), beforeService.DeepCopy()

	profile.Spec.Execution.Image = ""
	err := k8sClient.Update(context.Background(), profile)
	if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "spec.execution.image") {
		t.Fatalf("empty execution image update error = %v, want admission rejection at spec.execution.image", err)
	}
	afterSTS, afterService := getWorkspaceChildren(t, w)
	if !reflect.DeepEqual(beforeSTS, afterSTS) || !reflect.DeepEqual(beforeService, afterService) {
		t.Fatal("rejected empty-image Profile update changed a full child object")
	}
}

func TestWorkspaceReadyGeneration(t *testing.T) {
	w, _, _ := createWorkspaceManagerFixture(t, "ready-gen", 1)
	createManagerWorkspace(t, w)
	waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	sts, _ := getWorkspaceChildren(t, w)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: w.Name + "-0", Namespace: w.Namespace, Labels: sts.Spec.Template.Labels, Annotations: sts.Spec.Template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: sts.Name, UID: sts.UID}}}, Spec: sts.Spec.Template.Spec}
	mustApply(t, context.Background(), pod)
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), pod); err != nil {
		t.Fatal(err)
	}
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	if err := k8sClient.Status().Update(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	sts.Status.Replicas, sts.Status.ReadyReplicas, sts.Status.ObservedGeneration = 1, 1, sts.Generation-1
	if err := k8sClient.Status().Update(context.Background(), sts); err != nil {
		t.Fatal(err)
	}
	waitWorkspaceCondition(t, w, "WorkloadReady", metav1.ConditionFalse, "NotReady")
	sts.Status.ObservedGeneration = sts.Generation
	if err := k8sClient.Status().Update(context.Background(), sts); err != nil {
		t.Fatal(err)
	}
	waitWorkspaceCondition(t, w, "WorkloadReady", metav1.ConditionTrue, "Ready")
	ready := waitWorkspaceCondition(t, w, "WorkloadReady", metav1.ConditionTrue, "Ready")
	for _, conditionType := range []string{"OwnerResolved", "WorkloadApplied", "WorkloadReady", "UpdatePending"} {
		condition := apimeta.FindStatusCondition(ready.Status.Conditions, conditionType)
		if condition == nil || condition.ObservedGeneration != ready.Generation {
			t.Fatalf("condition %s missing or stale on ready generation: %+v", conditionType, condition)
		}
	}
	beforeSTS := sts.DeepCopy()
	pod.Labels[workspaceAgentUIDLabel] = "different-agent"
	if err := k8sClient.Update(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	invalid := waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionFalse, "WorkspaceConflict")
	if condition := apimeta.FindStatusCondition(invalid.Status.Conditions, "WorkloadReady"); condition == nil || condition.Status != metav1.ConditionUnknown {
		t.Fatalf("invalid Pod identity retained readiness: %+v", condition)
	}
	for _, conditionType := range []string{"OwnerResolved", "WorkloadApplied", "WorkloadReady", "UpdatePending"} {
		condition := apimeta.FindStatusCondition(invalid.Status.Conditions, conditionType)
		if condition == nil || condition.ObservedGeneration != invalid.Generation {
			t.Fatalf("condition %s missing or stale after invalid Pod: %+v", conditionType, condition)
		}
	}
	afterSTS, _ := getWorkspaceChildren(t, w)
	if !reflect.DeepEqual(beforeSTS, afterSTS) {
		t.Fatal("invalid current Pod identity changed the full StatefulSet")
	}
}

func TestWorkspaceDeleteRetainsWorkloads(t *testing.T) {
	w, agent, _ := createWorkspaceManagerFixture(t, "delete-retain", 1)
	createManagerWorkspace(t, w)
	waitWorkspaceCondition(t, w, "WorkloadApplied", metav1.ConditionTrue, "Applied")
	sts, svc := getWorkspaceChildren(t, w)
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Namespace: w.Namespace, Name: workspaceKeySecretName(string(agent.UID))}
	if err := k8sClient.Get(context.Background(), secretKey, secret); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: w.Name + "-0", Namespace: w.Namespace, Labels: sts.Spec.Template.Labels, Annotations: sts.Spec.Template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: sts.Name, UID: sts.UID}}}, Spec: sts.Spec.Template.Spec}
	mustApply(t, context.Background(), pod)
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), pod); err != nil {
		t.Fatal(err)
	}
	beforeSTS, beforeService, beforePod, beforeSecret := sts.DeepCopy(), svc.DeepCopy(), pod.DeepCopy(), secret.DeepCopy()
	if len(w.Finalizers) != 0 {
		t.Fatalf("Workspace has unexpected finalizers: %v", w.Finalizers)
	}
	if err := k8sClient.Delete(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if !Eventually(func() bool {
		var gone achv1alpha1.Workspace
		return apierrors.IsNotFound(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(w), &gone))
	}, 5*time.Second, 100*time.Millisecond) {
		t.Fatal("Workspace CR was not deleted")
	}
	var afterSTS appsv1.StatefulSet
	var afterService corev1.Service
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(sts), &afterSTS); err != nil {
		t.Fatalf("retained StatefulSet missing: %v", err)
	}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(svc), &afterService); err != nil {
		t.Fatalf("retained Service missing: %v", err)
	}
	var afterPod corev1.Pod
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &afterPod); err != nil {
		t.Fatalf("retained Pod missing: %v", err)
	}
	var afterSecret corev1.Secret
	if err := k8sClient.Get(context.Background(), secretKey, &afterSecret); err != nil {
		t.Fatalf("retained key Secret missing: %v", err)
	}
	if !reflect.DeepEqual(beforeSTS, &afterSTS) || !reflect.DeepEqual(beforeService, &afterService) || !reflect.DeepEqual(beforePod, &afterPod) || !reflect.DeepEqual(beforeSecret, &afterSecret) {
		t.Fatal("Workspace deletion changed a retained StatefulSet, Service, Pod, or key Secret")
	}
}

func differingWorkspaceStructFields(left, right any) []string {
	var differences []string
	var walk func(reflect.Value, reflect.Value, string)
	walk = func(a, b reflect.Value, path string) {
		if a.Type() != b.Type() {
			differences = append(differences, path)
			return
		}
		if a.Kind() == reflect.Pointer {
			if a.IsNil() || b.IsNil() {
				if a.IsNil() != b.IsNil() {
					differences = append(differences, path)
				}
				return
			}
			walk(a.Elem(), b.Elem(), path)
			return
		}
		switch a.Kind() {
		case reflect.Struct:
			for i := 0; i < a.NumField(); i++ {
				field := a.Type().Field(i)
				if field.PkgPath != "" {
					continue
				}
				fieldPath := field.Name
				if path != "" {
					fieldPath = path + "." + field.Name
				}
				walk(a.Field(i), b.Field(i), fieldPath)
			}
		case reflect.Slice:
			if a.Len() != b.Len() {
				differences = append(differences, path+".len")
				return
			}
			for i := 0; i < a.Len(); i++ {
				walk(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		default:
			if !reflect.DeepEqual(a.Interface(), b.Interface()) {
				differences = append(differences, path)
			}
		}
	}
	walk(reflect.ValueOf(left), reflect.ValueOf(right), "")
	return differences
}

func immutableWorkspaceUpdate(name string, mutate func(map[string]any), field string) func(*testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		u := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
			"agentRef": map[string]any{"name": "demo", "uid": testWorkspaceAgentUID}, "workspaceRef": testWorkspaceRef, "replicas": int64(0),
		}}}
		u.SetGroupVersionKind(achv1alpha1.GroupVersion.WithKind("Workspace"))
		u.SetNamespace(WatchNamespace)
		u.SetName("ws-admit-" + name)
		if err := k8sClient.Create(ctx, u); err != nil {
			t.Fatalf("create: %v", err)
		}
		spec, _, _ := unstructured.NestedMap(u.Object, "spec")
		mutate(spec)
		if err := unstructured.SetNestedMap(u.Object, spec, "spec"); err != nil {
			t.Fatal(err)
		}
		err := k8sClient.Update(ctx, u)
		if !apierrors.IsInvalid(err) {
			t.Fatalf("%s: want Invalid, got %v", name, err)
		}
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("%s: missing field diagnostic %q: %v", name, field, err)
		}
		if err := k8sClient.Delete(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
}
