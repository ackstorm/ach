// SPDX-License-Identifier: Apache-2.0

// Plan 02-05 Task 5: envtest coverage for the materializeExternalRef
// helper + Plugin reconciler steady-state success / not-modified /
// PluginTooLarge / Unauthorized / Unreachable / staleness escalation /
// force-refresh annotation paths.
//
// All tests run with DB=nil so the suite stays Docker-free; the
// reconciler's nil-DB branch skips Get/Upsert/Delete external_refs calls
// and the Plugin/Prompt/Artifact reconcilers' steady-state paths still
// exercise materializeExternalRef end-to-end via a fake fetcher.

package ach

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/cachefs"
	"github.com/ackstorm/ach/internal/sources"
)

// ─── Fake fetcher used by every test ───────────────────────────────────

// fakeFetcher implements sources.Fetcher for deterministic envtest
// assertions without live HTTPS traffic. body is read once; multi-fetch
// scenarios must allocate a new fakeFetcher per call.
type fakeFetcher struct {
	body        []byte
	upstreamRev string
	notModified bool
	err         error
}

func (f *fakeFetcher) Fetch(_ context.Context, _ sources.FetchRequest) (*sources.FetchResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.notModified {
		return &sources.FetchResult{NotModified: true, UpstreamRev: f.upstreamRev}, nil
	}
	return &sources.FetchResult{
		Body:        io.NopCloser(bytes.NewReader(f.body)),
		UpstreamRev: f.upstreamRev,
	}, nil
}

// fakeFactory returns a FetcherFactory that always yields the supplied
// fake — production wires registry.For; tests inject here.
func fakeFactory(f *fakeFetcher) FetcherFactory {
	return func(_ sources.SourceSpec) (sources.Fetcher, error) { return f, nil }
}

// minimalPluginTarGz returns a tiny gzipped tar that survives the
// Step 5.5 pluginpack.Filter (issue #26): it contains a valid
// `.claude-plugin/plugin.json` plus a README.md so the filter emits
// a non-empty filtered tarball. Used by envtest cases that exercise
// the Plugin reconciler happy path with a fake fetcher.
func minimalPluginTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		".claude-plugin/plugin.json": `{"name":"test-plugin"}`,
		"README.md":                  "# test plugin",
	}
	// Sort for deterministic output.
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	// Trivial sort (2 entries) — keep allocation-free.
	if len(names) == 2 && names[0] > names[1] {
		names[0], names[1] = names[1], names[0]
	}
	for _, name := range names {
		body := files[name]
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("Write %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close: %v", err)
	}
	return buf.Bytes()
}

// newCacheRoot allocates a per-test isolated cache root with the
// canonical OP-10 layout so materializeExternalRef's CreateTemp finds
// .tmp/ and computeFinalPath's plugin/, prompt/, artifact/ subdirs exist.
func newCacheRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := cachefs.EnsureLayout(root); err != nil {
		t.Fatalf("cachefs.EnsureLayout(%q): %v", root, err)
	}
	return root
}

// ─── Helper-level tests (no envtest API server traffic) ────────────────

func TestComputeFinalPath(t *testing.T) {
	root := "/cache"
	cases := []struct {
		kind, name, scope string
		want              string
	}{
		{"plugin", "p1", "", "/cache/plugin/p1.tar.gz"},
		{"prompt", "pr1", "", "/cache/prompt/pr1.tar.gz"},
		{"artifact", "a1", "object", "/cache/artifact/a1.tar.gz"},
		{"artifact", "a1", "directory", "/cache/artifact/a1.tar.gz"},
		{"unknown", "x", "", ""},
		{"artifact", "x", "bogus", ""},
	}
	for _, c := range cases {
		got := computeFinalPath(root, c.kind, c.name, c.scope)
		if got != c.want {
			t.Errorf("computeFinalPath(%q,%q,%q) = %q; want %q",
				c.kind, c.name, c.scope, got, c.want)
		}
	}
}

