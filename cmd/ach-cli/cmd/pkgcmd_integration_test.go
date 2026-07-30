// SPDX-License-Identifier: Apache-2.0

// Hermetic integration coverage for the `ach-cli plugin` command paths the
// manual battery exercised but pkgcmd_test.go does not:
//
//   - Group C: multi-target INSTALL in ONE command (comma-separated --target).
//   - Group D: `repo update` + `plugin update` SHA-drift full flow with --dest.
//   - Group E: `--global` install scope ($HOME), incl. opencode's $HOME remap.
//
// All fixtures are local git file:// repos built with `git init` — no network,
// no cluster. Tests skip when git is not on PATH. They reuse the git-fixture and
// seedRepo helpers from pkgcmd_test.go / repo_test.go (same package).

package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/localpkg/store"
	"github.com/ackstorm/ach/internal/featuregate"
)

// makeWorkingPluginRepo creates a NON-bare git working repo whose root is a
// direct plugin (commands/c.md with the given content). Returns the working-dir
// path; its file:// URL (used as the clone source) reflects later commits to the
// same working tree — required for the SHA-drift update flow (Group D).
func makeWorkingPluginRepo(t *testing.T, initialContent string) string {
	t.Helper()
	work := t.TempDir()
	runPkgGit(t, work, "init", "-b", "main", ".")
	runPkgGit(t, work, "config", "user.email", "t@t")
	runPkgGit(t, work, "config", "user.name", "t")

	cmdDir := filepath.Join(work, "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir commands: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "c.md"), []byte(initialContent), 0o644); err != nil {
		t.Fatalf("write c.md: %v", err)
	}
	runPkgGit(t, work, "add", "-A")
	runPkgGit(t, work, "commit", "-m", "v1")
	return work
}

// ---- Group F: --conflict policy (cross-install clash) -----------------------

// TestPluginCmd_Install_ConflictNamespace installs two direct plugins that both
// ship commands/x.md into the SAME dest+target. With the default --conflict
// namespace, the first keeps x.md and the second is leaf-prefixed to <name>-x.md,
// so both survive. Confirms the cross-install collision is de-collided, not
// clobbered.
func TestPluginCmd_Install_ConflictNamespace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	urlA := makeDirectPluginRepo(t)
	urlB := makeDirectPluginRepo(t)
	for name, url := range map[string]string{"a": urlA, "b": urlB} {
		seedRepo(t, store.RepoEntry{
			Name: name, Source: "git:" + url, Kind: "git", CloneURL: url, GitRef: "main",
			Provides: []store.Capability{{Lens: "plugin", Count: 1}},
			AddedAt:  "2026-01-01T00:00:00Z",
		})
	}

	destDir := t.TempDir()
	install := func(ref string, extra ...string) (string, error) {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(append([]string{"install", ref, "--target", "claude", "--dest", destDir}, extra...))
		err := cmd.Execute()
		return buf.String(), err
	}

	if out, err := install("a@a"); err != nil {
		t.Fatalf("install a@a: %v (%s)", err, out)
	}
	out, err := install("b@b")
	if err != nil {
		t.Fatalf("install b@b: %v (%s)", err, out)
	}
	if !strings.Contains(out, "namespaced") {
		t.Errorf("expected a namespaced-resolution line; got: %s", out)
	}

	first := filepath.Join(destDir, ".claude", "commands", "x.md")
	second := filepath.Join(destDir, ".claude", "commands", "b-x.md")
	if _, err := os.Stat(first); err != nil {
		t.Errorf("first install's x.md missing: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("second install's namespaced b-x.md missing: %v", err)
	}
}

// TestPluginCmd_Install_ConflictRefuse asserts --conflict refuse aborts the
// second (clashing) install with a non-nil error.
func TestPluginCmd_Install_ConflictRefuse(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	urlA := makeDirectPluginRepo(t)
	urlB := makeDirectPluginRepo(t)
	for name, url := range map[string]string{"a": urlA, "b": urlB} {
		seedRepo(t, store.RepoEntry{
			Name: name, Source: "git:" + url, Kind: "git", CloneURL: url, GitRef: "main",
			Provides: []store.Capability{{Lens: "plugin", Count: 1}},
			AddedAt:  "2026-01-01T00:00:00Z",
		})
	}

	destDir := t.TempDir()
	run := func(ref string, extra ...string) error {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(append([]string{"install", ref, "--target", "claude", "--dest", destDir}, extra...))
		return cmd.Execute()
	}

	if err := run("a@a"); err != nil {
		t.Fatalf("install a@a: %v", err)
	}
	if err := run("b@b", "--conflict", "refuse"); err == nil {
		t.Error("expected --conflict refuse to abort the clashing install, got nil error")
	}
}

