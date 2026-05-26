// SPDX-License-Identifier: Apache-2.0

// Plan 03-06 — envtest coverage for the informer-backed Store helpers.
//
// Bootstrap mirrors the sister suite in internal/controller/ach/suite_test.go:
// stdlib `testing` + TestMain + setupAndRun(m); kubebuilder envtest binaries
// resolved from KUBEBUILDER_ASSETS or the ach-devtools image's
// /opt/envtest/k8s/<release> paths; namespace-scoped manager.Cache so MULTI-01
// is enforced at the test layer.
//
// The CRD path is computed relative to this file: from
// internal/platformapi/store/ the bases live three levels up at
// <repo-root>/config/crd/bases (same convention as Plan 01-11 / 02-05).
//
// Tests proceed against the cached manager client (mgr.GetClient()) wrapped
// by store.New — proves that handler-side reads served from informer cache
// produce the expected projection without API-server round trips.

package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
)

// PrimaryNamespace is the namespace the test manager watches. A second
// namespace (otherNamespace) is created at suite startup so the
// cross-namespace isolation tests have a real target to write into via the
// direct k8sClient (the manager cache will NOT see CRs in otherNamespace,
// proving MULTI-01 enforcement).
const (
	primaryNamespace = "ach-system"
	otherNamespace   = "other-ns"
)

// Suite-global state populated by TestMain.
var (
	testEnv      *envtest.Environment
	cfg          *rest.Config
	k8sClient    client.Client // direct client; bypasses cache for fixture seeding
	cachedClient client.Client // manager-backed cached client passed to store.New
	mgrCtx       context.Context
	mgrCancel    context.CancelFunc
)

// TestMain bootstraps envtest and the namespace-scoped manager cache once
// per `go test ./internal/platformapi/store/...` invocation. Split into
// setupAndRun so deferred cleanup runs before os.Exit (deferred funcs do
// NOT run after os.Exit — same pattern as internal/controller/ach/suite_test.go).
func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if found := findEnvtestAssets(); found != "" {
			_ = os.Setenv("KUBEBUILDER_ASSETS", found)
		}
	}
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(os.Stderr)))
	os.Exit(setupAndRun(m))
}

func setupAndRun(m *testing.M) int {
	// CRD bases live at <repo-root>/config/crd/bases — three dirs up from
	// internal/platformapi/store/.
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

	scheme := k8sruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(achv1alpha1.AddToScheme(scheme))

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "k8sClient: %v\n", err)
		return 1
	}

	ctx := context.Background()
	// Pre-create both namespaces; the manager cache is restricted to
	// primaryNamespace, so CRs in otherNamespace must NEVER reach the cache.
	for _, ns := range []string{primaryNamespace, otherNamespace} {
		nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		if err := k8sClient.Create(ctx, nsObj); err != nil && !apierrors.IsAlreadyExists(err) {
			fmt.Fprintf(os.Stderr, "ensure ns %s: %v\n", ns, err)
			return 1
		}
	}

	mgr, err := manager.New(cfg, manager.Options{
		Scheme: scheme,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				primaryNamespace: {},
			},
		},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		Metrics:                metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "manager.New: %v\n", err)
		return 1
	}
	cachedClient = mgr.GetClient()

	mgrCtx, mgrCancel = context.WithCancel(ctx)
	defer mgrCancel()
	mgrDone := make(chan error, 1)
	go func() { mgrDone <- mgr.Start(mgrCtx) }()

	syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
	defer syncCancel()
	if !mgr.GetCache().WaitForCacheSync(syncCtx) {
		fmt.Fprintf(os.Stderr, "manager cache failed to sync within 30s\n")
		mgrCancel()
		return 1
	}
	// Brief settle window — sister suite pads 2s between cache-sync and the
	// first reconcile-relevant operation. Here the tests only read, but the
	// pad makes the WaitForCachedEnvironment loops below converge faster.
	time.Sleep(500 * time.Millisecond)

	rc := m.Run()
	mgrCancel()
	select {
	case <-mgrDone:
	case <-time.After(5 * time.Second):
	}
	return rc
}

