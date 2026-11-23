// SPDX-License-Identifier: Apache-2.0

// Hermetic integration coverage for the `ach-cli plugin` command paths the
// manual battery exercised but pkgcmd_test.go does not:
//
//   - Group C: multi-target INSTALL in ONE command (comma-separated --target).
//   - Group D: `repo update` + `plugin update` SHA-drift full flow with --dir.
//   - Group E: `--global` install scope ($HOME), incl. opencode's $HOME remap.
//
// All fixtures are local git file:// repos built with `git init` — no network,
// no cluster. Tests skip when git is not on PATH. They reuse the git-fixture and
// seedRepo helpers from pkgcmd_test.go / repo_test.go (same package).

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
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

// makeMarketplacePluginRepo creates a bare repo whose marketplace.json lists
// one plugin, name, shipped in-repo at plugins/<name>/ with commands/x.md.
// Returns the file:// clone URL.
func makeMarketplacePluginRepo(t *testing.T, name string) string {
	t.Helper()
	work := t.TempDir()
	runPkgGit(t, work, "init", "-b", "main", ".")
	runPkgGit(t, work, "config", "user.email", "t@t")
	runPkgGit(t, work, "config", "user.name", "t")
	for rel, body := range map[string]string{
		".claude-plugin/marketplace.json": `{"name":"m","owner":{"name":"o"},"plugins":[` +
			`{"name":"` + name + `","source":"./plugins/` + name + `"}]}`,
		"plugins/" + name + "/commands/x.md": "# x from the marketplace\n",
	} {
		p := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	runPkgGit(t, work, "add", "-A")
	runPkgGit(t, work, "commit", "-m", "init marketplace")
	bare := t.TempDir()
	runPkgGit(t, work, "clone", "--bare", ".", bare)
	return "file://" + bare
}

// seedSamePluginTwoRepos registers repo "a" (direct plugin a) and repo "m"
// (a marketplace that also offers a plugin named a). Both ship commands/x.md,
// so a@a and a@m project to the same .claude/commands/a/x.md: since plugins
// are namespaced by name, only the same plugin name from two repos can clash.
func seedSamePluginTwoRepos(t *testing.T) {
	t.Helper()
	urlA := makeDirectPluginRepo(t)
	urlM := makeMarketplacePluginRepo(t, "a")
	seedRepo(t, store.RepoEntry{
		Name: "a", Source: "git:" + urlA, Kind: "git", CloneURL: urlA, GitRef: "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})
	seedRepo(t, store.RepoEntry{
		Name: "m", Source: "git:" + urlM, Kind: "git", CloneURL: urlM, GitRef: "main",
		Provides: []store.Capability{{Lens: "plugin-marketplace", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})
}

// TestPluginCmd_Install_ConflictNamespace installs the same plugin name from
// two repos into the SAME dest+target. With the default --conflict namespace,
// the first keeps a/x.md and the second is leaf-prefixed to a/a-x.md, so both
// survive. Confirms the cross-install collision is de-collided, not clobbered.
func TestPluginCmd_Install_ConflictNamespace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seedSamePluginTwoRepos(t)

	destDir := t.TempDir()
	install := func(ref string, extra ...string) (string, error) {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(append([]string{"install", ref, "--target", "claude", "--dir", destDir}, extra...))
		err := cmd.Execute()
		return buf.String(), err
	}

	if out, err := install("a@a"); err != nil {
		t.Fatalf("install a@a: %v (%s)", err, out)
	}
	out, err := install("a@m")
	if err != nil {
		t.Fatalf("install a@m: %v (%s)", err, out)
	}
	if !strings.Contains(out, "namespaced") {
		t.Errorf("expected a namespaced-resolution line; got: %s", out)
	}

	first := filepath.Join(destDir, ".claude", "commands", "a", "x.md")
	second := filepath.Join(destDir, ".claude", "commands", "a", "a-x.md")
	if _, err := os.Stat(first); err != nil {
		t.Errorf("first install's a/x.md missing: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("second install's namespaced a/a-x.md missing: %v", err)
	}
}

// TestPluginCmd_Install_ConflictRefuse asserts --conflict refuse aborts the
// second (clashing) install with a non-nil error.
func TestPluginCmd_Install_ConflictRefuse(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seedSamePluginTwoRepos(t)

	destDir := t.TempDir()
	run := func(ref string, extra ...string) error {
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(append([]string{"install", ref, "--target", "claude", "--dir", destDir}, extra...))
		return cmd.Execute()
	}

	if err := run("a@a"); err != nil {
		t.Fatalf("install a@a: %v", err)
	}
	if err := run("a@m", "--conflict", "refuse"); err == nil {
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
	cmd.SetArgs([]string{"install", "v@v", "--target", "claude", "--dir", t.TempDir(), "--verbose"})
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
// `plugin install <name>@<repo> --target claude,codex,pi` (comma-separated
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
	cmd.SetArgs([]string{"install", "mt@mt", "--target", "claude,codex,pi", "--dir", destDir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plugin install multi-target: %v (output: %s)", err, buf.String())
	}

	// installed.json must have THREE entries for mt@mt — one per adapter.
	installed, err := store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	wantTargets := map[string]bool{"claude-code": false, "codex": false, "pi": false}
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
		// Every target namespaces the plugin's command with its name, in the
		// form that tool accepts: a subdir for claude, a "-" for codex and pi.
		filepath.Join(destDir, ".claude", "commands", "mt", "x.md"),
		filepath.Join(destDir, ".codex", "prompts", "mt-x.md"),
		filepath.Join(destDir, ".pi", "prompts", "mt-x.md"),
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
// the repo via `repo update`, then `plugin update --dir`. Asserts the repo
// update reports a SHA change, the new file appears, the changed file content is
// updated, and installed.json's resolvedSHA changed.
func TestPluginCmd_Update_SHADrift(t *testing.T) {
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
		cmd.SetArgs([]string{"install", "dr@dr", "--target", "claude", "--dir", destDir})
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
	cmdFile := filepath.Join(destDir, ".claude", "commands", "dr", "c.md")
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

	// --- plugin update --dir: re-resolve + cleanup + re-project ---
	{
		var buf bytes.Buffer
		cmd := newPluginCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"update", "dr@dr", "--dir", destDir})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("plugin update: %v (output: %s)", err, buf.String())
		}
		if !strings.Contains(buf.String(), "updated dr@dr") {
			t.Errorf("expected 'updated dr@dr' in plugin update output, got: %s", buf.String())
		}
	}

	// new.md must now exist.
	if _, err := os.Stat(filepath.Join(destDir, ".claude", "commands", "dr", "new.md")); err != nil {
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

// TestPluginCmd_Update_RefusedKeepsOldInstall: a new version that projects a
// file under a directory linked out of the project is refused before the old
// install is removed, and the record keeps the old resolved SHA.
func TestPluginCmd_Update_RefusedKeepsOldInstall(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	work := makeWorkingPluginRepo(t, "v1")
	run := func(args ...string) error {
		var buf bytes.Buffer
		cmd := newRepoCmd()
		if args[0] == "plugin" {
			cmd = newPluginCmd()
			args = args[1:]
		}
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			return fmt.Errorf("%w (output: %s)", err, buf.String())
		}
		return nil
	}
	dest := t.TempDir()
	if err := run("add", "git:file://"+work, "--name", "rk"); err != nil {
		t.Fatal(err)
	}
	if err := run("plugin", "install", "rk@rk", "--target", "claude", "--dir", dest); err != nil {
		t.Fatal(err)
	}
	before, _ := store.LoadInstalled()

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dest, ".claude", "agents")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "agents", "a.md"), []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runPkgGit(t, work, "add", "-A")
	runPkgGit(t, work, "commit", "-m", "v2")
	if err := run("update", "rk"); err != nil {
		t.Fatal(err)
	}

	if err := run("plugin", "update", "rk@rk", "--dir", dest); err == nil {
		t.Fatal("update through a linked dir succeeded")
	}
	if b, err := os.ReadFile(filepath.Join(dest, ".claude", "commands", "rk", "c.md")); err != nil || string(b) != "v1" {
		t.Errorf("old file = %q, %v; want v1 kept", b, err)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Errorf("outside dir written: %v", ents)
	}
	after, _ := store.LoadInstalled()
	if len(after.Installed) != len(before.Installed) || after.Installed[0].ResolvedSHA != before.Installed[0].ResolvedSHA {
		t.Errorf("record changed: %+v -> %+v", before.Installed, after.Installed)
	}
}

// ---- Group E: --global install scope ----------------------------------------

// pinConfigDirEnv neutralizes every per-adapter config-dir override that
// adapter.RemapGlobalPath consults, so a --global assertion against a
// $HOME-relative path cannot be redirected by the developer's own shell.
// CLAUDE_CONFIG_DIR in particular is set by anyone running a redirected Claude
// Code profile. Callers set the specific vars they want AFTER calling this.
//
// scripts/dev.sh forwards a closed -e allowlist that excludes these, so the
// containerized run is hermetic already — this keeps a HOST-side `go test`
// honest too.
func pinConfigDirEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"CLAUDE_CONFIG_DIR", "CODEX_HOME",
		"PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME",
	} {
		t.Setenv(v, "")
	}
}

// TestPluginCmd_Install_Global_Claude verifies that `--global` (no --dir)
// installs under $HOME for claude-code.
func TestPluginCmd_Install_Global_Claude(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	pinConfigDirEnv(t)
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
	cmd.SetArgs([]string{"install", "gfix@gfix", "-g", "--target", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plugin install --global: %v (output: %s)", err, buf.String())
	}

	want := filepath.Join(home, ".claude", "commands", "gfix", "x.md")
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

	pinConfigDirEnv(t)
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

	// The command (commands/x.md) projects to .opencode/commands/ocg-x.md, then the
	// global remap moves the whole .opencode/ tree to .config/opencode/.
	remapped := filepath.Join(home, ".config", "opencode", "commands", "ocg-x.md")
	if _, err := os.Stat(remapped); err != nil {
		t.Errorf("expected remapped global install at %s: %v", remapped, err)
	}
	// The un-remapped path must NOT exist.
	original := filepath.Join(home, ".opencode", "commands", "ocg-x.md")
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

	pinConfigDirEnv(t)
	home := t.TempDir()
	configRoot := filepath.Join(home, "skills")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configRoot)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	seedSamePluginTwoRepos(t)

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
	install("a@m")

	if _, err := os.Stat(filepath.Join(configRoot, "commands", "a", "x.md")); err != nil {
		t.Fatalf("first install missing under redirected root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configRoot, "commands", "a", "a-x.md")); err != nil {
		t.Fatalf("namespaced second install missing under redirected root: %v; output=%s", err, output.String())
	}
	if _, err := os.Stat(filepath.Join(home, "a-skills", "commands", "a", "x.md")); !os.IsNotExist(err) {
		t.Fatalf("namespace rewrote the root segment outside config root: err=%v", err)
	}
}

// runPluginCmd runs `ach-cli local plugin <args>` and returns its combined
// output and error.
func runPluginCmd(args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := newPluginCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// wantCollisionRefuse fails unless err carries exit code 7.
func wantCollisionRefuse(t *testing.T, out string, err error) {
	t.Helper()
	var ce *exit.CodedError
	if !errors.As(err, &ce) || ce.Code != exit.CollisionRefuse {
		t.Fatalf("error = %v; want exit code %d (output: %s)", err, exit.CollisionRefuse, out)
	}
}

// installedFiles returns the recorded files of ref at claude-code.
func installedFiles(t *testing.T, ref string) []store.FileRec {
	t.Helper()
	installed, err := store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	for _, e := range installed.Installed {
		if e.Ref == ref && e.Target == "claude-code" {
			return e.Files
		}
	}
	return nil
}

// TestPluginCmd_Install_RefusesUnownedFile: a file that exists at an install
// destination, with other content and recorded for no installed package, is the
// user's own. install refuses it with exit 7 and records nothing; --force
// replaces it.
func TestPluginCmd_Install_RefusesUnownedFile(t *testing.T) {
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
	target := filepath.Join(destDir, ".claude", "commands", "fix", "x.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runPluginCmd("install", "fix@fix", "--target", "claude", "--dir", destDir)
	wantCollisionRefuse(t, out, err)
	if b, _ := os.ReadFile(target); string(b) != "mine" {
		t.Errorf("x.md = %q; want the user's file untouched", b)
	}
	if files := installedFiles(t, "fix@fix"); files != nil {
		t.Errorf("a refused install was recorded: %+v", files)
	}

	if out, err := runPluginCmd("install", "fix@fix", "--target", "claude", "--dir", destDir, "--force"); err != nil {
		t.Fatalf("install --force: %v (output: %s)", err, out)
	}
	if b, _ := os.ReadFile(target); string(b) == "mine" {
		t.Errorf("x.md still holds the user's content after --force")
	}
}

// installV1ThenCommitV2 installs dr@dr at v1 (commands/c.md) into a fresh
// project and commits v2 upstream (c.md changed, new.md added). It returns the
// project's paths for c.md and new.md.
func installV1ThenCommitV2(t *testing.T) (destDir, cmdFile, newFile string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	work := makeWorkingPluginRepo(t, "v1")
	var buf bytes.Buffer
	cmd := newRepoCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"add", "git:file://" + work, "--name", "dr"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repo add: %v (output: %s)", err, buf.String())
	}
	destDir = t.TempDir()
	if out, err := runPluginCmd("install", "dr@dr", "--target", "claude", "--dir", destDir); err != nil {
		t.Fatalf("install v1: %v (output: %s)", err, out)
	}
	for name, body := range map[string]string{"c.md": "v2", "new.md": "new command\n"} {
		if err := os.WriteFile(filepath.Join(work, "commands", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runPkgGit(t, work, "add", "-A")
	runPkgGit(t, work, "commit", "-m", "v2")
	dir := filepath.Join(destDir, ".claude", "commands", "dr")
	return destDir, filepath.Join(dir, "c.md"), filepath.Join(dir, "new.md")
}

// TestPluginCmd_Update_KeepsUserModifiedFile: update leaves a file the user
// edited as it is, reports it as skipped and keeps tracking it.
func TestPluginCmd_Update_KeepsUserModifiedFile(t *testing.T) {
	destDir, cmdFile, newFile := installV1ThenCommitV2(t)
	if err := os.WriteFile(cmdFile, []byte("my edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runPluginCmd("update", "dr@dr", "--dir", destDir)
	if err != nil {
		t.Fatalf("update: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "skipped user-modified file") {
		t.Errorf("expected the user-modified file to be reported as skipped, got: %s", out)
	}
	if b, _ := os.ReadFile(cmdFile); string(b) != "my edit" {
		t.Errorf("c.md = %q; update overwrote the file it reported as skipped", b)
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Errorf("expected new.md after update: %v", err)
	}
	var tracked bool
	for _, f := range installedFiles(t, "dr@dr") {
		tracked = tracked || strings.HasSuffix(f.RelPath, "c.md")
	}
	if !tracked {
		t.Errorf("the skipped file is no longer tracked: %+v", installedFiles(t, "dr@dr"))
	}
}

// TestPluginCmd_Update_RefusesUnownedFile: a file the next version adds that
// already exists as the user's own stops the update with exit 7 before the old
// install is touched; --force replaces it.
func TestPluginCmd_Update_RefusesUnownedFile(t *testing.T) {
	destDir, cmdFile, newFile := installV1ThenCommitV2(t)
	if err := os.WriteFile(newFile, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runPluginCmd("update", "dr@dr", "--dir", destDir)
	wantCollisionRefuse(t, out, err)
	if b, _ := os.ReadFile(newFile); string(b) != "mine" {
		t.Errorf("new.md = %q; want the user's file untouched", b)
	}
	if b, _ := os.ReadFile(cmdFile); string(b) != "v1" {
		t.Errorf("c.md = %q after a refused update; want the old install kept", b)
	}

	if out, err := runPluginCmd("update", "dr@dr", "--dir", destDir, "--force"); err != nil {
		t.Fatalf("update --force: %v (output: %s)", err, out)
	}
	if b, _ := os.ReadFile(newFile); string(b) != "new command\n" {
		t.Errorf("new.md = %q; want the next version's file after --force", b)
	}
}

// makeMCPPluginRepo creates a bare repo for a direct plugin that ships a
// command and a root .mcp.json with one remote server. Returns the file:// URL.
func makeMCPPluginRepo(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	runPkgGit(t, work, "init", "-b", "main", ".")
	runPkgGit(t, work, "config", "user.email", "t@t")
	runPkgGit(t, work, "config", "user.name", "t")
	for rel, body := range map[string]string{
		"commands/x.md": "# x\nA test command.\n",
		".mcp.json":     `{"mcpServers":{"srv":{"url":"https://mcp.example.com/mcp"}}}`,
	} {
		p := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	runPkgGit(t, work, "add", "-A")
	runPkgGit(t, work, "commit", "-m", "init mcp plugin")
	bare := t.TempDir()
	runPkgGit(t, work, "clone", "--bare", ".", bare)
	return "file://" + bare
}

func seedPluginRepo(t *testing.T, name, url string) {
	t.Helper()
	seedRepo(t, store.RepoEntry{
		Name: name, Source: "git:" + url, Kind: "git", CloneURL: url, GitRef: "main",
		Provides: []store.Capability{{Lens: "plugin", Count: 1}},
		AddedAt:  "2026-01-01T00:00:00Z",
	})
}

// TestServerHints_Note: the opencode restart hint is due when the set of opencode server
// keys changes, including a record from before the OpenCode 2 format, and not
// for an update that keeps the same servers or for another tool.
func TestServerHints_Note(t *testing.T) {
	rec := func(keys ...string) []store.FileRec {
		return []store.FileRec{{RelPath: "x", Merge: "deep", Keys: keys}}
	}
	for name, tc := range map[string]struct {
		target        string
		before, after []store.FileRec
		want          bool
	}{
		"install adds":       {"opencode", nil, rec("mcp.servers.a"), true},
		"update same":        {"opencode", rec("mcp.servers.a"), rec("mcp.servers.a"), false},
		"update adds":        {"opencode", rec("mcp.servers.a"), rec("mcp.servers.a", "mcp.servers.b"), true},
		"uninstall legacy":   {"opencode", rec("mcp.a"), nil, true},
		"other tool":         {"claude-code", nil, rec("mcpServers.a"), false},
		"no server in files": {"opencode", rec(), rec(), false},
	} {
		var o serverHints
		o.note(tc.target, tc.before, tc.after)
		if o.restart != tc.want {
			t.Errorf("%s: hint due = %v, want %v", name, o.restart, tc.want)
		}
	}
}

// TestPluginCmd_OpencodeServiceRestartHint: the restart hint appears once when
// a plugin install or uninstall writes or removes an MCP server entry for
// opencode, and never otherwise.
func TestPluginCmd_OpencodeServiceRestartHint(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seedPluginRepo(t, "m", makeMCPPluginRepo(t))
	seedPluginRepo(t, "p", makeDirectPluginRepo(t))
	hints := func(out string) int { return strings.Count(out, opencodeRestartHint) }

	t.Run("install opencode with MCP", func(t *testing.T) {
		out, err := runPluginCmd("install", "m@m", "--target", "opencode", "--dir", t.TempDir())
		if err != nil || hints(out) != 1 {
			t.Fatalf("err=%v hints=%d; want 1\n%s", err, hints(out), out)
		}
	})
	t.Run("install claude-code with MCP", func(t *testing.T) {
		out, err := runPluginCmd("install", "m@m", "--target", "claude-code", "--dir", t.TempDir())
		if err != nil || hints(out) != 0 {
			t.Fatalf("err=%v hints=%d; want 0\n%s", err, hints(out), out)
		}
	})
	t.Run("install opencode without MCP", func(t *testing.T) {
		out, err := runPluginCmd("install", "p@p", "--target", "opencode", "--dir", t.TempDir())
		if err != nil || hints(out) != 0 {
			t.Fatalf("err=%v hints=%d; want 0\n%s", err, hints(out), out)
		}
	})
	t.Run("install opencode dry-run", func(t *testing.T) {
		out, err := runPluginCmd("install", "m@m", "--target", "opencode", "--dir", t.TempDir(), "--dry-run")
		if err != nil || hints(out) != 0 {
			t.Fatalf("err=%v hints=%d; want 0\n%s", err, hints(out), out)
		}
	})
	t.Run("uninstall opencode with MCP", func(t *testing.T) {
		dir := t.TempDir()
		if out, err := runPluginCmd("install", "m@m", "--target", "opencode", "--dir", dir); err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		out, err := runPluginCmd("uninstall", "m@m", "--target", "opencode", "--dir", dir)
		if err != nil || hints(out) != 1 {
			t.Fatalf("err=%v hints=%d; want 1\n%s", err, hints(out), out)
		}
	})
}
