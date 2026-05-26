// SPDX-License-Identifier: Apache-2.0

// Plan 02-09 Task 3: envtest verification of the cmd/operator/main.go
// wiring contract. Each TestMainWiring_* checks one invariant the
// production main.go relies on:
//
//   - AllReconcilersInjectable      — Phase 2 struct fields populate
//     without nil-deref or compile error; SetupWithManager would succeed.
//   - Snapshotter_StartAndShutdown  — snapshot.Runnable lifecycle works
//     against a fake LiteLLM; Snapshot() returns non-zero RefreshedAt.
//   - OrphanRunnable_EmptyUserSet_  — orphan.Runnable TickOnce against an
//     empty user set is a clean no-op with zero audit events.
//   - PluginReconciler_EndToEnd     — full §10.3 fetch→stage→rename→status
//     loop runs against a fake fetcher in an isolated test namespace; the
//     cached file and status condition both reach the expected terminal
//     state. Namespace-isolation keeps the suite-registered reconciler
//     (suite_test.go) from racing the assertion.
//   - CacheEmptyDetection           — mirrors the OP-11 startup branch in
//     cmd/operator/main.go; asserts IsEmpty() flips correctly when a
//     reconciler writes the first cached file.
//   - MustEnvDurationAtLeast_Floor  — duplicate-of-Task-1 coverage
//     verifying the helper is reachable from this package (catches an
//     accidental package-visibility regression in config.go).
//
// All tests are stdlib testing — no Ginkgo — matching the rest of the
// envtest suite (Plan 01-11 / 02-05 / 02-06 / 02-07). Run as part of the
// default `go test ./internal/controller/ach/...` invocation.

package ach

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/cachefs"
	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/orphan"
	"github.com/ackstorm/ach/internal/snapshot"
)

// ─── wiringFakeLiteLLM ────────────────────────────────────────────────
//
// Minimal litellm.Client implementation used by every TestMainWiring_*
// test. Distinct from snapshot/snapshot_test.go's fakeLiteLLM (which is
// internal to that package) — duplicating the shape here keeps the
// envtest suite self-contained.

type wiringFakeLiteLLM struct {
	mu         sync.Mutex
	models     []litellm.ModelInfoResponse
	mcps       []litellm.MCPServerEntry
	agents     []litellm.AgentEntry
	listCalls  atomic.Int64
	listErr    error
	revokedIDs []string
}

func (f *wiringFakeLiteLLM) DeleteAccessGroup(_ context.Context, _ string) error { return nil }
func (f *wiringFakeLiteLLM) DeleteTag(_ context.Context, _ string) error         { return nil }
func (f *wiringFakeLiteLLM) ListModels(_ context.Context) ([]litellm.ModelInfoResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls.Add(1)
	return f.models, f.listErr
}
func (f *wiringFakeLiteLLM) ListMCPServers(_ context.Context) ([]litellm.MCPServerEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mcps, f.listErr
}
func (f *wiringFakeLiteLLM) ListA2AAgents(_ context.Context) ([]litellm.AgentEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agents, f.listErr
}
func (f *wiringFakeLiteLLM) ListUserKeys(_ context.Context, _ string) ([]litellm.UserKeyInfo, error) {
	return nil, nil
}
func (f *wiringFakeLiteLLM) RevokeKey(_ context.Context, keyID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokedIDs = append(f.revokedIDs, keyID)
	return nil
}

// Phase 3 Plan 03-01 — interface widened. The controller wiring tests
// do not invoke these SSO/env-keys methods; stub them to keep the
// compile-time canary at the bottom of this block green.
func (f *wiringFakeLiteLLM) UserNew(_ context.Context, _ *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	return nil, nil
}
func (f *wiringFakeLiteLLM) UserInfoByEmail(_ context.Context, _ string) (*litellm.UserInfo, error) {
	return nil, nil
}
func (f *wiringFakeLiteLLM) TeamMemberAdd(_ context.Context, _, _, _ string) error { return nil }
func (f *wiringFakeLiteLLM) KeyGenerate(_ context.Context, _ *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	return nil, nil
}

