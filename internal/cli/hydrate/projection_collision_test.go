// SPDX-License-Identifier: Apache-2.0

package hydrate_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/conflict"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/hydrate"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// newProjectionManifest returns the minimal manifest shape the projection
// leg gates on (a non-nil Context block keeps projectPlugins=true effective;
// Runtime is present but empty). Mirrors the rehydrate harness.
func newProjectionManifest() *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: "v1alpha1",
		Environment:   "demo",
		Runtime:       &manifest.RuntimeBlock{},
		Context:       &manifest.ContextBlock{},
	}
}

// TestProjection_MultiPlugin_CollisionRejected proves CR-01's core fix: two
// distinct plugins ("plug-a" and "plug-b") both shipping rules/foo.md route
// — under D-01's flat kind-routing — to the SAME native Target
// .claude/rules/foo.md. Because there is NO per-plugin destination namespace
// to disambiguate, this is an unresolvable cross-plugin collision. Under the
// opt-in --conflict=refuse policy (the pre-Phase-1 default) the dispatcher
// MUST fail-fast with an error naming BOTH plugins and the contested Target,
// rather than silently letting the second publishFile overwrite the first
// (last-writer-wins) and recording a duplicate-Target row in state.Plugins[].
// The call erroring is itself the proof that no duplicate-Target row was
// appended to ProjectedFiles. (The default policy now namespaces — see
// TestProjection_MultiPlugin_Collision_NamespaceKeepsBoth.)
func TestProjection_MultiPlugin_CollisionRejected(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()
	toolRoot := t.TempDir()

	stagePluginTree(t, achDir, "plug-a", map[string]string{
		"rules/foo.md": "# from plug-a\n",
	})
	stagePluginTree(t, achDir, "plug-b", map[string]string{
		"rules/foo.md": "# from plug-b\n",
	})

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false, conflict.Refuse)
	m := newProjectionManifest()

	res, err := disp.Render(context.Background(), m, nil, achDir, toolRoot, true, true)
	if err == nil {
		t.Fatalf("Render with two plugins colliding on rules/foo.md returned nil error; want a cross-plugin collision error. ProjectedFiles=%+v", res.ProjectedFiles)
	}
	msg := err.Error()
	for _, want := range []string{"plug-a", "plug-b", ".claude/rules/foo.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("collision error %q does not mention %q", msg, want)
		}
	}
}

