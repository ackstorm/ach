// SPDX-License-Identifier: Apache-2.0

// Integration tests for `ach-cli plugin` and `ach-cli skill` cobra commands.
// All tests use local git file:// fixtures — no network required.
//
// The test exercises the DIRECT-PLUGIN install path (a repo whose root IS
// the plugin, providing "plugin" lens, not "plugin-marketplace"). This is the
// simplest end-to-end path:
//
//	plugin install p1@fix --target claude --dest <tmp>
//	→ <tmp>/.claude/commands/x.md written
//	→ installed.json has entry p1@fix / claude-code / Files
//	plugin list → output contains "p1@fix"
//	plugin uninstall p1@fix → file removed, entry gone
package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/localpkg/store"
)

// ---- git fixture helpers (local to this test file) --------------------------

// pkgTestGitEnv returns a minimal env for reproducible git commits.
func pkgTestGitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test",
		"GIT_TERMINAL_PROMPT=0",
	)
}

// runPkgGit runs a git command in dir, failing t on error.
func runPkgGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = pkgTestGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// makeDirectPluginRepo creates a bare git repo whose root IS the plugin
// (commands/x.md). Returns the file:// clone URL.
func makeDirectPluginRepo(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	runPkgGit(t, work, "init", "-b", "main", ".")
	runPkgGit(t, work, "config", "user.email", "t@t")
	runPkgGit(t, work, "config", "user.name", "t")

	cmdDir := filepath.Join(work, "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir commands: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "x.md"), []byte("# x\nA test command.\n"), 0o644); err != nil {
		t.Fatalf("write x.md: %v", err)
	}

	runPkgGit(t, work, "add", "-A")
	runPkgGit(t, work, "commit", "-m", "init plugin")

	bare := t.TempDir()
	runPkgGit(t, work, "clone", "--bare", ".", bare)
	return "file://" + bare
}

