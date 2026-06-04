// SPDX-License-Identifier: Apache-2.0

package git

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ackstorm/ach/internal/sources"
)

// AuthScheme selects how the upstream credential is conveyed in the git
// HTTP Authorization header. The zero value is AuthBearer, so any caller
// that does not set it keeps the original "Bearer <token>" behavior.
type AuthScheme int

const (
	// AuthBearer sends "Authorization: Bearer <token>". Honored by
	// github.com, bitbucket.org, and gitlab.com (>=15.x).
	AuthBearer AuthScheme = iota
	// AuthBasicOAuth2 sends Authorization: Basic base64("oauth2:"+token).
	// GitLab's documented PAT/Group/Project-token method over git-http and
	// the ONLY scheme self-hosted GitLab instances configured without
	// Bearer support honor (verified vs git.ackstorm.com: Bearer -> 401,
	// Basic oauth2:<token> -> 200).
	AuthBasicOAuth2
)

// authHeaderValue formats the HTTP Authorization header value for token
// under scheme. See AuthScheme for the per-provider rationale.
func authHeaderValue(token string, scheme AuthScheme) string {
	switch scheme {
	case AuthBasicOAuth2:
		return "Basic " + base64.StdEncoding.EncodeToString([]byte("oauth2:"+token))
	default:
		return "Bearer " + token
	}
}

// Spec configures a single git fetch. Constructed by the
// PluginMarketplace reconciler from a ClaudeCodeMarketplaceSource entry.
type Spec struct {
	// URL is the git remote (https or ssh — only https is exercised by
	// ACH today; ssh paths require host SSH keys mounted into the
	// operator pod and is out of scope for v1alpha1).
	URL string

	// Ref is the branch/tag to clone shallow. Required.
	Ref string

	// SHA is the pinned commit. After the shallow clone, the fetcher
	// does `git fetch origin <sha>` then `git checkout <sha>` to
	// guarantee reproducibility regardless of how far Ref has moved.
	SHA string

	// Subtree, when non-empty, narrows the produced tarball to a single
	// subdirectory of the worktree (the `path/` of a git-subdir entry).
	// Cleaned + slash-prefixed before use. Empty → whole worktree.
	Subtree string

	// Token, when non-empty, is sent to git via
	//   git -c http.extraHeader="Authorization: Bearer <token>" <subcommand>
	// so the credential never lands in the URL position (which would
	// leak via /proc/<pid>/cmdline AND persist on disk in
	// `git config remote.origin.url`). ssh:// URLs are left unchanged;
	// auth-via-SSH-key is out of scope for v1alpha1.
	Token string

	// AuthScheme selects the HTTP Authorization header form for Token. The
	// zero value (AuthBearer) preserves the legacy "Bearer <token>"
	// behavior; GitLab fetches set AuthBasicOAuth2. Ignored when Token == "".
	AuthScheme AuthScheme

	// CacheRoot is the operator's cache PVC root. The fetcher creates
	// an ephemeral clone under <CacheRoot>/.tmp/git-<rand>/ and removes
	// it on completion. Empty defaults to os.TempDir() — fine for tests.
	CacheRoot string

	// MaxCloneBytes caps the on-disk size of the clone. Zero defaults to
	// gitDefaultMaxCloneBytes. Exceeded → ErrCloneTooLarge (wraps
	// sources.ErrUpstreamInvalid so the reconciler maps to
	// ReasonUpstreamInvalid).
	MaxCloneBytes int64
}

// Request is currently empty — kept so the signature matches the
// internal/sources contract pattern and so future fields (e.g.
// PriorRev for short-circuiting) can land without API churn.
type Request struct{}

// Result mirrors internal/sources.FetchResult shape.
type Result struct {
	Body        io.ReadCloser
	UpstreamRev string
}

// gitDefaultMaxCloneBytes is the on-disk size cap when Spec.MaxCloneBytes
// is zero. 512 MiB covers the long tail of Artifact directory-scope
// targets that pull from larger upstream repos (e.g. openclaw/openclaw
// at ~261 MiB depth-1) while still bounding a runaway clone. Typical
// claude-plugin Plugin/Prompt repos are <10 MiB and well under the cap.
const gitDefaultMaxCloneBytes = 512 << 20