func TestClassifyFetchError_AllReasons(t *testing.T) {
	refresh := achv1alpha1.RefreshBlock{MaxStaleness: metav1.Duration{Duration: time.Hour}}
	cases := []struct {
		name    string
		err     error
		want    string
		lastRef time.Time
	}{
		{"oversize", &OversizeError{Bytes: 100, Cap: 10}, ReasonPluginTooLarge, time.Time{}},
		{"unauthorized", fmt.Errorf("401: %w", sources.ErrUnauthorized), ReasonUnauthorized, time.Time{}},
		{"not-found", fmt.Errorf("404: %w", sources.ErrNotFound), ReasonNotFound, time.Time{}},
		{"upstream-invalid", fmt.Errorf("bad json: %w", sources.ErrUpstreamInvalid), ReasonUpstreamInvalid, time.Time{}},
		{"unreachable", fmt.Errorf("dial: %w", sources.ErrUnreachable), ReasonUnreachable, time.Time{}},
		{"unknown-defaults-to-unreachable", fmt.Errorf("mystery"), ReasonUnreachable, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := classifyFetchError(c.err, refresh, c.lastRef)
			if got != c.want {
				t.Errorf("classifyFetchError(%v) reason = %q; want %q", c.err, got, c.want)
			}
		})
	}
}

func TestClassifyFetchError_StalenessEscalation(t *testing.T) {
	refresh := achv1alpha1.RefreshBlock{MaxStaleness: metav1.Duration{Duration: 10 * time.Minute}}
	lastRef := time.Now().Add(-20 * time.Minute)
	reason, msg := classifyFetchError(fmt.Errorf("net: %w", sources.ErrUnreachable), refresh, lastRef)
	if reason != ReasonStaleCacheExpired {
		t.Fatalf("staleness escalation: got reason %q; want %q", reason, ReasonStaleCacheExpired)
	}
	if !strings.Contains(msg, "cache expired") {
		t.Errorf("staleness message missing 'cache expired': %q", msg)
	}
}

func TestClassifyFetchError_NoEscalationForUnauthorized(t *testing.T) {
	// Auth + 404 + UpstreamInvalid + Oversize do NOT escalate even when
	// the staleness window has elapsed — those reasons are terminal/
	// configuration-derived, not transient.
	refresh := achv1alpha1.RefreshBlock{MaxStaleness: metav1.Duration{Duration: 10 * time.Minute}}
	lastRef := time.Now().Add(-1 * time.Hour)
	cases := map[string]error{
		ReasonUnauthorized:    fmt.Errorf("401: %w", sources.ErrUnauthorized),
		ReasonNotFound:        fmt.Errorf("404: %w", sources.ErrNotFound),
		ReasonUpstreamInvalid: fmt.Errorf("bad: %w", sources.ErrUpstreamInvalid),
		ReasonPluginTooLarge:  &OversizeError{Bytes: 100, Cap: 10},
	}
	for want, err := range cases {
		got, _ := classifyFetchError(err, refresh, lastRef)
		if got != want {
			t.Errorf("classifyFetchError(%v) with stale window = %q; want %q (no escalation)", err, got, want)
		}
	}
}

func TestBuildSourceSpec_Github(t *testing.T) {
	g := &achv1alpha1.GitHubSource{Repo: "x/y", Ref: "main"}
	got := buildSourceSpec("github", g, nil, nil, nil, nil, nil)
	if got.Type != "github" || got.GitHub != g {
		t.Errorf("buildSourceSpec did not preserve type/subobject: %+v", got)
	}
}

func TestExtractAuthSecretRef(t *testing.T) {
	g := &achv1alpha1.GitHubSource{
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{Name: "g-secret"},
	}
	httpSrc := &achv1alpha1.HTTPSource{
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{Name: "h-secret"},
	}
	if ref := extractAuthSecretRef("github", g, nil, nil, nil, nil, nil); ref == nil || ref.Name != "g-secret" {
		t.Errorf("github extractAuthSecretRef: got %+v", ref)
	}
	if ref := extractAuthSecretRef("http", nil, nil, nil, nil, nil, httpSrc); ref == nil || ref.Name != "h-secret" {
		t.Errorf("http extractAuthSecretRef: got %+v", ref)
	}
	// http with nil AuthSecretRef → nil (anonymous HTTPS).
	anon := &achv1alpha1.HTTPSource{}
	if ref := extractAuthSecretRef("http", nil, nil, nil, nil, nil, anon); ref != nil {
		t.Errorf("anonymous http should return nil; got %+v", ref)
	}
}