// TestPluginCmd_Install_Verbose asserts --verbose narrates the per-repo clone
// and per-target projection to stderr.
func TestPluginCmd_Install_Verbose(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	url := makeDirectPluginRepo(t)
	seedRepo(t, store.RepoEntry{
		Name: "v", Source: "git:" + url, Kind: "git", CloneURL: url, GitRef: "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})

	var buf bytes.Buffer
	cmd := newPluginCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"install", "v@v", "--target", "claude", "--dest", t.TempDir(), "--verbose"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verbose install: %v (%s)", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"resolving v@v", "projecting → claude-code"} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q; got:\n%s", want, out)
		}
	}
}

// ---- Group C: multi-target INSTALL in one command ---------------------------

// TestPluginCmd_Install_MultiTarget_OneArg verifies that
// `plugin install <name>@<repo> --target claude,codex,gemini` (comma-separated
// in ONE arg) installs to all three adapters: three installed.json entries and
// projected files under each adapter's dir.
func TestPluginCmd_Install_MultiTarget_OneArg(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	pluginURL := makeDirectPluginRepo(t)

	// Direct-plugin lens requires name == repo.Name → use "mt".
	seedRepo(t, store.RepoEntry{
		Name:     "mt",
		Source:   "git:" + pluginURL,
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})

	destDir := t.TempDir()

	var buf bytes.Buffer
	cmd := newPluginCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// All three targets in ONE comma-separated --target value.
	cmd.SetArgs([]string{"install", "mt@mt", "--target", "claude,codex,gemini", "--dest", destDir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plugin install multi-target: %v (output: %s)", err, buf.String())
	}

	// installed.json must have THREE entries for mt@mt — one per adapter.
	installed, err := store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	wantTargets := map[string]bool{"claude-code": false, "codex": false, "gemini-cli": false}
	for _, e := range installed.Installed {
		if e.Ref == "mt@mt" {
			if _, known := wantTargets[e.Target]; known {
				wantTargets[e.Target] = true
			} else {
				t.Errorf("unexpected target %q for mt@mt", e.Target)
			}
		}
	}
	for target, seen := range wantTargets {
		if !seen {
			t.Errorf("expected installed entry mt@mt → %s, not found", target)
		}
	}

	// Projected files must exist under each adapter's dir.
	wantFiles := []string{
		filepath.Join(destDir, ".claude", "commands", "x.md"),
		filepath.Join(destDir, ".codex", "prompts", "x.md"),
		// gemini-cli reads custom commands as TOML, so commands/x.md is
		// converted to .gemini/commands/x.toml (geminiCommandTOML), not copied.
		filepath.Join(destDir, ".gemini", "commands", "x.toml"),
	}
	for _, f := range wantFiles {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected projected file %s: %v", f, err)
		}
	}
}

// ---- Group D: update SHA-drift full flow ------------------------------------

// TestPluginCmd_Update_SHADrift exercises the full update path against a NON-bare
// working repo: install v1, mutate+commit v2 (changed file + new file), refresh
// the repo via `repo update`, then `plugin update --dest`. Asserts the repo
// update reports a SHA change, the new file appears, the changed file content is
// updated, and installed.json's resolvedSHA changed.
func TestPluginCmd_Update_SHADrift(t *testing.T) {
	if !featuregate.PluginsEnabled {
		t.Skip("plugins disabled via featuregate.PluginsEnabled")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	work := makeWorkingPluginRepo(t, "v1")
	source := "git:file://" + work

	// --- repo add ---
	{
		var buf bytes.Buffer
		cmd := newRepoCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"add", source, "--name", "dr"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("repo add: %v (output: %s)", err, buf.String())
		}
	}

	destDir := t.TempDir()

	// --- install v1 (claude) ---
	{
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"install", "dr@dr", "--target", "claude", "--dest", destDir})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin install v1: %v (output: %s)", err, buf.String())
		}
	}

	// Capture the v1 resolvedSHA + assert c.md == "v1".
	installed, err := store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	var v1SHA string
	for _, e := range installed.Installed {
		if e.Ref == "dr@dr" && e.Target == "claude-code" {
			v1SHA = e.ResolvedSHA
		}
	}
	if v1SHA == "" {
		t.Fatalf("v1 install entry not found; entries: %+v", installed.Installed)
	}
	cmdFile := filepath.Join(destDir, ".claude", "commands", "c.md")
	if b, err := os.ReadFile(cmdFile); err != nil || string(b) != "v1" {
		t.Fatalf("c.md content = %q (err=%v); want v1", b, err)
	}

	// --- mutate working tree: change c.md → v2, add new.md, commit ---
	if err := os.WriteFile(filepath.Join(work, "commands", "c.md"), []byte("v2"), 0o644); err != nil {
		t.Fatalf("rewrite c.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "commands", "new.md"), []byte("new command\n"), 0o644); err != nil {
		t.Fatalf("write new.md: %v", err)
	}
	runPkgGit(t, work, "add", "-A")
	runPkgGit(t, work, "commit", "-m", "v2")

	// --- repo update: must report a SHA change ---
	{
		var buf bytes.Buffer
		cmd := newRepoCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"update", "dr"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("repo update: %v (output: %s)", err, buf.String())
		}
		out := buf.String()
		if !strings.Contains(out, "updated repo") {
			t.Errorf("expected repo update to report a SHA change ('updated repo'), got: %s", out)
		}
	}

	// --- plugin update --dest: re-resolve + cleanup + re-project ---
	{
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"update", "dr@dr", "--dest", destDir})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin update: %v (output: %s)", err, buf.String())
		}
		if !strings.Contains(buf.String(), "updated dr@dr") {
			t.Errorf("expected 'updated dr@dr' in plugin update output, got: %s", buf.String())
		}
	}

	// new.md must now exist.
	if _, err := os.Stat(filepath.Join(destDir, ".claude", "commands", "new.md")); err != nil {
		t.Errorf("expected new.md after update: %v", err)
	}
	// c.md content must now be v2.
	if b, err := os.ReadFile(cmdFile); err != nil || string(b) != "v2" {
		t.Errorf("c.md content after update = %q (err=%v); want v2", b, err)
	}

	// installed.json resolvedSHA must have changed.
	installed2, err := store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled (post-update): %v", err)
	}
	var v2SHA string
	for _, e := range installed2.Installed {
		if e.Ref == "dr@dr" && e.Target == "claude-code" {
			v2SHA = e.ResolvedSHA
		}
	}
	if v2SHA == "" {
		t.Fatalf("post-update install entry not found")
	}
	if v2SHA == v1SHA {
		t.Errorf("resolvedSHA did not change after update: %s", v2SHA)
	}
}

