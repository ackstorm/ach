// SPDX-License-Identifier: Apache-2.0

// Plan 02-06 Task 2: pure-Go unit tests for resolveConflicts. The List
// helpers (listPluginCRNames / listOtherMarketplaceCatalogs) require
// envtest and are exercised indirectly through the Task 4 envtest suite.

package ach

import (
	"strings"
	"testing"
)

func candidate(name string) ClaudeCodeMarketplacePlugin {
	return ClaudeCodeMarketplacePlugin{Name: name}
}

func TestResolveConflicts_PluginCRDWins(t *testing.T) {
	pluginCRs := map[string]struct{}{"shared": {}}
	decisions := resolveConflicts(
		"aaa",
		[]ClaudeCodeMarketplacePlugin{candidate("shared"), candidate("unique")},
		map[string][]string{},
		pluginCRs,
	)
	if len(decisions) != 2 {
		t.Fatalf("expected 2 decisions; got %d", len(decisions))
	}
	if decisions[0].PluginName != "shared" || decisions[0].Kept {
		t.Errorf("shared: expected Kept=false; got %+v", decisions[0])
	}
	if !strings.Contains(decisions[0].Reason, "Plugin CRD") {
		t.Errorf("shared reason should mention 'Plugin CRD'; got %q", decisions[0].Reason)
	}
	if decisions[1].PluginName != "unique" || !decisions[1].Kept {
		t.Errorf("unique: expected Kept=true; got %+v", decisions[1])
	}
}

func TestResolveConflicts_AlphabeticalPriorityIWin(t *testing.T) {
	decisions := resolveConflicts(
		"aaa",
		[]ClaudeCodeMarketplacePlugin{candidate("shared")},
		map[string][]string{"bbb": {"shared"}},
		map[string]struct{}{},
	)
	if len(decisions) != 1 || !decisions[0].Kept {
		t.Fatalf("expected aaa to win (Kept=true); got %+v", decisions)
	}
}

func TestResolveConflicts_AlphabeticalPriorityILose(t *testing.T) {
	decisions := resolveConflicts(
		"zzz",
		[]ClaudeCodeMarketplacePlugin{candidate("shared")},
		map[string][]string{"aaa": {"shared"}},
		map[string]struct{}{},
	)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d", len(decisions))
	}
	if decisions[0].Kept {
		t.Fatalf("expected zzz to lose; got Kept=true")
	}
	if !strings.Contains(decisions[0].Reason, "aaa") {
		t.Errorf("reason should mention winner 'aaa'; got %q", decisions[0].Reason)
	}
	if !strings.Contains(decisions[0].Reason, "marketplace") {
		t.Errorf("reason should mention 'marketplace'; got %q", decisions[0].Reason)
	}
}

func TestResolveConflicts_NoConflict(t *testing.T) {
	decisions := resolveConflicts(
		"aaa",
		[]ClaudeCodeMarketplacePlugin{candidate("shared")},
		map[string][]string{"bbb": {"different"}},
		map[string]struct{}{},
	)
	if len(decisions) != 1 || !decisions[0].Kept {
		t.Fatalf("expected no conflict (Kept=true); got %+v", decisions)
	}
}

func TestResolveConflicts_TripleTie(t *testing.T) {
	// Three marketplaces aaa, mmm, zzz all expose "shared".
	others := map[string][]string{
		"mmm": {"shared"},
		"zzz": {"shared"},
	}
	d1 := resolveConflicts("aaa", []ClaudeCodeMarketplacePlugin{candidate("shared")}, others, map[string]struct{}{})
	if !d1[0].Kept {
		t.Errorf("aaa: expected Kept=true; got %+v", d1[0])
	}

	othersFromMmm := map[string][]string{
		"aaa": {"shared"},
		"zzz": {"shared"},
	}
	d2 := resolveConflicts("mmm", []ClaudeCodeMarketplacePlugin{candidate("shared")}, othersFromMmm, map[string]struct{}{})
	if d2[0].Kept {
		t.Errorf("mmm: expected Kept=false; got %+v", d2[0])
	}
	if !strings.Contains(d2[0].Reason, "aaa") {
		t.Errorf("mmm reason should mention 'aaa'; got %q", d2[0].Reason)
	}

	othersFromZzz := map[string][]string{
		"aaa": {"shared"},
		"mmm": {"shared"},
	}
	d3 := resolveConflicts("zzz", []ClaudeCodeMarketplacePlugin{candidate("shared")}, othersFromZzz, map[string]struct{}{})
	if d3[0].Kept {
		t.Errorf("zzz: expected Kept=false; got %+v", d3[0])
	}
	if !strings.Contains(d3[0].Reason, "aaa") {
		t.Errorf("zzz reason should mention 'aaa'; got %q", d3[0].Reason)
	}
}

func TestResolveConflicts_EmptyCandidates(t *testing.T) {
	decisions := resolveConflicts("aaa", []ClaudeCodeMarketplacePlugin{}, map[string][]string{}, map[string]struct{}{})
	if len(decisions) != 0 {
		t.Errorf("expected empty result; got %v", decisions)
	}
}

func TestResolveConflicts_PluginCRDBeatsAlphabeticalRule(t *testing.T) {
	// allPluginCRs = {"shared"}; myMarketplaceName="aaa" (would win
	// alphabetically); otherMarketplaceCatalogs={"bbb":["shared"]}.
	// Decision: Plugin CRD wins, reason mentions Plugin CRD (NOT marketplace bbb).
	decisions := resolveConflicts(
		"aaa",
		[]ClaudeCodeMarketplacePlugin{candidate("shared")},
		map[string][]string{"bbb": {"shared"}},
		map[string]struct{}{"shared": {}},
	)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d", len(decisions))
	}
	if decisions[0].Kept {
		t.Fatalf("expected Plugin CRD to win (Kept=false); got %+v", decisions[0])
	}
	if !strings.Contains(decisions[0].Reason, "Plugin CRD") {
		t.Errorf("reason should mention 'Plugin CRD'; got %q", decisions[0].Reason)
	}
	if strings.Contains(decisions[0].Reason, "marketplace") {
		t.Errorf("reason should NOT mention 'marketplace' when Plugin CRD wins; got %q", decisions[0].Reason)
	}
}

func TestResolveConflicts_PreservesInputOrder(t *testing.T) {
	// Defensive: decisions[i].PluginName MUST equal myCandidates[i].Name.
	decisions := resolveConflicts(
		"mmm",
		[]ClaudeCodeMarketplacePlugin{candidate("zebra"), candidate("apple"), candidate("mango")},
		map[string][]string{},
		map[string]struct{}{},
	)
	want := []string{"zebra", "apple", "mango"}
	for i, d := range decisions {
		if d.PluginName != want[i] {
			t.Errorf("decisions[%d].PluginName = %q; want %q", i, d.PluginName, want[i])
		}
	}
}