// makeDirectSkillRepo creates a bare git repo with a SKILL.md at the root.
// Returns the file:// clone URL.
func makeDirectSkillRepo(t *testing.T, skillName string) string {
	t.Helper()
	work := t.TempDir()
	runPkgGit(t, work, "init", "-b", "main", ".")
	runPkgGit(t, work, "config", "user.email", "t@t")
	runPkgGit(t, work, "config", "user.name", "t")

	skillMD := "---\nname: " + skillName + "\ndescription: A test skill.\n---\n\n# " + skillName + "\n"
	if err := os.WriteFile(filepath.Join(work, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	runPkgGit(t, work, "add", "-A")
	runPkgGit(t, work, "commit", "-m", "init skill")

	bare := t.TempDir()
	runPkgGit(t, work, "clone", "--bare", ".", bare)
	return "file://" + bare
}

// seedRepo registers a store.RepoEntry into repos.json.
func seedRepo(t *testing.T, entry store.RepoEntry) {
	t.Helper()
	repos, err := store.LoadRepos()
	if err != nil {
		t.Fatalf("LoadRepos: %v", err)
	}
	repos.Repos = append(repos.Repos, entry)
	if err := store.SaveRepos(repos); err != nil {
		t.Fatalf("SaveRepos: %v", err)
	}
}

// ---- tests ------------------------------------------------------------------

// TestPluginCmd_Install_List_Uninstall exercises the direct-plugin path.
// It seeds a "fix" repo with Provides=[{Lens:"plugin",Count:1}], then
// installs p1@fix --target claude --dest <tmp>, asserts the file is on disk
// and the installed.json entry is correct, checks list output, then uninstalls
// and verifies cleanup.
func TestPluginCmd_Install_List_Uninstall(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	// Freeze nowFn so InstalledAt is deterministic.
	frozen := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	origNow := nowFn
	nowFn = func() time.Time { return frozen }
	t.Cleanup(func() { nowFn = origNow })

	// Isolate store writes.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	pluginURL := makeDirectPluginRepo(t)

	// Seed "fix" repo with direct-plugin lens.
	seedRepo(t, store.RepoEntry{
		Name:     "fix",
		Source:   "git:" + pluginURL,
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})

	destDir := t.TempDir()

	// --- install ---
	t.Run("install", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"install", "fix@fix", "--target", "claude", "--dest", destDir})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin install: %v (output: %s)", err, buf.String())
		}

		out := buf.String()
		if !strings.Contains(out, "fix@fix") {
			t.Errorf("expected 'fix@fix' in output, got: %s", out)
		}

		// File must be on disk.
		target := filepath.Join(destDir, ".claude", "commands", "x.md")
		if _, err := os.Stat(target); err != nil {
			t.Errorf("expected %s to exist: %v", target, err)
		}

		// installed.json must have the entry.
		installed, err := store.LoadInstalled()
		if err != nil {
			t.Fatalf("LoadInstalled: %v", err)
		}
		var found *store.InstalledEntry
		for i := range installed.Installed {
			e := &installed.Installed[i]
			if e.Ref == "fix@fix" && e.Target == "claude-code" {
				found = e
				break
			}
		}
		if found == nil {
			t.Fatalf("installed entry for fix@fix/claude-code not found; entries: %+v", installed.Installed)
		}
		if found.Kind != "plugin" {
			t.Errorf("Kind = %q; want plugin", found.Kind)
		}
		if len(found.ResolvedSHA) != 40 {
			t.Errorf("ResolvedSHA = %q; want 40-hex", found.ResolvedSHA)
		}
		if len(found.Files) == 0 {
			t.Error("expected at least one FileRec in installed entry")
		}
		if found.InstalledAt != frozen.Format(time.RFC3339) {
			t.Errorf("InstalledAt = %q; want %q", found.InstalledAt, frozen.Format(time.RFC3339))
		}
	})

	// --- list ---
	t.Run("list", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"list"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin list: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "fix@fix") {
			t.Errorf("expected 'fix@fix' in list output, got: %s", out)
		}
		if !strings.Contains(out, "claude-code") {
			t.Errorf("expected 'claude-code' in list output, got: %s", out)
		}
	})

	// --- uninstall ---
	t.Run("uninstall", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"uninstall", "fix@fix", "--dest", destDir})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin uninstall: %v", err)
		}

		// File must be gone.
		target := filepath.Join(destDir, ".claude", "commands", "x.md")
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed after uninstall; err=%v", target, err)
		}

		// installed.json entry must be gone.
		installed, err := store.LoadInstalled()
		if err != nil {
			t.Fatalf("LoadInstalled: %v", err)
		}
		for _, e := range installed.Installed {
			if e.Ref == "fix@fix" && e.Target == "claude-code" {
				t.Errorf("installed entry for fix@fix/claude-code still present after uninstall")
			}
		}
	})
}

// TestPluginCmd_Install_NoAtSign verifies that `plugin install badname` without
// an @ separator returns a non-nil error (exits General).
func TestPluginCmd_Install_NoAtSign(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var buf bytes.Buffer
	cmd := newPluginCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"install", "badname", "--target", "claude", "--dest", t.TempDir()})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for missing @, got nil")
	}
}

// TestPluginCmd_Install_UnknownTarget verifies that `plugin install name@repo --target nonexistent`
// returns a non-nil error.
func TestPluginCmd_Install_UnknownTarget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var buf bytes.Buffer
	cmd := newPluginCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"install", "p@repo", "--target", "nonexistent-target-xyz", "--dest", t.TempDir()})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unknown target, got nil")
	}
}

// TestPluginCmd_List_Empty verifies that list with no installed plugins prints
// the "no plugins installed" message.
func TestPluginCmd_List_Empty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var buf bytes.Buffer
	cmd := newPluginCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plugin list (empty): %v", err)
	}
	if !strings.Contains(buf.String(), "no plugins installed") {
		t.Errorf("expected 'no plugins installed', got: %s", buf.String())
	}
}

