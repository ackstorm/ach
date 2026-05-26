// SPDX-License-Identifier: Apache-2.0

// Package-level envtest bootstrap for the ACH reconciler suite (Plan 01-11
// Task 2). Adopts the stdlib `testing` + TestMain pattern from the sister
// project's internal/controller/suite_test.go (the canonical 418-line
// reference) — namespace-scoped manager.Cache.DefaultNamespaces enforces
// MULTI-01 at envtest layer; a counting NoopClient lets finalizer specs
// assert the §6.5 step-2/step-3 LiteLLM call ordering; all six reconcilers
// register on the test manager so the per-kind finalizer tests share one
// running controller plane.
//
// The package uses stdlib `testing` for consistency with credhash / config
// / cachefs (Plan 01-04 / 01-06 / 01-07) — Ginkgo was scaffolded by
// kubebuilder but is removed here so all envtest specs in the project
// follow one idiom. Ginkgo + Gomega remain in go.mod for the e2e suite
// (Task 5) where the kubebuilder default is preserved.

package ach

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
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
	"github.com/ackstorm/ach/internal/connection"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/snapshot"
)

// WatchNamespace is the namespace the test manager watches (MULTI-01).
// Tests that create CRs outside this namespace MUST observe that the
// reconciler never sees them (no finalizer added; no status touched).
const WatchNamespace = "ach-system"

// Test-global state shared across the suite's test files
// (cel_admission_test.go, <kind>_finalizer_test.go). TestMain populates;
// each Test* function reads.
var (
	testEnv         *envtest.Environment
	cfg             *rest.Config
	k8sClient       client.Client
	mgrCtx          context.Context
	mgrCancel       context.CancelFunc
	testCacheRoot   string
	litellmCounter  *atomic.Int64
	connCache       *connection.Cache
	accessGroupFake *accessGroupFakeImpl
)

// countingNoopClient wraps litellm.NoopClient and bumps an atomic counter
// on each method invocation so finalizer specs can assert §6.5 step-2 +
// step-3 ordering ("DeleteAccessGroup then DeleteTag, exactly once each").
// The counter is monotonic and never decremented — tests Store(0) before
// the assertion-relevant action.
type countingNoopClient struct {
	*litellm.NoopClient
	counter     *atomic.Int64
	accessGroup *accessGroupFakeImpl
}

// DeleteAccessGroup increments the counter and delegates to the embedded
// NoopClient (which logs and returns nil).
func (c *countingNoopClient) DeleteAccessGroup(ctx context.Context, name string) error {
	c.counter.Add(1)
	return c.NoopClient.DeleteAccessGroup(ctx, name)
}

// DeleteTag increments the counter and delegates to the embedded
// NoopClient (which logs and returns nil).
func (c *countingNoopClient) DeleteTag(ctx context.Context, name string) error {
	c.counter.Add(1)
	return c.NoopClient.DeleteTag(ctx, name)
}

// §7 routing: forward access-group calls to the per-suite fake so tests
// can assert call counts + inject errors.
func (c *countingNoopClient) CreateAccessGroup(ctx context.Context, name string, modelNames []string) error {
	return c.accessGroup.CreateAccessGroup(ctx, name, modelNames)
}

func (c *countingNoopClient) BindTeamToAccessGroup(ctx context.Context, accessGroup, teamID string) error {
	return c.accessGroup.BindTeamToAccessGroup(ctx, accessGroup, teamID)
}

func (c *countingNoopClient) ListAccessGroupBindings(ctx context.Context, accessGroup string) ([]string, error) {
	return c.accessGroup.ListAccessGroupBindings(ctx, accessGroup)
}

// Compile-time interface assertion — if litellm.Client grows a method, the
// build breaks here until countingNoopClient catches up.
var _ litellm.Client = (*countingNoopClient)(nil)

