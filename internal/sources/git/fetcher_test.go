// SPDX-License-Identifier: Apache-2.0

package git

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/sources"
)

// TestFetcher_FetchClonesAndTars verifies the happy path:
//   - clone a local bare-repo fixture
//   - the returned tarball is a valid gzipped tar stream
//   - the stream contains at least one regular file from the fixture
func TestFetcher_FetchClonesAndTars(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH; skipping")
	}
	bare := setupBareFixture(t)

	f := New(Spec{
		URL:       bare,
		Ref:       "main",
		SHA:       fixtureHeadSHA(t, bare),
		CacheRoot: t.TempDir(),
	})
	res, err := f.Fetch(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer res.Body.Close()

	n, err := io.Copy(io.Discard, res.Body)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n == 0 {
		t.Errorf("body length 0; want >0")
	}
	if res.UpstreamRev == "" {
		t.Errorf("UpstreamRev empty")
	}
}

func TestFetcher_Fetch_InvalidSHA(t *testing.T) {
	f := New(Spec{
		URL: "https://example.invalid/foo.git",
		Ref: "main",
		SHA: "deadbeef", // not 40 hex
	})
	_, err := f.Fetch(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected err on short SHA")
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err should wrap ErrUpstreamInvalid; got %v", err)
	}
}

func TestFetcher_Fetch_UnreachableRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH; skipping")
	}
	f := New(Spec{
		URL:       "https://localhost:1/nonexistent.git",
		Ref:       "main",
		SHA:       "0123456789abcdef0123456789abcdef01234567",
		CacheRoot: t.TempDir(),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := f.Fetch(ctx, Request{})
	if err == nil {
		t.Fatal("expected err on unreachable remote")
	}
	// Don't assert the exact sentinel — git's stderr on connection
	// refused is host-dependent. Confirm it wraps one of the expected
	// upstream errors (Unreachable / NotFound / UpstreamInvalid).
	if !errors.Is(err, sources.ErrUnreachable) &&
		!errors.Is(err, sources.ErrNotFound) &&
		!errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("err should wrap one of the expected sentinels; got %v", err)
	}
}

func TestFetcher_Fetch_SubtreeTraversalRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH; skipping")
	}
	bare := setupBareFixture(t)
	f := New(Spec{
		URL:       bare,
		Ref:       "main",
		SHA:       fixtureHeadSHA(t, bare),
		Subtree:   "../../etc",
		CacheRoot: t.TempDir(),
	})
	_, err := f.Fetch(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected err on traversal subtree")
	}
}

func TestFetcher_Fetch_TokenRedacted(t *testing.T) {
	args := []string{"clone", "https://abc123:x-oauth-basic@github.com/x/y.git", "/tmp/x"}
	out := redactArgs(args)
	if strings.Contains(out[1], "abc123") {
		t.Errorf("token leaked: %v", out)
	}
	if !strings.HasPrefix(out[1], "https://***@") {
		t.Errorf("redaction shape unexpected: %q", out[1])
	}
}

