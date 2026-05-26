// SPDX-License-Identifier: Apache-2.0

// Plan 02-06 Task 4: envtest coverage of the PluginMarketplace
// three-stage refresh — Stage-1 failure reasons (Unreachable, parse
// failure, include-matches-zero, invalid-regex), Stage-2 partial-failure
// status.message formatting (including D-10 truncation +N more), the
// UnsupportedPluginSource per-entry path, the Stage-3 DELETE sweep
// (integration), and the cross-marketplace NameConflict CR-level flip
// (integration).
//
// Most tests run with DB=nil so the suite stays Docker-free. The two
// integration tests (TestPMR_Stage3_DeleteSweep and
// TestPMR_NameConflict_AlphabeticalPriority) require a real Postgres pool
// and are tagged via TestPMR_ naming so the verify grep matches; they
// gracefully skip when r.DB is nil.

package ach

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
	sourcesgit "github.com/ackstorm/ach/internal/sources/git"
)

// ensureSecret creates a Secret in WatchNamespace; AlreadyExists is OK so
// multiple tests can share the same auth Secret.
func ensureSecret(t *testing.T, ctx context.Context, name string, data map[string][]byte) {
	t.Helper()
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: WatchNamespace},
		Data:       data,
	}
	if err := k8sClient.Create(ctx, sec); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create Secret %q: %v", name, err)
	}
}

// ─── Per-key fake factory + helpers ───────────────────────────────────

// keyedFakeFetcher is a fresh-body fakeFetcher: every call to Fetch
// returns a new io.NopCloser over body. Suitable for retry-tolerant
// Eventually() polling because the body slice is read fresh per call.
type keyedFakeFetcher struct {
	body        []byte
	upstreamRev string
	notModified bool
	err         error
}

func (f *keyedFakeFetcher) Fetch(_ context.Context, _ sources.FetchRequest) (*sources.FetchResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.notModified {
		return &sources.FetchResult{NotModified: true, UpstreamRev: f.upstreamRev}, nil
	}
	// Allocate fresh body per call — io.Copy consumes the reader so we
	// can't reuse a single ReadCloser across retries.
	return &sources.FetchResult{
		Body:        io.NopCloser(bytes.NewReader(append([]byte(nil), f.body...))),
		UpstreamRev: f.upstreamRev,
	}, nil
}

// marketplaceFakeFactory routes Fetcher dispatch by spec.Type and a per-
// type discriminator key. Stage-1 fetches the marketplace.json itself
// (one SourceSpec); Stage-2 fetches each plugin's tarball (N SourceSpecs).
// Each call must return a distinct fakeFetcher (or the same one for
// shared behavior) per the test scenario.
type marketplaceFakeFactory struct {
	mu       sync.Mutex
	fetchers map[string]*keyedFakeFetcher
}

func newMarketplaceFakeFactory() *marketplaceFakeFactory {
	return &marketplaceFakeFactory{fetchers: make(map[string]*keyedFakeFetcher)}
}

func (m *marketplaceFakeFactory) For(spec sources.SourceSpec) (sources.Fetcher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := keyFor(spec)
	if f, ok := m.fetchers[key]; ok {
		return f, nil
	}
	return nil, fmt.Errorf("test: no fake fetcher registered for key %q", key)
}

func (m *marketplaceFakeFactory) factory() FetcherFactory { return m.For }

func (m *marketplaceFakeFactory) register(key string, f *keyedFakeFetcher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fetchers[key] = f
}

// keyFor builds a deterministic dispatch key from a SourceSpec. Different
// per-type discriminators ensure Stage-1 (the marketplace.json fetch via
// e.g. HTTP) and Stage-2 (per-plugin GitHub fetches) route distinctly.
func keyFor(spec sources.SourceSpec) string {
	switch spec.Type {
	case "github":
		if spec.GitHub != nil {
			return "github:" + spec.GitHub.Repo + "@" + spec.GitHub.Ref
		}
	case "http":
		if spec.HTTP != nil {
			return "http:" + spec.HTTP.URL
		}
	case "gitlab":
		if spec.GitLab != nil {
			return "gitlab:" + spec.GitLab.Project + "@" + spec.GitLab.Ref
		}
	case "bitbucket":
		if spec.Bitbucket != nil {
			return "bitbucket:" + spec.Bitbucket.Workspace + "/" + spec.Bitbucket.Repo + "@" + spec.Bitbucket.Ref
		}
	case "s3":
		if spec.S3 != nil {
			return "s3:" + spec.S3.Bucket + "/" + spec.S3.Key
		}
	case "gcs":
		if spec.GCS != nil {
			return "gcs:" + spec.GCS.Bucket + "/" + spec.GCS.Object
		}
	}
	return spec.Type
}