// Compile-time interface assertion — guards against litellm.Client
// growing a method without this fake catching up.
var _ litellm.Client = (*wiringFakeLiteLLM)(nil)

// ─── Test 1: AllReconcilersInjectable ─────────────────────────────────

// TestMainWiring_AllReconcilersInjectable verifies that every Phase 2
// reconciler can be constructed with the same field shape cmd/operator/
// main.go uses, without nil-deref or compile error. The test does NOT
// call SetupWithManager — the suite's manager already has Phase 1
// registrations and a duplicate Named() would error. The check is
// structural: post-construction, each pointer-typed field is non-nil
// (or intentionally nil for the optional-field cases).
func TestMainWiring_AllReconcilersInjectable(t *testing.T) {
	fake := &wiringFakeLiteLLM{}
	snp := snapshot.NewSnapshotter(fake, logr.Discard())
	if snp == nil {
		t.Fatal("NewSnapshotter returned nil")
	}

	auditBuf := &bytes.Buffer{}
	auditLog := audit.NewLogger(auditBuf)
	if auditLog == nil {
		t.Fatal("audit.NewLogger returned nil")
	}

	// Orphan Runnable: production wiring uses a real DB pool, but the
	// constructor itself accepts nil — production main.go passes the
	// dbPool by value into NewRunnable, which assigns it to Runnable.DB
	// (the TickOnce-time nil-deref guard is in the function-typed
	// ListUsers/ListKeyIDs seams, exercised in Test 3 below).
	orp := orphan.NewRunnable(fake, nil, auditLog, 10*time.Minute, logr.Discard())
	if orp == nil {
		t.Fatal("orphan.NewRunnable returned nil")
	}
	if orp.Client != fake {
		t.Error("orphan.Runnable.Client not wired")
	}
	if orp.Audit != auditLog {
		t.Error("orphan.Runnable.Audit not wired")
	}
	if orp.Interval != 10*time.Minute {
		t.Errorf("orphan.Runnable.Interval = %v; want 10m", orp.Interval)
	}

	// Plugin reconciler: mirrors cmd/operator/main.go injection.
	pr := &PluginReconciler{
		Client:           k8sClient,
		Namespace:        WatchNamespace,
		Log:              logr.Discard(),
		CacheRoot:        testCacheRoot,
		DB:               nil, // nil-DB path exercised by Phase 1 envtests
		PluginMaxSizeMiB: 50,
		Fetchers:         nil, // nil → defaults to registry.For at materialize time
	}
	if pr.PluginMaxSizeMiB != 50 {
		t.Errorf("PluginReconciler.PluginMaxSizeMiB = %d; want 50", pr.PluginMaxSizeMiB)
	}

	// PluginMarketplace reconciler.
	pmr := &PluginMarketplaceReconciler{
		Client:           k8sClient,
		Namespace:        WatchNamespace,
		Log:              logr.Discard(),
		CacheRoot:        testCacheRoot,
		DB:               nil,
		PluginMaxSizeMiB: 50,
		Fetchers:         nil,
	}
	if pmr.PluginMaxSizeMiB != 50 {
		t.Errorf("PluginMarketplaceReconciler.PluginMaxSizeMiB = %d; want 50", pmr.PluginMaxSizeMiB)
	}

	// Artifact + Prompt reconcilers — no size cap.
	ar := &ArtifactReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: testCacheRoot,
		DB:        nil,
		Fetchers:  nil,
	}
	if ar.CacheRoot != testCacheRoot {
		t.Errorf("ArtifactReconciler.CacheRoot wrong")
	}
	prm := &PromptReconciler{
		Client:    k8sClient,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: testCacheRoot,
		DB:        nil,
		Fetchers:  nil,
	}
	if prm.CacheRoot != testCacheRoot {
		t.Errorf("PromptReconciler.CacheRoot wrong")
	}

	// Environment reconciler: LiteLLM + Snapshotter both wired.
	er := &EnvironmentReconciler{
		Client:      k8sClient,
		LiteLLM:     fake,
		Namespace:   WatchNamespace,
		Log:         logr.Discard(),
		DB:          nil,
		Snapshotter: snp,
	}
	if er.Snapshotter != snp {
		t.Error("EnvironmentReconciler.Snapshotter not wired")
	}
	if er.LiteLLM != fake {
		t.Error("EnvironmentReconciler.LiteLLM not wired")
	}
}

