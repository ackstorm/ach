// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

func TestFormatAdminKeyList_RendersTypeColumnForBothKinds(t *testing.T) {
	rows := []KeyRowView{
		{KeyID: "ekid_abc", Type: "ek", Environment: "demo", Name: "laptop", OwnerEmail: "u@example", Status: "active", CreatedAt: "2026-05-28T10:00:00Z"},
		{KeyID: "pkid_xyz", Type: "pk", OwnerEmail: "u@example", Status: "active", CreatedAt: "2026-05-27T10:00:00Z"},
	}
	got := FormatAdminKeyList(rows)
	for _, want := range []string{
		"KEY-ID", "TYPE", "OWNER", "ENVIRONMENT", "NAME", "STATUS", "CREATED",
		"ekid_abc", "ek", "demo", "laptop",
		"pkid_xyz", "pk",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatAdminKeyList output missing %q; got:\n%s", want, got)
		}
	}
}

func TestFormatAdminKeyList_Empty(t *testing.T) {
	if got := FormatAdminKeyList(nil); !strings.Contains(got, "No keys found") {
		t.Errorf("empty render = %q; want contains \"No keys found\"", got)
	}
}

// TestFormatKeyList_PkRowShowsEmDashForEnvironmentAndName asserts that pk_ rows
// with empty Environment and Name cells render as — (em dash) rather than blank.
func TestFormatAdminKeyList_PkRowShowsEmDashForEnvironmentAndName(t *testing.T) {
	rows := []KeyRowView{
		{KeyID: "pkid_xyz", Type: "pk", OwnerEmail: "u@example", Status: "active", CreatedAt: "2026-05-27T10:00:00Z"},
	}
	got := FormatAdminKeyList(rows)
	// The em dash must appear at least twice (once for ENVIRONMENT, once for NAME).
	count := strings.Count(got, emDash)
	if count < 2 {
		t.Errorf("expected at least 2 em dash (—) for empty pk_ env/name cells; got %d in:\n%s", count, got)
	}
	// Blank tab runs (consecutive \t without content) must not appear for env/name.
	// A blank cell in the tabwriter output would look like two adjacent tab-stops
	// with nothing between them. We can't directly assert tab bytes here since
	// tabwriter aligns with spaces — but we can assert the em dash is present.
	if !strings.Contains(got, emDash) {
		t.Errorf("expected em dash in output for pk_ row; got:\n%s", got)
	}
}

// TestFormatKeyList_NewestFirstSort asserts rows are ordered newest-first by CreatedAt.
func TestFormatAdminKeyList_NewestFirstSort(t *testing.T) {
	rows := []KeyRowView{
		{KeyID: "ekid_old", Type: "ek", Environment: "demo", Name: "old", OwnerEmail: "u@x", Status: "active", CreatedAt: "2026-05-01T00:00:00Z"},
		{KeyID: "pkid_new", Type: "pk", OwnerEmail: "u@x", Status: "active", CreatedAt: "2026-06-01T00:00:00Z"},
		{KeyID: "ekid_mid", Type: "ek", Environment: "staging", Name: "mid", OwnerEmail: "u@x", Status: "active", CreatedAt: "2026-05-15T00:00:00Z"},
	}
	got := FormatAdminKeyList(rows)

	posNew := strings.Index(got, "pkid_new")
	posMid := strings.Index(got, "ekid_mid")
	posOld := strings.Index(got, "ekid_old")

	if posNew < 0 || posMid < 0 || posOld < 0 {
		t.Fatalf("one or more key IDs missing from output:\n%s", got)
	}
	if !(posNew < posMid && posMid < posOld) {
		t.Errorf("expected newest-first order (pkid_new < ekid_mid < ekid_old); got positions: new=%d mid=%d old=%d\n%s",
			posNew, posMid, posOld, got)
	}
}

func TestFormatKeyList_EnvKeyColumns(t *testing.T) {
	exp := "2026-12-31T00:00:00Z"
	got := FormatKeyList([]KeyRowView{
		{KeyID: "ekid_a", Environment: "demo", Name: "laptop", Status: "active", ExpiresAt: &exp, CreatedAt: "2026-05-02T00:00:00Z"},
		{KeyID: "ekid_b", Environment: "demo", Name: "ci", Status: "suspended", CreatedAt: "2026-05-01T00:00:00Z"},
	})
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 || strings.Join(strings.Fields(lines[0]), " ") != "NAME ENVIRONMENT STATUS EXPIRES ID" {
		t.Fatalf("header/rows wrong:\n%s", got)
	}
	if strings.Join(strings.Fields(lines[1]), " ") != "laptop demo active "+exp+" ekid_a" ||
		strings.Join(strings.Fields(lines[2]), " ") != "ci demo suspended never ekid_b" {
		t.Fatalf("rows wrong:\n%s", got)
	}
	if strings.Contains(got, "OWNER") || strings.Contains(got, "TYPE") {
		t.Fatalf("user list must not carry the admin columns:\n%s", got)
	}
}