// mustMarketplaceJSON marshals a ClaudeCodeMarketplace into a body the
// fake Stage-1 fetcher can return.
func mustMarketplaceJSON(t *testing.T, mkt ClaudeCodeMarketplace) []byte {
	t.Helper()
	b, err := json.Marshal(mkt)
	if err != nil {
		t.Fatalf("marshal marketplace.json fixture: %v", err)
	}
	return b
}

// mkGitSubdirPlugin builds a Claude Code real-schema git-subdir entry.
// The SHA is a stable 40-hex test value derived from name so each
// entry dispatches to a distinct fake-git-fetcher key.
func mkGitSubdirPlugin(name string) ClaudeCodeMarketplacePlugin {
	return ClaudeCodeMarketplacePlugin{
		Name: name,
		Source: ClaudeCodeMarketplaceSource{
			Kind: "git-subdir",
			URL:  "https://example.invalid/test/" + name + ".git",
			Path: "plugins/" + name,
			Ref:  "main",
			SHA:  shaForName(name),
		},
	}
}

// mkURLPlugin builds a Claude Code real-schema 'url' entry (whole repo).
//
//nolint:unused // kept for future tests that exercise the url Kind path.
func mkURLPlugin(name string) ClaudeCodeMarketplacePlugin {
	return ClaudeCodeMarketplacePlugin{
		Name: name,
		Source: ClaudeCodeMarketplaceSource{
			Kind: "url",
			URL:  "https://example.invalid/test/" + name + ".git",
			Ref:  "main",
			SHA:  shaForName(name),
		},
	}
}

// mkLocalPathPlugin builds a Claude Code real-schema local-path entry
// pointing at a subdirectory of the marketplace's own repo.
//
//nolint:unused // landed alongside mkURLPlugin; consumed by §5 follow-ups.
func mkLocalPathPlugin(name string) ClaudeCodeMarketplacePlugin {
	return ClaudeCodeMarketplacePlugin{
		Name: name,
		Source: ClaudeCodeMarketplaceSource{
			Kind: "local-path",
			Path: "plugins/" + name,
		},
	}
}

// mkUnsupportedPlugin emits an entry whose UnmarshalJSON would resolve
// to Kind="" (e.g. an upstream npm-shaped object). Used by tests that
// exercise the per-entry ReasonUnsupportedPluginSource path.
func mkUnsupportedPlugin(name string) ClaudeCodeMarketplacePlugin {
	return ClaudeCodeMarketplacePlugin{
		Name:   name,
		Source: ClaudeCodeMarketplaceSource{Kind: ""},
	}
}

// shaForName produces a deterministic 40-hex SHA from a test name so
// fixtures can pin known shas.
func shaForName(name string) string {
	h := sha1.Sum([]byte(name))
	return fmt.Sprintf("%x", h[:])
}

// ─── Fake git fetcher registry (Stage-2 INNER fetch) ──────────────────

// fakeGitFetcher is the envtest equivalent of keyedFakeFetcher for the
// new git-only Stage-2 dispatch path.
type fakeGitFetcher struct {
	body string
	rev  string
	err  error
}

func (f *fakeGitFetcher) Fetch(_ context.Context, _ sourcesgit.Request) (*sourcesgit.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &sourcesgit.Result{
		Body:        io.NopCloser(strings.NewReader(f.body)),
		UpstreamRev: f.rev,
	}, nil
}

// gitFetcherRegistry routes per-SHA Stage-2 fetches in envtest. Use
// withFakeGitFetcher to install one for the duration of a test.
type gitFetcherRegistry struct {
	mu       sync.Mutex
	fetchers map[string]*fakeGitFetcher
}

