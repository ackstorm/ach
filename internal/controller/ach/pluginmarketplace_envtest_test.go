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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/ackstorm/ach/internal/sources/registry"
)

// ─── Shared suite/per-test marketplace fetcher factory ────────────────
//
// The suite-level PluginMarketplaceReconciler (registered in suite_test.go
// against the running manager) and the per-test reconciler invoked by
// drainReconcileUntil both react to the same CR. Without a shared
// fetcher factory, the suite-level reconciler falls back to the real
// registry.For and tries live HTTPS against the fake "test/mkt-*"
// upstreams — every attempt fails with a different reason than the
// per-test fake produces. The two reconcilers then race on
// Status().Update(), and Stage-1 / Stage-2 tests intermittently observe
// the suite's overwrite instead of the per-test result. See GitHub
// issue #18.
//
// suiteMarketplaceFactory is a FetcherFactory wired into the suite-level
// PMR; when a per-test factory is installed via setSuiteMarketplaceFactory
// (called by drainReconcileUntil), the suite reconciler routes through
// the same per-test fakes — both reconcilers converge on identical status.
// When no per-test factory is set, it falls through to the production
// registry.For so the finalizer-only envtest spec keeps working.
var (
	sharedSuiteFactory   FetcherFactory
	sharedSuiteFactoryMu sync.RWMutex
)

func suiteMarketplaceFactory(spec sources.SourceSpec) (sources.Fetcher, error) {
	sharedSuiteFactoryMu.RLock()
	f := sharedSuiteFactory
	sharedSuiteFactoryMu.RUnlock()
	if f != nil {
		return f(spec)
	}
	return registry.For(spec)
}

// setSuiteMarketplaceFactory installs (or clears, when f is nil) the
// per-test factory the suite-level PMR delegates to. Returns a reset
// closure so the caller can restore the previous value via defer.
func setSuiteMarketplaceFactory(f FetcherFactory) func() {
	sharedSuiteFactoryMu.Lock()
	prev := sharedSuiteFactory
	sharedSuiteFactory = f
	sharedSuiteFactoryMu.Unlock()
	return func() {
		sharedSuiteFactoryMu.Lock()
		sharedSuiteFactory = prev
		sharedSuiteFactoryMu.Unlock()
	}
}

// ─── Shared suite/per-test PluginMaxSizeMiB ───────────────────────────
//
// Issue #18 follow-up: TestPMR_Stage2_PluginTooLarge constructs a
// per-test reconciler with PluginMaxSizeMiB=1 so a 2 MiB body trips the
// LimitReader cap and surfaces ReasonPluginTooLarge. The suite-level
// PMR has PluginMaxSizeMiB=0 (default, unlimited) and would happily
// stage the same body to disk, producing a Synced=True without the
// expected per-entry PluginTooLarge marker. This shared atomic lets
// drainReconcileUntil mirror the per-test cap onto the suite reconciler
// via PluginMaxSizeMiBFn (a function-typed override the suite PMR is
// wired to read from), so both reconcilers converge on the same per-
// entry classification. Zero (the default) preserves the suite's
// pre-existing no-cap behavior for tests that never opt into a cap.
var sharedSuitePluginMaxSizeMiB atomic.Int32

// suitePluginMaxSizeMiB is wired into the suite-level PMR's
// PluginMaxSizeMiBFn so every cap read goes through the shared atomic.
func suitePluginMaxSizeMiB() int {
	return int(sharedSuitePluginMaxSizeMiB.Load())
}

// setSuitePluginMaxSizeMiB installs the supplied cap on the shared
// atomic and returns a reset closure that restores the previous value
// — defer-friendly, race-free under -race.
func setSuitePluginMaxSizeMiB(n int) func() {
	prev := sharedSuitePluginMaxSizeMiB.Swap(int32(n))
	return func() {
		sharedSuitePluginMaxSizeMiB.Store(prev)
	}
}

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

// marshalMarketplaceSource returns the wire-format JSON for a
// ClaudeCodeMarketplaceSource so that the resulting bytes round-trip
// correctly through UnmarshalJSON.
//
// ClaudeCodeMarketplaceSource has no json struct tags (the fields use
// custom discriminator logic in UnmarshalJSON) so a bare json.Marshal
// produces capitalized field names that UnmarshalJSON doesn't parse —
// all entries appear as Kind="" (UnsupportedPluginSource). This helper
// emits the correct lowercase wire-format JSON instead.
func marshalMarketplaceSource(s ClaudeCodeMarketplaceSource) json.RawMessage {
	switch s.Kind {
	case "git-subdir", "url":
		type wireGitSubdir struct {
			Source string `json:"source"`
			URL    string `json:"url"`
			Path   string `json:"path,omitempty"`
			Ref    string `json:"ref,omitempty"`
			SHA    string `json:"sha,omitempty"`
		}
		b, _ := json.Marshal(wireGitSubdir{Source: s.Kind, URL: s.URL, Path: s.Path, Ref: s.Ref, SHA: s.SHA})
		return b
	case "github":
		type wireGitHub struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
			Ref    string `json:"ref,omitempty"`
			SHA    string `json:"sha,omitempty"`
		}
		b, _ := json.Marshal(wireGitHub{Source: "github", Repo: s.Repo, Ref: s.Ref, SHA: s.SHA})
		return b
	case "local-path":
		// local-path is the bare-string form: "source": "./plugins/x"
		b, _ := json.Marshal(s.Path)
		return b
	default:
		// Unsupported / empty Kind → emit a recognizably-unsupported
		// discriminator so UnmarshalJSON leaves Kind="".
		b, _ := json.Marshal(map[string]string{"source": "unsupported"})
		return b
	}
}