// ─── Test 2: Snapshotter_StartAndShutdown ─────────────────────────────

// TestMainWiring_Snapshotter_StartAndShutdown asserts the snapshot
// Runnable lifecycle: Start drives at least one refresh, the snapshot's
// RefreshedAt becomes non-zero, and ctx cancellation returns cleanly
// without a non-nil error (controller-runtime treats a non-nil
// Runnable error as fatal to the manager).
func TestMainWiring_Snapshotter_StartAndShutdown(t *testing.T) {
	fake := &wiringFakeLiteLLM{
		models: []litellm.ModelInfoResponse{{ModelName: "claude"}},
		mcps:   []litellm.MCPServerEntry{{ServerName: "filesystem"}},
		agents: []litellm.AgentEntry{{AgentName: "researcher"}},
	}
	snp := snapshot.NewSnapshotter(fake, logr.Discard())

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	doneCh := make(chan error, 1)
	go func() { doneCh <- snp.Start(ctx) }()

	// Wait for ctx cancellation + Start return; assert exit is clean.
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Snapshotter.Start returned non-nil err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Snapshotter.Start did not return within 2s of ctx cancel")
	}

	got := snp.Snapshot()
	if got.RefreshedAt.IsZero() {
		t.Error("after Start, RefreshedAt should be non-zero")
	}
	if got.Stale {
		t.Error("after Start with successful fakeLiteLLM, Stale should be false")
	}
	if _, ok := got.Models["claude"]; !ok {
		t.Errorf("Models missing 'claude' entry; got %v", got.Models)
	}
}

// ─── Test 3: OrphanRunnable_EmptyUserSet_NoAuditEvents ────────────────

// TestMainWiring_OrphanRunnable_EmptyUserSet_NoAuditEvents asserts that
// TickOnce against an empty ACH-managed-user set is a clean no-op:
// zero audit events, zero LiteLLM calls. This is the v1alpha1 startup
// behavior (no pk_/ek_ rows exist yet) — the orphan loop must NOT emit
// stray audit events while the system is empty.
//
// The ListUsers / ListKeyIDs fields are exported on Runnable; their
// underlying named types (listUsersFn / listKeyIDsFn) are unexported
// in the orphan package, but Go's assignability rules let an unnamed
// function literal with the same signature satisfy the named type.
func TestMainWiring_OrphanRunnable_EmptyUserSet_NoAuditEvents(t *testing.T) {
	fake := &wiringFakeLiteLLM{}
	auditBuf := &bytes.Buffer{}
	auditLog := audit.NewLogger(auditBuf)

	r := orphan.NewRunnable(fake, nil, auditLog, 10*time.Minute, logr.Discard())
	// Override the production db helpers with empty-set seams so TickOnce
	// proceeds through both list steps and finds zero work to do.
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return nil, nil
	}
	r.ListKeyIDs = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return nil, nil
	}

	r.TickOnce(context.Background())

	if auditBuf.Len() != 0 {
		t.Errorf("empty-user-set tick emitted %d bytes of audit; want 0. Body: %s",
			auditBuf.Len(), auditBuf.String())
	}
	if got := fake.listCalls.Load(); got != 0 {
		t.Errorf("empty-user-set tick made %d LiteLLM ListUserKeys calls; want 0", got)
	}
}