// TestSkillCmd_List_Empty verifies that skill list with no installed skills
// prints the "no skills installed" message.
func TestSkillCmd_List_Empty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var buf bytes.Buffer
	cmd := newSkillCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("skill list (empty): %v", err)
	}
	if !strings.Contains(buf.String(), "no skills installed") {
		t.Errorf("expected 'no skills installed', got: %s", buf.String())
	}
}

// TestSkillCmd_Install_List_Uninstall exercises the direct-skill install path.
// The skill's SKILL.md at repo root is extracted and the claudecode adapter's
// skills/**/* rule projects it under .claude/skills/<name>/SKILL.md.
func TestSkillCmd_Install_List_Uninstall(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	skillURL := makeDirectSkillRepo(t, "myskill")

	// Seed "sfix" repo with direct-skill lens.
	seedRepo(t, store.RepoEntry{
		Name:     "sfix",
		Source:   "git:" + skillURL,
		Kind:     "git",
		CloneURL: skillURL,
		GitRef:   "main",
		Provides: []store.Capability{{Lens: "skill", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})

	destDir := t.TempDir()

	// --- install ---
	t.Run("install", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newSkillCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"install", "myskill@sfix", "--target", "claude", "--dest", destDir})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("skill install: %v (output: %s)", err, buf.String())
		}

		out := buf.String()
		if !strings.Contains(out, "myskill@sfix") {
			t.Errorf("expected 'myskill@sfix' in output, got: %s", out)
		}

		// installed.json must have the entry.
		installed, err := store.LoadInstalled()
		if err != nil {
			t.Fatalf("LoadInstalled: %v", err)
		}
		var found *store.InstalledEntry
		for i := range installed.Installed {
			e := &installed.Installed[i]
			if e.Ref == "myskill@sfix" && e.Target == "claude-code" {
				found = e
				break
			}
		}
		if found == nil {
			t.Fatalf("installed entry for myskill@sfix/claude-code not found; entries: %+v", installed.Installed)
		}
		if found.Kind != "skill" {
			t.Errorf("Kind = %q; want skill", found.Kind)
		}
	})

	// --- list ---
	t.Run("list", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newSkillCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"list"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("skill list: %v", err)
		}
		if !strings.Contains(buf.String(), "myskill@sfix") {
			t.Errorf("expected 'myskill@sfix' in list output, got: %s", buf.String())
		}
	})

	// --- uninstall ---
	t.Run("uninstall", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newSkillCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"uninstall", "myskill@sfix", "--dest", destDir})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("skill uninstall: %v", err)
		}

		installed, err := store.LoadInstalled()
		if err != nil {
			t.Fatalf("LoadInstalled: %v", err)
		}
		for _, e := range installed.Installed {
			if e.Ref == "myskill@sfix" && e.Target == "claude-code" {
				t.Errorf("installed entry for myskill@sfix/claude-code still present after uninstall")
			}
		}
	})
}

// TestPluginCmd_Uninstall_Idempotent verifies that uninstalling a ref that is
// not installed exits 0 (idempotent behaviour).
func TestPluginCmd_Uninstall_Idempotent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var buf bytes.Buffer
	cmd := newPluginCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"uninstall", "ghost@repo", "--dest", t.TempDir()})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plugin uninstall (idempotent): expected exit 0, got: %v", err)
	}
}

