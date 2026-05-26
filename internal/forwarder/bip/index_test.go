// SPDX-License-Identifier: Apache-2.0

// Plan 04-04 Task 1 — envtest coverage for the BIP request-time indexer.
//
// Coverage matrix (B1-B12 from PLAN.md):
//   B1  — RegisterIndex on a fresh manager succeeds; the post-registration
//         MatchingFields query does NOT fail with "field not indexed".
//   B2  — Zero BIPs → ResolveWinner returns nil.
//   B3  — Single opt-in BIP → ResolveWinner returns it.
//   B4  — Single opt-out BIP → ResolveWinner returns nil (explicit opt-out).
//   B5  — Two opt-in BIPs {a,b} → ResolveWinner returns "b" (alpha-LAST).
//   B6  — {a:opt-in, b:opt-out} → ResolveWinner returns nil (LAST is b/opt-out).
//   B7  — {a:opt-out, b:opt-in} → ResolveWinner returns "b" (LAST opt-in).
//   B8  — Three opt-in {a,m,z} → ResolveWinner returns "z".
//   B9  — Rename idiom: {foo:opt-in, zz-foo-override:opt-out} → nil
//         (the "zz-" rename flips precedence per TODO §6).
//   B10 — (MCPServer,foo) and (A2AAgent,foo) are independent tuples.
//   B11 — BIPs in another namespace do NOT leak into the queried namespace.
//   B12 — Source-grep: index.go MUST NOT read .Status and MUST NOT mention
//         DuplicateTarget (excluding comment-only lines) — enforces OP-16.
//
// The package uses stdlib `testing` with a self-contained TestMain that
// boots a kubebuilder envtest, registers the field index via the
// production RegisterIndex helper, and shares a single manager+client
// across all sub-tests. Each test creates BIPs in a unique namespace to
// avoid cross-test pollution; t.Cleanup deletes them.

package bip_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/forwarder/bip"
)

// Test-global state shared across the suite (populated by TestMain). The
// shared envtest is started ONCE per process to amortize the ~5s boot
// across all twelve sub-tests; each test owns its own manager + namespaces
// so cache state never leaks between tests.
var (
	testEnv   *envtest.Environment
	cfg       *rest.Config
	rawClient client.Client // direct (non-cache) client for CRUD fixtures
	nsCounter atomic.Int64
)

// suiteScheme is the runtime.Scheme registered with both envtest and
// every per-test manager.
var suiteScheme = k8sruntime.NewScheme()

func TestMain(m *testing.M) {
	// Resolve envtest binaries. KUBEBUILDER_ASSETS may already be set by
	// the Makefile's envtest-pkg target. Otherwise probe the standard paths
	// the ach-devtools image (Dockerfile.devtools) provides.
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if found := findEnvtestAssets(); found != "" {
			_ = os.Setenv("KUBEBUILDER_ASSETS", found)
		}
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(os.Stderr)))

	if code := setupAndRun(m); code != 0 {
		os.Exit(code)
	}
}

func setupAndRun(m *testing.M) int {
	// CRD path: <repo-root>/config/crd/bases — three dirs up from
	// internal/forwarder/bip/.
	_, thisFile, _, _ := runtime.Caller(0)
	crdDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "config", "crd", "bases")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdDir},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest start failed: %v\n", err)
		return 1
	}
	defer func() {
		if err := testEnv.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "envtest stop: %v\n", err)
		}
	}()

	utilruntime.Must(clientgoscheme.AddToScheme(suiteScheme))
	utilruntime.Must(achv1alpha1.AddToScheme(suiteScheme))

	rawClient, err = client.New(cfg, client.Options{Scheme: suiteScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "k8s raw client: %v\n", err)
		return 1
	}

	return m.Run()
}