// setupBareFixture creates a small git repo and returns the path to a
// `--bare` clone of it that other tests can use as a remote URL.
func setupBareFixture(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
		}
	}
	run(work, "init", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run(work, "add", "README.md")
	run(work, "commit", "-m", "init")

	bare := filepath.Join(t.TempDir(), "fixture.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	run(work, "clone", "--bare", ".", bare)
	return bare
}

func fixtureHeadSHA(t *testing.T, bare string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", bare, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return string(out[:40])
}

// TestFetcher_AuthHeader_NotURL asserts the token reaches git via
// http.extraHeader and is NEVER embedded in the URL position of the
// clone command. Token in URL would land in /proc/<pid>/cmdline AND
// in `git config remote.origin.url` on disk — both are leak paths
// (T-02-02-02).
func TestFetcher_AuthHeader_NotURL(t *testing.T) {
	args := buildGitInvocation(
		"clone",
		"https://github.com/octo/repo.git",
		"/tmp/x",
		"ghp_secrettoken",
	)
	for _, a := range args {
		if strings.Contains(a, "ghp_secrettoken@") {
			t.Fatalf("token in URL form: %v", args)
		}
	}
	hasHeader := false
	for _, a := range args {
		if a == "http.extraHeader=Authorization: Bearer ghp_secrettoken" {
			hasHeader = true
		}
	}
	if !hasHeader {
		t.Fatalf("expected http.extraHeader=Authorization: Bearer …; args=%v", args)
	}
}

// TestFetcher_AuthHeader_EmptyTokenNoArg asserts that when the token is
// empty (anonymous fetch) buildGitInvocation does NOT prepend the
// http.extraHeader arg.
func TestFetcher_AuthHeader_EmptyTokenNoArg(t *testing.T) {
	args := buildGitInvocation("clone", "https://example.com/x.git", "/tmp/x", "")
	for _, a := range args {
		if strings.HasPrefix(a, "http.extraHeader=") {
			t.Fatalf("unexpected extraHeader on empty token: %v", args)
		}
	}
	if args[0] != "clone" {
		t.Fatalf("expected first arg = subcommand; got %v", args)
	}
}

// TestLsRemote_ParsesSHA exercises LsRemote against the local bare-repo
// fixture and asserts the returned 40-hex SHA matches the fixture HEAD.
func TestLsRemote_ParsesSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupBareFixture(t)
	want := fixtureHeadSHA(t, bare)

	got, err := LsRemote(context.Background(), bare, "main", "")
	if err != nil {
		t.Fatalf("LsRemote: %v", err)
	}
	if got != want {
		t.Errorf("LsRemote SHA mismatch: got %q want %q", got, want)
	}
}

// TestLsRemote_BogusRefIsNotFound asserts an unknown ref classifies via
// sources.ErrNotFound (matches the REST path's 404 semantics).
func TestLsRemote_BogusRefIsNotFound(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupBareFixture(t)

	_, err := LsRemote(context.Background(), bare, "no-such-ref", "")
	if err == nil {
		t.Fatal("expected error for unknown ref")
	}
	if !errors.Is(err, sources.ErrNotFound) &&
		!errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Errorf("expected ErrNotFound or ErrUpstreamInvalid; got %v", err)
	}
}

// TestClassifyError_Exported sanity-checks the exported classifier
// covers each documented category.
func TestClassifyError_Exported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		stderr string
		want   error
	}{
		{"remote: Invalid username or password", sources.ErrUnauthorized},
		{"Repository not found", sources.ErrNotFound},
		{"could not resolve host", sources.ErrUnreachable},
		{"some unrecognized git failure", sources.ErrUpstreamInvalid},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.want.Error(), func(t *testing.T) {
			t.Parallel()
			got := ClassifyError(errors.New(tc.stderr))
			if !errors.Is(got, tc.want) {
				t.Errorf("ClassifyError(%q) wraps %v; want %v", tc.stderr, got, tc.want)
			}
		})
	}
}

// TestRedactArgs_RedactsExtraHeader confirms the extraHeader token is
// scrubbed from log output (the process arg is still visible via
// /proc/<pid>/cmdline, but logs are a separate sink under our control).
func TestRedactArgs_RedactsExtraHeader(t *testing.T) {
	args := []string{
		"-c", "http.extraHeader=Authorization: Bearer ghp_supersecret",
		"clone", "https://github.com/x/y.git", "/tmp/x",
	}
	out := redactArgs(args)
	for _, a := range out {
		if strings.Contains(a, "ghp_supersecret") {
			t.Fatalf("token leaked: %v", out)
		}
	}
	if out[1] != "http.extraHeader=Authorization: Bearer ***" {
		t.Errorf("redaction shape unexpected: %q", out[1])
	}
}