// TestPluginCmd_Uninstall_MultiTarget is the regression test for Fix 1:
// when multiple --target values are given, only those specific targets are
// removed — NOT all targets for the ref.
//
// Setup: install p@fix2 to both claude-code and codex. Then uninstall
// p@fix2 --target claude. Assert that the claude-code entry is gone and
// the codex entry is still present in installed.json.
func TestPluginCmd_Uninstall_MultiTarget(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	pluginURL := makeDirectPluginRepo(t)

	// For the direct-plugin lens the name in <name@repo> must equal repo.Name.
	// We use "fix2@fix2" so both the plugin name and repo name are "fix2".
	seedRepo(t, store.RepoEntry{
		Name:     "fix2",
		Source:   "git:" + pluginURL,
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})

	destDir := t.TempDir()

	// Install to both claude and codex targets.
	t.Run("install_both", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{
			"install", "fix2@fix2",
			"--target", "claude",
			"--target", "codex-cli",
			"--dest", destDir,
		})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin install both targets: %v (output: %s)", err, buf.String())
		}
	})

	// Verify both entries are recorded.
	t.Run("both_entries_present", func(t *testing.T) {
		installed, err := store.LoadInstalled()
		if err != nil {
			t.Fatalf("LoadInstalled: %v", err)
		}
		var hasCC, hasCodex bool
		for _, e := range installed.Installed {
			if e.Ref == "fix2@fix2" {
				switch e.Target {
				case "claude-code":
					hasCC = true
				case "codex":
					hasCodex = true
				}
			}
		}
		if !hasCC {
			t.Error("expected claude-code entry after install; not found")
		}
		if !hasCodex {
			t.Error("expected codex entry after install; not found")
		}
	})

	// Uninstall only the claude target.
	t.Run("uninstall_claude_only", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"uninstall", "fix2@fix2", "--target", "claude", "--dest", destDir})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin uninstall --target claude: %v (output: %s)", err, buf.String())
		}
	})

	// Assert: claude-code entry gone, codex entry still present.
	t.Run("per_target_isolation", func(t *testing.T) {
		installed, err := store.LoadInstalled()
		if err != nil {
			t.Fatalf("LoadInstalled: %v", err)
		}
		var hasCC, hasCodex bool
		for _, e := range installed.Installed {
			if e.Ref == "fix2@fix2" {
				switch e.Target {
				case "claude-code":
					hasCC = true
				case "codex":
					hasCodex = true
				}
			}
		}
		if hasCC {
			t.Error("Fix 1 regression: claude-code entry still present after targeted uninstall")
		}
		if !hasCodex {
			t.Error("codex entry was incorrectly removed by targeted uninstall of claude only")
		}
	})
}

// TestPkgUpdateCmd_DestFlag is the regression test for Bug D: the `update`
// subcommand must register a `--dest` flag (so a package installed with --dest
// can also be updated with the same root override). Mirrors install's flag.
func TestPkgUpdateCmd_DestFlag(t *testing.T) {
	for _, kind := range []pkgKind{kindPlugin, kindSkill} {
		c := newPkgUpdateCmd(kind)
		f := c.Flags().Lookup("dest")
		if f == nil {
			t.Fatalf("%s update: expected --dest flag to be registered", kind)
		}
		if f.DefValue != "" {
			t.Errorf("%s update --dest: DefValue = %q; want empty", kind, f.DefValue)
		}
	}
}

// TestPluginCmd_Install_ZeroFiles is the regression test for Bug F: installing
// a repo whose plugin projection yields 0 files (a root-SKILL.md-only repo,
// which legitimately matches the plugin lens but projects nothing under the
// plugin rules) must:
//   - print a "0 files" warning to stderr,
//   - NOT print a "✓" success line,
//   - NOT record any entry in installed.json,
//   - exit 0 (the install is a no-op warning, not a hard error).
func TestPluginCmd_Install_ZeroFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// A root-SKILL.md-only repo matches the plugin lens (root SKILL.md is a
	// valid plugin component per the Stage-2 gate) but the claudecode plugin
	// rules project nothing from it → 0 files.
	skillURL := makeDirectSkillRepo(t, "zskill")

	seedRepo(t, store.RepoEntry{
		Name:     "zfix",
		Source:   "git:" + skillURL,
		Kind:     "git",
		CloneURL: skillURL,
		GitRef:   "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})

	destDir := t.TempDir()

	var out, errBuf bytes.Buffer
	cmd := newPluginCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"install", "zfix@zfix", "--target", "claude", "--dest", destDir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plugin install (0-file): expected exit nil, got: %v (stderr: %s)", err, errBuf.String())
	}

	if !strings.Contains(errBuf.String(), "0 files") {
		t.Errorf("expected '0 files' warning on stderr, got: %s", errBuf.String())
	}
	if strings.Contains(out.String(), "✓") {
		t.Errorf("expected NO success line for 0-file install, got stdout: %s", out.String())
	}

	// installed.json must have NO entry for this ref.
	installed, err := store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	for _, e := range installed.Installed {
		if e.Ref == "zfix@zfix" {
			t.Errorf("Bug F regression: 0-file install recorded an entry: %+v", e)
		}
	}
}

