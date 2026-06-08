// SPDX-License-Identifier: Apache-2.0

package manager_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// Blank-import the claude-code adapter so its init() registers it.
	"github.com/ackstorm/ach/internal/cli/adapter"
	_ "github.com/ackstorm/ach/internal/cli/adapter/claudecode"
	"github.com/ackstorm/ach/internal/cli/localpkg/manager"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
)

// ---- helpers ----------------------------------------------------------------

// gitEnv is a minimal git author+committer environment for reproducible commits.
var gitEnv = append(os.Environ(),
	"GIT_AUTHOR_NAME=test",
	"GIT_AUTHOR_EMAIL=test@test",
	"GIT_COMMITTER_NAME=test",
	"GIT_COMMITTER_EMAIL=test@test",
)

// runGit runs a git command in dir, failing t on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// headSHA returns the HEAD SHA of the git repo at dir.
func headSHA(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD in %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}

// makePluginRepo creates a bare git repo with a minimal Claude Code plugin
// layout (a commands/ directory) and returns the file:// URL.
func makePluginRepo(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "init", "-b", "main", ".")

	// Write a minimal commands/hello.md to make it look like a plugin.
	cmdDir := filepath.Join(work, "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir commands: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "hello.md"), []byte("# hello\nA greeting command.\n"), 0o644); err != nil {
		t.Fatalf("write hello.md: %v", err)
	}

	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init plugin")

	bare := t.TempDir()
	runGit(t, work, "clone", "--bare", ".", bare)
	return "file://" + bare
}

// makeSkillRepo creates a bare git repo with a minimal SKILL.md and returns
// the file:// URL.
func makeSkillRepo(t *testing.T, skillName string) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "init", "-b", "main", ".")

	skillMD := "---\nname: " + skillName + "\ndescription: A test skill for " + skillName + ".\n---\n\n# " + skillName + "\n"
	if err := os.WriteFile(filepath.Join(work, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init skill")

	bare := t.TempDir()
	runGit(t, work, "clone", "--bare", ".", bare)
	return "file://" + bare
}

// makeMarketplaceRepo creates a marketplace repo with a .claude-plugin/marketplace.json
// whose one plugin entry uses a "git-subdir" source pointing at a separate
// plugin repo (file:// URL). Returns the marketplace's file:// bare URL and
// the entry plugin's file:// bare URL.
func makeMarketplaceRepo(t *testing.T, pluginRepoURL string) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "init", "-b", "main", ".")

	marketplaceJSON := `{
  "name": "test-marketplace",
  "owner": {"name": "test"},
  "plugins": [
    {
      "name": "my-plugin",
      "description": "A test plugin.",
      "source": {
        "source": "url",
        "url": "` + pluginRepoURL + `"
      }
    }
  ]
}`
	pluginDir := filepath.Join(work, ".claude-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude-plugin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "marketplace.json"), []byte(marketplaceJSON), 0o644); err != nil {
		t.Fatalf("write marketplace.json: %v", err)
	}

	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init marketplace")

	bare := t.TempDir()
	runGit(t, work, "clone", "--bare", ".", bare)
	return "file://" + bare
}

// tarEntryPaths returns the sorted regular-file names from a gzip tar.
func tarEntryPaths(t *testing.T, tarball []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		t.Fatalf("gzip open: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, e := tr.Next()
		if e != nil {
			break
		}
		if hdr.Typeflag == tar.TypeReg {
			names = append(names, hdr.Name)
		}
	}
	return names
}

// ---- unit tests: Project (no git) -------------------------------------------

// TestProject_UnknownAdapter verifies that Project returns an error when the
// adapter ID is not registered.
func TestProject_UnknownAdapter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := manager.Project(dir, "no-such-adapter-xyz")
	if err == nil {
		t.Fatal("expected error for unknown adapter, got nil")
	}
}

// TestProject_ClaudeCode verifies Project("claude-code") over a minimal
// plugin tree with a commands/ file returns PlannedWrites with the
// expected .claude/commands/ path and MergeReplace kind.
func TestProject_ClaudeCode(t *testing.T) {
	t.Parallel()

	// Build a minimal staged tree in a temp dir.
	stageDir := t.TempDir()
	cmdDir := filepath.Join(stageDir, "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "greet.md"), []byte("# greet\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	writes, err := manager.Project(stageDir, "claude-code")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(writes) == 0 {
		t.Fatal("expected at least one PlannedWrite, got none")
	}

	var found bool
	for _, w := range writes {
		if w.Path == ".claude/commands/greet.md" {
			found = true
			if w.Merge != adapter.MergeReplace {
				t.Errorf("path %q: Merge = %v; want MergeReplace", w.Path, w.Merge)
			}
			if string(w.Content) != "# greet\n" {
				t.Errorf("path %q: content = %q; want %q", w.Path, w.Content, "# greet\n")
			}
		}
	}
	if !found {
		t.Errorf("expected write for .claude/commands/greet.md; got writes: %+v", writes)
	}
}

