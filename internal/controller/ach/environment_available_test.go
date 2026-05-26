// SPDX-License-Identifier: Apache-2.0

// Tests for TODO §9: Environment Available composite-condition rollup.
//
// The helper under test is pure (no k8s client, no DB) so this file is a
// stdlib `testing` table-driven unit test — NO envtest, NO suite_test
// fixtures. It runs in milliseconds via `go test ./internal/controller/ach/
// -run TestComputeAvailable`.

package ach

import (
	"context"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// TestAvailableReasonConstantsExist is a compile-time check that the three
// reason constants required by the rollup helper exist with the documented
// string values. The constants are referenced by computeAvailable AND by
// the §16 acceptance test (TODO.md:505). If a future cleanup pass renames
// them, this test catches the divergence before §16 fails.
func TestAvailableReasonConstantsExist(t *testing.T) {
	cases := []struct {
		name, got, want string
	}{
		{"AllSubConditionsTrue", ReasonAllSubConditionsTrue, "AllSubConditionsTrue"},
		{"SubConditionsNotReady", ReasonSubConditionsNotReady, "SubConditionsNotReady"},
		{"PendingSubConditions", ReasonPendingSubConditions, "PendingSubConditions"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q; want %q", tc.name, tc.got, tc.want)
		}
	}
	// metav1 is consumed by the table test below.
	_ = metav1.ConditionTrue
}

// TestComputeAvailable is the §9 rollup contract. Two sub-conditions are
// REQUIRED for the rollup to be True: AccessGroupSynced and
// ExecutionResourcesResolved. ContentReady is OPTIONAL (no reconciler in
// Phase 2 writes it; absence does not degrade the rollup) per the plan's
// "Mandatory-vs-optional" decision.
//
// Precedence rules (any False > any Unknown > all True):
//   - any REQUIRED sub-condition False → Available=False, reason=SubConditionsNotReady
//   - else any REQUIRED sub-condition Unknown or missing → Available=Unknown,
//     reason=PendingSubConditions
//   - all REQUIRED sub-conditions True → Available=True, reason=AllSubConditionsTrue
//
// The helper is pure: input is the conditions slice from
// env.Status.Conditions; output is a metav1.Condition the caller pushes
// back into the slice via apimeta.SetStatusCondition.
func TestComputeAvailable(t *testing.T) {
	cond := func(typ string, status metav1.ConditionStatus) metav1.Condition {
		return metav1.Condition{Type: typ, Status: status, Reason: "TestSeed"}
	}

	cases := []struct {
		name       string
		in         []metav1.Condition
		wantStatus metav1.ConditionStatus
		wantReason string
		// wantMsgContains is a substring assertion — the helper composes
		// a human-readable message that names the failing sub-conditions,
		// but the exact wording is not load-bearing for the rollup logic.
		wantMsgContains string
	}{
		{
			name: "all required True → Available True",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionTrue),
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonAllSubConditionsTrue,
		},
		{
			name: "all required True + optional ContentReady True → Available True",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionTrue),
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
				cond("ContentReady", metav1.ConditionTrue),
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonAllSubConditionsTrue,
		},
		{
			name: "ContentReady missing entirely → still True (optional, not blocking)",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionTrue),
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
				// ContentReady absent — plan's optional-sub-condition decision.
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonAllSubConditionsTrue,
		},
		{
			name: "one required False → Available False with reason SubConditionsNotReady",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionTrue),
				cond("ExecutionResourcesResolved", metav1.ConditionFalse),
			},
			wantStatus:      metav1.ConditionFalse,
			wantReason:      ReasonSubConditionsNotReady,
			wantMsgContains: "ExecutionResourcesResolved",
		},
		{
			name: "False beats Unknown — False outcome wins precedence",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionUnknown),
				cond("ExecutionResourcesResolved", metav1.ConditionFalse),
			},
			wantStatus:      metav1.ConditionFalse,
			wantReason:      ReasonSubConditionsNotReady,
			wantMsgContains: "ExecutionResourcesResolved",
		},
		{
			name: "one required Unknown, none False → Available Unknown",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionUnknown),
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
			},
			wantStatus:      metav1.ConditionUnknown,
			wantReason:      ReasonPendingSubConditions,
			wantMsgContains: "AccessGroupSynced",
		},
		{
			name: "required sub-condition missing entirely → Available Unknown (treated as Unknown)",
			in: []metav1.Condition{
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
				// AccessGroupSynced absent — required types not yet written
				// (e.g. pre-J.6, before placeholder commits).
			},
			wantStatus:      metav1.ConditionUnknown,
			wantReason:      ReasonPendingSubConditions,
			wantMsgContains: "AccessGroupSynced",
		},
		{
			name:            "empty conditions → Available Unknown",
			in:              []metav1.Condition{},
			wantStatus:      metav1.ConditionUnknown,
			wantReason:      ReasonPendingSubConditions,
			wantMsgContains: "AccessGroupSynced",
		},
		{
			name: "pre-existing Available in input is ignored (helper never reads its own type)",
			in: []metav1.Condition{
				cond("AccessGroupSynced", metav1.ConditionTrue),
				cond("ExecutionResourcesResolved", metav1.ConditionTrue),
				cond("Available", metav1.ConditionFalse), // stale prior write
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: ReasonAllSubConditionsTrue,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeAvailable(tc.in)
			if got.Type != "Available" {
				t.Errorf("Type = %q; want %q", got.Type, "Available")
			}
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q; want %q (message=%q)", got.Status, tc.wantStatus, got.Message)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q; want %q (message=%q)", got.Reason, tc.wantReason, got.Message)
			}
			if tc.wantMsgContains != "" && !contains(got.Message, tc.wantMsgContains) {
				t.Errorf("Message = %q; want substring %q", got.Message, tc.wantMsgContains)
			}
		})
	}
}

