// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func TestBuildSkillMarketplaceRow_MapsStatusToRow(t *testing.T) {
	cr := &achv1alpha1.SkillMarketplace{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ach", Name: "agentskills", ResourceVersion: "42"},
	}
	cr.Status.SkillsCount = 9

	row := buildSkillMarketplaceRow(cr, "True", "")
	if row.Namespace != "ach" || row.Name != "agentskills" || row.SkillsCount != 9 || row.SyncedStatus != "True" {
		t.Errorf("row = %+v, want ach/agentskills/9/True", row)
	}
	if row.ResourceVersion != "42" {
		t.Errorf("row.ResourceVersion = %q, want 42", row.ResourceVersion)
	}
	bad := buildSkillMarketplaceRow(cr, "False", "UpstreamInvalid")
	if bad.SyncedStatus != "False" || bad.SyncedReason != "UpstreamInvalid" {
		t.Errorf("row = %+v, want False/UpstreamInvalid", bad)
	}
}

func TestFormatSkillStage2Message(t *testing.T) {
	if got := formatSkillStage2Message(nil); got != "" {
		t.Errorf("empty failures = %q, want empty", got)
	}
	one := formatSkillStage2Message([]skillFailure{{name: "pdf", reason: "UpstreamInvalid"}})
	if one != "stage-2: 1 skill(s) failed: pdf: UpstreamInvalid" {
		t.Errorf("one failure = %q", one)
	}
	many := formatSkillStage2Message([]skillFailure{
		{"a", "X"}, {"b", "X"}, {"c", "X"}, {"d", "X"}, {"e", "X"}, {"f", "X"}, {"g", "X"},
	})
	if want := "stage-2: 7 skill(s) failed: a: X, b: X, c: X, d: X, e: X, +2 more"; many != want {
		t.Errorf("many failures = %q, want %q", many, want)
	}
}
