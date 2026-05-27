// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		Repo:      "fixture/repo",
		Ref:       "main",
		Transport: "git",
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
		Repo:      "fixture/repo",
		Ref:       "main",
		Transport: "git",
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

func TestGitTransport_GitHub_TransportRouting(t *testing.T) {
	cases := []struct {
		transport string
		want      string
	}{
		{"", "git"},
		{"git", "git"},
		{"rest", "rest"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.transport, func(t *testing.T) {
			f, _ := New(&achv1alpha1.GitHubSource{
				Repo:      "x/y",
				Ref:       "main",
				Transport: tc.transport,
			})
			if got := f.resolvedTransport(); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestGitTransport_GitHub_UnreachableClassifies(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	f, _ := New(&achv1alpha1.GitHubSource{
		Repo:      "no/such",
		Ref:       "main",
		Transport: "git",
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