// gitCloneTimeout is the wall-clock bound on a single fetch operation.
// Includes clone + checkout + tar. 5 minutes covers slow upstreams
// without letting a hung clone stall the reconciler indefinitely.
const gitCloneTimeout = 5 * time.Minute

// ErrCloneTooLarge surfaces when the on-disk clone exceeds Spec.MaxCloneBytes.
var ErrCloneTooLarge = errors.New("git clone exceeded size cap")

// sha40Re validates a full 40-hex commit SHA. Short SHAs are rejected
// because the reproducibility guarantee depends on the full hash.
var sha40Re = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Fetcher executes one git fetch end-to-end. Constructed via New.
type Fetcher struct{ spec Spec }

// New constructs a Fetcher. Validation of Spec.URL is intentionally
// thin — git itself surfaces malformed-URL errors on clone, and we
// want the same error surface for "no such repo" and "no such SHA"
// (the user-facing remediation is identical: fix the marketplace.json
// entry). SHA shape, however, IS validated up-front because a non-hex
// value would confuse the `git fetch origin <sha>` plumbing.
func New(spec Spec) *Fetcher {
	return &Fetcher{spec: spec}
}

// Fetch clones the remote at Spec.Ref, fetches + checks out Spec.SHA,
// and returns a gzipped tar of the worktree (or Spec.Subtree). The
// returned Body MUST be closed by the caller (which also triggers
// removal of the temporary clone directory).
func (f *Fetcher) Fetch(ctx context.Context, _ Request) (*Result, error) {
	spec := f.spec
	if spec.URL == "" {
		return nil, fmt.Errorf("git: spec.URL required: %w", sources.ErrUpstreamInvalid)
	}
	if spec.Ref == "" {
		return nil, fmt.Errorf("git: spec.Ref required: %w", sources.ErrUpstreamInvalid)
	}
	if !sha40Re.MatchString(spec.SHA) {
		return nil, fmt.Errorf("git: spec.SHA %q not 40-hex: %w", spec.SHA, sources.ErrUpstreamInvalid)
	}
	maxBytes := spec.MaxCloneBytes
	if maxBytes <= 0 {
		maxBytes = gitDefaultMaxCloneBytes
	}

	// Temp dir under CacheRoot/.tmp/git-<rand>/ so the clone shares the
	// same filesystem as the eventual rename(2) target (avoids EXDEV).
	tmpParent := spec.CacheRoot
	if tmpParent == "" {
		tmpParent = os.TempDir()
	} else {
		tmpParent = filepath.Join(tmpParent, ".tmp")
	}
	if err := os.MkdirAll(tmpParent, 0o755); err != nil {
		return nil, fmt.Errorf("git: mkdir tmp parent: %w", err)
	}
	// os.MkdirTemp uses crypto/rand internally AND returns an error
	// when allocation fails — earlier code did rand.Read with a
	// silently-discarded error, which on rand failure (rare but
	// possible on minimal containers with seccomp blocking getrandom
	// and no /dev/urandom fallback) would produce a predictable
	// .tmp/git-0000000000000000 path. Symlink-race vector on shared
	// cache PVCs. See PR #9 follow-up review finding #6.
	cloneDir, err := os.MkdirTemp(tmpParent, "git-*")
	if err != nil {
		return nil, fmt.Errorf("git: mkdir clone dir: %w", err)
	}

	cleanupOnErr := func() { _ = os.RemoveAll(cloneDir) }

	cloneURL := spec.URL

	ctx, cancel := context.WithTimeout(ctx, gitCloneTimeout)
	defer cancel()

	// git clone --depth=1 --branch=<ref> <url> <dst>
	// Auth (when spec.Token != "") rides on -c http.extraHeader= prepended
	// inside runGit; never URL-injected.
	if err := runGit(ctx, cloneDir, spec.Token, spec.AuthScheme, "clone", "--depth=1", "--branch="+spec.Ref, "--no-tags", "--single-branch", cloneURL, cloneDir); err != nil {
		cleanupOnErr()
		return nil, ClassifyError(err)
	}
	// git fetch origin <sha> (depth=1 may not include the pin; this widens just enough).
	if err := runGit(ctx, cloneDir, spec.Token, spec.AuthScheme, "fetch", "--depth=1", "origin", spec.SHA); err != nil {
		cleanupOnErr()
		return nil, ClassifyError(err)
	}
	// git checkout <sha> — purely local, no remote interaction, no auth needed.
	if err := runGit(ctx, cloneDir, "", AuthBearer, "checkout", "--detach", spec.SHA); err != nil {
		cleanupOnErr()
		return nil, ClassifyError(err)
	}

	// On-disk size cap.
	var total int64
	err = filepath.WalkDir(cloneDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
			if total > maxBytes {
				return ErrCloneTooLarge
			}
		}
		return nil
	})
	if err != nil {
		cleanupOnErr()
		if errors.Is(err, ErrCloneTooLarge) {
			return nil, fmt.Errorf("git: %w (cap %d): %w", ErrCloneTooLarge, maxBytes, sources.ErrUpstreamInvalid)
		}
		return nil, fmt.Errorf("git: walk clone dir: %w", err)
	}

	// Tar the worktree (or subtree) in memory. claude-plugin repos are
	// small — streaming would add complexity for no benefit. If a real
	// marketplace ships a 100MB plugin, the MaxCloneBytes cap catches it
	// before we get here.
	body, err := tarSubtree(cloneDir, spec.Subtree)
	if err != nil {
		cleanupOnErr()
		return nil, fmt.Errorf("git: tar: %w", err)
	}

	// Wrap the byte buffer in a Closer that removes the clone on close.
	rc := &cloneReadCloser{
		Reader:   bytes.NewReader(body),
		cloneDir: cloneDir,
	}
	return &Result{
		Body:        rc,
		UpstreamRev: spec.SHA,
	}, nil
}

