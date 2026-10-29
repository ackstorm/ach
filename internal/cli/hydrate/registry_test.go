// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/ackstorm/ach/internal/cli/state"
)

// TestCoOwnedRegistry locks LIFE-04 (D-32): it asserts that the
// Merge/Keys[] recorded per state.FileEntry IS the correct co-owned-file
// registry that drives Sync's inverse-merge branch selection, and that
// the inverse merge preserves co-owned (user / other-package) keys and
// surrounding user text. The assertions are driven through the PUBLIC
// hydrate.Sync API on a temp dir — no wiring.go change (Plan 03 owns it),
// no new registry structure (the recorded data is the registry).
//
// Each entry carries Hash:"" so the drift-wins gate is skipped and the
// inverse-merge branch always runs — the registry's branch dispatch,
// not the drift gate, is under test here. (The drift gate has its own
// coverage in the existing wiring_*_test.go.)
func TestCoOwnedRegistry(t *testing.T) {
	root := t.TempDir()

	// --- Fixtures on disk -------------------------------------------------

	// 1. composite host file (CLAUDE.md) — Merge=composite, Keys=[plugin].
	//    Surrounding user prose must survive; only the per-plugin marker
	//    block is removed.
	claudeMD := filepath.Join(root, "CLAUDE.md")
	const userPreamble = "# My project notes\nkeep this line\n\n"
	const userTrailer = "\n# more user notes\nkeep this too\n"
	claudeBody := userPreamble +
		"<!-- ach:begin:demo-plugin -->\nach-managed content\n<!-- ach:end:demo-plugin -->\n" +
		userTrailer
	if err := os.WriteFile(claudeMD, []byte(claudeBody), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	// 2. deep adapter doc (.mcp.json) — Merge=deep, dotted Keys. A co-owned
	//    sibling key under the same parent must survive the inverse merge.
	mcpJSON := filepath.Join(root, ".mcp.json")
	mcpDoc := map[string]any{
		"mcpServers": map[string]any{
			"ach-server":  map[string]any{"command": "ach"},  // projected → removed
			"user-server": map[string]any{"command": "mine"}, // co-owned → must survive
		},
		"otherTopLevel": "user-owned", // unrelated top-level key → must survive
	}
	writeJSON(t, mcpJSON, mcpDoc)

	// 3. deep TOML doc (.codex/config.toml) — Merge=deep, dotted Keys with
	//    the codex mcp_servers shape; sibling survives.
	codexTOML := filepath.Join(root, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexTOML), 0o755); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	codexDoc := map[string]any{
		"mcp_servers": map[string]any{
			"ach-codex":  map[string]any{"command": "ach"},
			"user-codex": map[string]any{"command": "mine"},
		},
	}
	writeTOML(t, codexTOML, codexDoc)

	// 4. file-owned replace target (.claude/commands/foo.md) — Merge=replace
	//    → whole-file delete.
	replaceFile := filepath.Join(root, ".claude", "commands", "foo.md")
	if err := os.MkdirAll(filepath.Dir(replaceFile), 0o755); err != nil {
		t.Fatalf("mkdir .claude/commands: %v", err)
	}
	if err := os.WriteFile(replaceFile, []byte("# projected command\n"), 0o644); err != nil {
		t.Fatalf("write replace file: %v", err)
	}

	// --- prev state: the recorded registry --------------------------------
	// All four entries live in Plugins[] (content bucket → resolved against
	// achDir=root). Hash:"" disables the drift gate so each branch runs.
	prev := &state.File{
		SchemaVersion: "3",
		Environment:   "prod",
		Plugins: []state.FileEntry{
			{Target: "CLAUDE.md", Merge: mergeStrComposite, Keys: []string{"demo-plugin"}},
			{Target: ".mcp.json", Merge: mergeStrDeep, Keys: []string{"mcpServers.ach-server"}},
			{Target: ".codex/config.toml", Merge: mergeStrDeep, Keys: []string{"mcp_servers.ach-codex"}},
			{Target: ".claude/commands/foo.md", Merge: mergeStrReplace},
		},
	}

	// newFile empty → every Target is in the to-delete set → every branch
	// of the registry dispatch fires.
	empty := &state.File{SchemaVersion: "3"}

	stats, err := Sync(prev, empty, root, root, SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Preserved != 0 {
		t.Fatalf("no entry should be preserved (Hash disabled), got Preserved=%d", stats.Preserved)
	}

	// --- composite: marker block removed, user text intact ----------------
	gotClaude, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatalf("read CLAUDE.md after sync: %v", err)
	}
	gc := string(gotClaude)
	if strings.Contains(gc, "ach:begin:demo-plugin") || strings.Contains(gc, "ach-managed content") {
		t.Fatalf("composite block must be removed, still present:\n%s", gc)
	}
	if !strings.Contains(gc, "keep this line") || !strings.Contains(gc, "keep this too") {
		t.Fatalf("surrounding user text must survive composite removal:\n%s", gc)
	}

	// --- deep JSON: projected key gone, co-owned sibling survives ---------
	gotMCP := readJSON(t, mcpJSON)
	servers, ok := gotMCP["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers parent must survive (had a co-owned sibling), got: %+v", gotMCP)
	}
	if _, gone := servers["ach-server"]; gone {
		t.Fatalf("projected key mcpServers.ach-server must be removed, got: %+v", servers)
	}
	if _, kept := servers["user-server"]; !kept {
		t.Fatalf("co-owned sibling mcpServers.user-server must survive, got: %+v", servers)
	}
	if _, kept := gotMCP["otherTopLevel"]; !kept {
		t.Fatalf("unrelated top-level user key must survive, got: %+v", gotMCP)
	}

	// --- deep TOML: projected key gone, sibling survives ------------------
	gotCodex := readTOML(t, codexTOML)
	cs, ok := gotCodex["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers parent must survive, got: %+v", gotCodex)
	}
	if _, gone := cs["ach-codex"]; gone {
		t.Fatalf("projected key mcp_servers.ach-codex must be removed, got: %+v", cs)
	}
	if _, kept := cs["user-codex"]; !kept {
		t.Fatalf("co-owned sibling mcp_servers.user-codex must survive, got: %+v", cs)
	}

	// --- replace: whole-file deleted --------------------------------------
	if _, statErr := os.Stat(replaceFile); !os.IsNotExist(statErr) {
		t.Fatalf("replace-merge target must be deleted whole, stat err=%v", statErr)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json %s: %v", path, err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write json %s: %v", path, err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read json %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal json %s: %v", path, err)
	}
	return out
}

func writeTOML(t *testing.T, path string, v any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open toml %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := toml.NewEncoder(f).Encode(v); err != nil {
		t.Fatalf("encode toml %s: %v", path, err)
	}
}

func readTOML(t *testing.T, path string) map[string]any {
	t.Helper()
	var out map[string]any
	if _, err := toml.DecodeFile(path, &out); err != nil {
		t.Fatalf("decode toml %s: %v", path, err)
	}
	return out
}