// startManager spins up a per-test controller-runtime manager scoped to
// the given namespaces, registers the BIP target index via
// bip.RegisterIndex (the production helper — RegisterIndex is exercised
// implicitly by every test that calls ResolveWinner), and waits for cache
// sync. Returns the cache-backed client + a cancel that stops the manager.
func startManager(t *testing.T, ctx context.Context, namespaces []string) (client.Client, func()) {
	t.Helper()

	nsConfig := make(map[string]cache.Config, len(namespaces))
	for _, ns := range namespaces {
		nsConfig[ns] = cache.Config{}
	}
	mgr, err := manager.New(cfg, manager.Options{
		Scheme:                 suiteScheme,
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		Cache: cache.Options{
			DefaultNamespaces: nsConfig,
		},
	})
	if err != nil {
		t.Fatalf("manager.New: %v", err)
	}

	// EXERCISE B1 — register the field indexer BEFORE GetInformer. If the
	// production code's RegisterIndex returns error or the cache rejects
	// the registration, every subsequent test fails fast.
	if err := bip.RegisterIndex(ctx, mgr); err != nil {
		t.Fatalf("bip.RegisterIndex: %v", err)
	}

	// Force informer creation for BackendIdentityPolicy so the cache
	// populates before WaitForCacheSync.
	if _, err := mgr.GetCache().GetInformer(ctx, &achv1alpha1.BackendIdentityPolicy{}); err != nil {
		t.Fatalf("GetInformer(BackendIdentityPolicy): %v", err)
	}

	mgrCtx, mgrCancel := context.WithCancel(ctx)
	mgrDone := make(chan error, 1)
	go func() { mgrDone <- mgr.Start(mgrCtx) }()

	syncCtx, syncCancel := context.WithTimeout(mgrCtx, 30*time.Second)
	defer syncCancel()
	if !mgr.GetCache().WaitForCacheSync(syncCtx) {
		mgrCancel()
		t.Fatalf("manager cache failed to sync within 30s")
	}

	cleanup := func() {
		mgrCancel()
		select {
		case <-mgrDone:
		case <-time.After(5 * time.Second):
		}
	}
	return mgr.GetClient(), cleanup
}

// newNS allocates a fresh test namespace name and creates it. Cleanup
// deletes the namespace at test end.
func newNS(t *testing.T, ctx context.Context) string {
	t.Helper()
	n := nsCounter.Add(1)
	ns := fmt.Sprintf("bip-test-%d", n)
	if err := rawClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}); err != nil {
		t.Fatalf("create namespace %s: %v", ns, err)
	}
	t.Cleanup(func() {
		_ = rawClient.Delete(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})
	})
	return ns
}

// mkBIP creates a BackendIdentityPolicy in the given namespace. forward is
// the spec.forwardIdentityJWT value.
func mkBIP(t *testing.T, ctx context.Context, ns, name, kind, target string, forward bool) {
	t.Helper()
	bipCR := &achv1alpha1.BackendIdentityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: achv1alpha1.BackendIdentityPolicySpec{
			Target: achv1alpha1.BackendTargetRef{
				Kind: kind,
				Name: target,
			},
			ForwardIdentityJWT: forward,
		},
	}
	if err := rawClient.Create(ctx, bipCR); err != nil {
		t.Fatalf("create BIP %s/%s: %v", ns, name, err)
	}
}

// waitForCachedBIPCount polls the cached client until the namespace-scoped
// list returns the expected count, bounded to 5s with 100ms intervals.
// Informer caches can lag a few hundred ms behind the raw apiserver.
func waitForCachedBIPCount(t *testing.T, ctx context.Context, c client.Client, ns string, want int) {
	t.Helper()
	pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := wait.PollUntilContextTimeout(pollCtx, 100*time.Millisecond, 5*time.Second, true,
		func(ctx context.Context) (bool, error) {
			var list achv1alpha1.BackendIdentityPolicyList
			if err := c.List(ctx, &list, client.InNamespace(ns)); err != nil {
				return false, nil
			}
			return len(list.Items) == want, nil
		})
	if err != nil {
		t.Fatalf("cache did not populate to %d BIPs in %s within 5s: %v", want, ns, err)
	}
}

// --- B1 ----------------------------------------------------------------

