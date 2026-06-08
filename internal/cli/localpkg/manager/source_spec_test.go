// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"testing"

	"github.com/ackstorm/ach/internal/contentkit"
	"github.com/ackstorm/ach/internal/gitfetch"
)

func TestBuildEntrySpec(t *testing.T) {
	t.Parallel()

	const (
		mktURL   = "https://git.example.com/org/marketplace.git"
		mktRef   = "main"
		token    = "tok-123"
		bearerSc = gitfetch.AuthBearer
	)

	tests := []struct {
		name    string
		src     contentkit.ClaudeCodeMarketplaceSource
		want    gitfetch.Spec
		wantErr bool
	}{
		{
			name: "git-subdir with all fields",
			src: contentkit.ClaudeCodeMarketplaceSource{
				Kind: "git-subdir",
				URL:  "https://github.com/org/plugin.git",
				Path: "subdir/plugin",
				Ref:  "v2",
				SHA:  "abcd1234abcd1234abcd1234abcd1234abcd1234",
			},
			want: gitfetch.Spec{
				URL:        "https://github.com/org/plugin.git",
				Ref:        "v2",
				SHA:        "abcd1234abcd1234abcd1234abcd1234abcd1234",
				Subtree:    "subdir/plugin",
				Token:      token,
				AuthScheme: bearerSc,
			},
		},
		{
			name: "git-subdir defaults ref to main",
			src: contentkit.ClaudeCodeMarketplaceSource{
				Kind: "git-subdir",
				URL:  "https://github.com/org/plugin.git",
				Path: "plugin",
			},
			want: gitfetch.Spec{
				URL:        "https://github.com/org/plugin.git",
				Ref:        "main",
				SHA:        "",
				Subtree:    "plugin",
				Token:      token,
				AuthScheme: bearerSc,
			},
		},
		{
			name: "url kind",
			src: contentkit.ClaudeCodeMarketplaceSource{
				Kind: "url",
				URL:  "https://git.example.com/repo.git",
				Path: "tools",
				Ref:  "v1",
				SHA:  "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			},
			want: gitfetch.Spec{
				URL:        "https://git.example.com/repo.git",
				Ref:        "v1",
				SHA:        "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				Subtree:    "tools",
				Token:      token,
				AuthScheme: bearerSc,
			},
		},
		{
			name: "url kind without path",
			src: contentkit.ClaudeCodeMarketplaceSource{
				Kind: "url",
				URL:  "https://git.example.com/repo.git",
			},
			want: gitfetch.Spec{
				URL:        "https://git.example.com/repo.git",
				Ref:        "main",
				SHA:        "",
				Subtree:    "",
				Token:      token,
				AuthScheme: bearerSc,
			},
		},
		{
			name: "github kind",
			src: contentkit.ClaudeCodeMarketplaceSource{
				Kind: "github",
				Repo: "owner/my-plugin",
				Ref:  "v3",
				SHA:  "cafebabecafebabecafebabecafebabecafebabe",
			},
			want: gitfetch.Spec{
				URL:        "https://github.com/owner/my-plugin.git",
				Ref:        "v3",
				SHA:        "cafebabecafebabecafebabecafebabecafebabe",
				Subtree:    "",
				Token:      token,
				AuthScheme: bearerSc,
			},
		},
		{
			name: "github kind defaults ref to main",
			src: contentkit.ClaudeCodeMarketplaceSource{
				Kind: "github",
				Repo: "owner/my-plugin",
			},
			want: gitfetch.Spec{
				URL:        "https://github.com/owner/my-plugin.git",
				Ref:        "main",
				SHA:        "",
				Subtree:    "",
				Token:      token,
				AuthScheme: bearerSc,
			},
		},
		{
			name: "local-path uses marketplace own repo",
			src: contentkit.ClaudeCodeMarketplaceSource{
				Kind: "local-path",
				Path: "plugins/my-plugin",
			},
			want: gitfetch.Spec{
				URL:        mktURL,
				Ref:        mktRef,
				SHA:        "", // resolved by Resolve
				Subtree:    "plugins/my-plugin",
				Token:      token,
				AuthScheme: bearerSc,
			},
		},
		{
			name:    "empty kind returns error",
			src:     contentkit.ClaudeCodeMarketplaceSource{Kind: ""},
			wantErr: true,
		},
		{
			name:    "unknown kind returns error",
			src:     contentkit.ClaudeCodeMarketplaceSource{Kind: "npm"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := BuildEntrySpec(tc.src, mktURL, mktRef, token, bearerSc)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("BuildEntrySpec(%q): expected error, got nil", tc.src.Kind)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildEntrySpec(%q): unexpected error: %v", tc.src.Kind, err)
			}
			if got.URL != tc.want.URL {
				t.Errorf("URL: got %q, want %q", got.URL, tc.want.URL)
			}
			if got.Ref != tc.want.Ref {
				t.Errorf("Ref: got %q, want %q", got.Ref, tc.want.Ref)
			}
			if got.SHA != tc.want.SHA {
				t.Errorf("SHA: got %q, want %q", got.SHA, tc.want.SHA)
			}
			if got.Subtree != tc.want.Subtree {
				t.Errorf("Subtree: got %q, want %q", got.Subtree, tc.want.Subtree)
			}
			if got.Token != tc.want.Token {
				t.Errorf("Token: got %q, want %q", got.Token, tc.want.Token)
			}
			if got.AuthScheme != tc.want.AuthScheme {
				t.Errorf("AuthScheme: got %v, want %v", got.AuthScheme, tc.want.AuthScheme)
			}
		})
	}
}

func TestDefaultRef(t *testing.T) {
	t.Parallel()
	if got := defaultRef(""); got != "main" {
		t.Errorf("defaultRef(%q) = %q; want %q", "", got, "main")
	}
	if got := defaultRef("v2"); got != "v2" {
		t.Errorf("defaultRef(%q) = %q; want %q", "v2", got, "v2")
	}
}

func TestSchemeFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want gitfetch.AuthScheme
	}{
		{"basic-oauth2", gitfetch.AuthBasicOAuth2},
		{"bearer", gitfetch.AuthBearer},
		{"", gitfetch.AuthBearer},
		{"anything-else", gitfetch.AuthBearer},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := schemeFor(tc.in)
			if got != tc.want {
				t.Errorf("schemeFor(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}
