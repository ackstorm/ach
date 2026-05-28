//go:build integration

// SPDX-License-Identifier: Apache-2.0

package litellmconn_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/litellmconn"
)

const testNS = "ach-system"

func setupPostgres(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test requires Docker; -short specified")
	}

	pgC, err := tcpostgres.Run(ctx,
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
		t.Skipf("docker required for integration tests: postgres container failed to start: %v", err)
	}

	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("connection string: %v", err)
	}

	migrationsPath, err := filepath.Abs("../../../db/migrations")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("abs migrationsPath: %v", err)
	}

	if err := db.Migrate(connStr, migrationsPath); err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("db.Migrate: %v", err)
	}

	pool, err := db.Open(ctx, connStr)
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("db.Open: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
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

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	return s
}

func seedConn(t *testing.T, ctx context.Context, pool *pgxpool.Pool, endpoint, secretNS, secretName, secretKey string) {
	t.Helper()
	require.NoError(t, db.UpsertLiteLLMConnection(ctx, pool, db.LiteLLMConnectionRow{
		Namespace:                testNS,
		Name:                     "default",
		Endpoint:                 endpoint,
		MasterKeySecretNamespace: secretNS,
		MasterKeySecretName:      secretName,
		MasterKeySecretKey:       secretKey,
		ResourceVersion:          "1",
	}))
}

func secret(ns, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Data:       data,
	}
}

func TestResolve_HappyPath(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	seedConn(t, ctx, pool, "http://litellm.example:4000", testNS, "litellm-master-key", "masterKey")
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
		secret(testNS, "litellm-master-key", map[string][]byte{"masterKey": []byte("sk-test-master-key")}),
	).Build()

	got, err := litellmconn.Resolve(ctx, pool, cli, testNS)
	require.NoError(t, err)
	require.Equal(t, "http://litellm.example:4000", got.Endpoint)
	require.Equal(t, "sk-test-master-key", got.MasterKey)
}

func TestResolve_ConnectionRowMissing(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	_, err := litellmconn.Resolve(ctx, pool, cli, testNS)
	require.True(t, errors.Is(err, litellmconn.ErrLiteLLMConnectionNotReady), "got %v", err)
}

func TestResolve_EndpointEmpty(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	seedConn(t, ctx, pool, "", testNS, "litellm-master-key", "masterKey")
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
		secret(testNS, "litellm-master-key", map[string][]byte{"masterKey": []byte("k")}),
	).Build()

	_, err := litellmconn.Resolve(ctx, pool, cli, testNS)
	require.True(t, errors.Is(err, litellmconn.ErrEndpointEmpty), "got %v", err)
}

func TestResolve_SecretMissing(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	seedConn(t, ctx, pool, "http://litellm.example:4000", testNS, "litellm-master-key", "masterKey")
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

	_, err := litellmconn.Resolve(ctx, pool, cli, testNS)
	require.True(t, errors.Is(err, litellmconn.ErrSecretNotFound), "got %v", err)
}

func TestResolve_SecretKeyMissing(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	seedConn(t, ctx, pool, "http://litellm.example:4000", testNS, "litellm-master-key", "masterKey")
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
		secret(testNS, "litellm-master-key", map[string][]byte{"otherKey": []byte("k")}),
	).Build()

	_, err := litellmconn.Resolve(ctx, pool, cli, testNS)
	require.True(t, errors.Is(err, litellmconn.ErrSecretKeyMissing), "got %v", err)
}

func TestResolve_SecretKeyEmptyValue(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	seedConn(t, ctx, pool, "http://litellm.example:4000", testNS, "litellm-master-key", "masterKey")
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
		secret(testNS, "litellm-master-key", map[string][]byte{"masterKey": {}}),
	).Build()

	_, err := litellmconn.Resolve(ctx, pool, cli, testNS)
	require.True(t, errors.Is(err, litellmconn.ErrSecretKeyMissing), "got %v", err)
}

// TestResolve_CrossNamespaceSecret confirms that when the projection row
// references a Secret in a different namespace from the forwarder's, the
// Secret lookup uses the projection's namespace field — not the
// forwarder namespace. This is the cross-namespace master-key path that
// C7's Helm RBAC permits.
func TestResolve_CrossNamespaceSecret(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupPostgres(t, ctx)
	defer cleanup()

	seedConn(t, ctx, pool, "http://litellm.example:4000", "litellm-system", "litellm-master-key", "masterKey")
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
		secret("litellm-system", "litellm-master-key", map[string][]byte{"masterKey": []byte("sk-x")}),
	).Build()

	got, err := litellmconn.Resolve(ctx, pool, cli, testNS)
	require.NoError(t, err)
	require.Equal(t, "sk-x", got.MasterKey)
}
