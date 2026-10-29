// SPDX-License-Identifier: Apache-2.0

package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// Happy path against a local bare-repo fixture (no network).
func TestGitTransport_GitHub_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupHubBareFixture(t)

	f, err := New(&achv1alpha1.GitHubSource{
		Repo: "fixture/repo",
		Ref:  "main",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.cloneURLForTesting = bare

	res, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer res.Body.Close()

	if res.NotModified {
		t.Errorf("NotModified=true on first fetch")
	}
	if len(res.UpstreamRev) != 40 {
		t.Errorf("UpstreamRev not 40-hex: %q", res.UpstreamRev)
	}
	n, _ := io.Copy(io.Discard, res.Body)
	if n == 0 {
		t.Errorf("empty body")
	}
}

func TestGitTransport_GitHub_NotModified(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupHubBareFixture(t)
	head := hubHeadSHA(t, bare)

	f, err := New(&achv1alpha1.GitHubSource{
		Repo: "fixture/repo",
		Ref:  "main",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.cloneURLForTesting = bare

	res, err := f.Fetch(context.Background(), sources.FetchRequest{PriorRev: head})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.NotModified {
		t.Errorf("expected NotModified=true when PriorRev matches HEAD")
	}
	if res.Body != nil {
		t.Errorf("expected nil Body on NotModified")
	}
	if res.UpstreamRev != head {
		t.Errorf("UpstreamRev should echo back the matched SHA (got %q want %q)", res.UpstreamRev, head)
	}
}

// TestGitTransport_GitHub_PathNarrows asserts spec.Path is plumbed through the
// git transport so the produced tarball is narrowed (re-rooted at the subtree
// contents) — the F1 wiring change.
func TestGitTransport_GitHub_PathNarrows(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupHubSubtreeBareFixture(t)

	f, err := New(&achv1alpha1.GitHubSource{
		Repo: "fixture/repo",
		Ref:  "main",
		Path: "skills/pdf",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.cloneURLForTesting = bare

	res, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := hubTarRegularNames(t, body)
	want := []string{"SKILL.md", "run.sh"}
	if len(got) != len(want) {
		t.Fatalf("narrowed entries = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("narrowed entries = %v; want %v (spec.Path not narrowed?)", got, want)
		}
	}
}

func TestGitTransport_GitHub_UnreachableClassifies(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	f, _ := New(&achv1alpha1.GitHubSource{
		Repo: "no/such",
		Ref:  "main",
	})
	f.cloneURLForTesting = "https://localhost:1/nonexistent.git"

	_, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sources.ErrUnreachable) &&
		!errors.Is(err, sources.ErrNotFound) &&
		!errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("expected one of {Unreachable, NotFound, UpstreamInvalid}; got %v", err)
	}
}

// setupHubBareFixture creates a small git repo under t.TempDir() and
// returns the path of a --bare clone suitable for use as the clone URL.
func setupHubBareFixture(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# fix\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
	bare := filepath.Join(t.TempDir(), "fixture.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	run("clone", "--bare", ".", bare)
	return bare
}

func hubHeadSHA(t *testing.T, bare string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", bare, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return string(out[:40])
}

// setupHubSubtreeBareFixture builds a repo with a skills/ monorepo layout and
// returns a --bare clone path (for the spec.Path narrowing test).
func setupHubSubtreeBareFixture(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main", ".")
	files := map[string]string{
		"README.md":           "# repo\n",
		"skills/pdf/SKILL.md": "---\nname: pdf\n---\n",
		"skills/pdf/run.sh":   "echo hi\n",
	}
	for name, content := range files {
		full := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	run("add", "-A")
	run("commit", "-m", "init")
	bare := filepath.Join(t.TempDir(), "fixture.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	run("clone", "--bare", ".", bare)
	return bare
}

func hubTarRegularNames(t *testing.T, tarball []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		t.Fatalf("gzip open: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var out []string
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatalf("tar read: %v", e)
		}
		if hdr.Typeflag == tar.TypeReg {
			out = append(out, hdr.Name)
		}
	}
	sort.Strings(out)
	return out
}