// ---- Group E: --global install scope ----------------------------------------

// TestPluginCmd_Install_Global_Claude verifies that `--global` (no --dest)
// installs under $HOME for claude-code.
func TestPluginCmd_Install_Global_Claude(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	// Keep store writes inside the per-test HOME for full isolation.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	pluginURL := makeDirectPluginRepo(t)
	seedRepo(t, store.RepoEntry{
		Name:     "gfix",
		Source:   "git:" + pluginURL,
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})

	var buf bytes.Buffer
	cmd := newPluginCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"install", "gfix@gfix", "--global", "--target", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plugin install --global: %v (output: %s)", err, buf.String())
	}

	want := filepath.Join(home, ".claude", "commands", "x.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected global install at %s: %v", want, err)
	}
}

// TestPluginCmd_Install_Global_OpencodeRemap verifies that `--global` for
// opencode remaps the .opencode/ tree to $HOME/.config/opencode/ (the global
// remap, mirrored from the manager-level remap test but at the command layer).
func TestPluginCmd_Install_Global_OpencodeRemap(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	pluginURL := makeDirectPluginRepo(t)
	seedRepo(t, store.RepoEntry{
		Name:     "ocg",
		Source:   "git:" + pluginURL,
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})

	var buf bytes.Buffer
	cmd := newPluginCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"install", "ocg@ocg", "--global", "--target", "opencode"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plugin install --global opencode: %v (output: %s)", err, buf.String())
	}

	// The command (commands/x.md) projects to .opencode/commands/x.md, then the
	// global remap moves the whole .opencode/ tree to .config/opencode/.
	remapped := filepath.Join(home, ".config", "opencode", "commands", "x.md")
	if _, err := os.Stat(remapped); err != nil {
		t.Errorf("expected remapped global install at %s: %v", remapped, err)
	}
	// The un-remapped path must NOT exist.
	original := filepath.Join(home, ".opencode", "commands", "x.md")
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Errorf("un-remapped path %s should not exist: err=%v", original, err)
	}
}

// TestPluginCmd_Install_Global_ConfigDirConflictNamespace verifies the full
// Project → global remap → conflict resolution → Commit chain when the
// redirected config root itself contains a "skills" segment. Namespacing must
// change the projected leaf under that root, never the root segment.
func TestPluginCmd_Install_Global_ConfigDirConflictNamespace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	home := t.TempDir()
	configRoot := filepath.Join(home, "skills")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configRoot)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	urlA := makeDirectPluginRepo(t)
	urlB := makeDirectPluginRepo(t)
	for name, url := range map[string]string{"a": urlA, "b": urlB} {
		seedRepo(t, store.RepoEntry{
			Name: name, Source: "git:" + url, Kind: "git", CloneURL: url, GitRef: "main",
			Provides: []store.Capability{{Lens: "plugin", Count: 1}},
			AddedAt:  "2026-01-01T00:00:00Z",
		})
	}

	var output bytes.Buffer
	install := func(ref string) {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"install", ref, "--global", "--target", "claude"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin install %s: %v (output: %s)", ref, err, buf.String())
		}
		output.WriteString(buf.String())
	}
	install("a@a")
	install("b@b")

	if _, err := os.Stat(filepath.Join(configRoot, "commands", "x.md")); err != nil {
		t.Fatalf("first install missing under redirected root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configRoot, "commands", "b-x.md")); err != nil {
		t.Fatalf("namespaced second install missing under redirected root: %v; output=%s", err, output.String())
	}
	if _, err := os.Stat(filepath.Join(home, "b-skills", "commands", "x.md")); !os.IsNotExist(err) {
		t.Fatalf("namespace rewrote the root segment outside config root: err=%v", err)
	}
}