// findEnvtestAssets mirrors the sister suite's discovery list.
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
			for _, mm := range matches {
				if isExecutable(filepath.Join(mm, "kube-apiserver")) {
					return mm
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

// waitForCachedEnv polls until the cached client observes the named
// Environment in primaryNamespace (cache catch-up after Create) or the
// deadline elapses. Returns true on observation, false on timeout.
//
// The manager cache populates from informer watch events, so a direct
// k8sClient.Create is observable through cachedClient.Get within a few
// hundred milliseconds — but never instantly.
func waitForCachedEnv(ctx context.Context, name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var got achv1alpha1.Environment
		err := cachedClient.Get(ctx, client.ObjectKey{Namespace: primaryNamespace, Name: name}, &got)
		if err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// waitForCachedEnvGone polls until the cached client reports NotFound for
// the named Environment. Used to ensure cleanup between subtests does not
// leak fixture state through the manager cache.
func waitForCachedEnvGone(ctx context.Context, name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var got achv1alpha1.Environment
		err := cachedClient.Get(ctx, client.ObjectKey{Namespace: primaryNamespace, Name: name}, &got)
		if apierrors.IsNotFound(err) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// newValidEnvSpec returns the minimum Environment spec the CRD admits
// (MinItems=1 on authorizedTeams; runtime + context both required per CEL).
func newValidEnvSpec(teams []string) achv1alpha1.EnvironmentSpec {
	if len(teams) == 0 {
		// CRD CEL admission requires >= 1 entry; the empty-team
		// authorizedTeams test substitutes a placeholder team that
		// cannot match any caller-team set.
		teams = []string{"__no-callers__"}
	}
	return achv1alpha1.EnvironmentSpec{
		AuthorizedTeams: teams,
		Runtime: achv1alpha1.RuntimeBlock{
			Models:     []string{},
			MCPServers: []string{},
			A2AAgents:  []string{},
		},
		Context: achv1alpha1.ContextBlock{
			Prompts:   []string{},
			Plugins:   []string{},
			Artifacts: []string{},
		},
	}
}

// createEnv seeds an Environment via the direct k8sClient (bypasses cache)
// and waits for the manager cache to catch up before returning. The
// returned env is the post-Create version (resourceVersion populated).
//
// When ns != primaryNamespace, the manager cache will NOT observe the CR —
// the function still returns whatever Create returned. Callers use this to
// verify cross-namespace isolation.
func createEnv(t *testing.T, ctx context.Context, name, ns string, spec achv1alpha1.EnvironmentSpec) *achv1alpha1.Environment {
	t.Helper()
	env := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       spec,
	}
	if err := k8sClient.Create(ctx, env); err != nil {
		t.Fatalf("create Environment %s/%s: %v", ns, name, err)
	}
	if ns == primaryNamespace {
		if !waitForCachedEnv(ctx, name, 10*time.Second) {
			t.Fatalf("cached client never observed Environment %q after Create", name)
		}
	}
	return env
}

// setEnvConditions writes a status.conditions slice via the direct client
// (Status subresource Update). The manager cache picks the change up through
// the informer watch; the function waits up to 5s for the cache to observe
// the new condition slice length, so the subsequent read sees the update.
func setEnvConditions(t *testing.T, ctx context.Context, name string, conds []metav1.Condition) {
	t.Helper()
	var env achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: primaryNamespace, Name: name}, &env); err != nil {
		t.Fatalf("get Environment %s for status update: %v", name, err)
	}
	env.Status.Conditions = conds
	if err := k8sClient.Status().Update(ctx, &env); err != nil {
		t.Fatalf("status update %s: %v", name, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var got achv1alpha1.Environment
		if err := cachedClient.Get(ctx, client.ObjectKey{Namespace: primaryNamespace, Name: name}, &got); err == nil {
			if len(got.Status.Conditions) == len(conds) {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// deleteEnvAndWait deletes the named Environment via the direct client AND
// removes any finalizers via a status-bypass update so the CR drains in the
// finalizer-free test fixture. The platformapi/store envtest does NOT
// register a finalizer-adding controller, so the CR has no finalizer to
// remove in the steady-state case — but if a previous test left one, the
// update ensures the delete completes.
func deleteEnvAndWait(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	var env achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: primaryNamespace, Name: name}, &env); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		t.Fatalf("pre-delete get %s: %v", name, err)
	}
	if len(env.Finalizers) > 0 {
		env.Finalizers = nil
		if err := k8sClient.Update(ctx, &env); err != nil && !apierrors.IsNotFound(err) {
			t.Fatalf("strip finalizers on %s: %v", name, err)
		}
	}
	if err := k8sClient.Delete(ctx, &env); err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("delete %s: %v", name, err)
	}
	if !waitForCachedEnvGone(ctx, name, 10*time.Second) {
		t.Fatalf("cached client still sees %s after delete", name)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Task 1 — GetEnvironment / EnvironmentTerminating / EnvironmentAccessGroupSynced
// ─────────────────────────────────────────────────────────────────────

// Test 1 — GetEnvironment returns the cached Environment when present.
func TestStore_GetEnvironment_Present(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "test-get-present"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"team-a"}))
	t.Cleanup(func() { deleteEnvAndWait(t, context.Background(), name) })

	env, err := s.GetEnvironment(ctx, name)
	if err != nil {
		t.Fatalf("GetEnvironment: unexpected err: %v", err)
	}
	if env == nil {
		t.Fatal("GetEnvironment returned (nil, nil) for present env")
	}
	if env.Name != name {
		t.Errorf("env.Name = %q; want %q", env.Name, name)
	}
	if got := env.Spec.AuthorizedTeams; len(got) != 1 || got[0] != "team-a" {
		t.Errorf("env.Spec.AuthorizedTeams = %v; want [team-a]", got)
	}
}

// Test 2 — GetEnvironment maps apierrors.IsNotFound to (nil, nil).
func TestStore_GetEnvironment_Absent(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	env, err := s.GetEnvironment(ctx, "this-env-does-not-exist")
	if err != nil {
		t.Fatalf("GetEnvironment(absent): err = %v; want nil", err)
	}
	if env != nil {
		t.Errorf("GetEnvironment(absent): env = %+v; want nil", env)
	}
}

// Test 3 — Cross-namespace isolation: Environments in other-ns are invisible
// to a Store scoped to ach-system (MULTI-01 enforcement at envtest layer).
func TestStore_GetEnvironment_OtherNamespaceIsolation(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "other-ns-env"
	// Create directly via k8sClient — the manager cache is scoped to
	// primaryNamespace so it will never see this CR. createEnv does NOT
	// wait when ns != primaryNamespace, so this Create-then-Get is the
	// real isolation probe.
	createEnv(t, ctx, name, otherNamespace, newValidEnvSpec([]string{"team-a"}))
	t.Cleanup(func() {
		probe := &achv1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: otherNamespace}}
		_ = k8sClient.Delete(context.Background(), probe)
	})

	env, err := s.GetEnvironment(ctx, name)
	if err != nil {
		t.Fatalf("GetEnvironment(other-ns name): err = %v; want nil", err)
	}
	if env != nil {
		t.Errorf("GetEnvironment(other-ns name): env = %+v; want nil — MULTI-01 isolation broken", env)
	}
}

