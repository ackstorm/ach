// SPDX-License-Identifier: Apache-2.0

package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAntigravity_ID(t *testing.T) {
	if got := (&Adapter{}).ID(); got != "antigravity" {
		t.Fatalf("ID() = %q", got)
	}
}

func TestAntigravity_Aliases(t *testing.T) {
	want := []string{"agy"}
	if got := (&Adapter{}).Aliases(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Aliases() = %v, want %v", got, want)
	}
}

func TestAntigravity_Detect_NoSignals_ZeroMatch(t *testing.T) {
	isolatedHome(t)
	got, err := (&Adapter{}).Detect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "" || len(got.Reasons) != 0 {
		t.Errorf("Detect(empty root) = %+v, want zero", got)
	}
}

func TestAntigravity_Detect_SharedAgentsSkillsIgnored(t *testing.T) {
	isolatedHome(t)
	root := t.TempDir()
	write(t, filepath.Join(root, ".agents", "skills", "s", "SKILL.md"))
	write(t, filepath.Join(root, "AGENTS.md"))
	got, err := (&Adapter{}).Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "" {
		t.Errorf("shared paths must not detect antigravity: %+v", got)
	}
}

func TestAntigravity_Detect_OneSignal_LowConfidence(t *testing.T) {
	isolatedHome(t)
	root := t.TempDir()
	write(t, filepath.Join(root, ".agents", "mcp_config.json"))
	got, err := (&Adapter{}).Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "antigravity" || got.Confidence != adapter.ConfidenceLow || len(got.Reasons) != 1 {
		t.Errorf("Detect with 1 signal = %+v", got)
	}
}

func TestAntigravity_Detect_TwoSignals_MediumConfidence(t *testing.T) {
	isolatedHome(t)
	root := t.TempDir()
	write(t, filepath.Join(root, ".agents", "mcp_config.json"))
	if err := os.MkdirAll(filepath.Join(root, ".agents", "agents"), 0o755); err != nil {
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

// The user config matches only when the scanned root is $HOME (--global), never
// in a project folder.
func TestAntigravity_Detect_UserConfig(t *testing.T) {
	home := isolatedHome(t)
	write(t, filepath.Join(home, ".gemini", "config", "mcp_config.json"))
	got, err := (&Adapter{}).Detect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "" {
		t.Errorf("project Detect with only a user config = %+v, want zero", got)
	}
	got, err = (&Adapter{}).Detect(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "antigravity" || got.Confidence != adapter.ConfidenceLow || len(got.Reasons) != 1 {
		t.Errorf("global Detect = %+v", got)
	}
}

func TestAntigravity_RenderRuntime(t *testing.T) {
	m := &manifest.Manifest{Environment: "dev", Runtime: &manifest.RuntimeBlock{
		MCPServers: []manifest.ContentRef{{ID: "mcp-echo", Endpoint: "https://ach.example/mcp/mcp-echo"}}}}
	fws, err := (&Adapter{}).RenderRuntime(adapter.WithCredential(context.Background(), "ek-test"), m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fws) != 1 || fws[0].Path != ".agents/mcp_config.json" || fws[0].Merge != adapter.MergeDeep {
		t.Fatalf("fws: %+v", fws)
	}
	var doc map[string]map[string]map[string]any
	if err := json.Unmarshal(fws[0].Content, &doc); err != nil {
		t.Fatal(err)
	}
	e := doc["mcpServers"]["mcp-echo"]
	if e["serverUrl"] != "https://ach.example/mcp/mcp-echo" || e["headers"].(map[string]any)["x-ach-key"] != "ek-test" {
		t.Fatalf("entry: %v", e)
	}
}

func TestAntigravity_ProjectionRules(t *testing.T) {
	got := map[string]string{}
	for _, r := range (&Adapter{}).ProjectionRules() {
		got[r.FromGlob] = r.ToGlob
	}
	want := map[string]string{
		"skills/**/*":    ".agents/skills/**/*",
		"agents/**/*.md": ".agents/agents/**/*.md",
		"mcp/**/*":       ".agents/mcp_config.json",
		".mcp.json":      ".agents/mcp_config.json",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("rules: %v", got)
	}
}

func TestSubagentFrontmatter_KeepsNameAndDescription(t *testing.T) {
	in := []byte("---\nname: reviewer\ndescription: Reviews diffs\ntools: Read, Grep\nmodel: sonnet\ncolor: blue\n---\nYou review code.\n")
	out, keys, err := subagentFrontmatter("agents/reviewer.md", in)
	if err != nil || keys != nil {
		t.Fatalf("err=%v keys=%v", err, keys)
	}
	s := string(out)
	for _, gone := range []string{"tools", "model", "color"} {
		if strings.Contains(s, gone+":") {
			t.Fatalf("%s must be dropped:\n%s", gone, s)
		}
	}
	if !strings.Contains(s, "reviewer") || !strings.Contains(s, "Reviews diffs") || !strings.HasSuffix(s, "You review code.\n") {
		t.Fatalf("out:\n%s", s)
	}
}

func TestSubagentFrontmatter_NoFrontmatter_Passthrough(t *testing.T) {
	in := []byte("Just a body.\n")
	out, _, err := subagentFrontmatter("agents/x.md", in)
	if err != nil || !bytes.Equal(out, in) {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestAntigravity_Registered(t *testing.T) {
	for _, id := range []string{"antigravity", "AGY"} {
		if a, ok := adapter.Lookup(id); !ok || a.ID() != "antigravity" {
			t.Fatalf("lookup %q", id)
		}
	}
}