// ─── materializeExternalRef tests (use envtest client.Client) ──────────

func TestMaterializeExternalRef_Success_StagesAndRenames(t *testing.T) {
	root := newCacheRoot(t)
	fake := &fakeFetcher{body: []byte("hello"), upstreamRev: "sha-abc"}
	// Kind="prompt" so the body is taken verbatim — bypasses the
	// Step 5.5 plugin content filter (issue #26) which only fires on
	// Kind="plugin" and would reject the raw "hello" bytes as
	// not-a-gzip. The test target here is the generic state machine
	// (stage/rename/cleanup), not plugin-specific filtering.
	finalPath := filepath.Join(root, "prompt", "mat-p1")

	deps := ExternalRefRefreshDeps{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		CacheRoot: root,
		Kind:      "prompt",
		Name:      "mat-p1",
		FinalPath: finalPath,
		Fetchers:  fakeFactory(fake),
		Refresh:   achv1alpha1.RefreshBlock{MaxStaleness: metav1.Duration{Duration: time.Hour}},
		Log:       logr.Discard(),
	}
	res := materializeExternalRef(context.Background(), deps)
	if res.Err != nil {
		t.Fatalf("materializeExternalRef returned err: %v", res.Err)
	}
	if res.NotModified {
		t.Errorf("expected NotModified=false")
	}
	if res.UpstreamRev != "sha-abc" {
		t.Errorf("UpstreamRev = %q; want %q", res.UpstreamRev, "sha-abc")
	}
	// The published prompt is now a 1-entry gzip tar, NOT raw bytes.
	published, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read finalPath: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(published))
	if err != nil {
		t.Fatalf("published file is not gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	if _, err := tr.Next(); err != nil {
		t.Fatalf("tar next: %v", err)
	}
	gotBody, _ := io.ReadAll(tr)
	if string(gotBody) != "hello" {
		t.Errorf("tar entry body=%q, want hello", gotBody)
	}
	// No orphan staging files left under .tmp/.
	tmpEntries, _ := os.ReadDir(filepath.Join(root, ".tmp"))
	if len(tmpEntries) != 0 {
		t.Errorf(".tmp/ left %d orphan(s): %v", len(tmpEntries), tmpEntries)
	}
}

func TestMaterializeExternalRef_NotModified(t *testing.T) {
	root := newCacheRoot(t)
	fake := &fakeFetcher{notModified: true, upstreamRev: "sha-prior"}
	finalPath := filepath.Join(root, "plugin", "mat-nm.tar.gz")

	deps := ExternalRefRefreshDeps{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		CacheRoot: root,
		Kind:      "plugin",
		Name:      "mat-nm",
		FinalPath: finalPath,
		PriorRev:  "sha-prior",
		Fetchers:  fakeFactory(fake),
		Refresh:   achv1alpha1.RefreshBlock{MaxStaleness: metav1.Duration{Duration: time.Hour}},
		Log:       logr.Discard(),
	}
	res := materializeExternalRef(context.Background(), deps)
	if res.Err != nil {
		t.Fatalf("materializeExternalRef err: %v", res.Err)
	}
	if !res.NotModified {
		t.Errorf("expected NotModified=true")
	}
	if res.UpstreamRev != "sha-prior" {
		t.Errorf("NotModified UpstreamRev = %q; want %q", res.UpstreamRev, "sha-prior")
	}
	if _, err := os.Stat(finalPath); err == nil {
		t.Errorf("NotModified branch wrote a file at %s", finalPath)
	}
	tmpEntries, _ := os.ReadDir(filepath.Join(root, ".tmp"))
	if len(tmpEntries) != 0 {
		t.Errorf(".tmp/ left %d orphan(s): %v", len(tmpEntries), tmpEntries)
	}
}

