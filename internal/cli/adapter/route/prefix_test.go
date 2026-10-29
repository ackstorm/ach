// SPDX-License-Identifier: Apache-2.0

package route

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
)

func writePrefixTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func writesByPath(fws []adapter.FileWrite) map[string]string {
	out := map[string]string{}
	for _, fw := range fws {
		out[fw.Path] = string(fw.Content)
	}
	return out
}

const ladderSkill = "---\nname: ladder\ndescription: d\n---\nbody\n"

func TestProjectPluginPrefixesSkillsAndCommands(t *testing.T) {
	src := writePrefixTree(t, map[string]string{
		"skills/ladder/SKILL.md":  ladderSkill,
		"skills/ladder/refs/a.md": "ref",
		"commands/sync.md":        "cmd",
		"commands/git/nested.md":  "nested",
		"agents/helper.md":        "agent",
	})
	rules := []Rule{
		{FromGlob: "commands/**/*.md", ToGlob: ".t/commands/**/*.md", Merge: adapter.MergeReplace, Prefix: PrefixCommand, Sep: "/"},
		{FromGlob: "agents/**/*", ToGlob: ".t/agents/**/*", Merge: adapter.MergeReplace},
		{FromGlob: "skills/**/*", ToGlob: ".t/skills/**/*", Merge: adapter.MergeReplace, Prefix: PrefixSkill, Sep: ":"},
	}
	pr, err := ProjectPlugin(rules, src, "bro")
	if err != nil {
		t.Fatal(err)
	}
	got := writesByPath(pr.FileWrites)
	want := map[string]string{
		".t/skills/bro:ladder/SKILL.md":  "---\nname: bro:ladder\ndescription: d\n---\nbody\n",
		".t/skills/bro:ladder/refs/a.md": "ref",
		".t/commands/bro/sync.md":        "cmd",
		".t/commands/bro/git/nested.md":  "nested",
		".t/agents/helper.md":            "agent",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d writes %v; want %d", len(got), got, len(want))
	}
	for p, body := range want {
		if got[p] != body {
			t.Errorf("%s = %q; want %q (all: %v)", p, got[p], body, got)
		}
	}
}

func TestProjectPluginNoSpecialCaseWhenSkillEqualsPlugin(t *testing.T) {
	src := writePrefixTree(t, map[string]string{"skills/bro/SKILL.md": "---\nname: bro\n---\n"})
	rules := []Rule{{FromGlob: "skills/**/*", ToGlob: ".t/skills/**/*", Merge: adapter.MergeReplace, Prefix: PrefixSkill, Sep: "-"}}
	pr, err := ProjectPlugin(rules, src, "bro")
	if err != nil {
		t.Fatal(err)
	}
	if got := writesByPath(pr.FileWrites)[".t/skills/bro-bro/SKILL.md"]; got != "---\nname: bro-bro\n---\n" {
		t.Fatalf("got %q", got)
	}
}

func TestProjectPluginKeepsMarketplaceSuffixOut(t *testing.T) {
	src := writePrefixTree(t, map[string]string{"commands/x.md": "c"})
	rules := []Rule{{FromGlob: "commands/**/*.md", ToGlob: ".t/commands/**/*.md", Merge: adapter.MergeReplace, Prefix: PrefixCommand, Sep: "-"}}
	pr, err := ProjectPlugin(rules, src, "git@ackstorm-defaults")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := writesByPath(pr.FileWrites)[".t/commands/git-x.md"]; !ok {
		t.Fatalf("got %v", writesByPath(pr.FileWrites))
	}
}