// TestProject_ClaudeCode_SkillsRouted verifies that skills/**/* files route
// to .claude/skills/**/* with MergeReplace.
func TestProject_ClaudeCode_SkillsRouted(t *testing.T) {
	t.Parallel()

	stageDir := t.TempDir()
	skillDir := filepath.Join(stageDir, "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	skillMD := "---\nname: my-skill\ndescription: A skill.\n---\n\n# my-skill\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	writes, err := manager.Project(stageDir, "claude-code")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	var found bool
	for _, w := range writes {
		if w.Path == ".claude/skills/my-skill/SKILL.md" {
			found = true
			if w.Merge != adapter.MergeReplace {
				t.Errorf("Merge = %v; want MergeReplace", w.Merge)
			}
		}
	}
	if !found {
		t.Errorf("expected .claude/skills/my-skill/SKILL.md in writes; got %+v", writes)
	}
}

// ---- integration tests: Resolve (requires git binary) -----------------------

func TestResolve_DirectPlugin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	pluginURL := makePluginRepo(t)

	repo := store.RepoEntry{
		Name:     "my-plugin",
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := manager.Resolve(ctx, repo, "", "my-plugin", "plugin")
	if err != nil {
		t.Fatalf("Resolve(plugin): %v", err)
	}
	defer func() { _ = os.RemoveAll(res.StageDir) }()

	if res.Kind != "plugin" {
		t.Errorf("Kind = %q; want %q", res.Kind, "plugin")
	}
	if res.Name != "my-plugin" {
		t.Errorf("Name = %q; want %q", res.Name, "my-plugin")
	}
	if len(res.ResolvedSHA) != 40 {
		t.Errorf("ResolvedSHA = %q; want 40-hex", res.ResolvedSHA)
	}
	if res.StageDir == "" {
		t.Fatal("StageDir is empty")
	}
	// The stage dir should contain commands/hello.md.
	if _, err := os.Stat(filepath.Join(res.StageDir, "commands", "hello.md")); err != nil {
		t.Errorf("expected commands/hello.md in stage dir: %v", err)
	}
}

// TestResolve_DirectPlugin_NameMismatch verifies that Resolve returns an
// error when the name does not match repo.Name for the direct-plugin lens.
func TestResolve_DirectPlugin_NameMismatch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	pluginURL := makePluginRepo(t)
	repo := store.RepoEntry{
		Name:     "my-plugin",
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, err := manager.Resolve(ctx, repo, "", "other-plugin", "plugin")
	if err == nil {
		t.Fatal("expected error for name mismatch, got nil")
	}
}

// TestResolve_DirectSkill exercises the skill lens against a minimal
// SKILL.md repo.
func TestResolve_DirectSkill(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	skillURL := makeSkillRepo(t, "my-skill")
	repo := store.RepoEntry{
		Name:     "my-skill",
		Kind:     "git",
		CloneURL: skillURL,
		GitRef:   "main",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := manager.Resolve(ctx, repo, "", "my-skill", "skill")
	if err != nil {
		t.Fatalf("Resolve(skill): %v", err)
	}
	defer func() { _ = os.RemoveAll(res.StageDir) }()

	if res.Kind != "skill" {
		t.Errorf("Kind = %q; want skill", res.Kind)
	}
	if res.Name != "my-skill" {
		t.Errorf("Name = %q; want my-skill", res.Name)
	}
	if len(res.ResolvedSHA) != 40 {
		t.Errorf("ResolvedSHA = %q; want 40-hex", res.ResolvedSHA)
	}
	// The manager nests the skill under skills/<name>/ so the claudecode
	// `skills/**/* → .claude/skills/**/*` projection rule (first-path-element
	// classifier) fires — SKILL.md is NOT left bare at the stage root.
	if _, err := os.Stat(filepath.Join(res.StageDir, "skills", "my-skill", "SKILL.md")); err != nil {
		t.Errorf("expected skills/my-skill/SKILL.md in stage dir: %v", err)
	}
}

// TestResolve_DirectSkill_ThenProject exercises the full pipeline:
// Resolve(skill) → Project(claude-code) → .claude/skills/ writes.
func TestResolve_DirectSkill_ThenProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	// The skill is stored at the repo root as SKILL.md. After Resolve the
	// manager nests it under skills/hello-skill/ so the claudecode adapter's
	// `skills/**/*` rule (which classifies on the FIRST path element) fires —
	// Project runs over res.StageDir directly with no caller-side wrapping.
	skillURL := makeSkillRepo(t, "hello-skill")
	repo := store.RepoEntry{
		Name:     "hello-skill",
		Kind:     "git",
		CloneURL: skillURL,
		GitRef:   "main",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := manager.Resolve(ctx, repo, "", "hello-skill", "skill")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer func() { _ = os.RemoveAll(res.StageDir) }()

	// The manager already nested the skill under skills/hello-skill/, so Project
	// runs over the stage dir directly and the claudecode `skills/**/*` rule
	// fires with no caller-side wrapping.
	if _, err := os.Stat(filepath.Join(res.StageDir, "skills", "hello-skill", "SKILL.md")); err != nil {
		t.Fatalf("expected skills/hello-skill/SKILL.md in stage dir: %v", err)
	}

	writes, err := manager.Project(res.StageDir, "claude-code")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	var found bool
	for _, w := range writes {
		if strings.HasPrefix(w.Path, ".claude/skills/hello-skill/") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected .claude/skills/hello-skill/... in writes; got %+v", writes)
	}
}

// TestResolve_DirectPlugin_ThenProject exercises Resolve(plugin) → Project.
func TestResolve_DirectPlugin_ThenProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	pluginURL := makePluginRepo(t)
	repo := store.RepoEntry{
		Name:     "my-plugin",
		Kind:     "git",
		CloneURL: pluginURL,
		GitRef:   "main",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := manager.Resolve(ctx, repo, "", "my-plugin", "plugin")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer func() { _ = os.RemoveAll(res.StageDir) }()

	writes, err := manager.Project(res.StageDir, "claude-code")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	var found bool
	for _, w := range writes {
		if w.Path == ".claude/commands/hello.md" {
			found = true
			if w.Merge != adapter.MergeReplace {
				t.Errorf("Merge = %v; want MergeReplace", w.Merge)
			}
		}
	}
	if !found {
		t.Errorf("expected .claude/commands/hello.md in writes; got %+v", writes)
	}
}

// TestResolve_MarketplacePlugin exercises the plugin-marketplace lens with a
// marketplace repo that contains a "url"-kind entry pointing at a local plugin
// repo.
//
// The marketplace lens is the tricky offline case: the manager fetches the
// marketplace repo, extracts marketplace.json, then fetches the entry plugin
// from a second local repo. Both repos are created as file:// paths so no
// network is needed.
func TestResolve_MarketplacePlugin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	// Plugin repo: has commands/hello.md.
	pluginURL := makePluginRepo(t)

	// Marketplace repo: has .claude-plugin/marketplace.json pointing at pluginURL.
	marketplaceURL := makeMarketplaceRepo(t, pluginURL)

	repo := store.RepoEntry{
		Name:     "test-marketplace",
		Kind:     "git",
		CloneURL: marketplaceURL,
		GitRef:   "main",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := manager.Resolve(ctx, repo, "", "my-plugin", "plugin-marketplace")
	if err != nil {
		t.Fatalf("Resolve(plugin-marketplace): %v", err)
	}
	defer func() { _ = os.RemoveAll(res.StageDir) }()

	if res.Kind != "plugin" {
		t.Errorf("Kind = %q; want plugin", res.Kind)
	}
	if res.Name != "my-plugin" {
		t.Errorf("Name = %q; want my-plugin", res.Name)
	}
	if len(res.ResolvedSHA) != 40 {
		t.Errorf("ResolvedSHA = %q; want 40-hex", res.ResolvedSHA)
	}
	// Plugin stage dir should have commands/hello.md.
	if _, err := os.Stat(filepath.Join(res.StageDir, "commands", "hello.md")); err != nil {
		t.Errorf("expected commands/hello.md in stage dir: %v", err)
	}

	// Project through claude-code.
	writes, err := manager.Project(res.StageDir, "claude-code")
	if err != nil {
		t.Fatalf("Project after marketplace resolve: %v", err)
	}
	var found bool
	for _, w := range writes {
		if w.Path == ".claude/commands/hello.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected .claude/commands/hello.md after marketplace+project; got %+v", writes)
	}
}

// TestResolve_UnknownLens verifies Resolve returns an error for an unrecognized lens.
func TestResolve_UnknownLens(t *testing.T) {
	ctx := context.Background()
	repo := store.RepoEntry{Name: "x", Kind: "git", CloneURL: "file:///tmp/nonexistent"}
	_, err := manager.Resolve(ctx, repo, "", "x", "unknown-lens")
	if err == nil {
		t.Fatal("expected error for unknown lens")
	}
}
