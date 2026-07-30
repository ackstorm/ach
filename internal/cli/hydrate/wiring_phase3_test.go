// SPDX-License-Identifier: Apache-2.0

package hydrate

// White-box tests for the Phase-3 cross-cutting plumbing (plan 03-01):
//   - publishFile threads fw.SourceHash (D-23) instead of hardcoding
//     SourceHash == Hash; an empty fw.SourceHash falls back to freshHash.
//   - adapter.RemapGlobalPath generalizes to ALL .opencode/* dirs under global
//     scope (D-22), not only .opencode/opencode.json.
//   - dropRuntimeOwnedMCP is format-aware (TOML for codex .toml targets,
//     JSON otherwise) and prefix-parameterized (mcp_servers./mcp./mcpServers.)
//     (D-17/D-10).

import (
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/hash"
)

// TestPublishFile_ThreadsSourceHash proves a FileWrite carrying a non-empty
// SourceHash (a converted projected file) records that SourceHash in the state
// row — distinct from the emitted-content Hash — while an empty SourceHash
// falls back to the emitted Hash (passthrough invariant).
func TestPublishFile_ThreadsSourceHash(t *testing.T) {
	toolRoot := t.TempDir()
	d := &adapterDispatcherImpl{platformID: "fake"}

	srcBytes := []byte("ORIGINAL SOURCE\n")
	emitted := []byte("CONVERTED EMITTED\n")
	srcHash := hash.HashBytes(srcBytes)

	// Converted file: SourceHash set, differs from emitted Hash.
	conv := adapter.FileWrite{
		Path:       ".codex/agents/foo.toml",
		Content:    emitted,
		Merge:      adapter.MergeReplace,
		SourceHash: srcHash,
	}
	got, err := d.publishFile(conv, nil, toolRoot)
	if err != nil {
		t.Fatalf("publishFile converted: %v", err)
	}
	if got.SourceHash != srcHash {
		t.Errorf("converted SourceHash = %q; want threaded %q", got.SourceHash, srcHash)
	}
	if got.Hash != hash.HashBytes(emitted) {
		t.Errorf("converted Hash = %q; want HashBytes(emitted) %q", got.Hash, hash.HashBytes(emitted))
	}
	if got.SourceHash == got.Hash {
		t.Errorf("converted SourceHash must differ from Hash; both %q", got.SourceHash)
	}

	// Passthrough file: empty SourceHash → falls back to freshHash == Hash.
	pass := adapter.FileWrite{
		Path:    ".claude/rules/bar.md",
		Content: emitted,
		Merge:   adapter.MergeReplace,
	}
	gotP, err := d.publishFile(pass, nil, toolRoot)
	if err != nil {
		t.Fatalf("publishFile passthrough: %v", err)
	}
	if gotP.SourceHash != gotP.Hash {
		t.Errorf("passthrough SourceHash (%q) must equal Hash (%q)", gotP.SourceHash, gotP.Hash)
	}
	if gotP.Hash != hash.HashBytes(emitted) {
		t.Errorf("passthrough Hash = %q; want %q", gotP.Hash, hash.HashBytes(emitted))
	}
}

// TestRemapGlobalPath_AllOpencodeDirs proves the D-22 generalization: every
// .opencode/* projected path remaps to .config/opencode/* under global scope,
// not just .opencode/opencode.json. Project scope keeps .opencode/, and
// non-opencode platforms are untouched.
func TestRemapGlobalPath_AllOpencodeDirs(t *testing.T) {
	for _, v := range []string{
		"CLAUDE_CONFIG_DIR", "CODEX_HOME", "GEMINI_CLI_HOME",
		"PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME",
	} {
		t.Setenv(v, "")
	}
	cases := []struct {
		platform, in, want string
	}{
		{"opencode", ".opencode/opencode.json", ".config/opencode/opencode.json"},
		{"opencode", ".opencode/commands/x.md", ".config/opencode/commands/x.md"},
		{"opencode", ".opencode/agents/y.md", ".config/opencode/agents/y.md"},
		{"opencode", ".opencode/skills/z/w.md", ".config/opencode/skills/z/w.md"},
		// non-opencode platforms unchanged.
		{"codex", ".codex/config.toml", ".codex/config.toml"},
		{"claude-code", ".claude/settings.json", ".claude/settings.json"},
		// opencode path that is NOT under .opencode/ is unchanged.
		{"opencode", "AGENTS.md", "AGENTS.md"},
	}
	for _, tc := range cases {
		got := adapter.RemapGlobalPath(tc.platform, "/home/u", tc.in)
		if got != tc.want {
			t.Errorf("remapGlobalPath(%q, %q) = %q; want %q", tc.platform, tc.in, got, tc.want)
		}
	}
}

