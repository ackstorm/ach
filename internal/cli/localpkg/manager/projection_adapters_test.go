// SPDX-License-Identifier: Apache-2.0

// Hermetic integration coverage for the localpkg manager's Resolve+Project
// pipeline across ALL FIVE registered adapters (Group A) and the
// skill-marketplace lens end-to-end (Group B).
//
// The existing manager_test.go only exercises the claude-code adapter; this
// file blank-imports every adapter so its init() registers it, then asserts
// the per-adapter projection destination paths and the two content-bearing
// transforms (codex agent TOML, claude-code MCP deep-merge).
//
// All fixtures are local git file:// bare clones built with `git init` — no
// network, no cluster — so the suite runs under `make test-unit` / CI / the
// pre-push gate. Tests skip when git is not on PATH (mirrors the existing
// helpers).

package manager_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	// Blank-import all five adapters so their init() registers them.
	_ "github.com/ackstorm/ach/internal/cli/adapter/claudecode"
	_ "github.com/ackstorm/ach/internal/cli/adapter/codex"
	_ "github.com/ackstorm/ach/internal/cli/adapter/gemini"
	_ "github.com/ackstorm/ach/internal/cli/adapter/opencode"
	_ "github.com/ackstorm/ach/internal/cli/adapter/pimono"

	"github.com/ackstorm/ach/internal/cli/localpkg/manager"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
)

// ---- fixtures ---------------------------------------------------------------

// makeRichPluginRepo creates a bare git repo with a direct-plugin layout that
// carries a command, an agent, and an MCP server definition. This lets a single
// Resolve drive every adapter's command/agent/mcp projection rules. Returns the
// file:// bare URL.
func makeRichPluginRepo(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "init", "-b", "main", ".")

	writeFile := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	writeFile("commands/hello.md", "# hello\nA greeting command.\n")
	// Agent with frontmatter so the codex TOML transform has a name + body to
	// lift. The body becomes developer_instructions.
	writeFile("agents/agent-a.md",
		"---\nname: agent-a\ndescription: Agent A.\nmodel: sonnet\n---\n\nYou are agent A.\n")
	writeFile("mcp/servers.json",
		`{"mcpServers":{"demo":{"command":"echo","args":["hi"]}}}`)

	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init rich plugin")

	bare := t.TempDir()
	runGit(t, work, "clone", "--bare", ".", bare)
	return "file://" + bare
}

// makeSkillMarketplaceRepo creates a bare git repo laid out as a
// skill-marketplace: two skills under skills/, each with a SKILL.md whose
// frontmatter name matches its directory plus an extra ref file. Returns the
// file:// bare URL.
func makeSkillMarketplaceRepo(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "init", "-b", "main", ".")

	writeSkill := func(name, extra string) {
		t.Helper()
		dir := filepath.Join(work, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir skill %s: %v", name, err)
		}
		skillMD := "---\nname: " + name + "\ndescription: Skill " + name + ".\n---\n\n# " + name + "\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
			t.Fatalf("write SKILL.md %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "ref.md"), []byte(extra), 0o644); err != nil {
			t.Fatalf("write ref.md %s: %v", name, err)
		}
	}

	writeSkill("alpha", "alpha reference\n")
	writeSkill("beta", "beta reference\n")

	// A top-level file (alongside skills/) keeps detectArchiveRoot from
	// collapsing skills/ into the archive root — so SkillsRootHint="skills"
	// is a genuine sub-path strip, mirroring a real anthropics/skills-style
	// monorepo layout (repo-root README + skills/ subtree). Without it the
	// single top-level skills/ dir would BE the archive root and the "skills"
	// hint would strip a second, non-existent level.
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# skills monorepo\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init skill marketplace")

	bare := t.TempDir()
	runGit(t, work, "clone", "--bare", ".", bare)
	return "file://" + bare
}