// Test 4 — EnvironmentTerminating returns false when DeletionTimestamp is nil.
func TestStore_EnvironmentTerminating_NotTerminating(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "test-not-terminating"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"team-a"}))
	t.Cleanup(func() { deleteEnvAndWait(t, context.Background(), name) })

	term, err := s.EnvironmentTerminating(ctx, name)
	if err != nil {
		t.Fatalf("EnvironmentTerminating: err = %v; want nil", err)
	}
	if term {
		t.Error("EnvironmentTerminating = true; want false (CR has nil DeletionTimestamp)")
	}
}

// Test 5 — EnvironmentTerminating returns true when the CR has a finalizer
// AND a non-nil DeletionTimestamp. Achieved by:
//  1. Create env.
//  2. Patch finalizer on (so Delete sets DeletionTimestamp without immediate
//     removal — finalizer pins the CR).
//  3. Delete (sets DeletionTimestamp; CR persists because finalizer is non-empty).
//  4. Wait for cache to observe the terminating state.
//  5. Assert EnvironmentTerminating returns true.
//  6. Cleanup: strip finalizer so CR drains.
func TestStore_EnvironmentTerminating_Terminating(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "test-terminating"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"team-a"}))

	// Add a finalizer so Delete only marks DeletionTimestamp.
	var live achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: primaryNamespace, Name: name}, &live); err != nil {
		t.Fatalf("pre-finalizer get: %v", err)
	}
	live.Finalizers = []string{"test.ach.ackstorm.ai/pin"}
	if err := k8sClient.Update(ctx, &live); err != nil {
		t.Fatalf("add finalizer: %v", err)
	}
	if err := k8sClient.Delete(ctx, &live); err != nil {
		t.Fatalf("delete (with finalizer): %v", err)
	}

	// Wait for cache to observe DeletionTimestamp.
	deadline := time.Now().Add(10 * time.Second)
	var sawTerm bool
	for time.Now().Before(deadline) {
		var got achv1alpha1.Environment
		if err := cachedClient.Get(ctx, client.ObjectKey{Namespace: primaryNamespace, Name: name}, &got); err == nil {
			if got.DeletionTimestamp != nil {
				sawTerm = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !sawTerm {
		t.Fatalf("cache never observed DeletionTimestamp on %s", name)
	}

	term, err := s.EnvironmentTerminating(ctx, name)
	if err != nil {
		t.Fatalf("EnvironmentTerminating: err = %v", err)
	}
	if !term {
		t.Error("EnvironmentTerminating = false; want true (CR has non-nil DeletionTimestamp)")
	}

	// Cleanup — strip finalizer so the CR drains.
	t.Cleanup(func() {
		var final achv1alpha1.Environment
		if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: primaryNamespace, Name: name}, &final); err == nil {
			final.Finalizers = nil
			_ = k8sClient.Update(context.Background(), &final)
		}
		_ = waitForCachedEnvGone(context.Background(), name, 10*time.Second)
	})
}