func TestMaterializeExternalRef_PluginTooLarge(t *testing.T) {
	root := newCacheRoot(t)
	fake := &fakeFetcher{body: bytes.Repeat([]byte("x"), 100), upstreamRev: "sha-big"}
	// Kind="artifact" so the body bytes flow through unmodified —
	// bypasses the Step 5.5 plugin content filter (issue #26) which
	// only fires on Kind="plugin". The OversizeError -> ReasonPluginTooLarge
	// mapping in classifyFetchError is kind-agnostic (it uses errors.As
	// on the typed OversizeError), so the assertion still covers the
	// size-cap state machine and reason classification.
	finalPath := filepath.Join(root, "artifact", "mat-big")

	deps := ExternalRefRefreshDeps{
		Client:       k8sClient,
		Namespace:    WatchNamespace,
		CacheRoot:    root,
		Kind:         "artifact",
		Name:         "mat-big",
		FinalPath:    finalPath,
		SizeCapBytes: 10,
		Fetchers:     fakeFactory(fake),
		Refresh:      achv1alpha1.RefreshBlock{MaxStaleness: metav1.Duration{Duration: time.Hour}},
		Log:          logr.Discard(),
	}
	res := materializeExternalRef(context.Background(), deps)
	if res.Err == nil {
		t.Fatal("expected oversize error; got nil")
	}
	oe, ok := res.Err.(*OversizeError)
	if !ok {
		t.Fatalf("expected *OversizeError; got %T: %v", res.Err, res.Err)
	}
	if oe.Cap != 10 {
		t.Errorf("Cap = %d; want 10", oe.Cap)
	}
	if oe.Bytes <= 10 {
		t.Errorf("Bytes = %d; expected > 10", oe.Bytes)
	}
	reason, _ := classifyFetchError(res.Err, deps.Refresh, time.Time{})
	if reason != ReasonPluginTooLarge {
		t.Errorf("classifyFetchError = %q; want %q", reason, ReasonPluginTooLarge)
	}
	// No file at finalPath; no orphan staging file.
	if _, err := os.Stat(finalPath); err == nil {
		t.Errorf("PluginTooLarge wrote a file at %s", finalPath)
	}
	tmpEntries, _ := os.ReadDir(filepath.Join(root, ".tmp"))
	if len(tmpEntries) != 0 {
		t.Errorf("PluginTooLarge left %d staging orphan(s)", len(tmpEntries))
	}
}

func TestMaterializeExternalRef_FetchError_Unauthorized(t *testing.T) {
	root := newCacheRoot(t)
	fake := &fakeFetcher{err: fmt.Errorf("missing token: %w", sources.ErrUnauthorized)}
	finalPath := filepath.Join(root, "plugin", "mat-auth.tar.gz")
	deps := ExternalRefRefreshDeps{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		CacheRoot: root,
		Kind:      "plugin",
		Name:      "mat-auth",
		FinalPath: finalPath,
		Fetchers:  fakeFactory(fake),
		Refresh:   achv1alpha1.RefreshBlock{MaxStaleness: metav1.Duration{Duration: time.Hour}},
		Log:       logr.Discard(),
	}
	res := materializeExternalRef(context.Background(), deps)
	if res.Err == nil {
		t.Fatal("expected unauthorized err")
	}
	reason, _ := classifyFetchError(res.Err, deps.Refresh, time.Time{})
	if reason != ReasonUnauthorized {
		t.Errorf("reason = %q; want %q", reason, ReasonUnauthorized)
	}
	if _, err := os.Stat(finalPath); err == nil {
		t.Errorf("Unauthorized branch wrote a file at %s", finalPath)
	}
}

func TestMaterializeExternalRef_FetchError_Unreachable(t *testing.T) {
	root := newCacheRoot(t)
	fake := &fakeFetcher{err: fmt.Errorf("dial tcp: %w", sources.ErrUnreachable)}
	deps := ExternalRefRefreshDeps{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		CacheRoot: root,
		Kind:      "plugin",
		Name:      "mat-unr",
		FinalPath: filepath.Join(root, "plugin", "mat-unr.tar.gz"),
		Fetchers:  fakeFactory(fake),
		Refresh:   achv1alpha1.RefreshBlock{MaxStaleness: metav1.Duration{Duration: time.Hour}},
		Log:       logr.Discard(),
	}
	res := materializeExternalRef(context.Background(), deps)
	if res.Err == nil {
		t.Fatal("expected unreachable err")
	}
	// Without prior successful refresh, no staleness escalation.
	reason, _ := classifyFetchError(res.Err, deps.Refresh, time.Time{})
	if reason != ReasonUnreachable {
		t.Errorf("reason = %q; want %q", reason, ReasonUnreachable)
	}
}