// TestDropRuntimeOwnedMCP_TOML proves the codex TOML / mcp_servers. arm: a
// projected mcp_servers.<id> the runtime owns is dropped from the TOML content,
// and a non-colliding id survives.
func TestDropRuntimeOwnedMCP_TOML(t *testing.T) {
	tomlContent := "[mcp_servers.foo]\n  url = \"https://PLUGIN-foo\"\n\n[mcp_servers.bar]\n  url = \"https://PLUGIN-bar\"\n"
	fw := adapter.FileWrite{
		Path:    ".codex/config.toml",
		Content: []byte(tomlContent),
		Merge:   adapter.MergeDeep,
		Keys:    []string{"mcp_servers.foo", "mcp_servers.bar"},
	}
	runtime := []FileWrite{
		{Target: ".codex/config.toml", Merge: mergeStrDeep, Keys: []string{"mcp_servers.foo"}},
	}
	published, drops, err := dropRuntimeOwnedMCP(&fw, runtime)
	if err != nil {
		t.Fatalf("dropRuntimeOwnedMCP TOML: %v", err)
	}
	if !published {
		t.Fatalf("publish=false; bar should survive so the file is still published")
	}
	if strings.Contains(string(fw.Content), "PLUGIN-foo") {
		t.Errorf("runtime-owned foo not dropped from TOML:\n%s", fw.Content)
	}
	if !strings.Contains(string(fw.Content), "PLUGIN-bar") {
		t.Errorf("non-colliding bar should survive in TOML:\n%s", fw.Content)
	}
	if len(fw.Keys) != 1 || fw.Keys[0] != "mcp_servers.bar" {
		t.Errorf("fw.Keys = %v; want [mcp_servers.bar]", fw.Keys)
	}
	var sawFoo bool
	for _, dr := range drops {
		if strings.Contains(dr, "foo") && strings.Contains(dr, "runtime-owned") {
			sawFoo = true
		}
	}
	if !sawFoo {
		t.Errorf("drops missing foo runtime-owned token; got %v", drops)
	}
}

// TestDropRuntimeOwnedMCP_OpencodeJSON proves the opencode JSON / mcp. prefix
// arm: a projected mcp.<id> the runtime owns is dropped from the JSON content.
func TestDropRuntimeOwnedMCP_OpencodeJSON(t *testing.T) {
	jsonContent := `{"mcp":{"foo":{"type":"remote","url":"https://PLUGIN-foo"},"bar":{"type":"remote","url":"https://PLUGIN-bar"}}}`
	fw := adapter.FileWrite{
		Path:    ".opencode/opencode.json",
		Content: []byte(jsonContent),
		Merge:   adapter.MergeDeep,
		Keys:    []string{"mcp.foo", "mcp.bar"},
	}
	runtime := []FileWrite{
		{Target: ".opencode/opencode.json", Merge: mergeStrDeep, Keys: []string{"mcp.foo"}},
	}
	published, drops, err := dropRuntimeOwnedMCP(&fw, runtime)
	if err != nil {
		t.Fatalf("dropRuntimeOwnedMCP opencode JSON: %v", err)
	}
	if !published {
		t.Fatalf("publish=false; bar should survive")
	}
	if strings.Contains(string(fw.Content), "PLUGIN-foo") {
		t.Errorf("runtime-owned foo not dropped from opencode JSON:\n%s", fw.Content)
	}
	if !strings.Contains(string(fw.Content), "PLUGIN-bar") {
		t.Errorf("non-colliding bar should survive:\n%s", fw.Content)
	}
	if len(fw.Keys) != 1 || fw.Keys[0] != "mcp.bar" {
		t.Errorf("fw.Keys = %v; want [mcp.bar]", fw.Keys)
	}
	var sawFoo bool
	for _, dr := range drops {
		if strings.Contains(dr, "foo") && strings.Contains(dr, "runtime-owned") {
			sawFoo = true
		}
	}
	if !sawFoo {
		t.Errorf("drops missing foo runtime-owned token; got %v", drops)
	}
}

