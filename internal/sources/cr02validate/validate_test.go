// SPDX-License-Identifier: Apache-2.0

package cr02validate

import (
	"errors"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

func TestFlatIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
		wantOK      bool
	}{
		{"happy", "acme", true},
		{"empty", "", false},
		{"slash", "ac/me", false},
		{"question", "ac?me", false},
		{"fragment", "ac#me", false},
		{"backslash", "ac\\me", false},
		{"space", "ac me", false},
		{"tab", "ac\tme", false},
		{"cr", "ac\rme", false},
		{"lf", "ac\nme", false},
		{"unicode-fine", "ácmé", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := FlatIdentifier("field", tc.value)
			if tc.wantOK && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected err for %q", tc.value)
			}
			if !tc.wantOK && err != nil && !errors.Is(err, sources.ErrUpstreamInvalid) {
				t.Errorf("err should wrap ErrUpstreamInvalid: %v", err)
			}
		})
	}
}

func TestRefIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
		wantOK      bool
	}{
		{"plain", "main", true},
		{"feature-slash", "feature/branch", true},
		{"tag-with-dots", "v1.2.3", true},
		{"empty", "", false},
		{"newline", "main\n", false},
		{"question", "main?evil", false},
		{"fragment", "main#anchor", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := RefIdentifier(tc.value)
			if tc.wantOK && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected err for %q", tc.value)
			}
		})
	}
}

func TestRepoSlashIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		value             string
		allowMultiSegment bool
		wantOK            bool
	}{
		{"github-happy", "octocat/repo", false, true},
		{"gitlab-deep", "group/sub/project", true, true},
		{"github-deep-rejected", "a/b/c", false, false},
		{"empty-segment", "owner/", false, false},
		{"leading-slash", "/repo", false, false},
		{"newline", "owner/repo\n", false, false},
		{"question", "owner/repo?evil=1", false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := RepoSlashIdentifier("repo", tc.value, tc.allowMultiSegment)
			if tc.wantOK && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected err for %q", tc.value)
			}
		})
	}
}

func TestHostIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
		wantOK      bool
	}{
		{"empty-ok", "", true},
		{"saas", "gitlab.com", true},
		{"self-hosted", "gitlab.example.com", true},
		{"with-port", "gitlab.example.com:8080", true},
		{"with-path", "gitlab.example.com/foo", false},
		{"with-scheme-stripped-by-caller", "https://gitlab.example.com", false},
		{"newline", "gitlab.example.com\n", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := HostIdentifier(tc.value)
			if tc.wantOK && err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected err for %q", tc.value)
			}
		})
	}
}