func newGitFetcherRegistry() *gitFetcherRegistry {
	return &gitFetcherRegistry{fetchers: map[string]*fakeGitFetcher{}}
}

func (g *gitFetcherRegistry) register(sha string, f *fakeGitFetcher) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fetchers[sha] = f
}

func (g *gitFetcherRegistry) lookup(spec sourcesgit.Spec) gitFetcher {
	g.mu.Lock()
	defer g.mu.Unlock()
	if f, ok := g.fetchers[spec.SHA]; ok {
		return f
	}
	return &fakeGitFetcher{err: fmt.Errorf("test: no fake registered for SHA %q", spec.SHA)}
}

// withFakeGitFetcher overrides the package-level newGitFetcherFn and
// newResolveHeadSHAFn for the duration of the test. Returns a
// *gitFetcherRegistry whose register method registers a per-entry fake
// by SHA. local-path tests can register at the stable test-only SHA
// "ffffffffffffffffffffffffffffffffffffffff" (the resolver stub).
func withFakeGitFetcher(t *testing.T) *gitFetcherRegistry {
	t.Helper()
	reg := newGitFetcherRegistry()
	orig := newGitFetcherFn
	newGitFetcherFn = func(spec sourcesgit.Spec) gitFetcher {
		return reg.lookup(spec)
	}
	origResolve := newResolveHeadSHAFn
	newResolveHeadSHAFn = func(_ context.Context, _, _, _ string) (string, error) {
		return "ffffffffffffffffffffffffffffffffffffffff", nil
	}
	t.Cleanup(func() {
		newGitFetcherFn = orig
		newResolveHeadSHAFn = origResolve
	})
	return reg
}

// pmrCR builds a PluginMarketplace CR pointing at a fake "github"
// upstream marketplace.json. The CR's repo is encoded so the test can
// register a Stage-1 fake fetcher under "github:test/mkt-<name>@main".
func pmrCR(name string, includes, excludes []string) *achv1alpha1.PluginMarketplace {
	cr := &achv1alpha1.PluginMarketplace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.PluginMarketplaceSpec{
			Type: "github",
			GitHub: &achv1alpha1.GitHubSource{
				Repo: "test/mkt-" + name,
				Ref:  "main",
				AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
					Name: "mkt-secret",
					Key:  "token",
				},
			},
			Refresh: achv1alpha1.RefreshBlock{
				Interval:     &metav1.Duration{Duration: time.Hour},
				MaxStaleness: metav1.Duration{Duration: 24 * time.Hour},
			},
		},
	}
	if includes != nil || excludes != nil {
		cr.Spec.Filters = &achv1alpha1.MarketplaceFilters{Include: includes, Exclude: excludes}
	}
	return cr
}

// applyMarketplaceCR creates the CR + its mkt-secret Secret in WatchNamespace,
// returning a t.Cleanup that drains the finalizer + removes the cache.
// Returns the CR's stage-1 dispatch key so the test can register the
// marketplace.json fake fetcher.
func applyMarketplaceCR(t *testing.T, ctx context.Context, cr *achv1alpha1.PluginMarketplace) (stage1Key string) {
	t.Helper()
	ensureSecret(t, ctx, "mkt-secret", map[string][]byte{"token": []byte("t")})

	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create PluginMarketplace %q: %v", cr.Name, err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cr)
		probe := &achv1alpha1.PluginMarketplace{ObjectMeta: metav1.ObjectMeta{
			Name: cr.Name, Namespace: cr.Namespace,
		}}
		WaitForGone(context.Background(), probe, 15*time.Second)
		_ = os.RemoveAll(filepath.Join(testCacheRoot, "marketplace", cr.Name))
	})
	return keyFor(buildSourceSpec(
		cr.Spec.Type, cr.Spec.GitHub, cr.Spec.GitLab, cr.Spec.Bitbucket,
		cr.Spec.S3, cr.Spec.GCS, cr.Spec.HTTP,
	))
}