// Test 6 — EnvironmentTerminating returns false when the env is absent.
// (The handler distinguishes env_not_found via GetEnvironment one step
// earlier — store layer treats absent uniformly as not-terminating.)
func TestStore_EnvironmentTerminating_Absent(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	term, err := s.EnvironmentTerminating(ctx, "absent-env-name")
	if err != nil {
		t.Fatalf("EnvironmentTerminating(absent): err = %v; want nil", err)
	}
	if term {
		t.Error("EnvironmentTerminating(absent) = true; want false")
	}
}

// Test 7 — AccessGroupSynced=True returns true.
func TestStore_EnvironmentAccessGroupSynced_True(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "test-ags-true"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"team-a"}))
	t.Cleanup(func() { deleteEnvAndWait(t, context.Background(), name) })

	setEnvConditions(t, ctx, name, []metav1.Condition{
		{
			Type:               ConditionTypeAccessGroupSynced,
			Status:             metav1.ConditionTrue,
			Reason:             "Synced",
			Message:            "access group reconciled",
			LastTransitionTime: metav1.Now(),
		},
	})

	synced, err := s.EnvironmentAccessGroupSynced(ctx, name)
	if err != nil {
		t.Fatalf("EnvironmentAccessGroupSynced: %v", err)
	}
	if !synced {
		t.Error("EnvironmentAccessGroupSynced = false; want true")
	}
}

