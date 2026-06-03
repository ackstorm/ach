// SPDX-License-Identifier: Apache-2.0

package hydrate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"

	// Blank-import the pimono adapter so its init() Register fires before the
	// NewWiring(nil, "pimono", ...) lookup resolves it (D-37). Mirrors the
	// autodetect_test.go pattern for the other four adapters.
	_ "github.com/ackstorm/ach/internal/cli/adapter/pimono"
)

// TestProjection_Pimono_Smoke is the D-37 hermetic pimono projection smoke.
// It drives ONE staged plugin through the full dispatcher Render against the
// registered pimono adapter (--platform pimono) and asserts, end-to-end:
//
//	(1) commands/*.md projects verbatim to .pi/agent/prompts/, skills/* to
//	    .pi/agent/skills/ (passthrough globs, native dests — SC2 routing);
//	(2) .pi/mcp.json carries BOTH the plugin MCP server (from mcp/servers.json,
//	    deep-merged via projection) AND the runtime MCP server (from
//	    RenderRuntime) under top-level mcpServers (D-33);
//	(3) drop accounting records exactly {AGENTS.md, agents, rules} as Dropped,
//	    accumulate-once; mcp is NOT dropped (it has the mcp/**/* rule);
//	(4) .pi/mcp.json is a co-owned deep-merge FileEntry — uninstall via
//	    Sync(prev, empty) does inverse-merge key subtraction preserving a
//	    co-owned user key, while the file-owned .pi/agent/... entries are
//	    plain-deleted (LIFE-04 + D-33);
//	(5) re-hydrate is a byte-identical no-op for every pimono-projected file
//	    (FMT-05 idempotence).
//
// Hermetic — no kind cluster; runs under `make test-unit`. The heavy matrix
// (collision fail-fast, cross-adapter, full idempotence) stays in the Phase 6
// verification gate (VER-01..03); this is deliberately minimal.
func TestProjection_Pimono_Smoke(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()
	toolRoot := t.TempDir()

	const (
		helloBody = "# hello\nproject me verbatim\n"
		skillBody = "# skill1\nverbatim skill body\n"
		pluginID  = "plugin-mcp"
		runtimeID = "runtime-mcp"
		coOwnedID = "user-pi"
	)

	// One plugin carrying: two passthrough globs (commands + skills), one
	// mcp file (→ .pi/mcp.json deep-merge), and three drop-only kinds
	// (rules/, agents/, AGENTS.md).
	stagePluginTree(t, achDir, "caveman", map[string]string{
		"commands/hello.md":      helloBody,
		"skills/skill1/SKILL.md": skillBody,
		"mcp/servers.json": `{"mcpServers":{"` + pluginID +
			`":{"type":"http","url":"http://localhost:9/plugin"}}}`,
		"rules/r.md":  "# rule\n",
		"agents/a.md": "# agent\n",
		"AGENTS.md":   "# agents top-level\n",
	})

	// Runtime block carrying ONE runtime MCP server so RenderRuntime emits it
	// into .pi/mcp.json (top-level mcpServers).
	m := &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime: &manifest.RuntimeBlock{
			MCPServers: []manifest.ContentRef{
				{ID: runtimeID, Endpoint: "http://localhost:8080/mcp/" + runtimeID},
			},
		},
		Context: &manifest.ContextBlock{},
	}

	_, disp := hydrate.NewWiring(nil, "pimono", extract.DefaultLimits(), false, false, false)

	res, err := disp.Render(context.Background(), m, nil, achDir, toolRoot, true)
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}

	// --- (1) passthrough globs land at the native .pi/agent/ dests ---------
	promptsAbs := filepath.Join(toolRoot, ".pi", "agent", "prompts", "hello.md")
	skillAbs := filepath.Join(toolRoot, ".pi", "agent", "skills", "skill1", "SKILL.md")
	mcpAbs := filepath.Join(toolRoot, ".pi", "mcp.json")

	if got := readFile(t, promptsAbs); got != helloBody {
		t.Errorf("commands/hello.md projection = %q; want verbatim %q", got, helloBody)
	}
	if got := readFile(t, skillAbs); got != skillBody {
		t.Errorf("skills/skill1/SKILL.md projection = %q; want verbatim %q", got, skillBody)
	}

	// SC2 routing: nothing leaks to the verbatim <toolRoot>/commands|skills paths.
	if _, serr := os.Stat(filepath.Join(toolRoot, "commands", "hello.md")); serr == nil {
		t.Errorf("SC2 violation: command leaked to verbatim <toolRoot>/commands/hello.md")
	}
	if _, serr := os.Stat(filepath.Join(toolRoot, "skills", "skill1", "SKILL.md")); serr == nil {
		t.Errorf("SC2 violation: skill leaked to verbatim <toolRoot>/skills/...")
	}

	// ProjectedFiles must include the two passthrough globs + the mcp deep-merge.
	wantProjected := map[string]bool{
		".pi/agent/prompts/hello.md":       false,
		".pi/agent/skills/skill1/SKILL.md": false,
		".pi/mcp.json":                     false,
	}
	for _, pf := range res.ProjectedFiles {
		if _, ok := wantProjected[pf.Target]; ok {
			wantProjected[pf.Target] = true
		}
	}
	for tgt, seen := range wantProjected {
		if !seen {
			t.Errorf("ProjectedFiles missing %q; got %+v", tgt, res.ProjectedFiles)
		}
	}

	// --- (2) .pi/mcp.json carries BOTH the plugin and runtime servers ------
	servers := mcpServers(t, mcpAbs)
	if _, ok := servers[pluginID]; !ok {
		t.Errorf(".pi/mcp.json missing plugin server %q; got %v", pluginID, mcpServerKeys(servers))
	}
	if _, ok := servers[runtimeID]; !ok {
		t.Errorf(".pi/mcp.json missing runtime server %q; got %v", runtimeID, mcpServerKeys(servers))
	}

	// --- (3) drop accounting == {AGENTS.md, agents, rules}, mcp excluded ----
	gotDropped := append([]string(nil), res.DroppedComponents...)
	sort.Strings(gotDropped)
	wantDropped := []string{"AGENTS.md", "agents", "rules"}
	if !reflect.DeepEqual(gotDropped, wantDropped) {
		t.Errorf("DroppedComponents = %v; want %v (mcp must NOT be dropped)", gotDropped, wantDropped)
	}

	// --- (4) co-owned-registry uninstall (LIFE-04 + D-33) ------------------
	// Pre-seed .pi/mcp.json with the projected ids PLUS a co-owned user key,
	// then Sync(prev, empty) must subtract ONLY the recorded projected keys.
	writeJSON(t, mcpAbs, map[string]any{
		"mcpServers": map[string]any{
			pluginID:  map[string]any{"type": "http", "url": "http://localhost:9/plugin"},
			runtimeID: map[string]any{"type": "http", "url": "http://localhost:8080/mcp/" + runtimeID},
			coOwnedID: map[string]any{"type": "http", "url": "http://localhost:7/user"},
		},
	})

	prev := &state.File{
		SchemaVersion: "3",
		Environment:   "demo",
		Plugins: []state.FileEntry{
			// Co-owned deep-merge entry: Hash:"" skips the drift gate so the
			// inverse-merge branch runs (registry_test.go pattern).
			{
				Target: ".pi/mcp.json",
				Merge:  "deep",
				Keys:   []string{"mcpServers." + pluginID, "mcpServers." + runtimeID},
			},
			// File-owned entries (replace) → plain whole-file deletion.
			{Target: ".pi/agent/prompts/hello.md", Merge: "replace"},
			{Target: ".pi/agent/skills/skill1/SKILL.md", Merge: "replace"},
		},
	}

	if _, serr := hydrate.Sync(prev, &state.File{SchemaVersion: "3"}, toolRoot, toolRoot, hydrate.SyncOptions{}); serr != nil {
		t.Fatalf("Sync uninstall: %v", serr)
	}

	afterServers := mcpServers(t, mcpAbs)
	if _, gone := afterServers[pluginID]; gone {
		t.Errorf("uninstall must subtract projected key mcpServers.%s; got %v", pluginID, mcpServerKeys(afterServers))
	}
	if _, gone := afterServers[runtimeID]; gone {
		t.Errorf("uninstall must subtract projected key mcpServers.%s; got %v", runtimeID, mcpServerKeys(afterServers))
	}
	if _, kept := afterServers[coOwnedID]; !kept {
		t.Errorf("co-owned key mcpServers.%s must survive inverse-merge; got %v", coOwnedID, mcpServerKeys(afterServers))
	}
	if _, statErr := os.Stat(promptsAbs); !os.IsNotExist(statErr) {
		t.Errorf("file-owned .pi/agent/prompts/hello.md must be plain-deleted; stat err=%v", statErr)
	}
	if _, statErr := os.Stat(skillAbs); !os.IsNotExist(statErr) {
		t.Errorf("file-owned .pi/agent/skills/skill1/SKILL.md must be plain-deleted; stat err=%v", statErr)
	}

	// --- (5) re-hydrate is a byte-identical no-op (FMT-05) ------------------
	// Run a SECOND Render on a FRESH toolRoot against the same staged source +
	// the prior projected state, then assert each projected file's bytes and
	// the recorded FileEntry are byte-identical across the two runs.
	freshRoot := t.TempDir()

	res1, err := disp.Render(context.Background(), m, nil, achDir, freshRoot, true)
	if err != nil {
		t.Fatalf("re-hydrate baseline Render: %v", err)
	}
	priorState := &state.File{SchemaVersion: "3", Environment: "demo"}
	firstBytes := map[string][]byte{}
	firstEntry := map[string]hydrate.FileWrite{}
	for _, pf := range res1.ProjectedFiles {
		priorState.Plugins = append(priorState.Plugins, state.FileEntry{
			Target:     pf.Target,
			Hash:       pf.Hash,
			SourceHash: pf.SourceHash,
			Merge:      pf.Merge,
			Keys:       pf.Keys,
		})
		firstBytes[pf.Target] = readBytes(t, filepath.Join(freshRoot, filepath.FromSlash(pf.Target)))
		firstEntry[pf.Target] = pf
	}

	res2, err := disp.Render(context.Background(), m, priorState, achDir, freshRoot, true)
	if err != nil {
		t.Fatalf("re-hydrate no-op Render: %v", err)
	}
	for _, pf := range res2.ProjectedFiles {
		want, ok := firstEntry[pf.Target]
		if !ok {
			t.Errorf("re-hydrate produced a new Target %q absent from first run", pf.Target)
			continue
		}
		if pf.Hash != want.Hash || pf.SourceHash != want.SourceHash || pf.Merge != want.Merge {
			t.Errorf("FileEntry changed across re-hydrate for %q:\n  first=%+v\n  second=%+v", pf.Target, want, pf)
		}
		gotBytes := readBytes(t, filepath.Join(freshRoot, filepath.FromSlash(pf.Target)))
		if !bytes.Equal(firstBytes[pf.Target], gotBytes) {
			t.Errorf("bytes changed across re-hydrate for %q:\n  before=%q\n  after=%q", pf.Target, firstBytes[pf.Target], gotBytes)
		}
	}
}

// writeJSON marshals v to JSON and writes it to path (local helper — the
// package-hydrate writeJSON in registry_test.go is not visible from
// package hydrate_test).
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json %s: %v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write json %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	return string(readBytes(t, path))
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// mcpServers reads path as JSON and returns its top-level mcpServers map.
func mcpServers(t *testing.T, path string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(readBytes(t, path), &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	ms, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("%s missing top-level mcpServers map; got %+v", path, doc)
	}
	return ms
}

func mcpServerKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