func TestMaterializeExternalRef_MissingSecret(t *testing.T) {
	root := newCacheRoot(t)
	fake := &fakeFetcher{body: []byte("never read"), upstreamRev: "x"}
	deps := ExternalRefRefreshDeps{
		Client:        k8sClient,
		Namespace:     WatchNamespace,
		CacheRoot:     root,
		Kind:          "plugin",
		Name:          "mat-no-sec",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{Name: "definitely-does-not-exist"},
		FinalPath:     filepath.Join(root, "plugin", "mat-no-sec.tar.gz"),
		Fetchers:      fakeFactory(fake),
		Refresh:       achv1alpha1.RefreshBlock{MaxStaleness: metav1.Duration{Duration: time.Hour}},
		Log:           logr.Discard(),
	}
	res := materializeExternalRef(context.Background(), deps)
	if res.Err == nil {
		t.Fatal("expected unauthorized err for missing Secret")
	}
	reason, _ := classifyFetchError(res.Err, deps.Refresh, time.Time{})
	if reason != ReasonUnauthorized {
		t.Errorf("missing Secret reason = %q; want %q", reason, ReasonUnauthorized)
	}
}

// ─── Plugin reconciler envtest: full Create → Reconcile cycle ──────────

// TestPluginReconciler_SteadyState_Success creates a Plugin CR via the
// envtest API server and exercises the steady-state §10.3 refresh end-
// to-end (finalizer add → fetch → stage → rename → status update). The
// suite_test.go reconciler is wired with Fetchers=nil so this test builds
// its OWN PluginReconciler with the fake factory and calls Reconcile
// directly — the suite-registered reconciler keeps running in the
// background but does not interfere because the fake one returns first.
//
// To avoid race conditions with the suite-registered reconciler (which
// also watches Plugin CRs), the test uses a unique name and waits for
// the suite reconciler to add the finalizer before invoking the fake
// reconciler manually.
func TestPluginReconciler_SteadyState_Success(t *testing.T) {
	ctx := context.Background()
	name := "ss-success-plugin"
	root := newCacheRoot(t)

	cr := &achv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.PluginSpec{
			Type: "github",
			GitHub: &achv1alpha1.GitHubSource{
				Repo: "ackstorm/example",
				Ref:  "main",
				AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
					Name: "github-readonly",
					Key:  "access-key",
				},
			},
			Refresh: achv1alpha1.RefreshBlock{
				Interval:     &metav1.Duration{Duration: time.Hour},
				MaxStaleness: metav1.Duration{Duration: 24 * time.Hour},
			},
		},
	}
	// Pre-seed the auth Secret so materializeExternalRef's Get succeeds.
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-readonly", Namespace: WatchNamespace},
		Data:       map[string][]byte{"access-key": []byte("token-xyz")},
	}
	if err := k8sClient.Create(ctx, authSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create auth secret: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), authSecret) })

	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Plugin: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cr)
		// Wait for the suite reconciler to drain the finalizer.
		probe := &achv1alpha1.Plugin{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: WatchNamespace}}
		WaitForGone(context.Background(), probe, 10*time.Second)
	})

	// Wait for finalizer add by suite reconciler.
	if !Eventually(func() bool {
		var got achv1alpha1.Plugin
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(&got, pluginFinalizer)
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatalf("suite reconciler never added finalizer")
	}

	// Now re-fetch and exercise OUR PluginReconciler with the fake
	// fetcher. The suite-registered reconciler will trip on the missing
	// auth-secret (it has no Fetchers set, so registry.For will run);
	// our reconciler exercises the §10.3 happy path.
	// Body is a real gzipped Plugin tarball so the Step 5.5 pluginpack
	// content filter (issue #26) accepts it; the assertion target here
	// is the reconciler state machine, not the filter behavior.
	fake := &fakeFetcher{body: minimalPluginTarGz(t), upstreamRev: "rev-hello"}
	r := &PluginReconciler{
		Client:    k8sClient,
		Scheme:    nil,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  fakeFactory(fake),
	}

	// Wait until the suite reconciler is between reconciles so our
	// direct Reconcile call doesn't fight an in-flight update. The
	// simplest stable strategy: poll until our Reconcile returns no err
	// AND the CR has SourceReachable=True+Synced. Multiple Reconcile
	// invocations are idempotent on the §10.3 path.
	ok := Eventually(func() bool {
		req := ctrlReq(cr)
		if _, err := r.Reconcile(ctx, req); err != nil {
			return false
		}
		var got achv1alpha1.Plugin
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "SourceReachable" && c.Status == metav1.ConditionTrue && c.Reason == ReasonSynced {
				return true
			}
		}
		return false
	}, 30*time.Second, 500*time.Millisecond)
	if !ok {
		t.Fatalf("never observed SourceReachable=True+Synced")
	}

	var got achv1alpha1.Plugin
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
		t.Fatalf("re-get Plugin: %v", err)
	}
	if got.Status.UpstreamRev != "rev-hello" {
		t.Errorf("status.UpstreamRev = %q; want %q", got.Status.UpstreamRev, "rev-hello")
	}
	wantPath := filepath.Join(root, "plugin", name+".tar.gz")
	if got.Status.StorageLocation != wantPath {
		t.Errorf("status.StorageLocation = %q; want %q", got.Status.StorageLocation, wantPath)
	}
	if got.Status.LastSuccessfulRefresh == nil {
		t.Errorf("status.LastSuccessfulRefresh is nil")
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("cached file missing at %s: %v", wantPath, err)
	}
}

