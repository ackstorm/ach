// SPDX-License-Identifier: Apache-2.0

package hydrate

// White-box tests for the D-07 CR-01 composite exemption + plugin-name
// threading and the D-10 runtime-wins MCP drop in projectPlugins. These call
// projectPlugins directly with a test-local fake adapter that supplies
// composite + MCP-deep ProjectionRules — the real claude/gemini rows land in
// plans 02-03/02-04, so end-to-end-through-Render coverage is out of scope here.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/route"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// fakeProjAdapter implements adapter.Adapter + route.RuleProvider with a
// composite (AGENTS.md→CLAUDE.md) and an MCP-deep (mcp/**/*→settings.json)
// projection rule. The MCP rule's Transform mirrors mcpDeepKeys: returns the
// input bytes unchanged + the enumerated mcpServers.<id> keys.
type fakeProjAdapter struct{}

func (fakeProjAdapter) ID() string        { return "fake" }
func (fakeProjAdapter) Aliases() []string { return nil }
func (fakeProjAdapter) Detect(string) (adapter.Match, error) {
	return adapter.Match{}, nil
}
func (fakeProjAdapter) RenderRuntime(context.Context, *manifest.Manifest, *state.File) ([]adapter.FileWrite, error) {
	return nil, nil
}
func (fakeProjAdapter) TransformPlugin(context.Context, string, string) (adapter.PluginWrite, error) {
	return adapter.PluginWrite{}, nil
}

func (fakeProjAdapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "AGENTS.md", ToGlob: "CLAUDE.md", Merge: adapter.MergeComposite},
		{
			// Exact-file source → fixed settings.json (no ** suffix-append).
			// The real D-11/D-12 mcp/**/*→settingsJSONPath routing lands in
			// plans 02-03/02-04; plan 02-02's projectPlugins consumes whatever
			// route.Project emits, so a single-file rule exercises the
			// runtime-wins drop identically.
			FromGlob: "mcp/servers.json",
			ToGlob:   ".claude/settings.json",
			Merge:    adapter.MergeDeep,
			Transform: func(_ string, in []byte) ([]byte, []string, error) {
				// Enumerate top-level mcpServers.<id> keys; return bytes unchanged.
				var doc struct {
					MCPServers map[string]any `json:"mcpServers"`
				}
				if err := json.Unmarshal(in, &doc); err != nil {
					return nil, nil, err
				}
				keys := make([]string, 0, len(doc.MCPServers))
				for id := range doc.MCPServers {
					keys = append(keys, "mcpServers."+id)
				}
				sort.Strings(keys)
				return in, keys, nil
			},
		},
	}
}

// TestProjectPlugins_CompositeExempt_FromCR01 proves two distinct plugins both
// projecting AGENTS.md→CLAUDE.md (MergeComposite) do NOT trip the CR-01
// fail-fast — both co-own the host memory file via per-id blocks, emitted in
// sorted plugin-name order.
func TestProjectPlugins_CompositeExempt_FromCR01(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()
	stageTree(t, achDir, "plug-a", map[string]string{"AGENTS.md": "A body\n"})
	stageTree(t, achDir, "plug-b", map[string]string{"AGENTS.md": "B body\n"})

	d := &adapterDispatcherImpl{platformID: "fake"}
	var result RenderResult
	if err := d.projectPlugins(fakeProjAdapter{}, nil, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectPlugins composite-exempt: want nil error, got %v", err)
	}
	if len(result.ProjectedFiles) != 2 {
		t.Fatalf("ProjectedFiles = %d; want 2 (both composite contributors)", len(result.ProjectedFiles))
	}
	// Both rows record Keys=[plugin-name].
	gotKeys := map[string]bool{}
	for _, pf := range result.ProjectedFiles {
		if pf.Target != "CLAUDE.md" {
			t.Errorf("composite Target = %q; want CLAUDE.md", pf.Target)
		}
		if len(pf.Keys) != 1 {
			t.Fatalf("composite Keys = %v; want [plugin-name]", pf.Keys)
		}
		gotKeys[pf.Keys[0]] = true
	}
	if !gotKeys["plug-a"] || !gotKeys["plug-b"] {
		t.Errorf("composite rows missing a plugin name; got %v", gotKeys)
	}

	// On-disk CLAUDE.md contains BOTH per-id blocks, sorted by plugin name.
	body, err := os.ReadFile(filepath.Join(toolRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	aIdx := strings.Index(string(body), "<!-- ach:begin:plug-a -->")
	bIdx := strings.Index(string(body), "<!-- ach:begin:plug-b -->")
	if aIdx < 0 || bIdx < 0 {
		t.Fatalf("both per-id blocks must be present:\n%s", body)
	}
	if aIdx > bIdx {
		t.Errorf("blocks not in sorted plugin-name order (plug-a after plug-b):\n%s", body)
	}
}

// TestProjectPlugins_ReplaceCollision_StillFailsFast proves MergeReplace
// targets KEEP the CR-01 fail-fast: two plugins both shipping rules/foo.md
// (which the fake does NOT route — so use the real claudecode behavior). We
// reuse the fake but add no rules/ rule; instead assert composite is exempt
// while a hypothetical replace collision still errors via a dedicated rule.
//
// Simpler: drive the replace-collision through a fake whose only rule is a
// MergeReplace rules/**/* → .claude/rules/**/* and two plugins colliding.
func TestProjectPlugins_ReplaceCollision_StillFailsFast(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()
	stageTree(t, achDir, "plug-a", map[string]string{"rules/foo.md": "A\n"})
	stageTree(t, achDir, "plug-b", map[string]string{"rules/foo.md": "B\n"})

	d := &adapterDispatcherImpl{platformID: "fakerepl"}
	var result RenderResult
	err := d.projectPlugins(fakeReplaceAdapter{}, nil, achDir, toolRoot, &result)
	if err == nil {
		t.Fatalf("two plugins colliding on a MergeReplace target: want CR-01 error, got nil")
	}
	for _, want := range []string{"plug-a", "plug-b", ".claude/rules/foo.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("collision error %q missing %q", err.Error(), want)
		}
	}
}

// TestProjectPlugins_ReplaceProjectedFileIs0o644 proves WR-04: a MergeReplace
// projected plugin resource file (no credential) is written world-readable at
// 0o644, NOT owner-only 0o600 — 0o600 is reserved for credential-bearing
// MergeDeep runtime configs, and 0o600 on a non-secret projected file breaks
// cross-account use (service user, mounted docker volume).
func TestProjectPlugins_ReplaceProjectedFileIs0o644(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()
	stageTree(t, achDir, "plug-a", map[string]string{"rules/foo.md": "A\n"})

	d := &adapterDispatcherImpl{platformID: "fakerepl"}
	var result RenderResult
	if err := d.projectPlugins(fakeReplaceAdapter{}, nil, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectPlugins: %v", err)
	}

	abs := filepath.Join(toolRoot, ".claude", "rules", "foo.md")
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat projected file: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("projected MergeReplace file mode = %o; want 0o644 (non-credential resource)", info.Mode().Perm())
	}
}