// contains is a tiny strings.Contains alias kept local so the test file
// imports stay minimal (no `strings` import for one helper).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestEnvironmentAvailableConditionEmitted verifies that a real
// Reconcile cycle against envtest writes an Available condition into
// env.Status.Conditions, with the rollup outcome the steady-state
// branch produces today.
//
// The suite wires a real Snapshotter (suite_test.go:252) AND the
// accessGroupFake (which BindTeam succeeds by default), so the
// steady-state branch runs end-to-end: ExecutionResourcesResolved=True
// (empty runtime → no unresolved names), AccessGroupSynced=True
// (fake create + bind succeed), and computeAvailable rolls up to
// Available=True reason=AllSubConditionsTrue.
//
// This proves Task 4's wiring landed AND that §7's True-path writer
// composes correctly with the §9 rollup. If the assertion ever flips
// back to Unknown, either §7's writer regressed or the rollup contract
// changed — both cases warrant investigation.
func TestEnvironmentAvailableConditionEmitted(t *testing.T) {
	ctx := context.Background()
	accessGroupFake.Reset()

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-available",
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

	// Poll for the Available condition to appear AND reach True. The
	// reconciler emits all three sub-conditions + Available in a single
	// Status().Update, so they land atomically — informer cache settles
	// within ~250ms.
	var final *metav1.Condition
	ok := Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		avail := apimeta.FindStatusCondition(got.Status.Conditions, "Available")
		if avail == nil {
			return false
		}
		final = avail
		return avail.Status == metav1.ConditionTrue
	}, 15*time.Second, 250*time.Millisecond)

	if !ok {
		if final == nil {
			t.Fatalf("Available condition never written within 15s")
		}
		t.Fatalf("Available condition never reached True within 15s: status=%s reason=%s message=%q",
			final.Status, final.Reason, final.Message)
	}
	if final.Reason != ReasonAllSubConditionsTrue {
		t.Errorf("Available.Reason = %q; want %q (message=%q)",
			final.Reason, ReasonAllSubConditionsTrue, final.Message)
	}
	t.Logf("OK: Available=%s reason=%s message=%q",
		final.Status, final.Reason, final.Message)
}