// projectPaths Resolves a direct-plugin repo and Projects it for adapterID,
// returning the sorted PlannedWrite paths. It fails t on any error.
func resolveRichPlugin(t *testing.T, url string) manager.ResolveResult {
	t.Helper()
	repo := store.RepoEntry{
		Name:     "rich-plugin",
		Kind:     "git",
		CloneURL: url,
		GitRef:   "main",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	res, err := manager.Resolve(ctx, repo, "", "rich-plugin", "plugin")
	if err != nil {
		t.Fatalf("Resolve(plugin): %v", err)
	}
	return res
}

// plannedPaths returns the set of PlannedWrite paths for quick membership checks.
func plannedPaths(writes []manager.PlannedWrite) map[string]manager.PlannedWrite {
	m := make(map[string]manager.PlannedWrite, len(writes))
	for _, w := range writes {
		m[w.Path] = w
	}
	return m
}

// pathList returns a sorted slice of the planned write paths (for error output).
func pathList(writes []manager.PlannedWrite) []string {
	out := make([]string, 0, len(writes))
	for _, w := range writes {
		out = append(out, w.Path)
	}
	sort.Strings(out)
	return out
}

// ---- Group A: projection across all five adapters ---------------------------

// TestProject_AllAdapters_DirectPlugin verifies that a single rich direct-plugin
// (command + agent + mcp) projects to the expected destination paths for each
// of the five registered adapters.
func TestProject_AllAdapters_DirectPlugin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	url := makeRichPluginRepo(t)
	res := resolveRichPlugin(t, url)
	defer func() { _ = os.RemoveAll(res.StageDir) }()

	cases := []struct {
		adapterID string
		want      []string // paths that MUST be present
		absent    []string // paths that MUST NOT be present
	}{
		{
			adapterID: "claude-code",
			want: []string{
				".claude/commands/hello.md",
				".claude/agents/agent-a.md",
				".claude/settings.json",
			},
		},
		{
			adapterID: "codex",
			want: []string{
				".codex/prompts/hello.md",
				".codex/agents/agent-a.toml", // transformed .md → .toml
				".codex/config.toml",
			},
		},
		{
			adapterID: "gemini-cli",
			want: []string{
				".gemini/commands/hello.toml", // claude .md command → gemini TOML
				".gemini/agents/agent-a.md",
				".gemini/settings.json",
			},
			absent: []string{
				".gemini/commands/hello.md", // must be converted, not copied verbatim
			},
		},
		{
			adapterID: "opencode",
			want: []string{
				".opencode/commands/hello.md",
				".opencode/agents/agent-a.md",
				".opencode/opencode.json",
			},
		},
		{
			adapterID: "pimono",
			// pimono routes commands → .pi/agent/prompts/** and mcp → .pi/mcp.json,
			// but has NO agents rule, so the agent must NOT be projected.
			want: []string{
				".pi/agent/prompts/hello.md",
				".pi/mcp.json",
			},
			absent: []string{
				".pi/agents/agent-a.md",
				".pi/agent/agents/agent-a.md",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.adapterID, func(t *testing.T) {
			writes, err := manager.Project(res.StageDir, tc.adapterID)
			if err != nil {
				t.Fatalf("Project(%s): %v", tc.adapterID, err)
			}
			got := plannedPaths(writes)
			for _, p := range tc.want {
				if _, ok := got[p]; !ok {
					t.Errorf("adapter %s: missing planned write %q; got: %v",
						tc.adapterID, p, pathList(writes))
				}
			}
			for _, p := range tc.absent {
				if _, ok := got[p]; ok {
					t.Errorf("adapter %s: unexpected planned write %q (should be absent); got: %v",
						tc.adapterID, p, pathList(writes))
				}
			}
			// Sanity: pimono must never emit an agent file under any path.
			if tc.adapterID == "pimono" {
				for _, w := range writes {
					if strings.Contains(w.Path, "agent-a") {
						t.Errorf("pimono projected an agent file %q; pimono has no agents rule", w.Path)
					}
				}
			}
		})
	}
}

// TestProject_Codex_AgentTOMLTransform commits the codex projection to disk and
// reads back .codex/agents/agent-a.toml, asserting the codexAgentTOML transform
// produced both the lifted `name` key and a `developer_instructions` key from
// the agent body. This proves the transform CONTENT, not merely the routing.
func TestProject_Codex_AgentTOMLTransform(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	url := makeRichPluginRepo(t)
	res := resolveRichPlugin(t, url)
	defer func() { _ = os.RemoveAll(res.StageDir) }()

	writes, err := manager.Project(res.StageDir, "codex")
	if err != nil {
		t.Fatalf("Project(codex): %v", err)
	}

	root := t.TempDir()
	if _, err := manager.Commit(root, false, "codex", "rich-plugin", writes); err != nil {
		t.Fatalf("Commit(codex): %v", err)
	}

	tomlBytes, err := os.ReadFile(filepath.Join(root, ".codex", "agents", "agent-a.toml"))
	if err != nil {
		t.Fatalf("read .codex/agents/agent-a.toml: %v", err)
	}
	toml := string(tomlBytes)

	if !strings.Contains(toml, `name = "agent-a"`) {
		t.Errorf("codex agent TOML missing `name = \"agent-a\"`; got:\n%s", toml)
	}
	if !strings.Contains(toml, "developer_instructions") {
		t.Errorf("codex agent TOML missing developer_instructions key; got:\n%s", toml)
	}
	// The agent body must have been lifted into developer_instructions.
	if !strings.Contains(toml, "You are agent A.") {
		t.Errorf("codex agent TOML developer_instructions missing the agent body; got:\n%s", toml)
	}
}

// TestProject_ClaudeCode_MCPMergeContent commits the claude-code projection and
// reads back .claude/settings.json, asserting the MCP deep-merge placed the
// plugin's server under mcpServers.demo. This proves the MCP merge content, not
// merely the destination path.
func TestProject_ClaudeCode_MCPMergeContent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	url := makeRichPluginRepo(t)
	res := resolveRichPlugin(t, url)
	defer func() { _ = os.RemoveAll(res.StageDir) }()

	writes, err := manager.Project(res.StageDir, "claude-code")
	if err != nil {
		t.Fatalf("Project(claude-code): %v", err)
	}

	root := t.TempDir()
	if _, err := manager.Commit(root, false, "claude-code", "rich-plugin", writes); err != nil {
		t.Fatalf("Commit(claude-code): %v", err)
	}

	settingsBytes, err := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read .claude/settings.json: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(settingsBytes, &settings); err != nil {
		t.Fatalf("unmarshal settings.json: %v\n%s", err, settingsBytes)
	}
	mcpRaw, ok := settings["mcpServers"]
	if !ok {
		t.Fatalf("settings.json missing mcpServers; got: %s", settingsBytes)
	}
	mcp, ok := mcpRaw.(map[string]any)
	if !ok || mcp["demo"] == nil {
		t.Errorf("settings.json mcpServers.demo missing; got: %s", settingsBytes)
	}
}

