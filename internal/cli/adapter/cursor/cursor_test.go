// SPDX-License-Identifier: Apache-2.0

package cursor

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/manifest"
)

func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestCursor_ID(t *testing.T) {
	if got := (&Adapter{}).ID(); got != "cursor" {
		t.Fatalf("ID() = %q", got)
	}
}

func TestCursor_Aliases(t *testing.T) {
	want := []string{"cursor-agent", "cursor-cli"}
	if got := (&Adapter{}).Aliases(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Aliases() = %v, want %v", got, want)
	}
}

func TestCursor_Detect_NoSignals_ZeroMatch(t *testing.T) {
	isolatedHome(t)
	got, err := (&Adapter{}).Detect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "" || got.Confidence != 0 {
		t.Errorf("Detect(empty root) = %+v, want zero", got)
	}
}

func TestCursor_Detect_OneSignal_LowConfidence(t *testing.T) {
	isolatedHome(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := (&Adapter{}).Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "cursor" || got.Confidence != adapter.ConfidenceLow || len(got.Reasons) != 1 {
		t.Errorf("Detect with 1 signal = %+v", got)
	}
}

func TestCursor_Detect_TwoSignals_MediumConfidence(t *testing.T) {
	isolatedHome(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := (&Adapter{}).Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Confidence != adapter.ConfidenceMedium {
		t.Errorf("Detect with 2 signals Confidence = %v, want medium", got.Confidence)
	}
}

func TestCursor_Detect_HighConfidence_AllSignals(t *testing.T) {
	home := isolatedHome(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, home} {
		if err := os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".cursor", "mcp.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := (&Adapter{}).Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Confidence != adapter.ConfidenceHigh || len(got.Reasons) < 3 {
		t.Errorf("Detect with 4 signals = %+v, want high", got)
	}
}

func TestCursor_Detect_GlobalOnly(t *testing.T) {
	home := isolatedHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cursor", "mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := (&Adapter{}).Detect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "cursor" || got.Confidence != adapter.ConfidenceLow || len(got.Reasons) != 1 {
		t.Errorf("global-only Detect = %+v", got)
	}
}

func TestCursor_RenderRuntime(t *testing.T) {
	m := &manifest.Manifest{Environment: "dev", Runtime: &manifest.RuntimeBlock{
		MCPServers: []manifest.ContentRef{{ID: "mcp-echo", Endpoint: "https://ach.example/mcp/mcp-echo"}}}}
	fws, err := (&Adapter{}).RenderRuntime(adapter.WithCredential(context.Background(), "ek-test"), m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fws) != 1 || fws[0].Path != ".cursor/mcp.json" || fws[0].Merge != adapter.MergeDeep {
		t.Fatalf("fws: %+v", fws)
	}
	var doc map[string]map[string]map[string]any
	if err := json.Unmarshal(fws[0].Content, &doc); err != nil {
		t.Fatal(err)
	}
	e := doc["mcpServers"]["mcp-echo"]
	if e["url"] != "https://ach.example/mcp/mcp-echo" || e["headers"].(map[string]any)["x-ach-key"] != "ek-test" {
		t.Fatalf("entry: %v", e)
	}
	if _, has := e["type"]; has {
		t.Fatalf("no type field expected: %v", e)
	}
}

func TestCursor_RenderRuntime_NilManifest(t *testing.T) {
	if _, err := (&Adapter{}).RenderRuntime(context.Background(), nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestCursor_ProjectionRules(t *testing.T) {
	got := map[string]string{}
	for _, r := range (&Adapter{}).ProjectionRules() {
		got[r.FromGlob] = r.ToGlob
	}
	want := map[string]string{
		"commands/**/*.md": ".cursor/commands/**/*.md",
		"skills/**/*":      ".agents/skills/**/*",
		"agents/**/*.md":   ".cursor/agents/**/*.md",
		"mcp/**/*":         ".cursor/mcp.json",
		".mcp.json":        ".cursor/mcp.json",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("rules: %v", got)
	}
}

func TestCursor_Registered(t *testing.T) {
	for _, id := range []string{"cursor", "cursor-agent", "Cursor-CLI"} {
		if a, ok := adapter.Lookup(id); !ok || a.ID() != "cursor" {
			t.Fatalf("lookup %q", id)
		}
	}
}