// buildGitInvocation returns the full args slice for a git subcommand.
// token, when non-empty, is prepended as
//
//	-c http.extraHeader=Authorization: Bearer <token>
//
// so it never appears in the URL position of any arg (the URL form
// would persist on disk via `git config remote.origin.url` AND remain
// visible in /proc/<pid>/cmdline). The extraHeader value itself is
// also in cmdline for the duration of the subprocess, which is
// unavoidable without GIT_ASKPASS plumbing — but it is colocated in
// one auditable arg slot and is redacted by redactArgs in any logs.
//
// token is positional + mandatory so callers cannot accidentally
// forget it (which under the previous variadic-last convention would
// silently put a real arg in the token slot — see PR #9 follow-up
// review finding #4).
func buildGitInvocation(subcommand, token string, scheme AuthScheme, args ...string) []string {
	// Pin allowed wire protocols. v1alpha1 only ever issues https://
	// clone URLs in production; the operator never accepts user-
	// supplied URLs at this layer. Block everything else (ssh://, git://,
	// ftp://, ...) so an accidental future code path can't honor an
	// attacker-controlled URL. See PR #9 follow-up review finding #7.
	//
	// protocol.file.allow=user permits file:// / local paths when they
	// originate from a top-level command argument (the operator's case
	// AND the test fixture case) but blocks them when injected via a
	// submodule URL (the threat that Git's CVE-2022-39253 closed).
	configPins := []string{
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "protocol.file.allow=user",
	}

	if token == "" {
		out := append([]string(nil), configPins...)
		out = append(out, subcommand)
		return append(out, args...)
	}
	prefix := append([]string(nil), configPins...)
	prefix = append(prefix,
		"-c", "http.extraHeader=Authorization: "+authHeaderValue(token, scheme),
		subcommand,
	)
	return append(prefix, args...)
}

// runGit runs a git subcommand without --recurse-submodules (security:
// arbitrary git submodule URLs in a marketplace plugin would be a
// remote-fetch primitive). Inherits ctx for the wall-clock cap.
//
// token, when non-empty, lands as -c http.extraHeader=Authorization:
// Bearer <token> via buildGitInvocation. Pass "" for purely local
// subcommands that don't touch the remote (e.g. checkout).
func runGit(ctx context.Context, workdir, token string, scheme AuthScheme, subcommand string, args ...string) error {
	full := buildGitInvocation(subcommand, token, scheme, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_HTTP_LOW_SPEED_LIMIT=1000",
		"GIT_HTTP_LOW_SPEED_TIME=60",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %v: %s", redactArgs(full), err, truncateBytes(out, 512))
	}
	return nil
}

