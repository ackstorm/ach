// SPDX-License-Identifier: Apache-2.0

// Containment + command-format scoping for the local installer's projection.
// These assert two local-installer policies enforced in manager.Project,
// independent of the shared adapter rules used by governed `env hydrate`:
//
//   - Containment: write NOTHING outside the target adapter's dot-dir. The
//     AGENTS.md→CLAUDE.md / →GEMINI.md composites are the only rules that land a
//     loose file in the PROJECT ROOT; the local installer must not touch the
//     user's own root files, so any project-root destination is dropped.
//   - .md command scoping: a multi-tool plugin may ship gemini-format
//     commands/*.toml alongside commands/*.md; the .toml must not leak into
//     .claude/ or .opencode/ command dirs (claude/opencode commands are markdown).
//
// Also pins the user-clarified read rule: a README.md that lives INSIDE a skill
// (skills/<name>/README.md) IS part of that skill and rides through, whereas a
// repo-root README.md is not a component and never projects.
package manager_test

import (
	"os"
	"path"
	"path/filepath"
	"testing"

	// Blank-import the two adapters under test so their init() registers them.
	_ "github.com/ackstorm/ach/internal/cli/adapter/claudecode"
	_ "github.com/ackstorm/ach/internal/cli/adapter/opencode"

	"github.com/ackstorm/ach/internal/cli/localpkg/manager"
)

// stageMixedTree builds a staged plugin tree carrying: a root AGENTS.md (the
// only root-escaping source), a command in BOTH markdown and gemini-toml form,
// an agent, and a skill that contains its own README.md. Returns the stage dir.
func stageMixedTree(t *testing.T) string {
	t.Helper()
	stage := t.TempDir()
	write := func(rel, content string) {
		abs := filepath.Join(stage, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("AGENTS.md", "plugin memory prose\n")
	write("commands/review.md", "# review\n")
	write("commands/review.toml", "description = \"gemini review\"\n")
	write("agents/agent-a.md", "---\nname: agent-a\n---\nbody\n")
	write("skills/superpowers/SKILL.md", "---\nname: superpowers\ndescription: x\n---\n# s\n")
	write("skills/superpowers/README.md", "skill-internal readme\n")
	return stage
}

func TestProject_Containment_NoRootWrites(t *testing.T) {
	stage := stageMixedTree(t)

	for _, id := range []string{"claude-code", "opencode"} {
		t.Run(id, func(t *testing.T) {
			writes, err := manager.Project(stage, id)
			if err != nil {
				t.Fatalf("Project(%s): %v", id, err)
			}
			if len(writes) == 0 {
				t.Fatalf("Project(%s) returned no writes", id)
			}
			for _, w := range writes {
				if path.Dir(w.Path) == "." {
					t.Errorf("project-root write not contained: %q (adapter %s)", w.Path, id)
				}
			}
			got := plannedPaths(writes)
			if _, ok := got["CLAUDE.md"]; ok {
				t.Errorf("adapter %s: root CLAUDE.md was projected; want dropped", id)
			}
		})
	}
}

func TestProject_CommandFormat_DropsForeignToml(t *testing.T) {
	stage := stageMixedTree(t)

	cases := []struct {
		id      string
		wantMD  string
		goneTML string
	}{
		{"claude-code", ".claude/commands/review.md", ".claude/commands/review.toml"},
		{"opencode", ".opencode/commands/review.md", ".opencode/commands/review.toml"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			writes, err := manager.Project(stage, tc.id)
			if err != nil {
				t.Fatalf("Project(%s): %v", tc.id, err)
			}
			got := plannedPaths(writes)
			if _, ok := got[tc.wantMD]; !ok {
				t.Errorf("adapter %s: missing %q; got %v", tc.id, tc.wantMD, pathList(writes))
			}
			if _, ok := got[tc.goneTML]; ok {
				t.Errorf("adapter %s: foreign-format %q leaked into commands; want dropped",
					tc.id, tc.goneTML)
			}
		})
	}
}

// TestProject_SkillInternalReadmeKept pins the read rule: a README.md INSIDE a
// skill directory is part of that skill and projects with it (claude-code routes
// skills/**/* verbatim), unlike a repo-root README which is never a component.
func TestProject_SkillInternalReadmeKept(t *testing.T) {
	stage := stageMixedTree(t)

	writes, err := manager.Project(stage, "claude-code")
	if err != nil {
		t.Fatalf("Project(claude-code): %v", err)
	}
	got := plannedPaths(writes)
	if _, ok := got[".claude/skills/superpowers/README.md"]; !ok {
		t.Errorf("skill-internal README.md was dropped; want kept. got %v", pathList(writes))
	}
}
