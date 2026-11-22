// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/localpkg/store"
)

// sharedSkillRepo seeds repo "dr": a plugin whose one skill codex, cursor,
// opencode, pi and antigravity all write to .agents/skills/dr-s. setSkill
// commits a new SKILL.md body upstream.
func sharedSkillRepo(t *testing.T) (setSkill func(body string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	work, bare := t.TempDir(), t.TempDir()
	runPkgGit(t, work, "init", "-b", "main", ".")
	runPkgGit(t, work, "config", "user.email", "t@t")
	runPkgGit(t, work, "config", "user.name", "t")
	setSkill = func(body string) {
		p := filepath.Join(work, "skills", "s", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\nname: s\ndescription: d\n---\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		runPkgGit(t, work, "add", "-A")
		runPkgGit(t, work, "commit", "-m", body)
		runPkgGit(t, work, "push", "-q", bare, "main")
	}
	runPkgGit(t, bare, "init", "--bare", "-b", "main", ".")
	setSkill("v1\n")
	seedRepo(t, store.RepoEntry{
		Name: "dr", Source: "git:file://" + bare, Kind: "git", CloneURL: "file://" + bare, GitRef: "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}}, AddedAt: "2026-01-01T00:00:00Z",
	})
	return setSkill
}

func sharedSkillHashes(t *testing.T, rel string) map[string]string {
	t.Helper()
	installed, err := store.LoadInstalled()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range installed.Installed {
		for _, f := range e.Files {
			if f.RelPath == rel {
				out[e.Target] = f.Hash
			}
		}
	}
	return out
}

// TestPluginCmd_SharedSkill_Update: updating a plugin installed for two tools
// that share .agents/skills writes the skill once, reports no edit and leaves
// both records at the new hash.
func TestPluginCmd_SharedSkill_Update(t *testing.T) {
	setSkill := sharedSkillRepo(t)
	dir := t.TempDir()
	rel := filepath.Join(".agents", "skills", "dr-s", "SKILL.md")
	mustPlugin(t, "install", "dr@dr", "--target", "codex,opencode", "--dir", dir)

	setSkill("v2\n")
	out, err := runPluginCmd("update", "dr@dr", "--dir", dir)
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if strings.Contains(out, "user-modified") {
		t.Errorf("update reported a user edit:\n%s", out)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, rel)); !strings.HasSuffix(string(got), "v2\n") {
		t.Errorf("skill = %q, want v2", got)
	}
	h := sharedSkillHashes(t, rel)
	if len(h) != 2 || h["codex"] != h["opencode"] {
		t.Errorf("records disagree: %v", h)
	}
}

// TestPluginCmd_SharedSkill_TargetAddedLater: installing for a second tool a
// skill file another tool's install already wrote is not a refusal, and a later
// update of the first tool is not a user edit.
func TestPluginCmd_SharedSkill_TargetAddedLater(t *testing.T) {
	setSkill := sharedSkillRepo(t)
	dir := t.TempDir()
	mustPlugin(t, "install", "dr@dr", "--target", "codex", "--dir", dir)

	setSkill("v2\n")
	mustPlugin(t, "install", "dr@dr", "--target", "opencode", "--dir", dir)
	out, err := runPluginCmd("update", "dr@dr", "--dir", dir)
	if err != nil || strings.Contains(out, "user-modified") {
		t.Fatalf("update: err=%v\n%s", err, out)
	}
}

// TestPluginCmd_SharedSkill_UninstallOneTarget: uninstalling one tool leaves
// the skill file the other tool's install still records.
func TestPluginCmd_SharedSkill_UninstallOneTarget(t *testing.T) {
	sharedSkillRepo(t)
	dir := t.TempDir()
	skill := filepath.Join(dir, ".agents", "skills", "dr-s", "SKILL.md")
	mustPlugin(t, "install", "dr@dr", "--target", "codex,opencode", "--dir", dir)

	mustPlugin(t, "uninstall", "dr@dr", "--target", "codex", "--dir", dir)
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("opencode's skill removed with codex: %v", err)
	}
	mustPlugin(t, "uninstall", "dr@dr", "--target", "opencode", "--dir", dir)
	if _, err := os.Stat(skill); !os.IsNotExist(err) {
		t.Errorf("skill left after the last uninstall (err=%v)", err)
	}
}