// TestBIP_B1_RegisterIndex_PostQuery confirms RegisterIndex registered the
// "spec.target" field; immediately querying via MatchingFields succeeds
// (would error with "field not indexed" otherwise).
func TestBIP_B1_RegisterIndex_PostQuery(t *testing.T) {
	ctx := context.Background()
	ns := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{ns})
	defer cancel()

	var list achv1alpha1.BackendIdentityPolicyList
	if err := c.List(ctx, &list,
		client.MatchingFields{bip.TargetIndexKey: "MCPServer/anything"},
		client.InNamespace(ns)); err != nil {
		t.Fatalf("MatchingFields list failed (field not indexed?): %v", err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("expected 0 BIPs in empty namespace, got %d", len(list.Items))
	}
}

// --- B2 ----------------------------------------------------------------

// TestBIP_B2_ZeroBIPs_NilWinner — no BIPs in namespace → nil.
func TestBIP_B2_ZeroBIPs_NilWinner(t *testing.T) {
	ctx := context.Background()
	ns := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{ns})
	defer cancel()

	if got := bip.ResolveWinner(ctx, c, "MCPServer", "foo", ns); got != nil {
		t.Fatalf("expected nil winner with zero BIPs, got %s", got.Name)
	}
}

// --- B3 ----------------------------------------------------------------

// TestBIP_B3_SingleOptIn_ReturnsThatBIP — single opt-in → return it.
func TestBIP_B3_SingleOptIn_ReturnsThatBIP(t *testing.T) {
	ctx := context.Background()
	ns := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{ns})
	defer cancel()

	mkBIP(t, ctx, ns, "a", "MCPServer", "foo", true)
	waitForCachedBIPCount(t, ctx, c, ns, 1)

	got := bip.ResolveWinner(ctx, c, "MCPServer", "foo", ns)
	if got == nil {
		t.Fatal("expected non-nil winner for single opt-in BIP, got nil")
	}
	if got.Name != "a" {
		t.Fatalf("expected winner.Name=a, got %s", got.Name)
	}
}

// --- B4 ----------------------------------------------------------------

// TestBIP_B4_SingleOptOut_ReturnsNil — single opt-out (forwardIdentityJWT=false)
// is the explicit opt-out path; ResolveWinner MUST return nil.
func TestBIP_B4_SingleOptOut_ReturnsNil(t *testing.T) {
	ctx := context.Background()
	ns := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{ns})
	defer cancel()

	mkBIP(t, ctx, ns, "a", "MCPServer", "foo", false)
	waitForCachedBIPCount(t, ctx, c, ns, 1)

	if got := bip.ResolveWinner(ctx, c, "MCPServer", "foo", ns); got != nil {
		t.Fatalf("expected nil for opt-out winner, got %s", got.Name)
	}
}

// --- B5 ----------------------------------------------------------------

// TestBIP_B5_TwoOptIn_AlphaLastWins — {a:on, b:on} → "b" wins alphabetically.
func TestBIP_B5_TwoOptIn_AlphaLastWins(t *testing.T) {
	ctx := context.Background()
	ns := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{ns})
	defer cancel()

	mkBIP(t, ctx, ns, "a", "MCPServer", "foo", true)
	mkBIP(t, ctx, ns, "b", "MCPServer", "foo", true)
	waitForCachedBIPCount(t, ctx, c, ns, 2)

	got := bip.ResolveWinner(ctx, c, "MCPServer", "foo", ns)
	if got == nil {
		t.Fatal("expected non-nil winner, got nil")
	}
	if got.Name != "b" {
		t.Fatalf("alpha-LAST: expected winner.Name=b, got %s", got.Name)
	}
}

// --- B6 ----------------------------------------------------------------

