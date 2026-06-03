// SPDX-License-Identifier: Apache-2.0

// Task B9: envtest coverage for the plugin content-present gate on
// ExecutionResourcesResolved.
//
// Gate: an Environment whose spec.context.plugins references a plugin
// that has a DB row but last_successful_refresh IS NULL must hold
// ExecutionResourcesResolved=False until the content is synced
// (last_successful_refresh becomes non-null).
//
// Two integration-gated cases (require real Postgres via testcontainers):
//
//   - TestEnvPluginContentPresent_NotSynced: bare plugin reference with
//     last_successful_refresh=NULL → ExecutionResourcesResolved=False,
//     UnresolvedContextPlugins contains the ref.
//
//   - TestEnvPluginContentPresent_Synced: same plugin row updated to a
//     non-null last_successful_refresh → ExecutionResourcesResolved=True,
//     UnresolvedContextPlugins=[].
//
// Both tests create their own EnvironmentReconciler with DB wired and call
// Reconcile() directly — no suite-level reconciler involvement. This avoids
// the suite reconciler (which has DB=nil) racing the per-test assertions.
//
// Skipped unless Docker is available (matches the testcontainers policy of
// every other DB-backed test in this repo).

package ach

import (
	"context"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/snapshot"
)

// setupPluginContentPresentDB boots a fresh postgres:16-alpine via
// testcontainers, applies all migrations, and returns a pool + cleanup.
// Skips the test if Docker is unavailable.
//
// The startup timeout (90s) is scoped only to the tcpostgres.Run call.
// The returned pool uses context.Background() so it survives the startup
// context's cancellation.
func setupPluginContentPresentDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	// Scope the startup wait to 90s; the pool lifetime outlasts this context.
	startCtx, startCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer startCancel()

	pgC, err := tcpostgres.Run(startCtx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("ach_test"),
		tcpostgres.WithUsername("ach_test"),
		tcpostgres.WithPassword("ach_test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("docker required for plugin content-present tests: %v", err)
	}

	// Use background context for the pool lifetime — the startup context
	// will be cancelled when setupPluginContentPresentDB returns but the
	// container and pool must survive for the duration of the test.
	connStr, err := pgC.ConnectionString(context.Background(), "sslmode=disable")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("connection string: %v", err)
	}

	// Resolve migrations relative to this file's location in the repo tree.
	_, thisFile, _, _ := runtime.Caller(0)
	migrationsPath, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "db", "migrations"))
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("abs migrationsPath: %v", err)
	}

	if err := achdb.Migrate(connStr, migrationsPath); err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("db.Migrate: %v", err)
	}

	pool, err := achdb.Open(context.Background(), connStr)
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("db.Open: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		_ = pgC.Terminate(context.Background())
		t.Fatalf("pool.Ping: %v", err)
	}

	cleanup := func() {
		pool.Close()
		if err := pgC.Terminate(context.Background()); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	}
	return pool, cleanup
}

// buildPluginTestReconciler returns an EnvironmentReconciler for the given
// namespace with DB wired and a Snapshotter (empty LiteLLM snapshot so
// spec.runtime.{Models,MCPServers,A2AAgents} are trivially unresolved-free
// when the test omits them). Routes access-group calls to the per-suite
// accessGroupFake so AccessGroupSynced reconciliation succeeds.
func buildPluginTestReconciler(t *testing.T, ns string, pool *pgxpool.Pool) *EnvironmentReconciler {
	t.Helper()
	fake := &wiringFakeLiteLLM{}
	snp := snapshot.NewSnapshotter(fake, logr.Discard())
	snp.RefreshForTest(context.Background())
	var ctr atomic.Int64
	llm := &countingNoopClient{
		NoopClient:  accessGroupFake.NoopClient,
		counter:     &ctr,
		accessGroup: accessGroupFake,
	}
	return &EnvironmentReconciler{
		Client:      k8sClient,
		LiteLLM:     llm,
		Namespace:   ns,
		Log:         logr.Discard(),
		DB:          pool,
		Snapshotter: snp,
	}
}

