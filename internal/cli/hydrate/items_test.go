// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

func st(plugins []state.FileEntry, mcpKeys ...string) *state.File {
	f := &state.File{Plugins: plugins}
	if len(mcpKeys) > 0 {
		f.Adapter.Files = []state.FileEntry{{Target: ".mcp.json", Hash: "h", Merge: "deep", Keys: mcpKeys}}
	}
	return f
}

func TestDiffItems(t *testing.T) {
	prev := st([]state.FileEntry{
		{Target: "a/1", Hash: "x", Source: "superpowers"},
		{Target: "a/2", Hash: "y", Source: "superpowers"},
		{Target: "b/1", Hash: "z", Source: "old-runbook"},
		{Target: "c/1", Hash: "k", Source: "same"},
	}, "mcpServers.github")
	next := st([]state.FileEntry{
		{Target: "a/1", Hash: "x2", Source: "superpowers"}, // content changed
		{Target: "a/2", Hash: "y", Source: "superpowers"},
		{Target: "c/1", Hash: "k", Source: "same"},
		{Target: "d/1", Hash: "n", Source: "mcp-feedback"},
		{Target: "d/2", Hash: "m", Source: "mcp-feedback"},
	}, "mcpServers.github", "mcpServers.mcp-artifact")

	got, unchanged := DiffItems(prev, next)
	want := []ItemChange{
		{OpAdd, "mcp", "mcp-artifact", 0},
		{OpAdd, "plugin", "mcp-feedback", 2},
		{OpRemove, "plugin", "old-runbook", 1},
		{OpUpdate, "plugin", "superpowers", 2},
	}
	if len(got) != len(want) {
		t.Fatalf("DiffItems = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if unchanged != 2 { // github mcp + "same" plugin
		t.Errorf("unchanged = %d, want 2", unchanged)
	}
}

func TestDiffItems_NilPrevIsAllAdds(t *testing.T) {
	got, unchanged := DiffItems(nil, st([]state.FileEntry{{Target: "a", Hash: "x", Source: "p"}}))
	if len(got) != 1 || got[0].Op != OpAdd || unchanged != 0 {
		t.Fatalf("got %+v unchanged %d", got, unchanged)
	}
}