// ---- Group B: skill-marketplace Resolve+Project end-to-end ------------------

// TestResolve_SkillMarketplace_ThenProject exercises the skill-marketplace lens
// end-to-end: discover the named skill in a skills/ monorepo, slice only that
// skill, nest it (bug-A nesting) under skills/<name>/, then project it for
// claude-code — asserting only the requested skill's files appear.
func TestResolve_SkillMarketplace_ThenProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	url := makeSkillMarketplaceRepo(t)
	repo := store.RepoEntry{
		Name:           "skills-mkt",
		Kind:           "git",
		CloneURL:       url,
		GitRef:         "main",
		SkillsRootHint: "skills",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := manager.Resolve(ctx, repo, "", "alpha", "skill-marketplace")
	if err != nil {
		t.Fatalf("Resolve(skill-marketplace, alpha): %v", err)
	}
	defer func() { _ = os.RemoveAll(res.StageDir) }()

	if res.Kind != "skill" {
		t.Errorf("Kind = %q; want skill", res.Kind)
	}
	if res.Name != "alpha" {
		t.Errorf("Name = %q; want alpha", res.Name)
	}
	if len(res.ResolvedSHA) != 40 {
		t.Errorf("ResolvedSHA = %q; want 40-hex", res.ResolvedSHA)
	}

	// The manager nests the sliced skill under skills/alpha/ (bug-A nesting) so
	// the claudecode skills/**/* rule fires.
	if _, err := os.Stat(filepath.Join(res.StageDir, "skills", "alpha", "SKILL.md")); err != nil {
		t.Fatalf("expected skills/alpha/SKILL.md in stage dir: %v", err)
	}
	// beta must NOT have been sliced into this stage dir (only the requested
	// skill is materialized).
	if _, err := os.Stat(filepath.Join(res.StageDir, "skills", "beta")); !os.IsNotExist(err) {
		t.Errorf("skills/beta should NOT be present in alpha's stage dir; err=%v", err)
	}

	writes, err := manager.Project(res.StageDir, "claude-code")
	if err != nil {
		t.Fatalf("Project(claude-code): %v", err)
	}
	got := plannedPaths(writes)
	for _, want := range []string{
		".claude/skills/alpha/SKILL.md",
		".claude/skills/alpha/ref.md",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing planned write %q; got: %v", want, pathList(writes))
		}
	}
	// No beta content may leak into the projection.
	for _, w := range writes {
		if strings.Contains(w.Path, "beta") {
			t.Errorf("unexpected beta content in projection: %q", w.Path)
		}
	}
}

// TestResolve_SkillMarketplace_NotFound asserts that requesting a skill that is
// not present in the marketplace returns an error mentioning "not found".
func TestResolve_SkillMarketplace_NotFound(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	url := makeSkillMarketplaceRepo(t)
	repo := store.RepoEntry{
		Name:           "skills-mkt",
		Kind:           "git",
		CloneURL:       url,
		GitRef:         "main",
		SkillsRootHint: "skills",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, err := manager.Resolve(ctx, repo, "", "missing", "skill-marketplace")
	if err == nil {
		t.Fatal("expected error for missing skill, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q does not contain %q", err.Error(), "not found")
	}
}