// marshalMarketplaceWire serializes a ClaudeCodeMarketplace into the
// wire-format JSON that parseClaudeCodeMarketplace / UnmarshalJSON
// accept. Use this instead of json.Marshal(mkt) to ensure the source
// discriminator survives the round-trip.
func marshalMarketplaceWire(t *testing.T, mkt ClaudeCodeMarketplace) []byte {
	t.Helper()
	type wirePlugin struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Source      json.RawMessage `json:"source"`
	}
	type wireMarketplace struct {
		Name    string       `json:"name"`
		Plugins []wirePlugin `json:"plugins"`
	}
	wm := wireMarketplace{Name: mkt.Name, Plugins: make([]wirePlugin, len(mkt.Plugins))}
	for i, p := range mkt.Plugins {
		wm.Plugins[i] = wirePlugin{
			Name:        p.Name,
			Description: p.Description,
			Source:      marshalMarketplaceSource(p.Source),
		}
	}
	b, err := json.Marshal(wm)
	if err != nil {
		t.Fatalf("marshalMarketplaceWire: %v", err)
	}
	return b
}

// mustMarketplaceJSON marshals a ClaudeCodeMarketplace into a gzipped
// tarball body the Stage-1 fake fetcher can return for github/gitlab/
// bitbucket source types. The reconciler's stage-1 path calls
// extractMarketplaceJSON for these tarball-typed sources, which walks
// the archive looking for `.claude-plugin/marketplace.json` (matching
// by suffix). Wrap the JSON in that path so the extract step succeeds
// and the body reaches Stage-1 parse.
func mustMarketplaceJSON(t *testing.T, mkt ClaudeCodeMarketplace) []byte {
	t.Helper()
	return mustMarketplaceTarball(t, marshalMarketplaceWire(t, mkt))
}

// mustPluginTarGz returns a minimal tar.gz body containing
// <subtree>/.claude-plugin/plugin.json so verifyPluginManifest (F4) is
// satisfied. The return type is `string` rather than `[]byte` because
// fakeGitFetcher's body field is `string` (the pre-F4 fake-fetcher
// contract); binary safety is preserved by Go's untyped byte-string
// semantics, so this is purely an ergonomic choice — callers feed the
// result directly to strings.NewReader.
func mustPluginTarGz(t *testing.T, subtree string) string {
	t.Helper()
	entryPath := ".claude-plugin/plugin.json"
	if subtree != "" {
		entryPath = subtree + "/" + entryPath
	}
	tgz := buildTarGz(t, map[string]string{entryPath: `{"name":"test"}`})
	return string(tgz)
}

// mustMarketplaceTarball wraps a raw marketplace.json body in a gzipped
// tarball whose single entry is `repo-fffffff/.claude-plugin/
// marketplace.json` — the same layout GitHub/GitLab/Bitbucket archive
// APIs emit. Used to feed Stage-1 invalid-JSON / valid-JSON fixtures
// into the github source-type extract path.
func mustMarketplaceTarball(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name:     "repo-fffffff/.claude-plugin/marketplace.json",
		Mode:     0o644,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
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
//
// Issue #18: the suite-level PMR also reconciles this CR in the manager
// goroutine. To prevent a resourceVersion race between the two
// reconcilers (which produce different Synced reasons whenever the
// suite reconciler hits the real registry instead of the per-test fake,
// or applies a different size cap), mirror the per-test FetcherFactory
// and PluginMaxSizeMiB onto the shared holders the suite reconciler
// reads from. Both reconcilers then converge on identical per-entry
// classification.
func drainReconcileUntil(ctx context.Context, r *PluginMarketplaceReconciler, cr *achv1alpha1.PluginMarketplace, cond func(*achv1alpha1.PluginMarketplace) bool) bool {
	if r.Fetchers != nil {
		defer setSuiteMarketplaceFactory(r.Fetchers)()
	}
	if r.PluginMaxSizeMiB != 0 {
		defer setSuitePluginMaxSizeMiB(r.PluginMaxSizeMiB)()
	}
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

	// `{not json` wrapped in a tarball so the github extract step succeeds
	// and the body reaches Stage-1 parse, which is what this test is for.
	factory := newMarketplaceFakeFactory()
	factory.register(stage1Key, &keyedFakeFetcher{body: mustMarketplaceTarball(t, []byte("{not json")), upstreamRev: "x"})

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
	// F4: fake bodies must be valid tarballs with .claude-plugin/plugin.json
	// at the entry's subtree path so verifyPluginManifest passes.
	// mkGitSubdirPlugin sets Path="plugins/<name>", so the manifest lives at
	// plugins/<name>/.claude-plugin/plugin.json inside the tarball.
	gitReg.register(shaForName("alpha"), &fakeGitFetcher{body: mustPluginTarGz(t, "plugins/alpha"), rev: shaForName("alpha")})
	gitReg.register(shaForName("beta"), &fakeGitFetcher{err: fmt.Errorf("dial: %w", sources.ErrUnreachable)})
	gitReg.register(shaForName("charlie"), &fakeGitFetcher{body: mustPluginTarGz(t, "plugins/charlie"), rev: shaForName("charlie")})

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
	// F4: alpha's body must be a valid tarball with .claude-plugin/plugin.json.
	gitReg.register(shaForName("alpha"), &fakeGitFetcher{body: mustPluginTarGz(t, "plugins/alpha"), rev: shaForName("alpha")})

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
	// F4: "good" body must be a valid tarball with .claude-plugin/plugin.json.
	gitReg.register(shaForName("good"), &fakeGitFetcher{body: mustPluginTarGz(t, "plugins/good"), rev: shaForName("good")})

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