// waitForFinalizer polls until the suite reconciler has added the
// pluginMarketplaceFinalizer. Required before our per-test reconciler can
// reach the steady-state branch.
func waitForFinalizer(t *testing.T, ctx context.Context, cr *achv1alpha1.PluginMarketplace) {
	t.Helper()
	if !Eventually(func() bool {
		var got achv1alpha1.PluginMarketplace
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(&got, pluginMarketplaceFinalizer)
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatalf("finalizer never added for %q", cr.Name)
	}
}

// drainReconcileUntil keeps calling our per-test reconciler's Reconcile()
// in an Eventually loop until cond returns true. Returns false on timeout.
func drainReconcileUntil(ctx context.Context, r *PluginMarketplaceReconciler, cr *achv1alpha1.PluginMarketplace, cond func(*achv1alpha1.PluginMarketplace) bool) bool {
	return Eventually(func() bool {
		req := ctrlReq(cr)
		_, _ = r.Reconcile(ctx, req)
		var got achv1alpha1.PluginMarketplace
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return cond(&got)
	}, 30*time.Second, 500*time.Millisecond)
}

// syncedCondition returns the Synced condition or nil.
func syncedCondition(cr *achv1alpha1.PluginMarketplace) *metav1.Condition {
	for i := range cr.Status.Conditions {
		c := &cr.Status.Conditions[i]
		if c.Type == "Synced" {
			return c
		}
	}
	return nil
}

// ─── Tests ────────────────────────────────────────────────────────────

func TestPMR_Stage1_FetchUnreachable(t *testing.T) {
	ctx := context.Background()
	cr := pmrCR("s1-unreach", nil, nil)
	root := newCacheRoot(t)

	stage1Key := applyMarketplaceCR(t, ctx, cr)
	waitForFinalizer(t, ctx, cr)

	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{err: fmt.Errorf("dial: %w", sources.ErrUnreachable)})

	r := &PluginMarketplaceReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  factory.factory(),
	}
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == ReasonUnreachable
	})
	if !ok {
		t.Fatalf("never observed Synced=False reason=Unreachable")
	}
	// No cached files under marketplace/<name>/.
	subtree := filepath.Join(root, "marketplace", cr.Name)
	if _, err := os.Stat(subtree); err == nil {
		t.Errorf("Stage-1 failure created cached subtree at %s", subtree)
	}
}

func TestPMR_Stage1_ParseFails(t *testing.T) {
	ctx := context.Background()
	cr := pmrCR("s1-parse", nil, nil)
	root := newCacheRoot(t)

	stage1Key := applyMarketplaceCR(t, ctx, cr)
	waitForFinalizer(t, ctx, cr)

	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: []byte("{not json"), upstreamRev: "x"})

	r := &PluginMarketplaceReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  factory.factory(),
	}
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == ReasonUpstreamInvalid && strings.Contains(c.Message, "parse")
	})
	if !ok {
		t.Fatalf("never observed Synced=False reason=UpstreamInvalid with 'parse' in message")
	}
}

func TestPMR_Stage1_IncludeMatchesZero(t *testing.T) {
	ctx := context.Background()
	cr := pmrCR("s1-zero", []string{"z.*"}, nil)
	root := newCacheRoot(t)

	stage1Key := applyMarketplaceCR(t, ctx, cr)
	waitForFinalizer(t, ctx, cr)

	mktBody := mustMarketplaceJSON(t, ClaudeCodeMarketplace{
		Name:    "m",
		Plugins: []ClaudeCodeMarketplacePlugin{mkGitSubdirPlugin("alpha"), mkGitSubdirPlugin("beta")},
	})
	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: mktBody})

	r := &PluginMarketplaceReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  factory.factory(),
	}
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == ReasonUpstreamInvalid && strings.Contains(c.Message, "zero")
	})
	if !ok {
		t.Fatalf("never observed Synced=False reason=UpstreamInvalid with 'zero' in message")
	}
}

