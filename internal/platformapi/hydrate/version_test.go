// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"testing"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/platformapi/store"
)

func TestVersionItems_RevsAndRuntime(t *testing.T) {
	row := &db.EnvironmentRow{
		ContextPlugins:    []string{"bro@ackstorm", "local-plugin"},
		ContextSkills:     []string{"triage", "deck@skills"},
		ContextPrompts:    []string{"p1"},
		RuntimeMCPServers: []string{"mcp-x"},
		RuntimeA2AAgents:  []string{"agent-x"},
		RuntimeModels:     []string{"smart"},
	}
	revs := store.Revs{
		Plugins:            map[string]string{"local-plugin": "aaa"},
		PluginMarketplaces: map[string]string{"ackstorm": "mmm"},
		Skills:             map[string]string{"triage": "sss"},
		SkillMarketplaces:  map[string]string{"skills": "kkk"},
		Prompts:            map[string]string{"p1": "ppp"},
	}
	got := versionItems(row, revs)
	want := []VersionItem{
		{Kind: "a2a", Name: "agent-x"},
		{Kind: "mcp", Name: "mcp-x"},
		{Kind: "model", Name: "smart"},
		{Kind: "plugin", Name: "bro@ackstorm", Rev: "mmm"},
		{Kind: "plugin", Name: "local-plugin", Rev: "aaa"},
		{Kind: "prompt", Name: "p1", Rev: "ppp"},
		{Kind: "skill", Name: "deck@skills", Rev: "kkk"},
		{Kind: "skill", Name: "triage", Rev: "sss"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestVersionOf_ChangesWithAnyItemOrRev(t *testing.T) {
	base := []VersionItem{{Kind: "plugin", Name: "a", Rev: "1"}, {Kind: "mcp", Name: "m"}}
	v := versionOf(base)
	if v != versionOf([]VersionItem{{Kind: "plugin", Name: "a", Rev: "1"}, {Kind: "mcp", Name: "m"}}) {
		t.Fatal("same items must give the same version")
	}
	for _, other := range [][]VersionItem{
		{{Kind: "plugin", Name: "a", Rev: "2"}, {Kind: "mcp", Name: "m"}},
		{{Kind: "plugin", Name: "a", Rev: "1"}},
		{{Kind: "plugin", Name: "a", Rev: "1"}, {Kind: "mcp", Name: "m"}, {Kind: "a2a", Name: "x"}},
	} {
		if versionOf(other) == v {
			t.Errorf("version did not change for %+v", other)
		}
	}
}
