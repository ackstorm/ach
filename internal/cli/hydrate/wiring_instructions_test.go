// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
	"github.com/ackstorm/ach/internal/cli/adapter/antigravity"
	"github.com/ackstorm/ach/internal/cli/adapter/claudecode"
	"github.com/ackstorm/ach/internal/cli/adapter/codex"
	"github.com/ackstorm/ach/internal/cli/adapter/cursor"
	"github.com/ackstorm/ach/internal/cli/adapter/opencode"
	"github.com/ackstorm/ach/internal/cli/adapter/pi"
)

func stageInstruction(t *testing.T, stage, name, body string) {
	t.Helper()
	dir := filepath.Join(stage, "instruction", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestProjectInstructions_ClaudeCode(t *testing.T) {
	stage, root := t.TempDir(), t.TempDir()
	stageInstruction(t, stage, "base", "# Rules\n")
	d := &adapterDispatcherImpl{platformID: "claude-code"}
	var res RenderResult
	if err := d.projectInstructions(&claudecode.Adapter{}, nil, stage, root, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.ProjectedInstructionFiles) != 1 {
		t.Fatalf("got %+v", res.ProjectedInstructionFiles)
	}
	e := res.ProjectedInstructionFiles[0]
	if e.Target != ".claude/rules/ach-base.md" || e.Source != "base" {
		t.Fatalf("entry %+v", e)
	}
	if got := readFile(t, filepath.Join(root, e.Target)); got != "# Rules\n" {
		t.Fatalf("content %q", got)
	}
}

func TestProjectInstructions_PiLoaderNotRecorded(t *testing.T) {
	stage, root := t.TempDir(), t.TempDir()
	stageInstruction(t, stage, "a", "A\n")
	stageInstruction(t, stage, "b", "B\n")
	d := &adapterDispatcherImpl{platformID: "pi"}
	var res RenderResult
	if err := d.projectInstructions(&pi.Adapter{}, nil, stage, root, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.ProjectedInstructionFiles) != 2 ||
		res.ProjectedInstructionFiles[0].Target != ".pi/ach/a.md" || res.ProjectedInstructionFiles[1].Target != ".pi/ach/b.md" {
		t.Fatalf("got %+v", res.ProjectedInstructionFiles)
	}
	ext := filepath.Join(root, ".pi/extensions/ach-instructions.ts")
	l := (&pi.Adapter{}).InstructionLoader(false)
	if readFile(t, ext) != string(l.Content) {
		t.Fatal("extension content differs")
	}
	for _, e := range res.ProjectedInstructionFiles {
		if strings.HasSuffix(e.Target, ".ts") {
			t.Fatal("loader recorded in state")
		}
	}
}

// The entry is absolute: OpenCode searches a relative one only up to the git
// root, so a project inside an Environment folder would miss it.
func TestProjectInstructions_OpencodeLoader(t *testing.T) {
	stage, root := t.TempDir(), t.TempDir()
	stageInstruction(t, stage, "base", "x\n")
	cfg := writeFile(t, root, ".opencode/opencode.json", `{"instructions":["CONTRIBUTING.md"],"mcp":{}}`)
	d := &adapterDispatcherImpl{platformID: "opencode"}
	for range 2 {
		var res RenderResult
		if err := d.projectInstructions(&opencode.Adapter{}, nil, stage, root, &res); err != nil {
			t.Fatal(err)
		}
		for _, e := range res.ProjectedInstructionFiles {
			if strings.HasSuffix(e.Target, "opencode.json") {
				t.Fatal("opencode.json recorded")
			}
		}
	}
	got := readFile(t, cfg)
	want := `"` + filepath.ToSlash(root) + `/.opencode/ach/*.md"`
	if strings.Count(got, want) != 1 || strings.Count(got, ".opencode/ach/*.md") != 1 ||
		!strings.Contains(got, "CONTRIBUTING.md") || !strings.Contains(got, `"mcp"`) {
		t.Fatalf("config %s", got)
	}
}

func TestProjectInstructions_OpencodeGlobalEntryFollowsFiles(t *testing.T) {
	stage, home, xdg := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	stageInstruction(t, stage, "base", "x\n")
	d := &adapterDispatcherImpl{platformID: "opencode", global: true}
	var res RenderResult
	if err := d.projectInstructions(&opencode.Adapter{}, nil, stage, home, &res); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(xdg, "opencode/ach/base.md")) {
		t.Fatalf("file not under XDG_CONFIG_HOME: %+v", res.ProjectedInstructionFiles)
	}
	got := readFile(t, filepath.Join(xdg, "opencode/opencode.json"))
	if !strings.Contains(got, `"`+filepath.ToSlash(xdg)+`/opencode/ach/*.md"`) {
		t.Fatalf("config %s", got)
	}
}

func TestProjectInstructions_OpencodeNonArrayRefused(t *testing.T) {
	stage, root := t.TempDir(), t.TempDir()
	stageInstruction(t, stage, "base", "x\n")
	body := `{"instructions":"CONTRIBUTING.md"}`
	cfg := writeFile(t, root, ".opencode/opencode.json", body)
	d := &adapterDispatcherImpl{platformID: "opencode"}
	err := d.projectInstructions(&opencode.Adapter{}, nil, stage, root, &RenderResult{})
	if err == nil || !strings.Contains(err.Error(), "opencode.json") {
		t.Fatalf("err %v", err)
	}
	if readFile(t, cfg) != body {
		t.Fatal("file changed")
	}
}

func TestProjectInstructions_NotesAndWarnings(t *testing.T) {
	stage, root := t.TempDir(), t.TempDir()
	var res RenderResult
	if err := (&adapterDispatcherImpl{platformID: "codex"}).projectInstructions(&codex.Adapter{}, nil, stage, root, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.InstructionNotes) != 0 || len(res.InstructionWarnings) != 0 {
		t.Fatal("nothing staged must report nothing")
	}
	stageInstruction(t, stage, "base", "x\n")
	if err := (&adapterDispatcherImpl{platformID: "codex"}).projectInstructions(&codex.Adapter{}, nil, stage, root, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.InstructionNotes) != 1 || !strings.Contains(res.InstructionNotes[0], "codex") || len(res.ProjectedInstructionFiles) != 0 {
		t.Fatalf("notes %v", res.InstructionNotes)
	}
	res = RenderResult{}
	d := &adapterDispatcherImpl{platformID: "cursor", global: true}
	if err := d.projectInstructions(&cursor.Adapter{}, nil, stage, root, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.InstructionWarnings) != 1 || len(res.ProjectedInstructionFiles) != 0 {
		t.Fatalf("warnings %v", res.InstructionWarnings)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("wrote %v", entries)
	}
}

func TestProjectInstructions_Antigravity(t *testing.T) {
	stage, root := t.TempDir(), t.TempDir()
	stageInstruction(t, stage, "base", "x\n")
	d := &adapterDispatcherImpl{platformID: "antigravity"}
	var res RenderResult
	if err := d.projectInstructions(&antigravity.Adapter{}, nil, stage, root, &res); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(readFile(t, filepath.Join(root, ".agents/rules/ach-base.md")), "---\ntrigger: always_on\n---") {
		t.Fatal("frontmatter missing")
	}
}

func TestProjectInstructions_DryRunWritesNothing(t *testing.T) {
	stage := t.TempDir()
	stageInstruction(t, stage, "base", "x\n")
	for id, ad := range map[string]adapter.Adapter{"opencode": &opencode.Adapter{}, "pi": &pi.Adapter{}} {
		root := t.TempDir()
		body := `{"instructions":["CONTRIBUTING.md"]}`
		cfg := writeFile(t, root, ".opencode/opencode.json", body)
		d := &adapterDispatcherImpl{platformID: id, dryRun: true}
		var res RenderResult
		if err := d.projectInstructions(ad, nil, stage, root, &res); err != nil {
			t.Fatal(err)
		}
		if readFile(t, cfg) != body || exists(filepath.Join(root, ".pi")) || exists(filepath.Join(root, ".opencode/ach")) {
			t.Fatalf("%s: dry run wrote", id)
		}
	}
}

func TestTidyInstructionLoader(t *testing.T) {
	t.Run("opencode keeps user entries", func(t *testing.T) {
		root := t.TempDir()
		cfg := writeFile(t, root, ".opencode/opencode.json", `{"instructions":["CONTRIBUTING.md","`+filepath.ToSlash(root)+`/.opencode/ach/*.md"]}`)
		if err := os.MkdirAll(filepath.Join(root, ".opencode/ach"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := TidyInstructionLoader("opencode", root, false); err != nil {
			t.Fatal(err)
		}
		got := readFile(t, cfg)
		if strings.Contains(got, "ach/*.md") || !strings.Contains(got, "CONTRIBUTING.md") {
			t.Fatalf("config %s", got)
		}
	})
	t.Run("opencode deletes a file it emptied", func(t *testing.T) {
		root := t.TempDir()
		cfg := writeFile(t, root, ".opencode/opencode.json", `{"instructions":["`+filepath.ToSlash(root)+`/.opencode/ach/*.md"]}`)
		if err := TidyInstructionLoader("opencode", root, false); err != nil {
			t.Fatal(err)
		}
		if exists(cfg) {
			t.Fatal("opencode.json should be deleted")
		}
	})
	t.Run("pi extension removed", func(t *testing.T) {
		root := t.TempDir()
		ext := writeFile(t, root, ".pi/extensions/ach-instructions.ts", "x")
		if err := TidyInstructionLoader("pi", root, false); err != nil {
			t.Fatal(err)
		}
		if exists(ext) {
			t.Fatal("extension should be deleted")
		}
	})
	t.Run("a sibling file keeps the loader", func(t *testing.T) {
		root := t.TempDir()
		ext := writeFile(t, root, ".pi/extensions/ach-instructions.ts", "x")
		writeFile(t, root, ".pi/ach/other.md", "y")
		if err := TidyInstructionLoader("pi", root, false); err != nil {
			t.Fatal(err)
		}
		if !exists(ext) {
			t.Fatal("extension must stay")
		}
	})
}

func TestTidyInstructionLoader_UntouchedJSONC(t *testing.T) {
	root := t.TempDir()
	const jsonc = "{\n  // user comment\n  \"theme\": \"x\",\n}\n"
	cfg := writeFile(t, root, ".opencode/opencode.json", jsonc)
	if err := os.MkdirAll(filepath.Join(root, ".opencode/ach"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := TidyInstructionLoader("opencode", root, false); err != nil {
		t.Fatal(err)
	}
	if readFile(t, cfg) != jsonc {
		t.Fatal("file must stay byte-identical")
	}
}