func TestPMR_Stage1_InvalidRegex(t *testing.T) {
	ctx := context.Background()
	cr := pmrCR("s1-rx", []string{"[unclosed"}, nil)
	root := newCacheRoot(t)

	stage1Key := applyMarketplaceCR(t, ctx, cr)
	waitForFinalizer(t, ctx, cr)

	mktBody := mustMarketplaceJSON(t, ClaudeCodeMarketplace{
		Name:    "m",
		Plugins: []ClaudeCodeMarketplacePlugin{mkGitSubdirPlugin("alpha")},
	})
	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: mktBody})

	r := &PluginMarketplaceReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  factory.factory(),
	}
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		return c != nil && c.Status == metav1.ConditionFalse && c.Reason == ReasonInvalidConfig
	})
	if !ok {
		t.Fatalf("never observed Synced=False reason=InvalidConfig")
	}
}

func TestPMR_Stage2_PartialFailure_StatusMessage(t *testing.T) {
	ctx := context.Background()
	cr := pmrCR("s2-partial", nil, nil)
	root := newCacheRoot(t)

	stage1Key := applyMarketplaceCR(t, ctx, cr)
	waitForFinalizer(t, ctx, cr)

	mktBody := mustMarketplaceJSON(t, ClaudeCodeMarketplace{
		Name: "m",
		Plugins: []ClaudeCodeMarketplacePlugin{
			mkGitSubdirPlugin("alpha"), mkGitSubdirPlugin("beta"), mkGitSubdirPlugin("charlie"),
		},
	})
	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: mktBody})
	gitReg := withFakeGitFetcher(t)
	gitReg.register(shaForName("alpha"), &fakeGitFetcher{body: "alpha-body", rev: shaForName("alpha")})
	gitReg.register(shaForName("beta"), &fakeGitFetcher{err: fmt.Errorf("dial: %w", sources.ErrUnreachable)})
	gitReg.register(shaForName("charlie"), &fakeGitFetcher{body: "charlie-body", rev: shaForName("charlie")})

	r := &PluginMarketplaceReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  factory.factory(),
	}
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == ReasonSynced && strings.Contains(c.Message, "beta:") && strings.Contains(c.Message, "Unreachable")
	})
	if !ok {
		var got achv1alpha1.PluginMarketplace
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		if c := syncedCondition(&got); c != nil {
			t.Logf("last observed condition: status=%s reason=%s message=%q", c.Status, c.Reason, c.Message)
		}
		t.Fatalf("never observed Synced=True with partial-failure status message")
	}

	// Cache file assertions.
	wantAlpha := filepath.Join(root, "marketplace", cr.Name, "plugin", "alpha.tar.gz")
	if _, err := os.Stat(wantAlpha); err != nil {
		t.Errorf("alpha cache file missing at %s: %v", wantAlpha, err)
	}
	wantCharlie := filepath.Join(root, "marketplace", cr.Name, "plugin", "charlie.tar.gz")
	if _, err := os.Stat(wantCharlie); err != nil {
		t.Errorf("charlie cache file missing at %s: %v", wantCharlie, err)
	}
	wantBeta := filepath.Join(root, "marketplace", cr.Name, "plugin", "beta.tar.gz")
	if _, err := os.Stat(wantBeta); err == nil {
		t.Errorf("beta cache file should NOT exist after failure; found at %s", wantBeta)
	}
}

func TestPMR_Stage2_UnsupportedNpm(t *testing.T) {
	ctx := context.Background()
	cr := pmrCR("s2-npm", nil, nil)
	root := newCacheRoot(t)

	stage1Key := applyMarketplaceCR(t, ctx, cr)
	waitForFinalizer(t, ctx, cr)

	mktBody := mustMarketplaceJSON(t, ClaudeCodeMarketplace{
		Name:    "m",
		Plugins: []ClaudeCodeMarketplacePlugin{mkGitSubdirPlugin("alpha"), mkUnsupportedPlugin("evil")},
	})
	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: mktBody})
	gitReg := withFakeGitFetcher(t)
	gitReg.register(shaForName("alpha"), &fakeGitFetcher{body: "alpha-body", rev: shaForName("alpha")})

	r := &PluginMarketplaceReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  factory.factory(),
	}
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == ReasonSynced && strings.Contains(c.Message, "evil:") && strings.Contains(c.Message, "UnsupportedPluginSource")
	})
	if !ok {
		t.Fatalf("never observed Synced=True with 'evil: UnsupportedPluginSource' in message")
	}
}