// Test 8 — AccessGroupSynced=False (explicit) returns false.
func TestStore_EnvironmentAccessGroupSynced_FalseExplicit(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "test-ags-false-explicit"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"team-a"}))
	t.Cleanup(func() { deleteEnvAndWait(t, context.Background(), name) })

	setEnvConditions(t, ctx, name, []metav1.Condition{
		{
			Type:               ConditionTypeAccessGroupSynced,
			Status:             metav1.ConditionFalse,
			Reason:             "LiteLLMUnreachable",
			Message:            "transient outage",
			LastTransitionTime: metav1.Now(),
		},
	})

	synced, err := s.EnvironmentAccessGroupSynced(ctx, name)
	if err != nil {
		t.Fatalf("EnvironmentAccessGroupSynced: %v", err)
	}
	if synced {
		t.Error("EnvironmentAccessGroupSynced = true; want false (explicit Status=False)")
	}
}

// Test 9 — AccessGroupSynced condition missing returns false. Phase 3 §8.2
// step 3 treats missing as not-ready (503 not_ready) — the store layer
// surfaces the boolean projection that the handler renders.
func TestStore_EnvironmentAccessGroupSynced_MissingCondition(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "test-ags-missing"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"team-a"}))
	t.Cleanup(func() { deleteEnvAndWait(t, context.Background(), name) })
	// No status.conditions written; CRD default is empty slice / nil.

	synced, err := s.EnvironmentAccessGroupSynced(ctx, name)
	if err != nil {
		t.Fatalf("EnvironmentAccessGroupSynced: %v", err)
	}
	if synced {
		t.Error("EnvironmentAccessGroupSynced = true; want false (condition not present)")
	}
}

// Test 10 — AccessGroupSynced on absent env returns (false, nil). Caller
// did the env existence check earlier via GetEnvironment; store reports
// the conservative not-ready boolean and nil error.
func TestStore_EnvironmentAccessGroupSynced_AbsentEnv(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	synced, err := s.EnvironmentAccessGroupSynced(ctx, "absent-env-for-ags")
	if err != nil {
		t.Fatalf("EnvironmentAccessGroupSynced(absent): err = %v; want nil", err)
	}
	if synced {
		t.Error("EnvironmentAccessGroupSynced(absent) = true; want false")
	}
}

// ─────────────────────────────────────────────────────────────────────
// Task 2 — ListAuthorizedEnvironments + EnvironmentView projection
// ─────────────────────────────────────────────────────────────────────

// Test 1 — Intersection-positive: caller team intersects authorizedTeams; env returned.
func TestListAuthorizedEnvironments_IntersectionPositive(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "list-intersect-positive"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"alpha", "beta"}))
	t.Cleanup(func() { deleteEnvAndWait(t, context.Background(), name) })

	got, err := s.ListAuthorizedEnvironments(ctx, []string{"beta", "gamma"}, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, env := range got {
		if env.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("env %q not in result; got %d items", name, len(got))
	}
}

// Test 2 — Intersection-empty: no shared team; env NOT returned.
func TestListAuthorizedEnvironments_IntersectionEmpty(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "list-intersect-empty"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"alpha"}))
	t.Cleanup(func() { deleteEnvAndWait(t, context.Background(), name) })

	got, err := s.ListAuthorizedEnvironments(ctx, []string{"beta"}, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, env := range got {
		if env.Name == name {
			t.Errorf("env %q must NOT be in non-admin result with empty intersection", name)
		}
	}
}

// Test 3 — isAdmin=true bypasses team intersection; both envs returned.
func TestListAuthorizedEnvironments_AdminOverride(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	createEnv(t, ctx, "list-admin-1", primaryNamespace, newValidEnvSpec([]string{"alpha"}))
	createEnv(t, ctx, "list-admin-2", primaryNamespace, newValidEnvSpec([]string{"alpha"}))
	t.Cleanup(func() {
		deleteEnvAndWait(t, context.Background(), "list-admin-1")
		deleteEnvAndWait(t, context.Background(), "list-admin-2")
	})

	got, err := s.ListAuthorizedEnvironments(ctx, []string{"beta"}, true)
	if err != nil {
		t.Fatalf("List(admin): %v", err)
	}
	saw := map[string]bool{}
	for _, env := range got {
		saw[env.Name] = true
	}
	if !saw["list-admin-1"] || !saw["list-admin-2"] {
		t.Errorf("admin override missed envs; saw=%v", saw)
	}
}