// TestProjection_MultiPlugin_Collision_NamespaceKeepsBoth proves the Phase-1
// default policy: the same two plugins colliding on rules/foo.md no longer
// abort — each write is leaf-prefixed by its plugin name so BOTH survive at
// distinct Targets (.claude/rules/plug-a-foo.md + plug-b-foo.md), with the
// bare Target written by neither.
func TestProjection_MultiPlugin_Collision_NamespaceKeepsBoth(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()
	toolRoot := t.TempDir()

	stagePluginTree(t, achDir, "plug-a", map[string]string{"rules/foo.md": "# from plug-a\n"})
	stagePluginTree(t, achDir, "plug-b", map[string]string{"rules/foo.md": "# from plug-b\n"})

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false, conflict.Namespace)
	m := newProjectionManifest()

	res, err := disp.Render(context.Background(), m, nil, achDir, toolRoot, true, true)
	if err != nil {
		t.Fatalf("namespace policy must not error on collision: %v", err)
	}
	if len(res.ProjectedFiles) != 2 {
		t.Fatalf("ProjectedFiles = %d; want 2 (both namespaced)", len(res.ProjectedFiles))
	}
	for _, rel := range []string{".claude/rules/plug-a-foo.md", ".claude/rules/plug-b-foo.md"} {
		if _, statErr := os.Stat(filepath.Join(toolRoot, filepath.FromSlash(rel))); statErr != nil {
			t.Errorf("expected namespaced file %q: %v", rel, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(toolRoot, ".claude", "rules", "foo.md")); statErr == nil {
		t.Errorf("bare .claude/rules/foo.md must not exist under namespace policy")
	}
}

// TestProjection_MultiPlugin_DistinctPaths_TwoTargets proves the common
// multi-plugin case still succeeds: "plug-a" ships rules/a.md and "plug-b"
// ships rules/b.md, projecting to two DISTINCT native Targets. Both files
// land on disk, both are recorded as distinct (non-duplicate) Targets, and a
// SECOND Render against the recorded prior state is a per-file byte no-op
// with the D-08 passthrough invariant (Hash == SourceHash) holding for each.
func TestProjection_MultiPlugin_DistinctPaths_TwoTargets(t *testing.T) {
	withCleanHome(t)
	achDir := t.TempDir()
	toolRoot := t.TempDir()

	const bodyA = "# rule a\n"
	const bodyB = "# rule b\n"
	stagePluginTree(t, achDir, "plug-a", map[string]string{"rules/a.md": bodyA})
	stagePluginTree(t, achDir, "plug-b", map[string]string{"rules/b.md": bodyB})

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false, conflict.Namespace)
	m := newProjectionManifest()

	res1, err := disp.Render(context.Background(), m, nil, achDir, toolRoot, true, true)
	if err != nil {
		t.Fatalf("first Render (distinct paths): %v", err)
	}
	if len(res1.ProjectedFiles) != 2 {
		t.Fatalf("first Render ProjectedFiles = %d; want 2", len(res1.ProjectedFiles))
	}

	// Index recorded entries by Target; assert exactly the two expected
	// distinct native Targets (no duplicate row).
	byTarget := map[string]hydrate.FileWrite{}
	for _, pf := range res1.ProjectedFiles {
		if _, dup := byTarget[pf.Target]; dup {
			t.Fatalf("duplicate-Target row recorded: %q appears twice in ProjectedFiles", pf.Target)
		}
		byTarget[pf.Target] = pf
	}
	wantA := ".claude/rules/a.md"
	wantB := ".claude/rules/b.md"
	if _, ok := byTarget[wantA]; !ok {
		t.Errorf("missing recorded Target %q; got %v", wantA, keysOf(byTarget))
	}
	if _, ok := byTarget[wantB]; !ok {
		t.Errorf("missing recorded Target %q; got %v", wantB, keysOf(byTarget))
	}
	if wantA == wantB {
		t.Fatalf("test bug: expected targets equal")
	}

	// Both files exist on disk at their native destinations.
	absA := filepath.Join(toolRoot, ".claude", "rules", "a.md")
	absB := filepath.Join(toolRoot, ".claude", "rules", "b.md")
	diskA1, errA := os.ReadFile(absA)
	if errA != nil {
		t.Fatalf("plug-a projected file missing at %s: %v", absA, errA)
	}
	diskB1, errB := os.ReadFile(absB)
	if errB != nil {
		t.Fatalf("plug-b projected file missing at %s: %v", absB, errB)
	}
	if string(diskA1) != bodyA || string(diskB1) != bodyB {
		t.Errorf("projected content mismatch: a=%q (want %q), b=%q (want %q)", diskA1, bodyA, diskB1, bodyB)
	}

	// D-08 passthrough invariant per entry.
	for _, pf := range res1.ProjectedFiles {
		if pf.Hash != pf.SourceHash {
			t.Errorf("entry %q: Hash (%q) != SourceHash (%q); want equal (D-08)", pf.Target, pf.Hash, pf.SourceHash)
		}
	}

	// Compose prior state.Plugins[] from both entries (commit.go step12 shape).
	prior := &state.File{SchemaVersion: "3", Environment: "demo"}
	for _, pf := range res1.ProjectedFiles {
		prior.Plugins = append(prior.Plugins, state.FileEntry{
			Target:     pf.Target,
			Hash:       pf.Hash,
			SourceHash: pf.SourceHash,
			Merge:      pf.Merge,
			Keys:       pf.Keys,
		})
	}

	// --- 2nd Render: unchanged source + prior state => per-file byte no-op ---
	res2, err := disp.Render(context.Background(), m, prior, achDir, toolRoot, true, true)
	if err != nil {
		t.Fatalf("second Render (no-op): %v", err)
	}
	if len(res2.ProjectedFiles) != 2 {
		t.Fatalf("second Render ProjectedFiles = %d; want 2", len(res2.ProjectedFiles))
	}
	got2 := map[string]bool{}
	for _, pf := range res2.ProjectedFiles {
		got2[pf.Target] = true
	}
	if !got2[wantA] || !got2[wantB] {
		t.Errorf("second Render lost a distinct Target; got %v", got2)
	}

	diskA2, _ := os.ReadFile(absA)
	diskB2, _ := os.ReadFile(absB)
	if !bytes.Equal(diskA1, diskA2) {
		t.Errorf("plug-a bytes changed across re-hydrate:\n  before=%q\n  after=%q", diskA1, diskA2)
	}
	if !bytes.Equal(diskB1, diskB2) {
		t.Errorf("plug-b bytes changed across re-hydrate:\n  before=%q\n  after=%q", diskB1, diskB2)
	}
}