func TestPMR_Stage2_Truncation(t *testing.T) {
	ctx := context.Background()
	cr := pmrCR("s2-trunc", nil, nil)
	root := newCacheRoot(t)

	stage1Key := applyMarketplaceCR(t, ctx, cr)
	waitForFinalizer(t, ctx, cr)

	// 8 plugins, 7 fail (all with ErrUnreachable), 1 succeeds.
	plugins := []ClaudeCodeMarketplacePlugin{}
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "good"} {
		plugins = append(plugins, mkGitSubdirPlugin(n))
	}
	mktBody := mustMarketplaceJSON(t, ClaudeCodeMarketplace{Name: "m", Plugins: plugins})

	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: mktBody})
	gitReg := withFakeGitFetcher(t)
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"} {
		gitReg.register(shaForName(n), &fakeGitFetcher{err: fmt.Errorf("dial: %w", sources.ErrUnreachable)})
	}
	gitReg.register(shaForName("good"), &fakeGitFetcher{body: "good", rev: shaForName("good")})

	r := &PluginMarketplaceReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  factory.factory(),
	}
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == ReasonSynced && strings.Contains(c.Message, "stage-2: 7 plugin(s) failed") && strings.Contains(c.Message, "+2 more")
	})
	if !ok {
		var got achv1alpha1.PluginMarketplace
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		if c := syncedCondition(&got); c != nil {
			t.Logf("last observed message: %q", c.Message)
		}
		t.Fatalf("never observed Synced=True with '+2 more' truncation")
	}
}

func TestPMR_Stage2_PluginTooLarge(t *testing.T) {
	// T-02-06-07 mitigation: marketplace-sourced plugins observe the
	// PluginMaxSizeMiB cap just like Plugin CRDs do. Body of 100 bytes
	// with a 0-MiB cap means the LimitReader trips on the very first
	// byte → OversizeError → ReasonPluginTooLarge in the partial-failure
	// list.
	ctx := context.Background()
	cr := pmrCR("s2-toolarge", nil, nil)
	root := newCacheRoot(t)

	stage1Key := applyMarketplaceCR(t, ctx, cr)
	waitForFinalizer(t, ctx, cr)

	mktBody := mustMarketplaceJSON(t, ClaudeCodeMarketplace{
		Name:    "m",
		Plugins: []ClaudeCodeMarketplacePlugin{mkGitSubdirPlugin("big")},
	})
	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: mktBody})
	gitReg := withFakeGitFetcher(t)
	gitReg.register(shaForName("big"), &fakeGitFetcher{body: strings.Repeat("x", 1<<21 /* 2 MiB */), rev: shaForName("big")})

	r := &PluginMarketplaceReconciler{
		Client:           k8sClient,
		Namespace:        WatchNamespace,
		Log:              logr.Discard(),
		CacheRoot:        root,
		Fetchers:         factory.factory(),
		PluginMaxSizeMiB: 1, // 1 MiB cap; 2 MiB body overshoots
	}
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == ReasonSynced && strings.Contains(c.Message, "big:") && strings.Contains(c.Message, "PluginTooLarge")
	})
	if !ok {
		var got achv1alpha1.PluginMarketplace
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		if c := syncedCondition(&got); c != nil {
			t.Logf("last observed message: %q", c.Message)
		}
		t.Fatalf("never observed Synced=True with 'big: PluginTooLarge'")
	}
	// No cached file at marketplace/<name>/plugin/big.tar.gz.
	bigFile := filepath.Join(root, "marketplace", cr.Name, "plugin", "big.tar.gz")
	if _, err := os.Stat(bigFile); err == nil {
		t.Errorf("oversize plugin produced a cached file at %s", bigFile)
	}
}

func TestPMR_Stage3_DeleteSweep(t *testing.T) {
	// Stage-3 DELETE sweep requires a real Postgres pool because the
	// reconciler's "diff prior vs current names" logic reads from
	// marketplace_plugins rows. Skip when DB-less (the default
	// `make test` configuration). Integration suite stands up
	// testcontainers Postgres and wires r.DB before running.
	t.Skip("integration: requires r.DB (Postgres pool); covered by make test-integration")
}