// TestPluginCmd_List_RepoFlag is the regression test for Fix 2: `plugin list
// --repo <name>` must not return "unknown flag: --repo" and must correctly
// filter entries by repo.
func TestPluginCmd_List_RepoFlag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	pluginURL := makeDirectPluginRepo(t)

	// Register two repos so we can assert that --repo filters correctly.
	// For the direct-plugin lens the name in <name@repo> must equal repo.Name,
	// so we use "rfix@rfix" and "other@other".
	seedRepo(t, store.RepoEntry{
		Name:     "rfix",
		Source:   "git:" + pluginURL,
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})
	seedRepo(t, store.RepoEntry{
		Name:     "other",
		Source:   "git:" + pluginURL,
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})

	destDir := t.TempDir()

	// Install one entry from each repo (name == repo for direct-plugin lens).
	for _, ref := range []string{"rfix@rfix", "other@other"} {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"install", ref, "--target", "claude", "--dest", destDir})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin install %s: %v (output: %s)", ref, err, buf.String())
		}
	}

	// --repo rfix: must succeed (no "unknown flag") and contain only rfix@rfix.
	t.Run("filter_rfix", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"list", "--repo", "rfix"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin list --repo rfix: %v (Fix 2: flag must be registered)", err)
		}
		out := buf.String()
		if !strings.Contains(out, "rfix@rfix") {
			t.Errorf("expected 'rfix@rfix' in output, got: %s", out)
		}
		if strings.Contains(out, "other@other") {
			t.Errorf("expected 'other@other' to be filtered out, got: %s", out)
		}
	})

	// --repo nonexistent: must succeed and print "no plugins installed".
	t.Run("filter_nonexistent", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"list", "--repo", "nonexistent"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin list --repo nonexistent: %v", err)
		}
		if !strings.Contains(buf.String(), "no plugins installed") {
			t.Errorf("expected 'no plugins installed' for unknown repo, got: %s", buf.String())
		}
	})
}

// TestCollisionWarn covers the cross-install collision warning (Bug H): a file
// written by one install that is already owned by a DIFFERENT installed entry at
// the same target must warn (last-wins overwrite, no managed conflict policy in
// local install). Different target, same ref (self-reinstall), and disjoint
// paths must NOT warn.
func TestCollisionWarn(t *testing.T) {
	mk := func(ref, target string, paths ...string) store.InstalledEntry {
		fs := make([]store.FileRec, 0, len(paths))
		for _, p := range paths {
			fs = append(fs, store.FileRec{RelPath: p})
		}
		return store.InstalledEntry{Ref: ref, Target: target, Files: fs}
	}
	installed := &store.InstalledFile{Installed: []store.InstalledEntry{
		mk("plugA@r", "claude-code", ".claude/commands/shared.md", ".claude/commands/a-only.md"),
		mk("plugC@r", "codex", ".codex/prompts/shared.md"), // different target — must NOT collide
	}}
	recs := []store.FileRec{
		{RelPath: ".claude/commands/shared.md"},
		{RelPath: ".claude/commands/b-only.md"},
	}

	t.Run("collision warns with owner", func(t *testing.T) {
		var buf bytes.Buffer
		collisionWarn(&buf, installed, "plugB@r", "claude-code", recs)
		out := buf.String()
		if !strings.Contains(out, ".claude/commands/shared.md") || !strings.Contains(out, "plugA@r") {
			t.Errorf("expected collision warning for shared.md owned by plugA@r, got: %q", out)
		}
		if strings.Contains(out, "b-only.md") {
			t.Errorf("non-colliding file b-only.md should not warn: %q", out)
		}
		if strings.Contains(out, ".codex") {
			t.Errorf("different-target file must not collide: %q", out)
		}
	})

	t.Run("same ref does not self-collide", func(t *testing.T) {
		var buf bytes.Buffer
		collisionWarn(&buf, installed, "plugA@r", "claude-code", recs)
		if buf.Len() != 0 {
			t.Errorf("same-ref reinstall must not warn (self), got: %q", buf.String())
		}
	})

	t.Run("disjoint paths do not warn", func(t *testing.T) {
		var buf bytes.Buffer
		collisionWarn(&buf, installed, "plugB@r", "claude-code",
			[]store.FileRec{{RelPath: ".claude/commands/fresh.md"}})
		if buf.Len() != 0 {
			t.Errorf("disjoint paths must not warn, got: %q", buf.String())
		}
	})

	// Bug #1: a shared path written with an ADDITIVE merge (composite marker
	// block, or keyed deep-merge) is not a clobber — the prior install's content
	// is preserved alongside this one — so it must NOT raise an "overwrote"
	// alarm, even though the path is owned by another entry. Only MergeReplace
	// ("") collisions warn.
	t.Run("additive merge does not warn", func(t *testing.T) {
		deepInstalled := &store.InstalledFile{Installed: []store.InstalledEntry{
			mk("plugA@r", "claude-code", ".claude/settings.json"),
		}}
		for _, merge := range []string{"deep", "composite"} {
			var buf bytes.Buffer
			collisionWarn(&buf, deepInstalled, "plugB@r", "claude-code",
				[]store.FileRec{{RelPath: ".claude/settings.json", Merge: merge}})
			if buf.Len() != 0 {
				t.Errorf("additive merge %q on shared path must not warn, got: %q", merge, buf.String())
			}
		}
	})
}