// Test 4 — Non-admin caller against an Environment with the CRD-minimum
// single authorizedTeams entry that the caller's team set does NOT include
// returns empty intersection. This is the canonical "wrong team" path.
//
// NOTE: Plan task description references "empty authorizedTeams = []", but
// the CRD enforces MinItems=1 on spec.authorizedTeams, so a truly empty
// slice is not admissible. Substituting "single sentinel team no caller has"
// preserves the intent: empty intersection ⇒ env not returned.
func TestListAuthorizedEnvironments_EmptyIntersection_NonAdmin(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "list-empty-teams"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"sentinel-team-zzz"}))
	t.Cleanup(func() { deleteEnvAndWait(t, context.Background(), name) })

	got, err := s.ListAuthorizedEnvironments(ctx, []string{"any-other-team"}, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, env := range got {
		if env.Name == name {
			t.Errorf("env %q with non-matching authorizedTeams must NOT appear in non-admin result", name)
		}
	}
}

// Test 5 — Same env as Test 4 but with isAdmin=true; admin override still wins.
func TestListAuthorizedEnvironments_EmptyIntersection_Admin(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "list-empty-teams-admin"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"sentinel-team-zzz"}))
	t.Cleanup(func() { deleteEnvAndWait(t, context.Background(), name) })

	got, err := s.ListAuthorizedEnvironments(ctx, []string{"any-other-team"}, true)
	if err != nil {
		t.Fatalf("List(admin): %v", err)
	}
	found := false
	for _, env := range got {
		if env.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("admin must see env %q regardless of intersection; got %d items", name, len(got))
	}
}

// Test 6 — Namespace isolation: env in other-ns is invisible to the store
// scoped to primaryNamespace, for both admin and non-admin callers.
func TestListAuthorizedEnvironments_NamespaceIsolation(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	primaryName := "list-isolation-primary"
	otherName := "list-isolation-other"
	createEnv(t, ctx, primaryName, primaryNamespace, newValidEnvSpec([]string{"alpha"}))
	createEnv(t, ctx, otherName, otherNamespace, newValidEnvSpec([]string{"alpha"}))
	t.Cleanup(func() {
		deleteEnvAndWait(t, context.Background(), primaryName)
		probe := &achv1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: otherName, Namespace: otherNamespace}}
		_ = k8sClient.Delete(context.Background(), probe)
	})

	// Non-admin
	got, err := s.ListAuthorizedEnvironments(ctx, []string{"alpha"}, false)
	if err != nil {
		t.Fatalf("List(non-admin): %v", err)
	}
	for _, env := range got {
		if env.Name == otherName {
			t.Errorf("non-admin saw env %q in other namespace — MULTI-01 broken", otherName)
		}
		if env.Namespace != "" && env.Namespace != primaryNamespace {
			t.Errorf("non-admin result contains env in unexpected namespace %q", env.Namespace)
		}
	}

	// Admin
	gotAdmin, err := s.ListAuthorizedEnvironments(ctx, nil, true)
	if err != nil {
		t.Fatalf("List(admin): %v", err)
	}
	for _, env := range gotAdmin {
		if env.Name == otherName {
			t.Errorf("admin saw env %q in other namespace — MULTI-01 broken", otherName)
		}
	}
}

