// SPDX-License-Identifier: Apache-2.0

package git

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
		"ghp_secrettoken",
		"https://github.com/octo/repo.git",
		"/tmp/x",
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
	args := buildGitInvocation("clone", "", "https://example.com/x.git", "/tmp/x")
	for _, a := range args {
		if strings.HasPrefix(a, "http.extraHeader=") {
			t.Fatalf("unexpected extraHeader on empty token: %v", args)
		}
	}
	// Subcommand follows the protocol-allow config pins (Task 8) but
	// before any further args.
	foundSubcommand := false
	for _, a := range args {
		if a == "clone" {
			foundSubcommand = true
			break
		}
	}
	if !foundSubcommand {
		t.Fatalf("expected subcommand in args; got %v", args)
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

// TestFetcher_PinsProtocolAllow asserts every git invocation carries
// the explicit protocol allow-list so an accidental file:// or ssh://
// URL cannot be honored. Defense in depth per PR #9 review finding #7.
func TestFetcher_PinsProtocolAllow(t *testing.T) {
	args := buildGitInvocation("clone", "tok", "https://example.com/x.git", "/tmp/x")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "protocol.allow=never") {
		t.Errorf("expected protocol.allow=never; got %q", joined)
	}
	if !strings.Contains(joined, "protocol.https.allow=always") {
		t.Errorf("expected protocol.https.allow=always; got %q", joined)
	}
}

// TestFetcher_TempDirCollisionResistance asserts that parallel Fetch
// calls against the same CacheRoot allocate distinct cloneDirs (PR #9
// follow-up review finding #6: defense against a zeroed-nonce
// collision when crypto/rand silently fails). Uses real Fetch calls
// against a local bare-repo fixture in parallel; if both calls shared
// a cloneDir name the second clone would race on EEXIST or overwrite.
func TestFetcher_TempDirCollisionResistance(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	bare := setupBareFixture(t)
	wantSHA := fixtureHeadSHA(t, bare)
	cacheRoot := t.TempDir()

	const parallel = 8
	errs := make(chan error, parallel)
	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := New(Spec{
				URL:       bare,
				Ref:       "main",
				SHA:       wantSHA,
				CacheRoot: cacheRoot,
			})
			res, err := f.Fetch(context.Background(), Request{})
			if err != nil {
				errs <- err
				return
			}
			_ = res.Body.Close()
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("parallel Fetch failed: %v", err)
		}
	}
}

// TestLsRemote_RespectsInnerTimeout asserts that even with a long
// (or absent) caller ctx, LsRemote bounds the subprocess via its own
// internal deadline so a stalled upstream cannot hang the reconciler
// indefinitely. Pins PR #9 follow-up review finding #3.
//
// NOTE: post Task 8 (protocol allow-list pinning), the http:// URL is
// rejected at git's URL-parse step before any TCP connect; this test
// now also exercises that fast-fail path. The 30s deadline still
// guards genuine HTTPS-handshake-stall scenarios; a dedicated TLS-
// fixture test would be needed to exercise the deadline directly.
func TestLsRemote_RespectsInnerTimeout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	// Hangs forever — accepts TCP, never sends a byte. git ls-remote
	// would block on GIT_HTTP_LOW_SPEED_TIME (60s) without an inner
	// deadline.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection; never reply. Closed when listener closes.
			_ = c
		}
	}()
	url := "http://" + ln.Addr().String() + "/x.git"

	start := time.Now()
	_, err = LsRemote(context.Background(), url, "main", "")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from stalled upstream")
	}
	// Inner deadline should be ~30s. Give a generous wall-clock cap
	// to avoid flake on slow CI.
	if elapsed > 45*time.Second {
		t.Errorf("LsRemote took %v; inner deadline should fire well before 45s", elapsed)
	}
}