// keysOf is a tiny helper to render the set of recorded Targets in an error.
func keysOf(m map[string]hydrate.FileWrite) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestProjection_MaliciousPluginName_Rejected exercises the plugin-name
// segment guard (T-01-04) directly. A real os.ReadDir yields only basenames,
// so a literal "../evil" or "a/b" cannot exist as a single on-disk dirent —
// the on-disk attack vector is already closed by SAFE-01/02 extract-time
// checks. This test therefore asserts the guard's defense-in-depth contract
// at the boundary it actually defends, feeding the traversal/absolute/
// multi-segment vectors directly via the hydrate.ValidatePluginName export.
func TestProjection_MaliciousPluginName_Rejected(t *testing.T) {
	malicious := []string{"../evil", "a/b", "/abs", "..", ""}
	for _, name := range malicious {
		name := name
		t.Run("reject_"+name, func(t *testing.T) {
			if err := hydrate.ValidatePluginName(name); err == nil {
				t.Errorf("ValidatePluginName(%q) = nil; want a rejection error", name)
			}
		})
	}
	if err := hydrate.ValidatePluginName("good-plugin"); err != nil {
		t.Errorf("ValidatePluginName(%q) = %v; want nil (valid single segment)", "good-plugin", err)
	}
}

// ---- --only (partial run) ----

// installedRows converts a Render's projected files into the state rows a
// hydrate records (the prior state of the next run).
func installedRows(res hydrate.RenderResult) *state.File {
	f := &state.File{SchemaVersion: "3", Environment: "demo"}
	for _, pf := range res.ProjectedFiles {
		f.Plugins = append(f.Plugins, state.FileEntry{Target: pf.Target, Hash: pf.Hash, SourceHash: pf.SourceHash,
			Merge: pf.Merge, Keys: pf.Keys, Source: pf.Source})
	}
	return f
}

// renderOnlyA wipes the plugin stage, stages ONLY plug-a (what `--only
// plugin/plug-a` extracts) and renders as a partial run against prior.
func renderOnlyA(t *testing.T, achDir, toolRoot string, policy conflict.Policy, prior *state.File) (hydrate.RenderResult, error) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(achDir, "plugin")); err != nil {
		t.Fatal(err)
	}
	stagePluginTree(t, achDir, "plug-a", map[string]string{"rules/foo.md": "# from plug-a\n"})
	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false, policy)
	return disp.Render(hydrate.WithPartialRun(context.Background()), newProjectionManifest(), prior, achDir, toolRoot, true, false)
}

// TestProjection_Only_CollidesWithInstalledPlugin: plug-b is installed at
// .claude/rules/foo.md; `--only plugin/plug-a` projecting the same file must
// resolve the collision like a full hydrate would (namespace → plug-a-foo.md)
// and leave plug-b's file untouched. Covers plug-b installed bare AND already
// namespaced by an earlier full hydrate of both.
func TestProjection_Only_CollidesWithInstalledPlugin(t *testing.T) {
	for name, stageBoth := range map[string]bool{"b bare": false, "b namespaced": true} {
		t.Run(name, func(t *testing.T) {
			withCleanHome(t)
			achDir, toolRoot := t.TempDir(), t.TempDir()
			stagePluginTree(t, achDir, "plug-b", map[string]string{"rules/foo.md": "# from plug-b\n"})
			if stageBoth {
				stagePluginTree(t, achDir, "plug-a", map[string]string{"rules/foo.md": "# from plug-a\n"})
			}
			_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false, conflict.Namespace)
			full, err := disp.Render(context.Background(), newProjectionManifest(), nil, achDir, toolRoot, true, true)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			prior := installedRows(full)
			bTarget := filepath.Join(toolRoot, ".claude", "rules", "foo.md")
			if stageBoth {
				bTarget = filepath.Join(toolRoot, ".claude", "rules", "plug-b-foo.md")
			}

			res, err := renderOnlyA(t, achDir, toolRoot, conflict.Namespace, prior)
			if err != nil {
				t.Fatalf("--only render: %v", err)
			}
			if len(res.ProjectedFiles) != 1 || res.ProjectedFiles[0].Target != ".claude/rules/plug-a-foo.md" {
				t.Fatalf("ProjectedFiles = %+v; want only .claude/rules/plug-a-foo.md", res.ProjectedFiles)
			}
			if b, _ := os.ReadFile(bTarget); string(b) != "# from plug-b\n" {
				t.Errorf("plug-b's file %s = %q; want untouched", bTarget, b)
			}
		})
	}
}