// TestDropRuntimeOwnedMCP_ClaudeJSON_Unchanged proves the existing claude/gemini
// JSON / mcpServers. arm still works after parameterization.
func TestDropRuntimeOwnedMCP_ClaudeJSON_Unchanged(t *testing.T) {
	jsonContent := `{"mcpServers":{"foo":{"type":"http","url":"https://PLUGIN-foo"}}}`
	fw := adapter.FileWrite{
		Path:    ".claude/settings.json",
		Content: []byte(jsonContent),
		Merge:   adapter.MergeDeep,
		Keys:    []string{"mcpServers.foo"},
	}
	runtime := []FileWrite{
		{Target: ".claude/settings.json", Merge: mergeStrDeep, Keys: []string{"mcpServers.foo"}},
	}
	published, drops, err := dropRuntimeOwnedMCP(&fw, runtime)
	if err != nil {
		t.Fatalf("dropRuntimeOwnedMCP claude JSON: %v", err)
	}
	if published {
		t.Errorf("publish=true; all keys collided so publish should be false")
	}
	if len(drops) != 1 || !strings.Contains(drops[0], "foo") {
		t.Errorf("drops = %v; want one foo runtime-owned token", drops)
	}
}

// TestDropRuntimeOwnedMCP_AllCollide_NonMCPSurvives_ReEncodes proves WR-07:
// when ALL MCP keys collide (survivors == 0) but the doc still carries OTHER
// top-level keys, fw.Content is re-encoded from the de-conflicted doc — NOT
// left at its pre-removal bytes carrying the runtime-owned subtree. Honors the
// "fw is mutated IN PLACE" contract for a future projection rule contributing
// non-MCP deep-merge keys.
func TestDropRuntimeOwnedMCP_AllCollide_NonMCPSurvives_ReEncodes(t *testing.T) {
	// doc has a non-MCP key (other) plus a single mcpServers.foo that collides.
	jsonContent := `{"other":{"keep":true},"mcpServers":{"foo":{"type":"http","url":"https://PLUGIN-foo"}}}`
	fw := adapter.FileWrite{
		Path:    ".claude/settings.json",
		Content: []byte(jsonContent),
		Merge:   adapter.MergeDeep,
		Keys:    []string{"mcpServers.foo"},
	}
	runtime := []FileWrite{
		{Target: ".claude/settings.json", Merge: mergeStrDeep, Keys: []string{"mcpServers.foo"}},
	}
	published, _, err := dropRuntimeOwnedMCP(&fw, runtime)
	if err != nil {
		t.Fatalf("dropRuntimeOwnedMCP: %v", err)
	}
	// Caller still skips publishing (nothing of ours survives).
	if published {
		t.Errorf("publish=true; all OUR keys collided so publish should be false")
	}
	// But fw.Content must be re-encoded: the runtime-owned subtree is GONE and
	// the non-MCP key survives (no stale pre-removal bytes).
	if strings.Contains(string(fw.Content), "PLUGIN-foo") {
		t.Errorf("stale runtime-owned subtree left in fw.Content:\n%s", fw.Content)
	}
	if !strings.Contains(string(fw.Content), "keep") {
		t.Errorf("non-MCP key dropped from re-encoded fw.Content:\n%s", fw.Content)
	}
}

// TestRuntimeKindForKey proves the hydrate-summary component tally maps every
// adapter's contributed runtime key shape to the right kind, and ignores
// non-runtime keys (so 0 runtime servers yields no "mcp"/"a2a" bucket and the
// summary never prints "0 mcp"). MCP prefixes vary per adapter: mcpServers.
// (claude/gemini/pimono), mcp_servers. (codex), mcp. (opencode); A2A is
// a2aAgents. (most) / a2a_agents. (codex).
func TestRuntimeKindForKey(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"mcpServers.my-mcp", "mcp"},   // claude/gemini/pimono
		{"mcp_servers.my-mcp", "mcp"},  // codex (TOML snake_case)
		{"mcp.my-mcp", "mcp"},          // opencode
		{"a2aAgents.my-agent", "a2a"},  // claude/gemini/opencode/most
		{"a2a_agents.my-agent", "a2a"}, // codex
		{"ach:begin", ""},              // composite marker — not a runtime component
		{"settings.foo", ""},           // unrelated key
		{"", ""},                       // empty
	}
	for _, tc := range cases {
		if got := runtimeKindForKey(tc.key); got != tc.want {
			t.Errorf("runtimeKindForKey(%q) = %q; want %q", tc.key, got, tc.want)
		}
	}
}
