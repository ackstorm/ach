// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/localpkg/store"
)

// setupXDG sets XDG_CONFIG_HOME to a fresh temp dir so Dir() resolves
// into a test-isolated location.  It also ensures the ach sub-dir
// exists so config.Path() succeeds (it does not create the dir, only
// returns the path).
func setupXDG(t *testing.T) string {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// config.Path() returns <xdg>/ach/config.yaml — the ach dir must
	// exist so os.MkdirAll inside Dir() can parent off it.
	if err := os.MkdirAll(filepath.Join(xdg, "ach"), 0o700); err != nil {
		t.Fatalf("setup: mkdir ach: %v", err)
	}
	return xdg
}

// TestAbsentFiles verifies that Load* functions return empty defaults
// when no files exist yet.
func TestAbsentFiles(t *testing.T) {
	setupXDG(t)

	rf, err := store.LoadRepos()
	if err != nil {
		t.Fatalf("LoadRepos absent: %v", err)
	}
	if rf.Version != 1 {
		t.Errorf("LoadRepos absent: want Version=1, got %d", rf.Version)
	}
	if len(rf.Repos) != 0 {
		t.Errorf("LoadRepos absent: want empty Repos, got %d entries", len(rf.Repos))
	}

	inf, err := store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled absent: %v", err)
	}
	if inf.Version != 1 {
		t.Errorf("LoadInstalled absent: want Version=1, got %d", inf.Version)
	}
	if len(inf.Installed) != 0 {
		t.Errorf("LoadInstalled absent: want empty Installed, got %d entries", len(inf.Installed))
	}

	tok, err := store.LoadToken("x")
	if err != nil {
		t.Fatalf("LoadToken absent: %v", err)
	}
	if tok != "" {
		t.Errorf("LoadToken absent: want empty string, got %q", tok)
	}
}

// TestSaveLoadRepos verifies round-trip fidelity for ReposFile including
// a nested Capability slice.
func TestSaveLoadRepos(t *testing.T) {
	setupXDG(t)

	orig := &store.ReposFile{
		Version: 1,
		Repos: []store.RepoEntry{
			{
				Name:        "my-plugins",
				Source:      "github:acme/plugins",
				Kind:        "github",
				CloneURL:    "https://github.com/acme/plugins.git",
				GitRef:      "main",
				AuthScheme:  "token",
				HasToken:    true,
				DetectedSHA: "abc123",
				AddedAt:     "2026-06-08T00:00:00Z",
				Provides: []store.Capability{
					{Lens: "plugin-marketplace", Count: 5},
					{Lens: "plugin", Count: 3},
				},
			},
		},
	}

	if err := store.SaveRepos(orig); err != nil {
		t.Fatalf("SaveRepos: %v", err)
	}

	got, err := store.LoadRepos()
	if err != nil {
		t.Fatalf("LoadRepos after save: %v", err)
	}
	if got.Version != orig.Version {
		t.Errorf("Version: want %d got %d", orig.Version, got.Version)
	}
	if len(got.Repos) != 1 {
		t.Fatalf("Repos len: want 1 got %d", len(got.Repos))
	}
	r := got.Repos[0]
	if r.Name != "my-plugins" {
		t.Errorf("Name: want my-plugins got %q", r.Name)
	}
	if r.Source != "github:acme/plugins" {
		t.Errorf("Source: want github:acme/plugins got %q", r.Source)
	}
	if r.Kind != "github" {
		t.Errorf("Kind: want github got %q", r.Kind)
	}
	if r.CloneURL != "https://github.com/acme/plugins.git" {
		t.Errorf("CloneURL mismatch: %q", r.CloneURL)
	}
	if r.GitRef != "main" {
		t.Errorf("GitRef: want main got %q", r.GitRef)
	}
	if !r.HasToken {
		t.Errorf("HasToken: want true got false")
	}
	if r.DetectedSHA != "abc123" {
		t.Errorf("DetectedSHA: want abc123 got %q", r.DetectedSHA)
	}
	if len(r.Provides) != 2 {
		t.Fatalf("Provides len: want 2 got %d", len(r.Provides))
	}
	if r.Provides[0].Lens != "plugin-marketplace" || r.Provides[0].Count != 5 {
		t.Errorf("Provides[0]: want {plugin-marketplace,5} got %+v", r.Provides[0])
	}
	if r.Provides[1].Lens != "plugin" || r.Provides[1].Count != 3 {
		t.Errorf("Provides[1]: want {plugin,3} got %+v", r.Provides[1])
	}
}

