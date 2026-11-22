// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/state"
)

// TestRefreshSharedRows: after one target records a shared skill file, the
// Environment's other target holds the same hash for it, so its next hydrate
// or uninstall does not take the new content for a local edit. Deep rows and
// rows for other paths are left alone.
func TestRefreshSharedRows(t *testing.T) {
	achDir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(achDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const shared = ".agents/skills/plug-sk/SKILL.md"
	codex := filepath.Join(achDir, "state-codex.json")
	if err := state.Save(codex, &state.File{SchemaVersion: "3", Environment: "demo", Plugins: []state.FileEntry{
		{Target: shared, Hash: "v1", SourceHash: "v1", Merge: mergeStrReplace},
		{Target: ".codex/prompts/plug-x.md", Hash: "p1", Merge: mergeStrReplace},
		{Target: ".codex/config.toml", Hash: "d1", Merge: mergeStrDeep, Keys: []string{"mcp_servers.x"}},
	}}); err != nil {
		t.Fatal(err)
	}

	c := &commit{stateStore: defaultStateStore{}, statePath: filepath.Join(achDir, "state-opencode.json"),
		toolRoot: t.TempDir()}
	next := &state.File{SchemaVersion: "3", Environment: "demo", Plugins: []state.FileEntry{
		{Target: shared, Hash: "v2", SourceHash: "v2", Merge: mergeStrReplace},
	}}
	if err := c.refreshSharedRows(next); err != nil {
		t.Fatal(err)
	}

	got, err := state.Load(codex)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{shared: "v2", ".codex/prompts/plug-x.md": "p1", ".codex/config.toml": "d1"}
	for _, e := range got.Plugins {
		if e.Hash != want[e.Target] {
			t.Errorf("%s hash = %q, want %q", e.Target, e.Hash, want[e.Target])
		}
	}
}
