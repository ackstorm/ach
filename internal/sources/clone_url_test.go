// SPDX-License-Identifier: Apache-2.0

package sources_test

import (
	"errors"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

func TestGitLabCloneURL(t *testing.T) {
	cases := []struct{ host, project, want string }{
		{"", "g/p", "https://gitlab.com/g/p.git"},
		{"git.example.com", "g/p", "https://git.example.com/g/p.git"},
		{"https://git.example.com", "g/p", "https://git.example.com/g/p.git"},
		{"https://git.example.com/", "g/p", "https://git.example.com/g/p.git"},
	}
	for _, tc := range cases {
		if got := sources.GitLabCloneURL(tc.host, tc.project); got != tc.want {
			t.Errorf("GitLabCloneURL(%q,%q) = %q; want %q", tc.host, tc.project, got, tc.want)
		}
	}
}

func TestGitHubCloneURL(t *testing.T) {
	if got := sources.GitHubCloneURL("octo/repo"); got != "https://github.com/octo/repo.git" {
		t.Errorf("GitHubCloneURL = %q", got)
	}
}

func TestBitbucketCloneURL(t *testing.T) {
	if got := sources.BitbucketCloneURL("ws", "repo"); got != "https://bitbucket.org/ws/repo.git" {
		t.Errorf("BitbucketCloneURL = %q", got)
	}
}

func TestCanonicalCloneURL_OK(t *testing.T) {
	cases := []struct{ in, wantURL, wantHost string }{
		{"https://github.com/o/r.git", "https://github.com/o/r.git", "github.com"},
		{"https://git.example.com/g/p.git/", "https://git.example.com/g/p.git", "git.example.com"},
		{"git.example.com/g/p.git", "https://git.example.com/g/p.git", "git.example.com"},
		{"https://Git.Example.COM/g/p.git", "https://git.example.com/g/p.git", "git.example.com"},
	}
	for _, tc := range cases {
		gotURL, gotHost, err := sources.CanonicalCloneURL(tc.in)
		if err != nil {
			t.Fatalf("CanonicalCloneURL(%q) unexpected err: %v", tc.in, err)
		}
		if gotURL != tc.wantURL || gotHost != tc.wantHost {
			t.Errorf("CanonicalCloneURL(%q) = (%q,%q); want (%q,%q)", tc.in, gotURL, gotHost, tc.wantURL, tc.wantHost)
		}
	}
}

func TestCanonicalCloneURL_Rejects(t *testing.T) {
	for _, in := range []string{"http://git.example.com/g/p.git", "ssh://git@git.example.com/g/p.git", "git://x/y.git", ""} {
		if _, _, err := sources.CanonicalCloneURL(in); !errors.Is(err, sources.ErrUpstreamInvalid) {
			t.Errorf("CanonicalCloneURL(%q): want ErrUpstreamInvalid, got %v", in, err)
		}
	}
}
