// SPDX-License-Identifier: Apache-2.0

package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ackstorm/ach/internal/sources"
)

// lsRemoteTimeout bounds an individual ls-remote subprocess. The
// caller's ctx may not carry a deadline (some controllers pass
// context.Background()); this internal wrapper guarantees an upper
// bound so a stalled upstream cannot block the reconciler. 30s is
// generous for a one-round-trip protocol exchange.
const lsRemoteTimeout = 30 * time.Second

// LsRemote resolves ref against url and returns the 40-hex commit SHA
// it points at.
//
// Ref scoping: a bare ref like "main" is intentionally matched against
// refs/heads/<ref> AND refs/tags/<ref> only, with branch preferred when
// both exist. `git ls-remote --refs <url> main` (no namespace prefix)
// matches every ref whose path component is "main" — including
// `refs/heads/daisy/caffeinate/main` and friends — and the caller
// would then pick an arbitrary alphabetically-first match. That
// matters in real repos: a Stage-2 fetch landed on
// `daisy/caffeinate/main` instead of the actual `main` branch and
// silently served stale content. Explicit scoping eliminates the
// ambiguity. Callers passing an already-scoped ref ("refs/heads/x",
// "refs/tags/v1", "refs/pull/123/head") get a one-shot lookup with no
// namespace expansion.
//
// authToken, when non-empty, is sent via
// `-c http.extraHeader=Authorization: Bearer <token>` so it never
// appears in the URL (URL injection leaks via /proc/<pid>/cmdline AND
// via local git config — both threats are closed by construction).
//
// Errors are classified via [ClassifyError] so the caller observes the
// same internal/sources sentinel set as a Fetch failure does.
func LsRemote(ctx context.Context, url, ref, authToken string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, lsRemoteTimeout)
	defer cancel()

	// Build the namespace-scoped query. A pre-scoped ref ("refs/...")
	// is passed through unchanged. A bare ref ("main", "v1.5.5") is
	// expanded to refs/heads/<ref> + refs/tags/<ref> so ls-remote
	// returns AT MOST two lines and we can disambiguate.
	var patterns []string
	if strings.HasPrefix(ref, "refs/") {
		patterns = []string{ref}
	} else {
		patterns = []string{"refs/heads/" + ref, "refs/tags/" + ref}
	}

	full := buildGitInvocation("ls-remote", authToken, "--refs", url)
	full = append(full, patterns...)
	cmd := exec.CommandContext(ctx, "git", full...)
	// git ls-remote forks git-remote-http(s) which inherits the
	// stdout/stderr pipes. When the parent git is SIGKILLed by
	// CommandContext on ctx expiry, the helper survives (orphaned to
	// init) and keeps the pipes open — CombinedOutput would block
	// waiting for EOF that never arrives. WaitDelay closes the pipes
	// from our side a beat after the process exits.
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_HTTP_LOW_SPEED_LIMIT=1000",
		"GIT_HTTP_LOW_SPEED_TIME=60",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", ClassifyError(fmt.Errorf("ls-remote %s %s: %v: %s",
			url, ref, err, truncateBytes(out, 512)))
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "", fmt.Errorf("ls-remote %s %s: empty output: %w",
			url, ref, sources.ErrNotFound)
	}
	// Prefer the branch hit when both branch and tag with the same
	// name exist (rare in practice but legal in git). Two-line scan
	// rather than first-line-wins.
	var branchSHA, tagSHA string
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 42 || line[40] != '\t' {
			return "", fmt.Errorf("ls-remote %s %s: malformed line %q: %w",
				url, ref, line, sources.ErrUpstreamInvalid)
		}
		sha := line[:40]
		refPath := line[41:]
		switch {
		case strings.HasPrefix(refPath, "refs/heads/"):
			branchSHA = sha
		case strings.HasPrefix(refPath, "refs/tags/"):
			tagSHA = sha
		default:
			// Non-branch, non-tag ref (e.g. refs/pull/N/head when the
			// caller passed a fully-qualified ref). Treat the single
			// match as authoritative.
			return sha, nil
		}
	}
	if branchSHA != "" {
		return branchSHA, nil
	}
	if tagSHA != "" {
		return tagSHA, nil
	}
	return "", fmt.Errorf("ls-remote %s %s: no branch or tag match: %w",
		url, ref, sources.ErrNotFound)
}