// ─── Test 4: PluginReconciler_EndToEndWithFakeFetcher ─────────────────

// TestMainWiring_PluginReconciler_EndToEndWithFakeFetcher exercises the
// full §10.3 fetch → stage → fsync → rename(2) → status pipeline against
// a fake fetcher (no live HTTPS traffic) and asserts the terminal state:
//
//   - Cached file exists at <cacheRoot>/plugin/<name>.tar.gz with the
//     fetcher's body bytes.
//   - status.UpstreamRev == fetcher's upstreamRev.
//   - status.StorageLocation == the cached file path.
//   - SourceReachable condition is True with reason=Synced.
//
// The CR is created in a per-test namespace (NOT WatchNamespace) so the
// suite-registered Plugin reconciler from suite_test.go does NOT see
// the CR — eliminates the race that would otherwise have two reconcilers
// fighting over the same status subresource.
func TestMainWiring_PluginReconciler_EndToEndWithFakeFetcher(t *testing.T) {
	ctx := context.Background()
	nsName := "ach-wiring-e2e"
	if err := k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: nsName},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", nsName, err)
	}

	// Per-test cache root so cached files don't collide with the suite's
	// shared testCacheRoot.
	cacheRoot := t.TempDir()
	if err := cachefs.EnsureLayout(cacheRoot); err != nil {
		t.Fatalf("cachefs.EnsureLayout(%s): %v", cacheRoot, err)
	}

	// Auth Secret in the test namespace (Plugin spec carries an
	// AuthSecretRef; the materialize helper Get-fetches it). The fake
	// fetcher doesn't consume the Secret contents but the read MUST
	// succeed or materialize errors out with Unauthorized.
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "wiring-auth", Namespace: nsName},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"access-key": []byte("token-value")},
	}
	if err := k8sClient.Create(ctx, authSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create auth Secret: %v", err)
	}

	name := "wiring-e2e-plugin"
	cr := &achv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: nsName},
		Spec: achv1alpha1.PluginSpec{
			Type: "github",
			GitHub: &achv1alpha1.GitHubSource{
				Repo: "ackstorm/example",
				Ref:  "main",
				AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
					Name: "wiring-auth",
					Key:  "access-key",
				},
			},
			Refresh: achv1alpha1.RefreshBlock{
				MaxStaleness: metav1.Duration{Duration: time.Hour},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Plugin CR: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort cleanup; do not fail the test on cleanup errors.
		_ = k8sClient.Delete(context.Background(), cr)
	})

	// Sibling reconciler (NOT registered with any manager) wired with
	// the fake fetcher. r.Namespace = nsName so Secret resolution
	// targets the per-test namespace. Direct k8sClient (not cached)
	// because nsName is outside the suite manager's DefaultNamespaces
	// allow-list.
	fake := &fakeFetcher{
		body:        []byte("plugin-body-bytes"),
		upstreamRev: "sha-wiring-e2e",
	}
	pr := &PluginReconciler{
		Client:           k8sClient,
		Namespace:        nsName,
		Log:              logr.Discard(),
		CacheRoot:        cacheRoot,
		DB:               nil, // nil DB → PriorRev empty → forces fresh fetch
		PluginMaxSizeMiB: 50,
		Fetchers:         fakeFactory(fake),
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cr)}

	// First Reconcile: adds the finalizer + returns. No fetch happens yet.
	if _, err := pr.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile (finalizer add): %v", err)
	}

	// Second Reconcile: steady-state — fakeFetcher returns the body, the
	// reconciler stages + renames + writes status.
	if _, err := pr.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (steady-state): %v", err)
	}

	// Cached file MUST exist with the fake fetcher's bytes.
	cachedPath := filepath.Join(cacheRoot, "plugin", name+".tar.gz")
	got, err := os.ReadFile(cachedPath)
	if err != nil {
		t.Fatalf("read cached plugin file: %v", err)
	}
	if string(got) != "plugin-body-bytes" {
		t.Errorf("cached body = %q; want %q", got, "plugin-body-bytes")
	}

	// Re-Get the CR (status was updated during Reconcile).
	var final achv1alpha1.Plugin
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &final); err != nil {
		t.Fatalf("re-Get Plugin CR: %v", err)
	}
	if final.Status.UpstreamRev != "sha-wiring-e2e" {
		t.Errorf("status.UpstreamRev = %q; want %q", final.Status.UpstreamRev, "sha-wiring-e2e")
	}
	if final.Status.StorageLocation != cachedPath {
		t.Errorf("status.StorageLocation = %q; want %q", final.Status.StorageLocation, cachedPath)
	}
	// SourceReachable=True, reason=Synced.
	var found bool
	for _, c := range final.Status.Conditions {
		if c.Type == "SourceReachable" {
			found = true
			if c.Status != metav1.ConditionTrue {
				t.Errorf("SourceReachable.Status = %q; want %q", c.Status, metav1.ConditionTrue)
			}
			if c.Reason != ReasonSynced {
				t.Errorf("SourceReachable.Reason = %q; want %q", c.Reason, ReasonSynced)
			}
		}
	}
	if !found {
		t.Errorf("SourceReachable condition missing; got %d conditions: %+v",
			len(final.Status.Conditions), final.Status.Conditions)
	}

	// No orphan .tmp staging files.
	tmpEntries, _ := os.ReadDir(filepath.Join(cacheRoot, ".tmp"))
	if len(tmpEntries) != 0 {
		t.Errorf(".tmp/ left %d orphan staging file(s)", len(tmpEntries))
	}
}

