// SPDX-License-Identifier: Apache-2.0

package hydrate_test

import (
	"bytes"
	"context"
	"encoding/json"
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

// nativeWorkspace stages one plugin the way step 6 leaves it and returns the
// workspace, the Environment's directory (which a direct Render call also uses
// as its stage) and a manifest naming the plugin with the given delivery.
func nativeWorkspace(t *testing.T, delivery string, files map[string]string) (ws, stage string, m *manifest.Manifest) {
	t.Helper()
	withCleanHome(t)
	ws = t.TempDir()
	stage = filepath.Join(ws, ".ach", "dev")
	stagePluginTree(t, stage, "memory", files)
	m = &manifest.Manifest{Environment: "dev", Context: &manifest.ContextBlock{Plugins: []manifest.ContentRef{{
		ID: "memory", Name: "memory", DownloadURL: "http://x/content/plugin/memory", Delivery: delivery,
	}}}}
	return ws, stage, m
}

var piPlugin = map[string]string{
	"package.json":      `{"name":"memory","pi":{"extensions":["./ext.js"]}}`,
	"ext.js":            "export default function () {}\n",
	"skills/m/SKILL.md": "---\nname: m\ndescription: d\n---\n\nbody\n",
	"hooks/run.sh":      "#!/bin/sh\necho hi\n",
}

func piPackages(t *testing.T, ws string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(ws, ".pi", "settings.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Packages []string }
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Packages
}

func TestNative_InstallsTheTreeIntactAndRecordsOneRow(t *testing.T) {
	ws, stage, m := nativeWorkspace(t, manifest.DeliveryNative, piPlugin)
	_, disp := hydrate.NewWiring(nil, "pi", extract.DefaultLimits(), false, false, false, conflict.Namespace)

	rr, err := disp.Render(context.Background(), m, nil, stage, ws, true, false)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	dir := filepath.Join(ws, ".ach", "dev", "native", "pi", "plugins", "memory")
	for rel, want := range piPlugin {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want the staged bytes", rel, got, err)
		}
	}
	// Nothing of a native plugin is split into the tool's own folders.
	if _, err := os.Stat(filepath.Join(ws, ".agents", "skills")); !os.IsNotExist(err) {
		t.Error("a native plugin's skill was projected into .agents/skills")
	}
	if len(rr.ProjectedFiles) != 1 {
		t.Fatalf("ProjectedFiles = %d rows; want 1", len(rr.ProjectedFiles))
	}
	row := rr.ProjectedFiles[0]
	if row.Target != ".ach/dev/native/pi/plugins/memory" || row.Merge != "native" || row.Source != "memory" || row.Hash == "" {
		t.Fatalf("row = %+v", row)
	}
	if got := piPackages(t, ws); len(got) != 1 || got[0] != dir {
		t.Fatalf("pi packages = %v; want [%s]", got, dir)
	}
	if len(rr.NativeNotes) != 1 || !strings.Contains(rr.NativeNotes[0], "trusted project") {
		t.Errorf("NativeNotes = %v; want pi's trust note", rr.NativeNotes)
	}

	// Same bytes again: same hash, and no note the second time.
	prev := &state.File{SchemaVersion: "3", Environment: "dev", Plugins: []state.FileEntry{{
		Target: row.Target, Hash: row.Hash, Merge: row.Merge, Source: row.Source}}}
	rr2, err := disp.Render(context.Background(), m, prev, stage, ws, true, false)
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if rr2.ProjectedFiles[0].Hash != row.Hash {
		t.Error("the hash of an unchanged tree moved")
	}
	if len(rr2.NativeNotes) != 0 {
		t.Errorf("an unchanged plugin repeated its note: %v", rr2.NativeNotes)
	}

	// A changed file moves the hash and replaces the tree, dropping removed files.
	if err := os.WriteFile(filepath.Join(stage, "plugin", "memory", "ext.js"), []byte("// v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(stage, "plugin", "memory", "hooks", "run.sh")); err != nil {
		t.Fatal(err)
	}
	rr3, err := disp.Render(context.Background(), m, prev, stage, ws, true, false)
	if err != nil {
		t.Fatalf("third Render: %v", err)
	}
	if rr3.ProjectedFiles[0].Hash == row.Hash {
		t.Error("a changed tree kept its hash")
	}
	if _, err := os.Stat(filepath.Join(dir, "hooks", "run.sh")); !os.IsNotExist(err) {
		t.Error("a file the plugin dropped is still on disk")
	}
}

func TestNative_NoManifestForTheToolMeansNothingAndAWarning(t *testing.T) {
	ws, stage, m := nativeWorkspace(t, manifest.DeliveryNative, map[string]string{
		".claude-plugin/plugin.json": `{"name":"memory"}`,
		"skills/m/SKILL.md":          "---\nname: m\ndescription: d\n---\n\nbody\n",
	})
	_, disp := hydrate.NewWiring(nil, "pi", extract.DefaultLimits(), false, false, false, conflict.Namespace)
	rr, err := disp.Render(context.Background(), m, nil, stage, ws, true, false)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(rr.ProjectedFiles) != 0 {
		t.Errorf("ProjectedFiles = %v; want none (no fallback to the file split)", rr.ProjectedFiles)
	}
	if _, err := os.Stat(filepath.Join(ws, ".agents")); !os.IsNotExist(err) {
		t.Error("the plugin was split into files for a tool it does not support")
	}
	if len(rr.NativeWarnings) != 1 || !strings.Contains(rr.NativeWarnings[0], "memory") ||
		!strings.Contains(rr.NativeWarnings[0], "carries no pi plugin manifest") {
		t.Errorf("NativeWarnings = %v; want one naming the plugin and the missing pi manifest", rr.NativeWarnings)
	}
}

func TestNative_CursorGetsAWarning(t *testing.T) {
	ws, stage, m := nativeWorkspace(t, manifest.DeliveryNative, piPlugin)
	_, disp := hydrate.NewWiring(nil, "cursor", extract.DefaultLimits(), false, false, false, conflict.Namespace)
	rr, err := disp.Render(context.Background(), m, nil, stage, ws, true, false)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(rr.ProjectedFiles) != 0 || len(rr.NativeWarnings) != 1 {
		t.Fatalf("files = %v, warnings = %v; want no file and one warning", rr.ProjectedFiles, rr.NativeWarnings)
	}
	if !strings.Contains(rr.NativeWarnings[0], "does not offer for cursor") {
		t.Errorf("NativeWarnings = %v; want the no-native-delivery wording", rr.NativeWarnings)
	}
}

func TestNative_AnAchPluginIsStillSplit(t *testing.T) {
	ws, stage, m := nativeWorkspace(t, "", piPlugin)
	_, disp := hydrate.NewWiring(nil, "pi", extract.DefaultLimits(), false, false, false, conflict.Namespace)
	rr, err := disp.Render(context.Background(), m, nil, stage, ws, true, false)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".ach", "dev", "native")); !os.IsNotExist(err) {
		t.Error("an ach plugin was installed natively")
	}
	if _, err := os.Stat(filepath.Join(ws, ".agents", "skills")); err != nil {
		t.Errorf("an ach plugin's skill was not projected: %v", err)
	}
	for _, f := range rr.ProjectedFiles {
		if f.Merge == "native" {
			t.Errorf("row %+v is native", f)
		}
	}
}

