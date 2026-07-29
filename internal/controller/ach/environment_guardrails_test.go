// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// TestGuardrailUnresolvedBlocksEkProvisioning is the P0-A barrier test.
//
// LiteLLM accepts an unknown guardrail name into a team's metadata and simply
// never runs it, so a typo is a silent fail-open hole. ACH converts that into a
// refusal to hand out NEW environment keys: the unresolved name fails
// AccessGroupSynced, and POST /platform/keys returns 503 not_ready when that
// condition is not True (internal/platformapi/envkeys/handler.go:301).
//
// This asserts BOTH conditions on purpose. ExecutionResourcesResolved alone is
// only a label — nothing reads it. AccessGroupSynced is the gate.
//
// It does NOT assert that hydrate or forwarded traffic is blocked, because
// neither is: hydrate reads no conditions and the forwarder checks only
// DeletionTimestamp and name membership. Existing keys keep serving.
func TestGuardrailUnresolvedBlocksEkProvisioning(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-guardrail-typo",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			// The suite's LiteLLM fake reports no guardrails, so any declared
			// name is unresolved by construction.
			Runtime: achv1alpha1.RuntimeBlock{Guardrails: []string{"does-not-exist"}},
			Context: achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	var got achv1alpha1.Environment
	var exec, ag *metav1.Condition
	ok := Eventually(func() bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		exec = apimeta.FindStatusCondition(got.Status.Conditions, "ExecutionResourcesResolved")
		ag = apimeta.FindStatusCondition(got.Status.Conditions, "AccessGroupSynced")
		return exec != nil && ag != nil &&
			exec.Status == metav1.ConditionFalse && ag.Status == metav1.ConditionFalse
	}, 15*time.Second, 250*time.Millisecond)
	if !ok {
		t.Fatalf("conditions never both False within 15s: exec=%+v ag=%+v", exec, ag)
	}

	if exec.Reason != reasonResourceUnresolved {
		t.Errorf("ExecutionResourcesResolved reason = %q, want ResourceUnresolved", exec.Reason)
	}
	if !strings.Contains(exec.Message, "guardrails=1") {
		t.Errorf("message %q missing guardrails=1", exec.Message)
	}
	if ag.Reason != "UnresolvedReferences" {
		t.Errorf("AccessGroupSynced reason = %q, want UnresolvedReferences", ag.Reason)
	}
	if !strings.Contains(ag.Message, "does-not-exist") {
		t.Errorf("AccessGroupSynced message %q should name the offending guardrail", ag.Message)
	}

	if got.Status.UnresolvedRuntime == nil ||
		len(got.Status.UnresolvedRuntime.Guardrails) != 1 ||
		got.Status.UnresolvedRuntime.Guardrails[0] != "does-not-exist" {
		t.Errorf("unresolvedRuntime.guardrails = %+v", got.Status.UnresolvedRuntime)
	}

	if avail := apimeta.FindStatusCondition(got.Status.Conditions, "Available"); avail != nil &&
		avail.Status == metav1.ConditionTrue {
		t.Error("Available must not be True with an unresolved guardrail")
	}
}

// TestGuardrailResolvedAttachesToShellTeam exercises the full Reconcile ->
// reconcileAccessGroup -> ensureShellTeam chain with a guardrail name that
// DOES resolve — the counterpart to TestGuardrailUnresolvedBlocksEkProvisioning
// (unresolved) and TestEnsureShellTeamCarriesGuardrails (shell-team unit,
// bypasses snap.Guardrails name-matching). Without this, a regression in
// guardrailsByName or the snapshot wiring would pass envtest silently.
func TestGuardrailResolvedAttachesToShellTeam(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")
	accessGroupFake.SeedGuardrail("pii-filter")
	envSnapshotter.RefreshForTest(ctx)
	t.Cleanup(func() {
		accessGroupFake.Reset()
		envSnapshotter.RefreshForTest(context.Background())
	})

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-guardrail-resolved",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{Guardrails: []string{"pii-filter"}},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	var final *metav1.Condition
	ok := Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		final = apimeta.FindStatusCondition(got.Status.Conditions, "Available")
		return final != nil && final.Status == metav1.ConditionTrue
	}, 15*time.Second, 250*time.Millisecond)
	if !ok {
		t.Fatalf("Available never True within 15s: %+v", final)
	}

	shellAlias := "ach-env-test-env-guardrail-resolved"
	req, ok := accessGroupFake.lastTeamCreate[shellAlias]
	if !ok {
		t.Fatalf("no CreateTeam recorded for shell %s", shellAlias)
	}
	if !slices.Equal(req.Guardrails, []string{"pii-filter"}) {
		t.Errorf("shell team CreateTeam.Guardrails = %v, want [pii-filter]", req.Guardrails)
	}
}

// TestNoGuardrailsDoesNotBlock: the axis is opt-in — an Environment declaring
// none must reconcile to Available exactly as before this feature existed.
func TestNoGuardrailsDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-guardrail-absent",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context:         achv1alpha1.ContextBlock{},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	var final *metav1.Condition
	ok := Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		final = apimeta.FindStatusCondition(got.Status.Conditions, "Available")
		return final != nil && final.Status == metav1.ConditionTrue
	}, 15*time.Second, 250*time.Millisecond)
	if !ok {
		t.Fatalf("Available never True within 15s: %+v", final)
	}
}