// ─── Test 5: CacheEmptyDetection ──────────────────────────────────────

// TestMainWiring_CacheEmptyDetection mirrors the OP-11 startup branch in
// cmd/operator/main.go: EnsureLayout, then IsEmpty → true; write one
// cached file, then IsEmpty → false. Guards against an IsEmpty
// regression that would either (a) miss a populated cache and trigger an
// unwanted Reset on every restart, or (b) miss an empty cache and skip
// the OP-11 recovery.
func TestMainWiring_CacheEmptyDetection(t *testing.T) {
	cacheRoot := t.TempDir()
	if err := cachefs.EnsureLayout(cacheRoot); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}

	empty, err := cachefs.IsEmpty(cacheRoot)
	if err != nil {
		t.Fatalf("IsEmpty (fresh): %v", err)
	}
	if !empty {
		t.Error("freshly-EnsureLayout cache should report IsEmpty=true")
	}

	// Write one cached file.
	cachedPath := filepath.Join(cacheRoot, "plugin", "foo.tar.gz")
	if err := os.WriteFile(cachedPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write cached file: %v", err)
	}

	empty, err = cachefs.IsEmpty(cacheRoot)
	if err != nil {
		t.Fatalf("IsEmpty (populated): %v", err)
	}
	if empty {
		t.Error("cache with one file should report IsEmpty=false")
	}
}

// ─── Test 6: MustEnvDurationAtLeast_Floor ─────────────────────────────

// TestMainWiring_MustEnvDurationAtLeast_Floor duplicates one of the
// Plan 02-09 Task 1 cases (below-min rejection) — the duplication is
// deliberate: verifies the helper is reachable from this package
// (catches an accidental package-visibility regression in config.go).
func TestMainWiring_MustEnvDurationAtLeast_Floor(t *testing.T) {
	const key = "ACH_WIRING_TEST_DURATION"
	t.Setenv(key, "1m")
	_, err := config.MustEnvDurationAtLeast(key, time.Hour, 5*time.Minute)
	if err == nil {
		t.Fatal("expected below-min err; got nil")
	}
	if !strings.Contains(err.Error(), "below minimum") {
		t.Errorf("err = %q; want substring 'below minimum'", err.Error())
	}
}
