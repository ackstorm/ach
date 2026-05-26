// SPDX-License-Identifier: Apache-2.0

// Tests for TODO §9: Environment Available composite-condition rollup.
//
// The helper under test is pure (no k8s client, no DB) so this file is a
// stdlib `testing` table-driven unit test — NO envtest, NO suite_test
// fixtures. It runs in milliseconds via `go test ./internal/controller/ach/
// -run TestComputeAvailable`.

package ach

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// Silence unused-import for metav1 — referenced once Task 3 lands.
	_ = metav1.ConditionTrue
}
