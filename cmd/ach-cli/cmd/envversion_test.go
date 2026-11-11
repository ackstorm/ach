// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"testing"

	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/state"
)

func TestVersionChanges(t *testing.T) {
	recorded := &state.EnvVersion{Items: map[string]string{
		"plugin/keep": "1", "plugin/moved": "1", "skill/gone": "x", "mcp/m": "",
	}}
	current := &state.EnvVersion{Items: map[string]string{
		"plugin/keep": "1", "plugin/moved": "2", "plugin/new": "9", "mcp/m": "", "a2a/agent": "",
	}}
	got := versionChanges(recorded, current)
	want := []hydrate.ItemChange{
		{Op: hydrate.OpAdd, Kind: "a2a", Name: "agent"},
		{Op: hydrate.OpUpdate, Kind: "plugin", Name: "moved"},
		{Op: hydrate.OpAdd, Kind: "plugin", Name: "new"},
		{Op: hydrate.OpRemove, Kind: "skill", Name: "gone"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
