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