func TestProjectWithoutPluginLeavesPathsAlone(t *testing.T) {
	src := writePrefixTree(t, map[string]string{"skills/ladder/SKILL.md": ladderSkill})
	rules := []Rule{{FromGlob: "skills/**/*", ToGlob: ".t/skills/**/*", Merge: adapter.MergeReplace, Prefix: PrefixSkill, Sep: ":"}}
	pr, err := Project(rules, src, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := writesByPath(pr.FileWrites)[".t/skills/ladder/SKILL.md"]; got != ladderSkill {
		t.Fatalf("standalone projection changed: %v", writesByPath(pr.FileWrites))
	}
}

func TestProjectPluginUserOnlySkillAlsoBecomesCommand(t *testing.T) {
	userOnly := "---\nname: readback\ndescription: Restate it.\ndisable-model-invocation: true\n---\nState it back.\n"
	src := writePrefixTree(t, map[string]string{
		"skills/readback/SKILL.md": userOnly,
		"skills/ladder/SKILL.md":   ladderSkill,
	})
	rules := []Rule{
		{FromGlob: "skills/**/*", ToGlob: ".t/skills/**/*", Merge: adapter.MergeReplace, Prefix: PrefixSkill, Sep: "-", UserOnlyCommandDir: ".t/commands"},
	}
	pr, err := ProjectPlugin(rules, src, "bro")
	if err != nil {
		t.Fatal(err)
	}
	got := writesByPath(pr.FileWrites)
	if _, ok := got[".t/skills/bro-readback/SKILL.md"]; !ok {
		t.Errorf("user-only skill must stay a skill: %v", got)
	}
	cmd, ok := got[".t/commands/bro-readback.md"]
	if !ok {
		t.Fatalf("user-only skill not projected as a command: %v", got)
	}
	if cmd != "---\ndescription: Restate it.\n---\nState it back.\n" {
		t.Errorf("command = %q", cmd)
	}
	if _, ok := got[".t/commands/bro-ladder.md"]; ok {
		t.Errorf("model-invocable skill must not become a command")
	}
}

func TestRenameFrontmatterName(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "---\nname: a\nx: 1\n---\nb", "---\nname: p:a\nx: 1\n---\nb"},
		{"quoted", "---\nname: \"a\"\n---\n", "---\nname: p:a\n---\n"},
		{"crlf", "---\r\nname: a\r\n---\r\nb", "---\r\nname: p:a\r\n---\r\nb"},
		{"missing name added", "---\nx: 1\n---\nb", "---\nname: p:a\nx: 1\n---\nb"},
		{"no frontmatter added", "body", "---\nname: p:a\n---\nbody"},
		{"name in body untouched", "---\nx: 1\n---\nname: a\n", "---\nname: p:a\nx: 1\n---\nname: a\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(renameFrontmatterName([]byte(c.in), "p:a")); got != c.want {
				t.Errorf("got %q; want %q", got, c.want)
			}
		})
	}
}

func TestPluginBaseName(t *testing.T) {
	for in, want := range map[string]string{"bro@bro": "bro", "git@ackstorm-defaults": "git", "plain": "plain"} {
		if got := pluginBaseName(in); got != want {
			t.Errorf("pluginBaseName(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestProjectPluginSkillNameSepDiffersFromDirSep(t *testing.T) {
	src := writePrefixTree(t, map[string]string{"skills/ladder/SKILL.md": ladderSkill})
	rules := []Rule{{FromGlob: "skills/**/*", ToGlob: ".t/skills/**/*", Merge: adapter.MergeReplace, Prefix: PrefixSkill, Sep: "-", NameSep: ":"}}
	pr, err := ProjectPlugin(rules, src, "bro")
	if err != nil {
		t.Fatal(err)
	}
	if got := writesByPath(pr.FileWrites)[".t/skills/bro-ladder/SKILL.md"]; got != "---\nname: bro:ladder\ndescription: d\n---\nbody\n" {
		t.Fatalf("got %v", writesByPath(pr.FileWrites))
	}
}

func TestSkillDirSep(t *testing.T) {
	if skillDirSep("windows") != "-" || skillDirSep("linux") != ":" || skillDirSep("darwin") != ":" {
		t.Fatal("windows must not get ':' in a directory name")
	}
}
