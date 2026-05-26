// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"io"
	"regexp"
	"strings"
	"testing"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	sourcesgit "github.com/ackstorm/ach/internal/sources/git"
)

// fakeDispatchGitFetcher records the Spec it was constructed with and returns
// a canned body / SHA. Tests swap newGitFetcherFn for a closure that
// returns this fake. Named with "Dispatch" prefix to avoid clashing with
// the envtest-side fakeGitFetcher type in the same package.
type fakeDispatchGitFetcher struct {
	body string
	rev  string
	err  error
}

func (f *fakeDispatchGitFetcher) Fetch(_ context.Context, _ sourcesgit.Request) (*sourcesgit.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &sourcesgit.Result{
		Body:        io.NopCloser(strings.NewReader(f.body)),
		UpstreamRev: f.rev,
	}, nil
}

func TestDispatchMarketplacePlugin_GitSubdir(t *testing.T) {
	var captured sourcesgit.Spec
	orig := newGitFetcherFn
	defer func() { newGitFetcherFn = orig }()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		captured = spec
		return &fakeDispatchGitFetcher{body: "tarball-bytes", rev: spec.SHA}
	}
	entry := ClaudeCodeMarketplacePlugin{
		Name: "x",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "git-subdir",
			URL:  "https://github.com/o/r.git",
			Path: "plugins/x",
			Ref:  "v1",
			SHA:  "0123456789abcdef0123456789abcdef01234567",
		},
	}
	mp := &achv1alpha1.PluginMarketplace{}
	body, rev, err := dispatchMarketplacePlugin(context.Background(), mp, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	defer body.Close()
	if rev != entry.Source.SHA {
		t.Errorf("rev = %q", rev)
	}
	if captured.Subtree != "plugins/x" {
		t.Errorf("Subtree = %q", captured.Subtree)
	}
	if captured.URL != entry.Source.URL {
		t.Errorf("URL = %q", captured.URL)
	}
}

func TestDispatchMarketplacePlugin_LocalPathResolvesMarketplaceRepo(t *testing.T) {
	var captured sourcesgit.Spec
	orig := newGitFetcherFn
	defer func() { newGitFetcherFn = orig }()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		captured = spec
		return &fakeDispatchGitFetcher{body: "subtree-bytes", rev: spec.SHA}
	}
	origResolve := newResolveHeadSHAFn
	defer func() { newResolveHeadSHAFn = origResolve }()
	newResolveHeadSHAFn = func(_ context.Context, _, _, _ string) (string, error) {
		return "abcdef0123456789abcdef0123456789abcdef01", nil
	}
	mp := &achv1alpha1.PluginMarketplace{
		Spec: achv1alpha1.PluginMarketplaceSpec{
			Type: "github",
			GitHub: &achv1alpha1.GitHubSource{
				Repo: "anthropics/claude-plugins-official",
				Ref:  "main",
			},
		},
	}
	entry := ClaudeCodeMarketplacePlugin{
		Name: "agent-sdk-dev",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "local-path",
			Path: "./plugins/agent-sdk-dev",
		},
	}
	_, rev, err := dispatchMarketplacePlugin(context.Background(), mp, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(rev) {
		t.Errorf("rev not 40-hex: %q", rev)
	}
	if captured.URL != "https://github.com/anthropics/claude-plugins-official.git" {
		t.Errorf("URL = %q", captured.URL)
	}
	if captured.Subtree != "./plugins/agent-sdk-dev" {
		t.Errorf("Subtree = %q", captured.Subtree)
	}
	// SHA was empty in the Spec the dispatch built — the local-path
	// resolver must set it to a 40-hex value BEFORE Fetch is called.
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(captured.SHA) {
		t.Errorf("local-path: SHA passed to Fetch was %q (want 40-hex)", captured.SHA)
	}
}

func TestDispatchMarketplacePlugin_UnsupportedKind(t *testing.T) {
	entry := ClaudeCodeMarketplacePlugin{
		Name:   "y",
		Source: ClaudeCodeMarketplaceSource{Kind: ""},
	}
	_, _, err := dispatchMarketplacePlugin(context.Background(), &achv1alpha1.PluginMarketplace{}, entry, nil, "/tmp")
	if err == nil || err != errUnsupportedPluginSource {
		t.Errorf("err = %v; want errUnsupportedPluginSource", err)
	}
}
