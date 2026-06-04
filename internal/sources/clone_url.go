// SPDX-License-Identifier: Apache-2.0

package sources

import (
	"fmt"
	neturl "net/url"
	"strings"
)

// GitLabCloneURL builds the canonical https clone URL for a GitLab project.
// host accepts a bare host or an https://-prefixed form (normalized via
// NormalizeGitLabHost); empty defaults to gitlab.com. Single source of
// truth for both the gitlab source fetcher and the marketplace dispatch
// path so the SAME GitLabSource always yields the SAME clone URL.
func GitLabCloneURL(host, project string) string {
	h := NormalizeGitLabHost(host)
	if h == "" {
		h = "gitlab.com"
	}
	return fmt.Sprintf("https://%s/%s.git", h, project)
}

// GitHubCloneURL builds the canonical https clone URL for a github.com
// repo. repo is the "<owner>/<name>" identifier (callers validate the
// single-slash shape before calling).
func GitHubCloneURL(repo string) string {
	return fmt.Sprintf("https://github.com/%s.git", repo)
}

// BitbucketCloneURL builds the canonical https clone URL for a Bitbucket
// Cloud repo.
func BitbucketCloneURL(workspace, repo string) string {
	return fmt.Sprintf("https://bitbucket.org/%s/%s.git", workspace, repo)
}

// CanonicalCloneURL normalizes a raw git clone URL (as carried verbatim by
// a marketplace git-subdir / url plugin entry) to canonical https form and
// returns the bare lowercased host[:port] for auth-scheme decisions.
//
//   - A scheme-less input ("git.example.com/g/p.git") defaults to https so
//     url.Parse populates Host instead of treating the whole string as a path.
//   - Non-https schemes (http://, ssh://, git://, file://) and empty hosts
//     are rejected with ErrUpstreamInvalid. The operator only ever clones
//     over https — the internal/sources/git protocol pins block the rest
//     anyway; rejecting here yields a clear config error instead of an
//     opaque downstream git failure.
//   - The host is lowercased; the path case is preserved (git paths are
//     case-sensitive on some hosts); any trailing slash is stripped.
func CanonicalCloneURL(raw string) (canonical, host string, err error) {
	s := strings.TrimSpace(raw)
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "https://"):
		// ok
	case strings.HasPrefix(low, "http://"):
		return "", "", fmt.Errorf("clone url %q: only https is supported: %w", raw, ErrUpstreamInvalid)
	case strings.Contains(s, "://"):
		return "", "", fmt.Errorf("clone url %q: only https is supported: %w", raw, ErrUpstreamInvalid)
	default:
		s = "https://" + s
	}
	u, perr := neturl.Parse(s)
	if perr != nil {
		return "", "", fmt.Errorf("clone url %q: %v: %w", raw, perr, ErrUpstreamInvalid)
	}
	if u.Scheme != "https" || u.Host == "" {
		return "", "", fmt.Errorf("clone url %q: must be https://<host>/...: %w", raw, ErrUpstreamInvalid)
	}
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/"), u.Host, nil
}