func TestNative_SyncRemovesTheTreeAndTheRegistration(t *testing.T) {
	ws, stage, m := nativeWorkspace(t, manifest.DeliveryNative, piPlugin)
	_, disp := hydrate.NewWiring(nil, "pi", extract.DefaultLimits(), false, false, false, conflict.Namespace)
	rr, err := disp.Render(context.Background(), m, nil, stage, ws, true, false)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	row := rr.ProjectedFiles[0]
	prev := &state.File{SchemaVersion: "3", Environment: "dev", Plugins: []state.FileEntry{{
		Target: row.Target, Hash: row.Hash, Merge: row.Merge, Source: row.Source}}}
	next := &state.File{SchemaVersion: "3", Environment: "dev"}
	achDir := filepath.Join(ws, ".ach", "dev")

	var out bytes.Buffer
	if _, err := hydrate.Sync(prev, next, achDir, ws, hydrate.SyncOptions{Stderr: &out, DryRun: true}); err != nil {
		t.Fatalf("dry-run Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(row.Target))); err != nil {
		t.Fatalf("a dry run removed the tree: %v", err)
	}

	stats, err := hydrate.Sync(prev, next, achDir, ws, hydrate.SyncOptions{Stderr: &out})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Pruned != 1 || stats.Preserved != 0 {
		t.Errorf("stats = %+v; want 1 pruned", stats)
	}
	if _, err := os.Stat(filepath.Join(achDir, "native")); !os.IsNotExist(err) {
		t.Error("the native folder was left behind")
	}
	if got := piPackages(t, ws); len(got) != 0 {
		t.Errorf("pi packages = %v; want none", got)
	}
	if strings.Contains(out.String(), "never recursively deleted") {
		t.Errorf("Sync treated the native row as a stray directory: %s", out.String())
	}
}