// TestProjection_Only_SkipKeepsInstalledIncumbent: under --conflict=skip an
// --only run keeps another item's installed file untouched and warns; it
// never fails.
func TestProjection_Only_SkipKeepsInstalledIncumbent(t *testing.T) {
	withCleanHome(t)
	achDir, toolRoot := t.TempDir(), t.TempDir()
	stagePluginTree(t, achDir, "plug-b", map[string]string{"rules/foo.md": "# from plug-b\n"})
	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false, conflict.Skip)
	full, err := disp.Render(context.Background(), newProjectionManifest(), nil, achDir, toolRoot, true, true)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	res, err := renderOnlyA(t, achDir, toolRoot, conflict.Skip, installedRows(full))
	if err != nil {
		t.Fatalf("--only render: %v", err)
	}
	if len(res.ProjectedFiles) != 0 {
		t.Errorf("ProjectedFiles = %+v; want plug-a skipped", res.ProjectedFiles)
	}
	if b, _ := os.ReadFile(filepath.Join(toolRoot, ".claude", "rules", "foo.md")); string(b) != "# from plug-b\n" {
		t.Errorf("plug-b's file = %q; want untouched", b)
	}
	if len(res.ConflictWarnings) != 1 {
		t.Fatalf("ConflictWarnings = %v; want exactly one warning naming the file and its owner", res.ConflictWarnings)
	}
	for _, want := range []string{".claude/rules/foo.md", "plug-b"} {
		if !strings.Contains(res.ConflictWarnings[0], want) {
			t.Errorf("ConflictWarnings[0] = %q; missing %q", res.ConflictWarnings[0], want)
		}
	}
}

// TestProjection_Only_OverwriteRefusesInstalledIncumbent: under
// --conflict=overwrite an --only run has nothing staged of its own to prefer
// over another item's installed file, so it refuses outright — nothing is
// written and the incumbent survives untouched.
func TestProjection_Only_OverwriteRefusesInstalledIncumbent(t *testing.T) {
	withCleanHome(t)
	achDir, toolRoot := t.TempDir(), t.TempDir()
	stagePluginTree(t, achDir, "plug-b", map[string]string{"rules/foo.md": "# from plug-b\n"})
	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false, conflict.Overwrite)
	full, err := disp.Render(context.Background(), newProjectionManifest(), nil, achDir, toolRoot, true, true)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	_, err = renderOnlyA(t, achDir, toolRoot, conflict.Overwrite, installedRows(full))
	if err == nil {
		t.Fatal("--only render with --conflict=overwrite colliding with an installed file: want an error, got nil")
	}
	for _, want := range []string{"--conflict=overwrite", ".claude/rules/foo.md", "plug-b", "uninstall it first"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(toolRoot, ".claude", "rules", "foo.md")); string(b) != "# from plug-b\n" {
		t.Errorf("plug-b's file = %q; want untouched", b)
	}
	if _, statErr := os.Stat(filepath.Join(toolRoot, ".claude", "rules", "plug-a-foo.md")); statErr == nil {
		t.Errorf("plug-a's file must not have been written on refusal")
	}
}

// TestProjection_Only_RuntimeWinsAgainstPriorRuntime: runtime is not written
// under --only, so the runtime-wins MCP drop (D-10) must use the prior
// state's runtime keys — a plugin MCP id equal to a runtime-owned one is
// dropped and the runtime entry stays, as in a full hydrate.
func TestProjection_Only_RuntimeWinsAgainstPriorRuntime(t *testing.T) {
	withCleanHome(t)
	achDir, toolRoot := t.TempDir(), t.TempDir()
	stagePluginTree(t, achDir, "plug-a", map[string]string{
		".mcp.json": `{"mcpServers":{"foo":{"type":"http","url":"https://PLUGIN-foo"},"bar":{"type":"http","url":"https://PLUGIN-bar"}}}`,
	})
	mcpAbs := filepath.Join(toolRoot, ".mcp.json")
	if err := os.WriteFile(mcpAbs, []byte(`{"mcpServers":{"foo":{"type":"http","url":"https://RUNTIME-foo"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prior := &state.File{SchemaVersion: "3", Environment: "demo", Adapter: state.AdapterSection{ID: "claude-code",
		Files: []state.FileEntry{{Target: ".mcp.json", Merge: "deep", Keys: []string{"mcpServers.foo"}}}}}

	_, disp := hydrate.NewWiring(nil, "claude-code", extract.DefaultLimits(), false, false, false, conflict.Namespace)
	res, err := disp.Render(hydrate.WithPartialRun(context.Background()), newProjectionManifest(), prior, achDir, toolRoot, true, false)
	if err != nil {
		t.Fatalf("--only render: %v", err)
	}
	body, _ := os.ReadFile(mcpAbs)
	if strings.Contains(string(body), "PLUGIN-foo") || !strings.Contains(string(body), "RUNTIME-foo") {
		t.Errorf("runtime-owned foo shadowed: %s", body)
	}
	if !strings.Contains(string(body), "PLUGIN-bar") {
		t.Errorf("non-colliding plugin bar missing: %s", body)
	}
	for _, pf := range res.ProjectedFiles {
		for _, k := range pf.Keys {
			if k == "mcpServers.foo" {
				t.Errorf("plugin row claims runtime-owned mcpServers.foo: %+v", pf)
			}
		}
	}
}