// TestBIP_B6_TwoBIPs_LastOptOut_Nil — {a:on, b:off}: LAST is b (opt-out) → nil.
func TestBIP_B6_TwoBIPs_LastOptOut_Nil(t *testing.T) {
	ctx := context.Background()
	ns := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{ns})
	defer cancel()

	mkBIP(t, ctx, ns, "a", "MCPServer", "foo", true)
	mkBIP(t, ctx, ns, "b", "MCPServer", "foo", false)
	waitForCachedBIPCount(t, ctx, c, ns, 2)

	if got := bip.ResolveWinner(ctx, c, "MCPServer", "foo", ns); got != nil {
		t.Fatalf("expected nil (LAST=b is opt-out), got %s", got.Name)
	}
}

// --- B7 ----------------------------------------------------------------

// TestBIP_B7_TwoBIPs_LastOptIn — {a:off, b:on}: LAST is b (opt-in) → "b".
func TestBIP_B7_TwoBIPs_LastOptIn(t *testing.T) {
	ctx := context.Background()
	ns := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{ns})
	defer cancel()

	mkBIP(t, ctx, ns, "a", "MCPServer", "foo", false)
	mkBIP(t, ctx, ns, "b", "MCPServer", "foo", true)
	waitForCachedBIPCount(t, ctx, c, ns, 2)

	got := bip.ResolveWinner(ctx, c, "MCPServer", "foo", ns)
	if got == nil {
		t.Fatal("expected non-nil winner, got nil")
	}
	if got.Name != "b" {
		t.Fatalf("expected winner.Name=b, got %s", got.Name)
	}
}

// --- B8 ----------------------------------------------------------------

// TestBIP_B8_ThreeOptIn_AlphaLastZ — {a,m,z} all opt-in → "z".
func TestBIP_B8_ThreeOptIn_AlphaLastZ(t *testing.T) {
	ctx := context.Background()
	ns := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{ns})
	defer cancel()

	mkBIP(t, ctx, ns, "a", "MCPServer", "foo", true)
	mkBIP(t, ctx, ns, "m", "MCPServer", "foo", true)
	mkBIP(t, ctx, ns, "z", "MCPServer", "foo", true)
	waitForCachedBIPCount(t, ctx, c, ns, 3)

	got := bip.ResolveWinner(ctx, c, "MCPServer", "foo", ns)
	if got == nil {
		t.Fatal("expected non-nil winner, got nil")
	}
	if got.Name != "z" {
		t.Fatalf("expected winner.Name=z, got %s", got.Name)
	}
}

// --- B9 ----------------------------------------------------------------

// TestBIP_B9_RenameFlip_ZZPrefix_OptOut — the canonical TODO §6 idiom:
// an operator wants to suppress JWT minting on /mcp/foo even though a
// prior foo:opt-in BIP exists, so they create zz-foo-override:opt-out.
// "zz-foo-override" > "foo" lexicographically → it is the alpha-LAST
// winner → opt-out → ResolveWinner returns nil.
func TestBIP_B9_RenameFlip_ZZPrefix_OptOut(t *testing.T) {
	ctx := context.Background()
	ns := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{ns})
	defer cancel()

	mkBIP(t, ctx, ns, "foo", "MCPServer", "foo", true)
	mkBIP(t, ctx, ns, "zz-foo-override", "MCPServer", "foo", false)
	waitForCachedBIPCount(t, ctx, c, ns, 2)

	if got := bip.ResolveWinner(ctx, c, "MCPServer", "foo", ns); got != nil {
		t.Fatalf("rename-flip: expected nil (zz- prefix opt-out wins), got %s", got.Name)
	}
}

// --- B10 ---------------------------------------------------------------

// TestBIP_B10_KindIndependence — BIPs for (MCPServer,foo) and (A2AAgent,foo)
// do NOT interact. Querying MCPServer/foo returns only the MCPServer BIP;
// querying A2AAgent/foo returns only the A2AAgent BIP.
func TestBIP_B10_KindIndependence(t *testing.T) {
	ctx := context.Background()
	ns := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{ns})
	defer cancel()

	mkBIP(t, ctx, ns, "mcp-only", "MCPServer", "foo", true)
	mkBIP(t, ctx, ns, "a2a-only", "A2AAgent", "foo", true)
	waitForCachedBIPCount(t, ctx, c, ns, 2)

	mcp := bip.ResolveWinner(ctx, c, "MCPServer", "foo", ns)
	if mcp == nil || mcp.Name != "mcp-only" {
		t.Fatalf("expected MCPServer/foo → mcp-only, got %+v", mcp)
	}
	a2a := bip.ResolveWinner(ctx, c, "A2AAgent", "foo", ns)
	if a2a == nil || a2a.Name != "a2a-only" {
		t.Fatalf("expected A2AAgent/foo → a2a-only, got %+v", a2a)
	}
}