// ClassifyError maps git subprocess failures to wrapped sources sentinels.
// Exported so per-provider outer fetchers (internal/sources/{github,
// gitlab,bitbucket}) reuse the same regex set after composing LsRemote
// + Fetcher.Fetch.
//
// The classification is intentionally coarse — git's exit codes don't
// distinguish 404 vs 401 vs DNS-failure cleanly, and the upstream
// status.message surfaces the underlying git stderr anyway.
func ClassifyError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Authentication failed"),
		strings.Contains(msg, "could not read Username"),
		strings.Contains(msg, "remote: Invalid username or password"):
		return fmt.Errorf("git: %w: %v", sources.ErrUnauthorized, err)
	case strings.Contains(msg, "Repository not found"),
		strings.Contains(msg, "does not appear to be a git repository"):
		return fmt.Errorf("git: %w: %v", sources.ErrNotFound, err)
	case strings.Contains(msg, "could not resolve host"),
		strings.Contains(msg, "Connection timed out"),
		strings.Contains(msg, "Connection refused"):
		return fmt.Errorf("git: %w: %v", sources.ErrUnreachable, err)
	case strings.Contains(msg, "context deadline exceeded"):
		return fmt.Errorf("git: %w: %v", sources.ErrUnreachable, err)
	default:
		return fmt.Errorf("git: %w: %v", sources.ErrUpstreamInvalid, err)
	}
}

// tarSubtree gzip-tars the contents of root (or root/subtree if non-empty),
// stripping the root prefix from entry names so the resulting archive
// looks like the worktree was at /.
func tarSubtree(root, subtree string) ([]byte, error) {
	start := root
	relStrip := root
	if subtree != "" {
		// Defense in depth: tarSubtree is called with a parser-validated
		// local-path / git-subdir path, but reject traversal here too.
		clean := filepath.Clean(subtree)
		if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
			return nil, fmt.Errorf("subtree %q escapes root", subtree)
		}
		start = filepath.Join(root, clean)
		info, err := os.Stat(start)
		if err != nil {
			return nil, fmt.Errorf("subtree %q: %w", subtree, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("subtree %q: not a directory", subtree)
		}
		relStrip = start
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && filepath.Base(path) == ".git" {
			return fs.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.Mode().IsRegular() && !d.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(relStrip, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(relPath)
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, file)
			_ = file.Close()
			if copyErr != nil {
				return copyErr
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// cloneReadCloser wraps a *bytes.Reader and removes the temp clone
// directory on Close.
type cloneReadCloser struct {
	*bytes.Reader
	cloneDir string
}

func (c *cloneReadCloser) Close() error {
	return os.RemoveAll(c.cloneDir)
}

// redactArgs strips embedded tokens from git subcommand args before
// logging. Covers two leak shapes:
//
//   - https://<token>@host/... — the legacy URL-injection form. Should
//     not appear in fresh code paths after the buildGitInvocation swap,
//     but logged-arg redaction stays defensive so an accidental URL
//     credential doesn't reach disk.
//   - http.extraHeader=Authorization: Bearer <token> — the current
//     auth-conveyance form. Token value scrubbed; key name preserved.
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		switch {
		case strings.HasPrefix(a, "http.extraHeader=Authorization: Bearer "):
			out[i] = "http.extraHeader=Authorization: Bearer ***"
		case strings.HasPrefix(a, "http.extraHeader=Authorization: Basic "):
			out[i] = "http.extraHeader=Authorization: Basic ***"
		case strings.HasPrefix(a, "https://") && strings.Contains(a, "@"):
			at := strings.LastIndex(a, "@")
			out[i] = "https://***" + a[at:]
		default:
			out[i] = a
		}
	}
	return out
}

func truncateBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return append(b[:n:n], []byte("…")...)
}
