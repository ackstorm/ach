// SPDX-License-Identifier: Apache-2.0

package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ackstorm/ach/internal/sources"
)

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
	full := buildGitInvocation("ls-remote", "--refs", url, ref, authToken)
	cmd := exec.CommandContext(ctx, "git", full...)
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