// --- B11 ---------------------------------------------------------------

// TestBIP_B11_NamespaceScoping — A BIP in namespace "other" does NOT appear
// in a query against namespace "primary". Verified via two namespaces, two
// BIPs (same target tuple, different namespaces), query each in turn.
func TestBIP_B11_NamespaceScoping(t *testing.T) {
	ctx := context.Background()
	nsPrimary := newNS(t, ctx)
	nsOther := newNS(t, ctx)
	c, cancel := startManager(t, ctx, []string{nsPrimary, nsOther})
	defer cancel()

	mkBIP(t, ctx, nsPrimary, "in-primary", "MCPServer", "foo", true)
	mkBIP(t, ctx, nsOther, "in-other", "MCPServer", "foo", true)
	waitForCachedBIPCount(t, ctx, c, nsPrimary, 1)
	waitForCachedBIPCount(t, ctx, c, nsOther, 1)

	gotPrimary := bip.ResolveWinner(ctx, c, "MCPServer", "foo", nsPrimary)
	if gotPrimary == nil || gotPrimary.Name != "in-primary" {
		t.Fatalf("expected primary → in-primary, got %+v", gotPrimary)
	}
	gotOther := bip.ResolveWinner(ctx, c, "MCPServer", "foo", nsOther)
	if gotOther == nil || gotOther.Name != "in-other" {
		t.Fatalf("expected other → in-other, got %+v", gotOther)
	}
}

// --- B12 ---------------------------------------------------------------

// TestBIP_B12_SourceContractNoStatusNoDuplicateTarget enforces OP-16 at
// the source level: index.go MUST NOT read .Status, and MUST NOT mention
// DuplicateTarget. Comment-only lines are exempt (per the PLAN.md
// acceptance criterion `grep -v '^//'`).
func TestBIP_B12_SourceContractNoStatusNoDuplicateTarget(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	srcPath := filepath.Join(filepath.Dir(thisFile), "index.go")
	f, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("open %s: %v", srcPath, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		// Skip pure comment lines (mirrors `grep -v '^//'` after trim).
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(line, ".Status") {
			t.Fatalf("index.go:%d non-comment line reads .Status (OP-16 violation): %s", lineNo, line)
		}
		if strings.Contains(line, "DuplicateTarget") {
			t.Fatalf("index.go:%d non-comment line mentions DuplicateTarget (TODO §6 violation): %s", lineNo, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", srcPath, err)
	}
}

// --- envtest helpers ---------------------------------------------------

// findEnvtestAssets — same probes as internal/controller/ach/suite_test.go.
func findEnvtestAssets() string {
	candidates := []string{
		"/opt/envtest/k8s/1.31.0-linux-amd64",
		"/opt/envtest/k8s/1.31.0-linux-arm64",
		"/opt/envtest/k8s",
		"/workspace/.gocache/envtest/k8s/1.31.0-linux-amd64",
		"/workspace/.gocache/envtest/k8s/1.31.0-linux-arm64",
	}
	for _, c := range candidates {
		if isExecutable(filepath.Join(c, "kube-apiserver")) {
			return c
		}
	}
	for _, root := range []string{
		"/workspace/.gocache/envtest/k8s/*",
		"/opt/envtest/k8s/*",
	} {
		if matches, err := filepath.Glob(root); err == nil {
			for _, m := range matches {
				if isExecutable(filepath.Join(m, "kube-apiserver")) {
					return m
				}
			}
		}
	}
	return ""
}

func isExecutable(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !st.IsDir() && st.Mode()&0o111 != 0
}