// TestEnvPluginContentPresent_NotSynced: an Environment whose
// spec.context.plugins references a bare plugin that has a plugins row
// with last_successful_refresh IS NULL must hold
// ExecutionResourcesResolved=False.
//
// This is the false-green scenario: the plugin exists in Postgres (the
// operator wrote it) but the artifact was never fetched. Before this fix,
// the condition was unconditionally True → hydrate would 404 at runtime.
func TestEnvPluginContentPresent_NotSynced(t *testing.T) {
	pool, cleanup := setupPluginContentPresentDB(t)
	defer cleanup()

	ctx := context.Background()
	const ns = WatchNamespace
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")

	// Seed a plugins row with NULL last_successful_refresh (no content).
	if err := achdb.UpsertPlugin(ctx, pool, achdb.PluginRow{
		Namespace:             ns,
		Name:                  "my-plugin",
		StorageLocation:       "",    // no content yet
		LastSuccessfulRefresh: nil,   // NOT synced — this is the gate trigger
		MaxStalenessSeconds:   86400, // 24h
		ResourceVersion:       "1",
	}); err != nil {
		t.Fatalf("seed plugins row: %v", err)
	}

	// Create the Environment CR with the bare plugin reference.
	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-plugin-not-synced",
			Namespace: ns,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context: achv1alpha1.ContextBlock{
				Plugins: []string{"my-plugin"}, // bare ref → plugins table
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment CR: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	// Wait for the suite reconciler to add the finalizer (it runs DB=nil so
	// it won't check plugins). Then call our DB-wired reconciler directly.
	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, f := range got.Finalizers {
			if f == environmentFinalizer {
				return true
			}
		}
		return false
	}, 10*time.Second, 250*time.Millisecond) {
		t.Fatal("finalizer never added within 10s")
	}

	// Call Reconcile() with the DB-wired reconciler.
	r := buildPluginTestReconciler(t, ns, pool)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cr)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	// Re-read status from the API server.
	var final achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &final); err != nil {
		t.Fatalf("re-Get Environment: %v", err)
	}

	// Assert ExecutionResourcesResolved=False because plugin content is absent.
	var errCond *metav1.Condition
	for i := range final.Status.Conditions {
		if final.Status.Conditions[i].Type == "ExecutionResourcesResolved" {
			errCond = &final.Status.Conditions[i]
			break
		}
	}
	if errCond == nil {
		t.Fatalf("ExecutionResourcesResolved condition not found; conditions=%+v", final.Status.Conditions)
	}
	if errCond.Status != metav1.ConditionFalse {
		t.Errorf("ExecutionResourcesResolved.Status = %q; want False (plugin content absent must block resolution). message=%q",
			errCond.Status, errCond.Message)
	}
	if errCond.Reason != "ResourceUnresolved" {
		t.Errorf("ExecutionResourcesResolved.Reason = %q; want ResourceUnresolved", errCond.Reason)
	}

	// UnresolvedContextPlugins must contain "my-plugin".
	found := false
	for _, p := range final.Status.UnresolvedContextPlugins {
		if p == "my-plugin" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("UnresolvedContextPlugins = %v; want to contain \"my-plugin\"", final.Status.UnresolvedContextPlugins)
	}

	t.Logf("PASS (not-synced): ExecutionResourcesResolved=%s reason=%s message=%q UnresolvedContextPlugins=%v",
		errCond.Status, errCond.Reason, errCond.Message, final.Status.UnresolvedContextPlugins)
}

// TestEnvPluginContentPresent_Synced: same scenario but with a non-null
// last_successful_refresh → ExecutionResourcesResolved=True and
// UnresolvedContextPlugins=[].
func TestEnvPluginContentPresent_Synced(t *testing.T) {
	pool, cleanup := setupPluginContentPresentDB(t)
	defer cleanup()

	ctx := context.Background()
	const ns = WatchNamespace
	accessGroupFake.Reset()
	accessGroupFake.SeedTeam("default", "t-uuid-default")

	// Seed a plugins row WITH a non-null last_successful_refresh (content present).
	now := time.Now().UTC()
	storagePath := "/cache/plugin/my-synced-plugin.tar.gz"
	if err := achdb.UpsertPlugin(ctx, pool, achdb.PluginRow{
		Namespace:             ns,
		Name:                  "my-synced-plugin",
		StorageLocation:       storagePath,
		LastSuccessfulRefresh: &now, // content IS present
		MaxStalenessSeconds:   86400,
		ResourceVersion:       "1",
	}); err != nil {
		t.Fatalf("seed plugins row: %v", err)
	}

	cr := &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env-plugin-synced",
			Namespace: ns,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: []string{"default"},
			Runtime:         achv1alpha1.RuntimeBlock{},
			Context: achv1alpha1.ContextBlock{
				Plugins: []string{"my-synced-plugin"},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Environment CR: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	// Wait for finalizer then call DB-wired reconciler.
	if !Eventually(func() bool {
		var got achv1alpha1.Environment
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		for _, f := range got.Finalizers {
			if f == environmentFinalizer {
				return true
			}
		}
		return false
	}, 10*time.Second, 250*time.Millisecond) {
		t.Fatal("finalizer never added within 10s")
	}

	r := buildPluginTestReconciler(t, ns, pool)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cr)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var final achv1alpha1.Environment
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &final); err != nil {
		t.Fatalf("re-Get Environment: %v", err)
	}

	var errCond *metav1.Condition
	for i := range final.Status.Conditions {
		if final.Status.Conditions[i].Type == "ExecutionResourcesResolved" {
			errCond = &final.Status.Conditions[i]
			break
		}
	}
	if errCond == nil {
		t.Fatalf("ExecutionResourcesResolved condition not found; conditions=%+v", final.Status.Conditions)
	}
	if errCond.Status != metav1.ConditionTrue {
		t.Errorf("ExecutionResourcesResolved.Status = %q; want True (synced plugin should not block). message=%q",
			errCond.Status, errCond.Message)
	}
	if len(final.Status.UnresolvedContextPlugins) != 0 {
		t.Errorf("UnresolvedContextPlugins = %v; want [] (plugin is content-present)", final.Status.UnresolvedContextPlugins)
	}

	t.Logf("PASS (synced): ExecutionResourcesResolved=%s reason=%s message=%q UnresolvedContextPlugins=%v",
		errCond.Status, errCond.Reason, errCond.Message, final.Status.UnresolvedContextPlugins)
}
