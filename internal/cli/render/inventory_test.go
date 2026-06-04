// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

func TestFormatAdminInventory_Empty(t *testing.T) {
	if got := FormatAdminInventory(map[string][]AdminObjectView{}); got != "No objects found\n" {
		t.Errorf("got %q, want 'No objects found'", got)
	}
}

// TestFormatAdminInventory_FootnoteOnFalseGreen: a fresh* row triggers the
// prompts/artifacts footnote; the header + row render.
func TestFormatAdminInventory_FootnoteOnFalseGreen(t *testing.T) {
	out := FormatAdminInventory(map[string][]AdminObjectView{
		"prompts": {{Kind: "prompt", Name: "greet", Namespace: "ach", Version: "1", Sync: "fresh*"}},
	})
	for _, want := range []string{"PROMPTS (1)", "greet", "fresh*", "content presence is not gated"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestFormatAdminInventory_NoFootnoteWhenClean: no fresh* row → no footnote.
func TestFormatAdminInventory_NoFootnoteWhenClean(t *testing.T) {
	out := FormatAdminInventory(map[string][]AdminObjectView{
		"plugins": {{Kind: "plugin", Name: "a", Sync: "fresh"}},
	})
	if strings.Contains(out, "not gated") {
		t.Errorf("unexpected footnote:\n%s", out)
	}
}

// TestSyncCell: reason is appended in parentheses when present.
func TestSyncCell(t *testing.T) {
	if got := syncCell(AdminObjectView{Sync: "STALE", SyncReason: "2h over"}); got != "STALE(2h over)" {
		t.Errorf("got %q, want STALE(2h over)", got)
	}
	if got := syncCell(AdminObjectView{Sync: "fresh"}); got != "fresh" {
		t.Errorf("got %q, want fresh", got)
	}
}

// TestFormatAdminInventory_SortsByName: rows within a group sort by name.
func TestFormatAdminInventory_SortsByName(t *testing.T) {
	out := FormatAdminInventory(map[string][]AdminObjectView{
		"plugins": {
			{Name: "zeta", Namespace: "ach", Sync: "fresh"},
			{Name: "alpha", Namespace: "ach", Sync: "fresh"},
		},
	})
	if strings.Index(out, "alpha") > strings.Index(out, "zeta") {
		t.Errorf("rows not sorted by name:\n%s", out)
	}
}

// TestDash: empty cells render "-".
func TestDash(t *testing.T) {
	if dash("") != "-" || dash("x") != "x" {
		t.Errorf("dash wrong: %q %q", dash(""), dash("x"))
	}
}
