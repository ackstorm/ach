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
// it points at. `git ls-remote --refs <url> <ref>` outputs one or more
// lines of the form `<sha>\trefs/heads/<branch>` (or refs/tags/…);
// LsRemote returns the first matching line's SHA.
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

	full := buildGitInvocation("ls-remote", authToken, "--refs", url, ref)
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
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", fmt.Errorf("ls-remote %s %s: empty output: %w",
			url, ref, sources.ErrNotFound)
	}
	tabIdx := strings.IndexByte(line, '\t')
	if tabIdx != 40 {
		return "", fmt.Errorf("ls-remote %s %s: malformed line %q: %w",
			url, ref, line, sources.ErrUpstreamInvalid)
	}
	return line[:40], nil
}
