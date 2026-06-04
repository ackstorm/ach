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

func TestFormatAdminInventory_PluginsSourceColumn(t *testing.T) {
	out := FormatAdminInventory(map[string][]AdminObjectView{
		"plugins": {
			{Kind: "plugin", Name: "frontend-design", Version: "5", Sync: "fresh", Extra: map[string]string{"source": "plugin"}},
			{Kind: "plugin", Name: "branding@ackstorm", Version: "b899e89", Sync: "fresh", Extra: map[string]string{"source": "marketplace"}},
		},
	})
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "SOURCE") {
		t.Errorf("plugins header missing SOURCE column:\n%s", out)
	}
	if !strings.Contains(out, "branding@ackstorm") || !strings.Contains(out, "marketplace") {
		t.Errorf("marketplace-sourced plugin row missing:\n%s", out)
	}
	if !strings.Contains(out, "frontend-design") || !strings.Contains(out, "plugin") {
		t.Errorf("standalone plugin row missing:\n%s", out)
	}
}

func TestFormatAdminInventory_MarketplacesStatusAndCount(t *testing.T) {
	out := FormatAdminInventory(map[string][]AdminObjectView{
		"marketplaces": {
			{Kind: "marketplace", Namespace: "ach", Name: "ackstorm", Version: "100",
				Sync: "Synced", Extra: map[string]string{"pluginsCount": "12"}},
		},
	})
	if !strings.Contains(out, "STATUS") || !strings.Contains(out, "PLUGINS") {
		t.Errorf("marketplaces header missing STATUS/PLUGINS columns:\n%s", out)
	}
	if !strings.Contains(out, "Synced") || !strings.Contains(out, "12") {
		t.Errorf("marketplace status/count missing:\n%s", out)
	}
}
