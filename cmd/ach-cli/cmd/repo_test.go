// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/localpkg/store"
)

// initFixtureRepo creates a local git repository with a .claude-plugin/marketplace.json
// containing 2 plugins, commits it on branch main, and returns the directory path.
func initFixtureRepo(t *testing.T) string {
	t.Helper()

	// Check git is available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()

	// Initialize repo on branch main
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(),
			"GIT_TERMINAL_PROMPT=0",
			"HOME="+dir, // isolate git config
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run("-C", dir, "init", "-b", "main")
	run("-C", dir, "config", "user.email", "t@t")
	run("-C", dir, "config", "user.name", "t")

	// Write marketplace.json with 2 plugins
	pluginDir := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	marketplace := `{"name":"m","owner":{"name":"o"},"plugins":[{"name":"p1","source":{"source":"github","repo":"a/b"}},{"name":"p2","source":{"source":"github","repo":"c/d"}}]}`
	if err := os.WriteFile(filepath.Join(pluginDir, "marketplace.json"), []byte(marketplace), 0o644); err != nil {
		t.Fatalf("write marketplace.json: %v", err)
	}

	run("-C", dir, "add", "-A")
	run("-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "init")

	return dir
}

func TestRepo(t *testing.T) {
	// Isolate store writes to a temp config dir
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dir := initFixtureRepo(t)
	source := "git:file://" + dir

	t.Run("add", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newRepoCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"add", source, "--name", "fix", "--auth", "bearer"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("repo add: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "fix") {
			t.Errorf("expected output to contain 'fix', got: %s", out)
		}

		// Verify repo was stored
		repos, err := store.LoadRepos()
		if err != nil {
			t.Fatalf("LoadRepos: %v", err)
		}
		var found *store.RepoEntry
		for i := range repos.Repos {
			if repos.Repos[i].Name == "fix" {
				found = &repos.Repos[i]
				break
			}
		}
		if found == nil {
			t.Fatal("repo 'fix' not found in repos.json")
		}
		if found.DetectedSHA == "" {
			t.Error("expected non-empty DetectedSHA")
		}
		// Check Provides contains plugin-marketplace:2
		hasPluginMarketplace := false
		for _, cap := range found.Provides {
			if cap.Lens == "plugin-marketplace" && cap.Count == 2 {
				hasPluginMarketplace = true
			}
		}
		if !hasPluginMarketplace {
			t.Errorf("expected Provides to contain plugin-marketplace:2, got: %+v", found.Provides)
		}
	})

	t.Run("list", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newRepoCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"list"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("repo list: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "fix") {
			t.Errorf("expected output to contain 'fix', got: %s", out)
		}
		if !strings.Contains(out, "plugin-marketplace:2") {
			t.Errorf("expected output to contain 'plugin-marketplace:2', got: %s", out)
		}
		// AUTH column should be present
		if !strings.Contains(out, "AUTH") {
			t.Errorf("expected AUTH column header in list output, got: %s", out)
		}
		// NEVER print tokens — no token was given so this is trivially satisfied,
		// but verify nothing that looks like a token credential appears.
		// (we didn't pass --token so HasToken is false → auth column shows "-")
		if strings.Contains(out, "•••") {
			t.Errorf("token marker should not appear when no token was set: %s", out)
		}
	})

	t.Run("remove", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newRepoCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"remove", "fix"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("repo remove: %v", err)
		}

		repos, err := store.LoadRepos()
		if err != nil {
			t.Fatalf("LoadRepos: %v", err)
		}
		for _, r := range repos.Repos {
			if r.Name == "fix" {
				t.Error("repo 'fix' still present after remove")
			}
		}
	})
}
