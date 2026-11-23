// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/conflict"
	"github.com/ackstorm/ach/internal/cli/extract"
	"github.com/ackstorm/ach/internal/cli/hash"
	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/cli/state"
)

// The tests below go through commit.run, where Render receives the per-run
// stage root, not the Environment's directory that holds the state files.

// skillBodyExtractor writes body as one SKILL.md into the stage: the skill
// "sk" of a plugin (a /content/plugin/ URL), else a standalone Skill.
type skillBodyExtractor struct{ body string }

func (e skillBodyExtractor) ExtractContent(_ context.Context, ref manifest.ContentRef, base string, _ *state.File) (ExtractResult, error) {
	p := filepath.Join(base, "skill", ref.Name, "SKILL.md")
	if strings.Contains(ref.DownloadURL, "/content/plugin/") {
		p = filepath.Join(base, "plugin", ref.Name, "skills", "sk", "SKILL.md")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return ExtractResult{}, err
	}
	return ExtractResult{}, os.WriteFile(p, []byte(e.body), 0o644)
}

func runTarget(t *testing.T, c *commit, platform string, ctx *manifest.ContextBlock) error {
	t.Helper()
	c.statePath = filepath.Join(c.achDir, "state-"+platform+".json")
	c.fetcher = func(context.Context, string) (*manifest.Manifest, error) {
		return &manifest.Manifest{SchemaVersion: "v1alpha1", Environment: c.opts.Environment,
			Runtime: &manifest.RuntimeBlock{}, Context: ctx}, nil
	}
	_, disp := NewWiring(nil, platform, extract.DefaultLimits(), false, false, true, conflict.Namespace)
	c.adapter = disp
	_, err := c.run(context.Background())
	return err
}

// A target added later finds a shared skill file another target of the same
// Environment wrote, unedited, and updates it instead of refusing it (exit 7).
func TestCommit_TargetAddedLaterUpdatesSharedSkill(t *testing.T) {
	for _, dry := range []bool{false, true} {
		c, _, _ := newTestCommit(t)
		c.opts.DryRun = dry
		c.toolRoot = t.TempDir()
		const rel = ".agents/skills/plug-sk/SKILL.md"
		abs := writeFile(t, c.toolRoot, rel, "v1\n")
		h := hash.HashBytes([]byte("v1\n"))
		if err := state.Save(filepath.Join(c.achDir, "state-codex.json"), &state.File{SchemaVersion: "3", Environment: "demo",
			Plugins: []state.FileEntry{{Target: rel, Hash: h, SourceHash: h, Merge: mergeStrReplace, Source: "plug"}}}); err != nil {
			t.Fatal(err)
		}
		c.extractor = skillBodyExtractor{body: "v2\n"}
		plugins := &manifest.ContextBlock{Plugins: []manifest.ContentRef{
			{Name: "plug", DownloadURL: "https://x/content/plugin/plug"}}}
		if err := runTarget(t, c, "pi", plugins); err != nil {
			t.Fatalf("dryRun=%v: %v", dry, err)
		}
		// The plugin's skill gets a namespaced name: line ahead of the body.
		if got := readFile(t, abs); strings.HasSuffix(got, "v2\n") == dry {
			t.Fatalf("dryRun=%v: content %q", dry, got)
		}
	}
}

// A skill name a sibling Environment of the folder already projected is
// staged as <env>-<name>, so the second hydrate does not overwrite the first.
func TestCommit_SiblingEnvironmentSkillIsPrefixed(t *testing.T) {
	c, _, _ := newTestCommit(t)
	c.toolRoot = t.TempDir()
	achRoot := t.TempDir()
	c.achDir = filepath.Join(achRoot, "images")
	c.opts.Environment = "images"
	writeForeignSkillState(t, achRoot, "slides", "claude-code", "pdf")
	c.extractor = skillBodyExtractor{body: "---\nname: pdf\ndescription: y\n---\nbody"}
	skills := &manifest.ContextBlock{Skills: []manifest.ContentRef{
		{Name: "pdf", DownloadURL: "https://x/content/skill/pdf"}}}
	if err := runTarget(t, c, "claude-code", skills); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(c.toolRoot, ".claude/skills/images-pdf/SKILL.md")) {
		t.Fatal("skill not prefixed with the Environment name")
	}
	if exists(filepath.Join(c.toolRoot, ".claude/skills/pdf")) {
		t.Fatal("the sibling's bare skills/pdf was written over")
	}
}
