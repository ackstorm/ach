// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

func TestFormatEkList_Empty(t *testing.T) {
	got := FormatEkList(nil)
	if !strings.Contains(got, "No env-keys found") {
		t.Errorf("FormatEkList([]) = %q; want 'No env-keys found'", got)
	}
}

func TestFormatEkList_RendersAllColumns(t *testing.T) {
	rows := []EkRowView{
		{
			KeyID:       "ekid_abc",
			Environment: "demo",
			Name:        "local-laptop",
			OwnerEmail:  "u@example",
			Status:      "active",
			CreatedAt:   "2026-05-28T10:00:00Z",
		},
	}
	got := FormatEkList(rows)
	for _, want := range []string{
		"KEY-ID", "OWNER", "ENVIRONMENT", "NAME", "STATUS", "CREATED",
		"ekid_abc", "u@example", "demo", "local-laptop", "active", "2026-05-28T10:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatEkList output missing %q; got:\n%s", want, got)
		}
	}
}

func TestFormatEkList_DeterministicOrderByKeyID(t *testing.T) {
	rows := []EkRowView{
		{KeyID: "ekid_zzz", Environment: "prod", Name: "z", OwnerEmail: "z@e", Status: "active", CreatedAt: "2026-05-28T12:00:00Z"},
		{KeyID: "ekid_aaa", Environment: "dev", Name: "a", OwnerEmail: "a@e", Status: "active", CreatedAt: "2026-05-28T11:00:00Z"},
		{KeyID: "ekid_mmm", Environment: "stg", Name: "m", OwnerEmail: "m@e", Status: "revoked", CreatedAt: "2026-05-28T13:00:00Z"},
	}
	got := FormatEkList(rows)
	aaaIdx := strings.Index(got, "ekid_aaa")
	mmmIdx := strings.Index(got, "ekid_mmm")
	zzzIdx := strings.Index(got, "ekid_zzz")
	if aaaIdx < 0 || mmmIdx < 0 || zzzIdx < 0 {
		t.Fatalf("expected all three KEY-IDs in output; got:\n%s", got)
	}
	if !(aaaIdx < mmmIdx && mmmIdx < zzzIdx) {
		t.Errorf("rows not sorted by KEY-ID ascending; got:\n%s", got)
	}
}