// TestMain is the envtest bootstrap. It mirrors the sister project's
// TestMain → setupAndRun(m) split so deferred cleanup runs before
// os.Exit (deferred funcs do NOT run after os.Exit).
func TestMain(m *testing.M) {
	// Resolve envtest binaries. KUBEBUILDER_ASSETS may already be set by
	// the Makefile target. Otherwise probe the standard paths the
	// ach-devtools image (D-16) provides.
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

// setupAndRun owns the envtest lifecycle: start, register reconcilers,
// run, stop. Split from TestMain so deferred cleanup (testEnv.Stop, mgr
// cancellation) runs before the process exits.
func setupAndRun(m *testing.M) int {
	// CRD path: <repo-root>/config/crd/bases — three dirs up from
	// internal/controller/ach/.
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

	// Build scheme: clientgoscheme + our v1alpha1 types.
	scheme := k8sruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(achv1alpha1.AddToScheme(scheme))

	// Direct client for test fixtures (Create/Delete bypassing the manager
	// cache). Per-test logic uses this; finalizer add/remove polling reads
	// via the same client.
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "k8s client: %v\n", err)
		return 1
	}

	// Ensure WatchNamespace exists for CRs created there.
	ctx := context.Background()
	if err := k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: WatchNamespace},
	}); err != nil && !ignoreAlreadyExists(err) {
		fmt.Fprintf(os.Stderr, "ensure %s: %v\n", WatchNamespace, err)
		return 1
	}

	// Per-suite cache root for external-ref reconcilers (Plugin, Prompt,
	// Artifact, PluginMarketplace). Per-test t.TempDir() would not work
	// here because suite_test.go has no *testing.T — use os.MkdirTemp and
	// clean up after m.Run().
	testCacheRoot, err = os.MkdirTemp("", "ach-test-cache-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdir test cache root: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(testCacheRoot) }()
	for _, d := range []string{"plugin", "prompt", "artifact", "marketplace"} {
		if err := os.MkdirAll(filepath.Join(testCacheRoot, d), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", d, err)
			return 1
		}
	}

	// Counting LiteLLM fake for §6.5 finalizer assertions + §7
	// AccessGroup tally fake. countingNoopClient embeds the same
	// NoopClient instance the accessGroupFake wraps, so non-§7 calls
	// resolve through one chain while §7 calls flow through the fake.
	litellmCounter = &atomic.Int64{}
	accessGroupFake = newAccessGroupFake()
	llm := &countingNoopClient{
		NoopClient:  accessGroupFake.NoopClient,
		counter:     litellmCounter,
		accessGroup: accessGroupFake,
	}

	// Manager with WatchNamespace-scoped informer cache (MULTI-01) —
	// matches cmd/operator/main.go production wiring so the envtest
	// pass exercises the same code path the deployed Operator runs.
	mgr, err := manager.New(cfg, manager.Options{
		Scheme: scheme,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				WatchNamespace: {},
			},
		},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		// Disable the metrics server in tests — controller-runtime binds
		// :8080 by default and the kind-cluster e2e pass already takes that
		// port. envtest doesn't care.
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "manager.New: %v\n", err)
		return 1
	}

	connCache = connection.NewCache()

	if err := (&LiteLLMConnectionReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Cache:     connCache,
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "SetupWithManager(LiteLLMConnection): %v\n", err)
		return 1
	}

	// Register all domain reconcilers — the finalizer tests exercise each
	// one's add-then-remove cycle; the CEL admission test exercises CRD
	// validation BEFORE any reconciler runs.
	// §7: Snapshotter wired so the steady-state branch runs instead of
	// the back-compat Unknown-placeholder shortcut. Synchronous refresh
	// here avoids spawning the ticker goroutine in envtest.
	envSnapshotter := snapshot.NewSnapshotter(llm, logr.Discard())
	envSnapshotter.RefreshForTest(context.Background())

	if err := (&EnvironmentReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		LiteLLM:     llm,
		Namespace:   WatchNamespace,
		Log:         logr.Discard(),
		Snapshotter: envSnapshotter,
		// DB nil — drainEkRows trivially exits with slog.Info per Plan 05.
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "SetupWithManager(Environment): %v\n", err)
		return 1
	}
	if err := (&PluginReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: testCacheRoot,
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "SetupWithManager(Plugin): %v\n", err)
		return 1
	}
	if err := (&PluginMarketplaceReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: testCacheRoot,
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "SetupWithManager(PluginMarketplace): %v\n", err)
		return 1
	}
	if err := (&ArtifactReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: testCacheRoot,
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "SetupWithManager(Artifact): %v\n", err)
		return 1
	}
	if err := (&PromptReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
		CacheRoot: testCacheRoot,
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "SetupWithManager(Prompt): %v\n", err)
		return 1
	}
	if err := (&BackendIdentityPolicyReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Namespace: WatchNamespace,
		Log:       logr.Discard(),
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "SetupWithManager(BackendIdentityPolicy): %v\n", err)
		return 1
	}

	// Start the manager in a goroutine and wait for the cache to sync —
	// MULTI-03 readiness gate: no Reconcile is observed until the
	// informer cache reports synced.
	mgrCtx, mgrCancel = context.WithCancel(ctx)
	defer mgrCancel()
	mgrDone := make(chan error, 1)
	go func() { mgrDone <- mgr.Start(mgrCtx) }()

	// WaitForCacheSync returns true once all informers are ready.
	syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
	defer syncCancel()
	if !mgr.GetCache().WaitForCacheSync(syncCtx) {
		fmt.Fprintf(os.Stderr, "manager cache failed to sync within 30s (MULTI-03 gate)\n")
		mgrCancel()
		return 1
	}

	// Brief settle window — informers are synced but the first reconcile
	// event may still be in flight. The sister project uses the same
	// 2-second pad.
	time.Sleep(2 * time.Second)

	rc := m.Run()

	// Stop manager before deferred testEnv.Stop runs.
	mgrCancel()
	select {
	case <-mgrDone:
	case <-time.After(5 * time.Second):
	}

	return rc
}

// findEnvtestAssets probes the standard paths for envtest binaries
// pre-baked into the ach-devtools image (Dockerfile.devtools). Returns
// "" if none found — testEnv.Start() will then attempt its own
// discovery. The probes match the sister project's pattern.
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
	// Glob fallback under /workspace/.gocache/envtest/k8s/* and /opt/envtest/k8s/*
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

// ignoreAlreadyExists treats "already exists" / "AlreadyExists" as
// success — used by the namespace creation step which is idempotent
// across reruns.
func ignoreAlreadyExists(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	for _, s := range []string{"already exists", "AlreadyExists"} {
		if containsSubstring(msg, s) {
			return true
		}
	}
	return false
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
