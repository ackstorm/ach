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

// defaultRefMain is the default git ref assumed when a marketplace entry
// omits Ref. Extracted as a constant to satisfy goconst (3+ occurrences
// across dispatch tests).
const defaultRefMain = "main"

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
				Ref:  defaultRefMain,
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

func TestDispatchMarketplacePlugin_GitSubdir_RefOnly_PreResolvesSHA(t *testing.T) {
	// Entry without sha: dispatcher MUST call newResolveHeadSHAFn to
	// resolve ref→sha before constructing the git.Spec, then pass the
	// resolved SHA to Fetch (40-hex contract preserved).
	const resolvedSHA = "fedcba9876543210fedcba9876543210fedcba98"
	var captured sourcesgit.Spec
	var resolveCalled bool

	origFetch := newGitFetcherFn
	origResolve := newResolveHeadSHAFn
	defer func() {
		newGitFetcherFn = origFetch
		newResolveHeadSHAFn = origResolve
	}()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		captured = spec
		return &fakeDispatchGitFetcher{body: "tar", rev: spec.SHA}
	}
	newResolveHeadSHAFn = func(_ context.Context, url, ref, _ string) (string, error) {
		resolveCalled = true
		if url != "https://github.com/o/r.git" {
			t.Errorf("LsRemote url = %q", url)
		}
		if ref != defaultRefMain {
			t.Errorf("LsRemote ref = %q; want main (default)", ref)
		}
		return resolvedSHA, nil
	}

	entry := ClaudeCodeMarketplacePlugin{
		Name: "x",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "git-subdir",
			URL:  "https://github.com/o/r.git",
			Path: "plugins/x",
			// No Ref, no SHA — both should default + resolve.
		},
	}
	mp := &achv1alpha1.PluginMarketplace{}
	_, rev, err := dispatchMarketplacePlugin(context.Background(), mp, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !resolveCalled {
		t.Fatal("newResolveHeadSHAFn was not called for sha-less entry")
	}
	if captured.SHA != resolvedSHA {
		t.Errorf("captured.SHA = %q; want %q", captured.SHA, resolvedSHA)
	}
	if rev != resolvedSHA {
		t.Errorf("rev = %q; want %q", rev, resolvedSHA)
	}
}

func TestDispatchMarketplacePlugin_SHA_TakesPrecedenceOverRef(t *testing.T) {
	// When both sha and ref are present, the explicit sha wins —
	// LsRemote MUST NOT be invoked.
	const pinnedSHA = "0123456789abcdef0123456789abcdef01234567"
	origFetch := newGitFetcherFn
	origResolve := newResolveHeadSHAFn
	defer func() {
		newGitFetcherFn = origFetch
		newResolveHeadSHAFn = origResolve
	}()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		return &fakeDispatchGitFetcher{body: "tar", rev: spec.SHA}
	}
	newResolveHeadSHAFn = func(_ context.Context, _, _, _ string) (string, error) {
		t.Fatal("LsRemote called even though entry has explicit sha")
		return "", nil
	}
	entry := ClaudeCodeMarketplacePlugin{
		Name: "x",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "git-subdir",
			URL:  "https://github.com/o/r.git",
			Path: "plugins/x",
			Ref:  "v1",
			SHA:  pinnedSHA,
		},
	}
	_, rev, err := dispatchMarketplacePlugin(context.Background(), &achv1alpha1.PluginMarketplace{}, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if rev != pinnedSHA {
		t.Errorf("rev = %q; want %q", rev, pinnedSHA)
	}
}

func TestDispatchMarketplacePlugin_GitHub(t *testing.T) {
	const pinnedSHA = "0123456789abcdef0123456789abcdef01234567"
	var captured sourcesgit.Spec
	origFetch := newGitFetcherFn
	defer func() { newGitFetcherFn = origFetch }()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		captured = spec
		return &fakeDispatchGitFetcher{body: "tar", rev: spec.SHA}
	}
	entry := ClaudeCodeMarketplacePlugin{
		Name: "x",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "github",
			Repo: "owner/name",
			Ref:  "v2",
			SHA:  pinnedSHA,
		},
	}
	_, _, err := dispatchMarketplacePlugin(context.Background(), &achv1alpha1.PluginMarketplace{}, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if captured.URL != "https://github.com/owner/name.git" {
		t.Errorf("URL = %q; want https://github.com/owner/name.git", captured.URL)
	}
	if captured.Subtree != "" {
		t.Errorf("Subtree = %q; want \"\" (github clones whole repo)", captured.Subtree)
	}
	if captured.Ref != "v2" || captured.SHA != pinnedSHA {
		t.Errorf("captured = %+v", captured)
	}
}

func TestDispatchMarketplacePlugin_UrlWithPath_TreatedAsGitSubdir(t *testing.T) {
	const pinnedSHA = "abcdefabcdefabcdefabcdefabcdefabcdef0123"
	var captured sourcesgit.Spec
	origFetch := newGitFetcherFn
	defer func() { newGitFetcherFn = origFetch }()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		captured = spec
		return &fakeDispatchGitFetcher{body: "tar", rev: spec.SHA}
	}
	entry := ClaudeCodeMarketplacePlugin{
		Name: "zilliz",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "url",
			URL:  "https://github.com/zilliztech/zilliz-plugin.git",
			Path: "plugins/zilliz",
			SHA:  pinnedSHA,
		},
	}
	_, _, err := dispatchMarketplacePlugin(context.Background(), &achv1alpha1.PluginMarketplace{}, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if captured.Subtree != "plugins/zilliz" {
		t.Errorf("Subtree = %q; want plugins/zilliz (url+path collapsed to git-subdir)", captured.Subtree)
	}
}

func TestDispatchMarketplacePlugin_GitHub_ShalessPreResolves(t *testing.T) {
	// github Kind without sha must trigger the generic ref→sha
	// pre-resolution path (Phase 2). Validates that the github URL
	// pattern (https://github.com/<repo>.git) is passed to LsRemote
	// and that Subtree stays empty (github Kind has no path concept).
	const resolvedSHA = "fedcba9876543210fedcba9876543210fedcba98"
	var captured sourcesgit.Spec
	var resolveCalled bool

	origFetch := newGitFetcherFn
	origResolve := newResolveHeadSHAFn
	defer func() {
		newGitFetcherFn = origFetch
		newResolveHeadSHAFn = origResolve
	}()
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		captured = spec
		return &fakeDispatchGitFetcher{body: "tar", rev: spec.SHA}
	}
	newResolveHeadSHAFn = func(_ context.Context, url, ref, _ string) (string, error) {
		resolveCalled = true
		if url != "https://github.com/owner/name.git" {
			t.Errorf("LsRemote url = %q", url)
		}
		if ref != defaultRefMain {
			t.Errorf("LsRemote ref = %q; want main (default)", ref)
		}
		return resolvedSHA, nil
	}
	entry := ClaudeCodeMarketplacePlugin{
		Name: "x",
		Source: ClaudeCodeMarketplaceSource{
			Kind: "github",
			Repo: "owner/name",
			// No Ref, no SHA.
		},
	}
	_, rev, err := dispatchMarketplacePlugin(context.Background(), &achv1alpha1.PluginMarketplace{}, entry, nil, "/tmp")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !resolveCalled {
		t.Fatal("newResolveHeadSHAFn was not called for sha-less github entry")
	}
	if rev != resolvedSHA {
		t.Errorf("rev = %q; want %q", rev, resolvedSHA)
	}
	if captured.Subtree != "" {
		t.Errorf("Subtree = %q; want empty (github Kind has no path)", captured.Subtree)
	}
}