// Test 7 — conditions[] preserved verbatim through the projection.
func TestToEnvironmentView_ConditionsVerbatim(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "view-conditions-verbatim"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"alpha"}))
	t.Cleanup(func() { deleteEnvAndWait(t, context.Background(), name) })

	now := metav1.Now()
	setEnvConditions(t, ctx, name, []metav1.Condition{
		{
			Type:               ConditionTypeAccessGroupSynced,
			Status:             metav1.ConditionTrue,
			Reason:             "Synced",
			Message:            "ok",
			LastTransitionTime: now,
		},
		{
			Type:               "ContentReady",
			Status:             metav1.ConditionFalse,
			Reason:             "StaleCacheExpired",
			Message:            "cache too old",
			LastTransitionTime: now,
		},
	})

	env, err := s.GetEnvironment(ctx, name)
	if err != nil || env == nil {
		t.Fatalf("GetEnvironment: %v / %v", env, err)
	}
	view := ToEnvironmentView(*env)
	if view.Name != name {
		t.Errorf("view.Name = %q; want %q", view.Name, name)
	}
	if len(view.Conditions) != 2 {
		t.Fatalf("view.Conditions length = %d; want 2", len(view.Conditions))
	}
	// Map conditions by type for stable assertions.
	condByType := make(map[string]metav1.Condition)
	for _, c := range view.Conditions {
		condByType[c.Type] = c
	}
	if c, ok := condByType[ConditionTypeAccessGroupSynced]; !ok || c.Status != metav1.ConditionTrue || c.Reason != "Synced" {
		t.Errorf("AccessGroupSynced not carried verbatim: got %+v", c)
	}
	if c, ok := condByType["ContentReady"]; !ok || c.Status != metav1.ConditionFalse || c.Reason != "StaleCacheExpired" {
		t.Errorf("ContentReady not carried verbatim: got %+v", c)
	}
	if got := view.Spec.AuthorizedTeams; len(got) != 1 || got[0] != "alpha" {
		t.Errorf("view.Spec.AuthorizedTeams = %v; want [alpha]", got)
	}
}

// Test 8 — Terminating envs ARE included in ListAuthorizedEnvironments
// (drain semantics are Phase 5 / CS-09 concern; listing during drain is
// allowed so callers can observe the terminating state).
func TestListAuthorizedEnvironments_TerminatingVisible(t *testing.T) {
	ctx := context.Background()
	s := New(cachedClient, primaryNamespace, logr.Discard())

	name := "list-terminating-visible"
	createEnv(t, ctx, name, primaryNamespace, newValidEnvSpec([]string{"alpha"}))

	// Add finalizer + delete so the CR enters terminating state.
	var live achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: primaryNamespace, Name: name}, &live); err != nil {
		t.Fatalf("get for finalizer: %v", err)
	}
	live.Finalizers = []string{"test.ach.ackstorm.ai/pin-list-term"}
	if err := k8sClient.Update(ctx, &live); err != nil {
		t.Fatalf("add finalizer: %v", err)
	}
	if err := k8sClient.Delete(ctx, &live); err != nil {
		t.Fatalf("delete to set DeletionTimestamp: %v", err)
	}

	// Wait for cache to see DeletionTimestamp.
	deadline := time.Now().Add(10 * time.Second)
	sawTerm := false
	for time.Now().Before(deadline) {
		var got achv1alpha1.Environment
		if err := cachedClient.Get(ctx, client.ObjectKey{Namespace: primaryNamespace, Name: name}, &got); err == nil && got.DeletionTimestamp != nil {
			sawTerm = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !sawTerm {
		t.Fatalf("cache never saw DeletionTimestamp on %s", name)
	}

	// Cleanup: strip finalizer after the assertion so the CR drains.
	t.Cleanup(func() {
		var final achv1alpha1.Environment
		if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: primaryNamespace, Name: name}, &final); err == nil {
			final.Finalizers = nil
			_ = k8sClient.Update(context.Background(), &final)
		}
		_ = waitForCachedEnvGone(context.Background(), name, 10*time.Second)
	})

	got, err := s.ListAuthorizedEnvironments(ctx, []string{"alpha"}, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, env := range got {
		if env.Name == name {
			found = true
			if env.DeletionTimestamp == nil {
				t.Error("env returned by ListAuthorizedEnvironments has nil DeletionTimestamp; expected non-nil (terminating)")
			}
			break
		}
	}
	if !found {
		t.Errorf("terminating env %q must be visible to ListAuthorizedEnvironments", name)
	}
}