// fakeReplaceAdapter routes rules/**/* → .claude/rules/**/* (MergeReplace) only,
// so a two-plugin same-file collision exercises the CR-01 fail-fast that
// composite is exempt from.
type fakeReplaceAdapter struct{ fakeProjAdapter }

func (fakeReplaceAdapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "rules/**/*", ToGlob: ".claude/rules/**/*", Merge: adapter.MergeReplace},
	}
}

// TestProjectPlugins_RuntimeWins_DropsPluginMCP proves D-10: a runtime
// settings.json owning mcpServers.foo + a plugin mcp.json also declaring foo
// → the published settings retains the runtime foo, the plugin foo is dropped
// and recorded in DroppedComponents. A non-colliding plugin server (bar)
// survives.
func TestProjectPlugins_RuntimeWins_DropsPluginMCP(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()

	// Plugin mcp.json declares foo (clashes with runtime) + bar (survives).
	pluginMCP := `{"mcpServers":{"foo":{"type":"http","url":"https://PLUGIN-foo"},"bar":{"type":"http","url":"https://PLUGIN-bar"}}}`
	stageTree(t, achDir, "plug-a", map[string]string{"mcp/servers.json": pluginMCP})

	// Seed the runtime-owned settings.json on disk with foo (runtime URL) so
	// the deep-merge would otherwise overwrite it.
	settingsAbs := filepath.Join(toolRoot, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsAbs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runtimeSeed := `{"mcpServers":{"foo":{"type":"http","url":"https://RUNTIME-foo"}}}`
	if err := os.WriteFile(settingsAbs, []byte(runtimeSeed), 0o600); err != nil {
		t.Fatalf("seed runtime settings: %v", err)
	}

	d := &adapterDispatcherImpl{platformID: "fake"}
	// Pre-populate result.WrittenFiles with the runtime entry owning foo (this
	// is what the RenderRuntime loop fills BEFORE projectPlugins runs).
	result := RenderResult{
		WrittenFiles: []FileWrite{
			{Target: ".claude/settings.json", Merge: mergeStrDeep, Keys: []string{"mcpServers.foo"}},
		},
	}
	if err := d.projectPlugins(fakeProjAdapter{}, nil, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectPlugins runtime-wins: %v", err)
	}

	// On-disk settings.json keeps the RUNTIME foo, gains bar, drops PLUGIN foo.
	body, err := os.ReadFile(settingsAbs)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	ms, _ := doc["mcpServers"].(map[string]any)
	foo, _ := ms["foo"].(map[string]any)
	if foo == nil || foo["url"] != "https://RUNTIME-foo" {
		t.Errorf("runtime-owned mcpServers.foo was shadowed; got %v", ms["foo"])
	}
	if strings.Contains(string(body), "PLUGIN-foo") {
		t.Errorf("plugin foo leaked into settings: %s", body)
	}
	bar, _ := ms["bar"].(map[string]any)
	if bar == nil || bar["url"] != "https://PLUGIN-bar" {
		t.Errorf("non-colliding plugin bar should survive; got %v", ms["bar"])
	}

	// The drop is recorded once in DroppedComponents.
	var found bool
	for _, dc := range result.DroppedComponents {
		if strings.Contains(dc, "foo") && strings.Contains(dc, "runtime-owned") {
			found = true
		}
	}
	if !found {
		t.Errorf("DroppedComponents missing the foo runtime-owned drop; got %v", result.DroppedComponents)
	}
}

// TestProjectPlugins_RuntimeWins_AllCollide_SkipsPublish proves the publish is
// skipped entirely when EVERY contributed key collides with a runtime-owned id
// (still recording the drop). The would-be projected settings file must not be
// created when no runtime seed exists at the target path.
func TestProjectPlugins_RuntimeWins_AllCollide_SkipsPublish(t *testing.T) {
	achDir := t.TempDir()
	toolRoot := t.TempDir()
	stageTree(t, achDir, "plug-a", map[string]string{
		"mcp/servers.json": `{"mcpServers":{"foo":{"type":"http","url":"https://PLUGIN-foo"}}}`,
	})

	d := &adapterDispatcherImpl{platformID: "fake"}
	result := RenderResult{
		WrittenFiles: []FileWrite{
			{Target: ".claude/settings.json", Merge: mergeStrDeep, Keys: []string{"mcpServers.foo"}},
		},
	}
	if err := d.projectPlugins(fakeProjAdapter{}, nil, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectPlugins all-collide: %v", err)
	}
	// No projected MCP row (the only contributed key collided → skip publish).
	for _, pf := range result.ProjectedFiles {
		if pf.Target == ".claude/settings.json" {
			t.Errorf("settings.json published despite all keys colliding: %+v", pf)
		}
	}
	// The settings file must not have been created by the projection.
	if _, err := os.Stat(filepath.Join(toolRoot, ".claude", "settings.json")); err == nil {
		t.Errorf("projection created settings.json despite all-collide skip")
	}
	// Drop still recorded.
	if len(result.DroppedComponents) == 0 {
		t.Errorf("all-collide skip must still record the drop")
	}
}

// fakeSkillsAdapter routes skills/**/* → .claude/skills/**/* (MergeReplace).
// hooks/ is a KnownComponentKind with no rule here → will be dropped.
// .claude-plugin/ and README.md are metadata/docs → silently skipped (not dropped).
type fakeSkillsAdapter struct{ fakeProjAdapter }

func (fakeSkillsAdapter) ProjectionRules() []route.Rule {
	return []route.Rule{
		{FromGlob: "skills/**/*", ToGlob: ".claude/skills/**/*", Merge: adapter.MergeReplace},
	}
}

// TestProjectPlugins_DroppedByKind_AndProjectedByKind proves that:
//   - ProjectedByKind["skills"] > 0 after projecting a skills/ entry,
//   - DroppedByKind["hooks"] == [pluginName] (hooks/ is a KNOWN kind with no rule),
//   - .claude-plugin/ and README.md are SILENTLY skipped (NOT in DroppedByKind).
func TestProjectPlugins_DroppedByKind_AndProjectedByKind(t *testing.T) {
	const pluginName = "my-plugin"
	achDir := t.TempDir()
	toolRoot := t.TempDir()

	// Stage: skills/foo.md (routed), hooks/pre.sh (known, unrouted → dropped),
	// .claude-plugin/manifest.json + README.md (metadata/docs → silently skipped).
	stageTree(t, achDir, pluginName, map[string]string{
		"skills/foo.md":               "# Foo skill\n",
		"hooks/pre.sh":                "#!/bin/sh\necho hi\n",
		".claude-plugin/manifest.json": `{"name":"my-plugin"}`,
		"README.md":                   "# My Plugin\n",
	})

	d := &adapterDispatcherImpl{platformID: "fakeskills"}
	var result RenderResult
	if err := d.projectPlugins(fakeSkillsAdapter{}, nil, achDir, toolRoot, &result); err != nil {
		t.Fatalf("projectPlugins: %v", err)
	}

	// .claude-plugin must NOT appear in DroppedByKind (it is metadata — silently skipped).
	if _, ok := result.DroppedByKind[".claude-plugin"]; ok {
		t.Errorf("metadata .claude-plugin must not be dropped; got %v", result.DroppedByKind)
	}
	// README.md is docs — must also be absent.
	if _, ok := result.DroppedByKind["README.md"]; ok {
		t.Errorf("docs README.md must not be dropped; got %v", result.DroppedByKind)
	}

	// hooks/ is a KnownComponentKind with no rule → exactly one plugin attributed.
	if got := result.DroppedByKind["hooks"]; len(got) != 1 || got[0] != pluginName {
		t.Errorf("DroppedByKind[hooks] = %v; want [%s]", got, pluginName)
	}

	// skills/ was routed → tally must be > 0.
	if result.ProjectedByKind["skills"] == 0 {
		t.Errorf("ProjectedByKind[skills] = 0; want > 0")
	}
}

// stageTree writes a plugin source tree under <achDir>/plugin/<name>/ (internal
// sibling of the external-package stagePluginTree helper).
func stageTree(t *testing.T, achDir, name string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(achDir, "plugin", name, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", abs, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", abs, err)
		}
	}
}