// TestPluginCmd_DryRun_And_Outdated covers the read-only additions: install
// --dry-run (plan only, nothing written), outdated (read-only source check), and
// uninstall --dry-run (plan only, nothing removed).
func TestPluginCmd_DryRun_And_Outdated(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	pluginURL := makeDirectPluginRepo(t)
	seedRepo(t, store.RepoEntry{
		Name: "fix", Source: "git:" + pluginURL, Kind: "git",
		CloneURL: pluginURL, GitRef: "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})
	destDir := t.TempDir()
	target := filepath.Join(destDir, ".claude", "commands", "x.md")

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v (out: %s)", args, err, buf.String())
		}
		return buf.String()
	}

	t.Run("install_dry_run_writes_nothing", func(t *testing.T) {
		out := run(t, "install", "fix@fix", "--target", "claude", "--dest", destDir, "--dry-run")
		if !strings.Contains(out, "[dry-run]") || !strings.Contains(out, "no changes written") {
			t.Errorf("missing dry-run markers in: %s", out)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Errorf("dry-run install must not write %s (err=%v)", target, err)
		}
		installed, _ := store.LoadInstalled()
		if len(installed.Installed) != 0 {
			t.Errorf("dry-run install must not record installed.json: %+v", installed.Installed)
		}
	})

	t.Run("real_install", func(t *testing.T) {
		run(t, "install", "fix@fix", "--target", "claude", "--dest", destDir)
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("expected %s after real install: %v", target, err)
		}
	})

	t.Run("outdated_up_to_date", func(t *testing.T) {
		out := run(t, "outdated")
		if !strings.Contains(out, "fix@fix") || !strings.Contains(out, "up to date") {
			t.Errorf("expected fix@fix up to date in: %s", out)
		}
	})

	t.Run("uninstall_dry_run_removes_nothing", func(t *testing.T) {
		out := run(t, "uninstall", "fix@fix", "--dest", destDir, "--dry-run")
		if !strings.Contains(out, "would uninstall") || !strings.Contains(out, "remove") {
			t.Errorf("missing uninstall plan in: %s", out)
		}
		if _, err := os.Stat(target); err != nil {
			t.Errorf("dry-run uninstall must not remove %s: %v", target, err)
		}
		installed, _ := store.LoadInstalled()
		if len(installed.Installed) == 0 {
			t.Error("dry-run uninstall must not mutate installed.json")
		}
	})
}