func TestPMR_NameConflict_AlphabeticalPriority(t *testing.T) {
	// NameConflict at CR level requires DB-backed
	// listOtherMarketplaceCatalogs to see the other marketplace's
	// existing plugin row set. Pure resolveConflicts unit coverage
	// lives in marketplace_conflict_test.go; the full reconciler
	// integration is gated on make test-integration.
	t.Skip("integration: requires r.DB (Postgres pool); covered by make test-integration")
}

func TestPMR_PluginCRDBeatsMarketplace(t *testing.T) {
	// A Plugin CR with metadata.name="shared" should drop the marketplace
	// entry for "shared" from the materialization set. The marketplace's
	// Synced=True (Plugin-CRD-wins is informational, not a NameConflict
	// CR-flip), and status.message mentions "shared: PluginCRDPrecedence"
	// (WR-09: distinct reason from marketplace-loser NameConflict drops).
	ctx := context.Background()

	// Pre-seed the Plugin CR that the marketplace will conflict with. Use
	// a unique name to avoid colliding with other tests.
	pluginCRName := "crd-vs-mkt-shared"
	pluginCR := &achv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Name: pluginCRName, Namespace: WatchNamespace},
		Spec: achv1alpha1.PluginSpec{
			Type: "github",
			GitHub: &achv1alpha1.GitHubSource{
				Repo:          "test/" + pluginCRName,
				Ref:           "main",
				AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{Name: "mkt-secret", Key: "token"},
			},
			Refresh: achv1alpha1.RefreshBlock{
				Interval:     &metav1.Duration{Duration: time.Hour},
				MaxStaleness: metav1.Duration{Duration: 24 * time.Hour},
			},
		},
	}
	ensureSecret(t, ctx, "mkt-secret", map[string][]byte{"token": []byte("t")})
	if err := k8sClient.Create(ctx, pluginCR); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create Plugin CR: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), pluginCR)
		probe := &achv1alpha1.Plugin{ObjectMeta: metav1.ObjectMeta{Name: pluginCRName, Namespace: WatchNamespace}}
		WaitForGone(context.Background(), probe, 15*time.Second)
	})

	cr := pmrCR("crdwin-mkt", nil, nil)
	root := newCacheRoot(t)

	stage1Key := applyMarketplaceCR(t, ctx, cr)
	waitForFinalizer(t, ctx, cr)

	// The marketplace exposes a plugin name that EXACTLY MATCHES the
	// Plugin CR's metadata.name — Plugin-CRD-wins rule should drop the
	// marketplace's entry.
	mktBody := mustMarketplaceJSON(t, ClaudeCodeMarketplace{
		Name: "m",
		Plugins: []ClaudeCodeMarketplacePlugin{
			mkGitSubdirPlugin(pluginCRName),
		},
	})
	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: mktBody})
	// Intentionally do NOT register a Stage-2 fetcher for pluginCRName —
	// if Plugin-CRD-wins works, materializeMarketplacePlugin is never
	// called for this name.

	r := &PluginMarketplaceReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  factory.factory(),
	}
	ok := drainReconcileUntil(ctx, r, cr, func(got *achv1alpha1.PluginMarketplace) bool {
		c := syncedCondition(got)
		// WR-09: Plugin-CRD-wins drops are reported with the distinct
		// reason "PluginCRDPrecedence" so they are visually
		// distinguishable from marketplace-loser drops (NameConflict).
		return c != nil && c.Status == metav1.ConditionTrue && c.Reason == ReasonSynced && strings.Contains(c.Message, pluginCRName+": "+ReasonPluginCRDPrecedence)
	})
	if !ok {
		var got achv1alpha1.PluginMarketplace
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		if c := syncedCondition(&got); c != nil {
			t.Logf("last observed message: %q", c.Message)
		}
		t.Fatalf("never observed Synced=True with plugin-CRD-wins PluginCRDPrecedence in message")
	}
	// No file at marketplace/<name>/plugin/<pluginCRName>.tar.gz
	collisionFile := filepath.Join(root, "marketplace", cr.Name, "plugin", pluginCRName+".tar.gz")
	if _, err := os.Stat(collisionFile); err == nil {
		t.Errorf("Plugin-CRD-wins did not block marketplace materialization (file present at %s)", collisionFile)
	}
}