// TestTokenRoundTrip verifies save, lookup-existing, lookup-absent.
func TestTokenRoundTrip(t *testing.T) {
	setupXDG(t)

	if err := store.SaveToken("repoA", "secret"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	tok, err := store.LoadToken("repoA")
	if err != nil {
		t.Fatalf("LoadToken repoA: %v", err)
	}
	if tok != "secret" {
		t.Errorf("LoadToken repoA: want secret got %q", tok)
	}

	other, err := store.LoadToken("other")
	if err != nil {
		t.Fatalf("LoadToken other: %v", err)
	}
	if other != "" {
		t.Errorf("LoadToken other: want empty got %q", other)
	}
}

// TestCredentialsFileMode verifies credentials.json is written at mode 0600.
func TestCredentialsFileMode(t *testing.T) {
	xdg := setupXDG(t)

	if err := store.SaveToken("repo", "tok"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	credPath := filepath.Join(xdg, "ach", "local", "credentials.json")
	st, err := os.Stat(credPath)
	if err != nil {
		t.Fatalf("stat credentials.json: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("credentials.json mode: want 0600 got %04o", got)
	}
}

// TestTokenNotInReposJSON verifies that a secret token is NOT persisted
// inside repos.json even when HasToken:true is set on the RepoEntry.
func TestTokenNotInReposJSON(t *testing.T) {
	xdg := setupXDG(t)

	rf := &store.ReposFile{
		Version: 1,
		Repos: []store.RepoEntry{
			{
				Name:     "secured",
				Source:   "github:acme/secured",
				Kind:     "github",
				HasToken: true,
				AddedAt:  "2026-06-08T00:00:00Z",
			},
		},
	}
	if err := store.SaveRepos(rf); err != nil {
		t.Fatalf("SaveRepos: %v", err)
	}
	// Save the actual token to credentials.json (separate file).
	if err := store.SaveToken("secured", "supersecret"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	reposPath := filepath.Join(xdg, "ach", "local", "repos.json")
	raw, err := os.ReadFile(reposPath)
	if err != nil {
		t.Fatalf("read repos.json: %v", err)
	}
	if strings.Contains(string(raw), "supersecret") {
		t.Errorf("repos.json contains the token secret — it must not")
	}
}

// TestDeleteToken verifies delete removes the key and deleting an
// absent key is a no-op.
func TestDeleteToken(t *testing.T) {
	setupXDG(t)

	if err := store.SaveToken("repoA", "secret"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	if err := store.DeleteToken("repoA"); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	tok, err := store.LoadToken("repoA")
	if err != nil {
		t.Fatalf("LoadToken after delete: %v", err)
	}
	if tok != "" {
		t.Errorf("LoadToken after delete: want empty got %q", tok)
	}

	// No-op: deleting absent key must not error.
	if err := store.DeleteToken("neverExisted"); err != nil {
		t.Errorf("DeleteToken absent: want no error, got %v", err)
	}
}

// TestFileRecMergeMetaRoundTrip verifies that FileRec.Merge and FileRec.Keys
// persist through a SaveInstalled/LoadInstalled round-trip.
func TestFileRecMergeMetaRoundTrip(t *testing.T) {
	setupXDG(t)

	in := &store.InstalledFile{
		Version: 1,
		Installed: []store.InstalledEntry{{
			Ref:    "demo@repo",
			Repo:   "repo",
			Name:   "demo",
			Kind:   "plugin",
			Target: "claude-code",
			Files: []store.FileRec{
				{RelPath: ".claude/settings.json", Hash: "xxh3:a", Merge: "deep", Keys: []string{"mcpServers.demo"}},
				{RelPath: "CLAUDE.md", Hash: "xxh3:b", Merge: "composite", Keys: []string{"demo"}},
				{RelPath: ".claude/agents/x.md", Hash: "xxh3:c"}, // replace (no merge meta)
			},
			InstalledAt: "2026-06-08T00:00:00Z",
		}},
	}
	if err := store.SaveInstalled(in); err != nil {
		t.Fatalf("SaveInstalled: %v", err)
	}
	out, err := store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if len(out.Installed) != 1 {
		t.Fatalf("want 1 entry, got %d", len(out.Installed))
	}
	files := out.Installed[0].Files
	if len(files) != 3 {
		t.Fatalf("want 3 files, got %d", len(files))
	}
	if files[0].Merge != "deep" || len(files[0].Keys) != 1 || files[0].Keys[0] != "mcpServers.demo" {
		t.Errorf("deep FileRec round-trip lost meta: %+v", files[0])
	}
	if files[1].Merge != "composite" || len(files[1].Keys) != 1 || files[1].Keys[0] != "demo" {
		t.Errorf("composite FileRec round-trip lost meta: %+v", files[1])
	}
	if files[2].Merge != "" || len(files[2].Keys) != 0 {
		t.Errorf("replace FileRec should carry no merge meta: %+v", files[2])
	}
}

// TestFileRecLegacyLoadsAsReplace verifies a pre-existing installed.json
// record (no merge/keys fields) unmarshals as Merge=="" (replace), so older
// state remains back-compatible without migration.
func TestFileRecLegacyLoadsAsReplace(t *testing.T) {
	xdg := setupXDG(t)
	legacy := `{"version":1,"installed":[{"ref":"old@repo","repo":"repo","name":"old","kind":"plugin","target":"claude-code","files":[{"relPath":"a.txt","hash":"xxh3:z"}],"installedAt":"2026-01-01T00:00:00Z"}]}`
	path := filepath.Join(xdg, "ach", "local", "installed.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	out, err := store.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	f := out.Installed[0].Files[0]
	if f.Merge != "" || len(f.Keys) != 0 {
		t.Errorf("legacy FileRec should load as replace: %+v", f)
	}
}