// TestPluginReconciler_ForceRefreshAnnotation_Cleared verifies D-07:
// when the ach.ackstorm.ai/force-refresh annotation is present, a
// successful reconcile removes it.
func TestPluginReconciler_ForceRefreshAnnotation_Cleared(t *testing.T) {
	ctx := context.Background()
	name := "ss-forcerefresh-plugin"
	root := newCacheRoot(t)

	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-fr", Namespace: WatchNamespace},
		Data:       map[string][]byte{"access-key": []byte("token-fr")},
	}
	if err := k8sClient.Create(ctx, authSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create auth secret: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), authSecret) })

	cr := &achv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: WatchNamespace,
			Annotations: map[string]string{
				"ach.ackstorm.ai/force-refresh": "2026-05-15T12:00:00Z",
			},
		},
		Spec: achv1alpha1.PluginSpec{
			Type: "github",
			GitHub: &achv1alpha1.GitHubSource{
				Repo: "ackstorm/example",
				Ref:  "main",
				AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
					Name: "github-fr",
					Key:  "access-key",
				},
			},
			Refresh: achv1alpha1.RefreshBlock{
				Interval:     &metav1.Duration{Duration: time.Hour},
				MaxStaleness: metav1.Duration{Duration: 24 * time.Hour},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Plugin: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cr)
		probe := &achv1alpha1.Plugin{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: WatchNamespace}}
		WaitForGone(context.Background(), probe, 10*time.Second)
	})

	// Wait for finalizer add (suite reconciler).
	if !Eventually(func() bool {
		var got achv1alpha1.Plugin
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(&got, pluginFinalizer)
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatalf("finalizer never added")
	}

	// Body must be a valid Plugin tarball so the issue-#26 Step 5.5
	// filter accepts it; the assertion target is the force-refresh
	// annotation lifecycle, not the filter content.
	fake := &fakeFetcher{body: minimalPluginTarGz(t), upstreamRev: "rev-fr"}
	r := &PluginReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: root,
		Fetchers:  fakeFactory(fake),
	}

	// Drive the reconcile cycle to completion (force-refresh cleared +
	// SourceReachable=True+Synced).
	ok := Eventually(func() bool {
		req := ctrlReq(cr)
		if _, err := r.Reconcile(ctx, req); err != nil {
			return false
		}
		var got achv1alpha1.Plugin
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		_, hasAnn := got.Annotations["ach.ackstorm.ai/force-refresh"]
		return !hasAnn
	}, 30*time.Second, 500*time.Millisecond)
	if !ok {
		var got achv1alpha1.Plugin
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got)
		t.Fatalf("force-refresh annotation never removed; annotations=%v", got.Annotations)
	}
}

// ctrlReq builds a controller-runtime reconcile.Request for an object.
func ctrlReq(obj client.Object) ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
}
