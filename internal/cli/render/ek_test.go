// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

func TestFormatKeyList_RendersTypeColumnForBothKinds(t *testing.T) {
	rows := []KeyRowView{
		{KeyID: "ekid_abc", Type: "ek", Environment: "demo", Name: "laptop", OwnerEmail: "u@example", Status: "active", CreatedAt: "2026-05-28T10:00:00Z"},
		{KeyID: "pkid_xyz", Type: "pk", OwnerEmail: "u@example", Status: "active", CreatedAt: "2026-05-27T10:00:00Z"},
	}
	got := FormatKeyList(rows)
	for _, want := range []string{
		"KEY-ID", "TYPE", "OWNER", "ENVIRONMENT", "NAME", "STATUS", "CREATED",
		"ekid_abc", "ek", "demo", "laptop",
		"pkid_xyz", "pk",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatKeyList output missing %q; got:\n%s", want, got)
		}
	}
}

func TestFormatKeyList_Empty(t *testing.T) {
	if got := FormatKeyList(nil); !strings.Contains(got, "No keys found") {
		t.Errorf("empty render = %q; want contains \"No keys found\"", got)
	}
}
