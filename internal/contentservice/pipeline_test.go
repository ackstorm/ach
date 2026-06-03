//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration tests for the §15.6 7-gate Content Service pipeline.
//
// Coverage matrix:
//
//   TestPipeline_EndToEnd                    — every D-03 outcome (16+ subtests).
//   TestPipeline_PluginPrecedence            — B2 bare/scoped resolution semantics (5 subtests).
//   TestPipeline_InFlightReadSurvivesRename  — D-02 + SC#4 inode-pin proof.
//   TestPipeline_EmitsOneAuditEventPerRequest — audit emission shape on success + denial.
//   TestPipeline_NoStoreHeader               — drift flag #3 lockdown.
//
// Harness: testcontainers Postgres (via setupPostgresIntegration) +
// miniredis (in-memory) + an in-process mock LiteLLM TeamsResolver that
// returns a configurable team list per email. No real LiteLLM client.

package contentservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/contentservice/envcache"
	"github.com/ackstorm/ach/internal/credhash"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/metrics"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type testFixtures struct {
	t            *testing.T
	pool         *pgxpool.Pool
	mr           *miniredis.Miniredis
	rdb          *redis.Client
	cacheRoot    string
	pepper       []byte
	deps         Deps
	auditBuf     *bytes.Buffer
	teamsFake    *fakeTeams
	registry     *prometheus.Registry
	cleanupFuncs []func()
}

func (f *testFixtures) cleanup() {
	for _, fn := range f.cleanupFuncs {
		fn()
	}
}

// setupPostgresIntegration boots a fresh postgres:16-alpine via
// testcontainers-go and applies migrations. Cleanup terminates the
// container.
func setupPostgresIntegration(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
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
	// Migration path: from internal/contentservice → ../../db/migrations
	migrationsPath, err := filepath.Abs("../../db/migrations")
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

// fakeTeams is the in-process TeamsResolver stand-in. teamsByEmail
// drives the response; err overrides everything for transport-failure
// tests.
type fakeTeams struct {
	mu           sync.Mutex
	teamsByEmail map[string][]string
	err          error
	calls        int32
}

func (f *fakeTeams) Resolve(_ context.Context, email string) ([]string, error) {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if teams, ok := f.teamsByEmail[email]; ok {
		return teams, nil
	}
	return []string{}, nil
}

func (f *fakeTeams) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeTeams) setTeams(email string, teams []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.teamsByEmail == nil {
		f.teamsByEmail = map[string][]string{}
	}
	f.teamsByEmail[email] = teams
}

// setupIntegration wires the harness: Postgres + miniredis + cache root
// tempdir + Deps with all real implementations except the mock teams
// resolver.
func setupIntegration(t *testing.T) *testFixtures {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	pool, pgCleanup := setupPostgresIntegration(t, ctx)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	pepper := []byte("integration-test-pepper-32-bytes!")

	// Resolver: real keystore.NewDBResolver wrapping the pool.
	baseResolver, err := keystore.NewDBResolver(pool, pepper)
	if err != nil {
		t.Fatalf("NewDBResolver: %v", err)
	}

	// EnvCache: real envcache wrapping db.GetEnvironmentByName via a
	// Loader closure that converts EnvironmentRow → EnvRow.
	loader := func(ctx context.Context, ns, name string) (*envcache.EnvRow, error) {
		row, err := db.GetEnvironmentByName(ctx, pool, ns, name)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, nil //nolint:nilnil
		}
		return &envcache.EnvRow{
			AuthorizedTeams:  row.AuthorizedTeams,
			ContextPrompts:   row.ContextPrompts,
			ContextPlugins:   row.ContextPlugins,
			ContextArtifacts: row.ContextArtifacts,
		}, nil
	}
	envCache, err := envcache.NewCachedEnvCache(loader, rdb)
	if err != nil {
		t.Fatalf("NewCachedEnvCache: %v", err)
	}

	teams := &fakeTeams{}
	reg := prometheus.NewRegistry()
	col := metrics.NewContentServiceCollectors(reg)
	llmUnreach := metrics.MustRegisterLitellmUnreachable(reg)
	auditBuf := &bytes.Buffer{}
	auditLog := slog.New(slog.NewJSONHandler(auditBuf, nil))

	cacheRoot := t.TempDir()
	for _, sub := range []string{"prompt", "plugin", "artifact"} {
		if err := os.MkdirAll(filepath.Join(cacheRoot, sub), 0o755); err != nil {
			t.Fatalf("mkdir cache root: %v", err)
		}
	}

	deps := Deps{
		CacheRoot:          cacheRoot,
		Namespace:          "default",
		Pool:               pool,
		EnvCache:           envCache,
		Resolver:           baseResolver,
		Teams:              teams,
		Metrics:            col,
		LiteLLMUnreachable: llmUnreach,
		AuditLog:           auditLog,
		Logger:             slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	return &testFixtures{
		t:            t,
		pool:         pool,
		mr:           mr,
		rdb:          rdb,
		cacheRoot:    cacheRoot,
		pepper:       pepper,
		deps:         deps,
		auditBuf:     auditBuf,
		teamsFake:    teams,
		registry:     reg,
		cleanupFuncs: []func(){pgCleanup, func() { _ = rdb.Close() }},
	}
}

// ---------------------------------------------------------------------------
// Seed helpers
// ---------------------------------------------------------------------------

func (f *testFixtures) seedEnvironment(name string, authorizedTeams, prompts, plugins, artifacts []string) {
	f.t.Helper()
	ctx := context.Background()
	// Postgres NOT NULL applies to every text[] column in the
	// environments projection. Default any nil slice to an empty
	// slice so the SQL INSERT does not fail with a constraint error.
	defaultSlice := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	row := db.EnvironmentRow{
		Namespace:         "default",
		Name:              name,
		AuthorizedTeams:   defaultSlice(authorizedTeams),
		ContextPrompts:    defaultSlice(prompts),
		ContextPlugins:    defaultSlice(plugins),
		ContextArtifacts:  defaultSlice(artifacts),
		RuntimeModels:     []string{},
		RuntimeMCPServers: []string{},
		RuntimeA2AAgents:  []string{},
		ResourceVersion:   "1",
	}
	if err := db.UpsertEnvironment(ctx, f.pool, row); err != nil {
		f.t.Fatalf("UpsertEnvironment(%s): %v", name, err)
	}
}

func (f *testFixtures) seedPrompt(name string, lsr *time.Time, maxStaleness int64, contentType *string, body []byte) {
	f.t.Helper()
	ctx := context.Background()
	path := filepath.Join(f.cacheRoot, "prompt", name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		f.t.Fatalf("write prompt file: %v", err)
	}
	row := db.PromptRow{
		Namespace:             "default",
		Name:                  name,
		StorageLocation:       path,
		ContentType:           contentType,
		LastSuccessfulRefresh: lsr,
		MaxStalenessSeconds:   maxStaleness,
		ResourceVersion:       "1",
	}
	if err := db.UpsertPrompt(ctx, f.pool, row); err != nil {
		f.t.Fatalf("UpsertPrompt: %v", err)
	}
}

func (f *testFixtures) seedPlugin(name string, lsr *time.Time, maxStaleness int64, body []byte) {
	f.t.Helper()
	ctx := context.Background()
	path := filepath.Join(f.cacheRoot, "plugin", name+".tar.gz")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		f.t.Fatalf("write plugin file: %v", err)
	}
	row := db.PluginRow{
		Namespace:             "default",
		Name:                  name,
		StorageLocation:       path,
		LastSuccessfulRefresh: lsr,
		MaxStalenessSeconds:   maxStaleness,
		ResourceVersion:       "1",
	}
	if err := db.UpsertPlugin(ctx, f.pool, row); err != nil {
		f.t.Fatalf("UpsertPlugin: %v", err)
	}
}

func (f *testFixtures) softDeletePlugin(name string) {
	f.t.Helper()
	if err := db.SoftDeletePlugin(context.Background(), f.pool, "default", name); err != nil {
		f.t.Fatalf("SoftDeletePlugin: %v", err)
	}
}

func (f *testFixtures) seedMarketplacePlugin(marketplaceName, pluginName string, lsr time.Time, maxStaleness int64, body []byte) {
	f.t.Helper()
	ctx := context.Background()
	// All marketplace plugin files live in cache/plugin/<name>.tar.gz —
	// the marketplace_name namespace is not part of the on-disk path
	// per CS-07 / paths.go.
	path := filepath.Join(f.cacheRoot, "plugin", pluginName+".tar.gz")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		f.t.Fatalf("write marketplace plugin file: %v", err)
	}
	p := db.MarketplacePlugin{
		MarketplaceName:       marketplaceName,
		Name:                  pluginName,
		StorageLocation:       path,
		UpstreamRev:           "rev1",
		LastSuccessfulRefresh: lsr,
		NextRefreshAt:         lsr.Add(time.Hour),
		MaxStalenessSeconds:   maxStaleness,
	}
	if err := db.UpsertMarketplacePlugin(ctx, f.pool, p); err != nil {
		f.t.Fatalf("UpsertMarketplacePlugin: %v", err)
	}
}

// seedMarketplacePluginWithLocation seeds a marketplace_plugins row whose
// storage_location is the caller-supplied path (NOT derived from cacheRoot).
// Used by security regression tests that need to inject an out-of-root path.
// No file is written; the containment check (PluginStoragePathWithinRoot) must
// fire before the path is ever opened.
func (f *testFixtures) seedMarketplacePluginWithLocation(marketplaceName, pluginName, storageLocation string, lsr time.Time, maxStaleness int64) {
	f.t.Helper()
	ctx := context.Background()
	p := db.MarketplacePlugin{
		MarketplaceName:       marketplaceName,
		Name:                  pluginName,
		StorageLocation:       storageLocation,
		UpstreamRev:           "rev1",
		LastSuccessfulRefresh: lsr,
		NextRefreshAt:         lsr.Add(time.Hour),
		MaxStalenessSeconds:   maxStaleness,
	}
	if err := db.UpsertMarketplacePlugin(ctx, f.pool, p); err != nil {
		f.t.Fatalf("UpsertMarketplacePluginWithLocation: %v", err)
	}
}

func (f *testFixtures) seedArtifact(name, scope string, lsr *time.Time, maxStaleness int64, body []byte) {
	f.t.Helper()
	ctx := context.Background()
	var path string
	if scope == "directory" {
		path = filepath.Join(f.cacheRoot, "artifact", name+".tar.gz")
	} else {
		path = filepath.Join(f.cacheRoot, "artifact", name)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		f.t.Fatalf("write artifact file: %v", err)
	}
	row := db.ArtifactRow{
		Namespace:             "default",
		Name:                  name,
		StorageLocation:       path,
		Scope:                 scope,
		LastSuccessfulRefresh: lsr,
		MaxStalenessSeconds:   maxStaleness,
		ResourceVersion:       "1",
	}
	if err := db.UpsertArtifact(ctx, f.pool, row); err != nil {
		f.t.Fatalf("UpsertArtifact: %v", err)
	}
}

// seedPersonalKey inserts an active pk_ row and returns the plaintext.
func (f *testFixtures) seedPersonalKey(keyID, plaintext, ownerEmail string) {
	f.t.Helper()
	hash, err := credhash.Hash(f.pepper, []byte(plaintext))
	if err != nil {
		f.t.Fatalf("credhash: %v", err)
	}
	expires := time.Now().Add(7 * 24 * time.Hour).UTC()
	row := db.PkInsertRow{
		KeyID:          keyID,
		CredentialHash: hash,
		OwnerEmail:     ownerEmail,
		ExpiresAt:      expires,
	}
	if err := db.InsertPersonalKey(context.Background(), f.pool, row); err != nil {
		f.t.Fatalf("InsertPersonalKey: %v", err)
	}
}

// seedEnvironmentKey inserts an active ek_ row and returns the plaintext.
func (f *testFixtures) seedEnvironmentKey(keyID, plaintext, ownerEmail, environment string) {
	f.t.Helper()
	hash, err := credhash.Hash(f.pepper, []byte(plaintext))
	if err != nil {
		f.t.Fatalf("credhash: %v", err)
	}
	row := db.EkInsertRow{
		KeyID:          keyID,
		CredentialHash: hash,
		Environment:    environment,
		OwnerEmail:     ownerEmail,
		Name:           "test-ek",
	}
	if err := db.InsertEnvironmentKey(context.Background(), f.pool, row); err != nil {
		f.t.Fatalf("InsertEnvironmentKey: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Request helpers
// ---------------------------------------------------------------------------

// doRequest builds a chi.Router with the fixture's Deps and serves a
// single request. The request always carries a stable request-id so
// audit/envelope assertions can match it.
func (f *testFixtures) doRequest(method, path string, headers map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	RegisterRoutes(r, f.deps)
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// requireOutcome decodes the response envelope and asserts the code.
func requireOutcome(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	resp := rec.Result()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantStatus {
		t.Errorf("status=%d, want %d", resp.StatusCode, wantStatus)
	}
	if wantCode == "" {
		return
	}
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	e, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("body missing error object: %v", env)
	}
	if got := e["code"]; got != wantCode {
		t.Errorf("error.code=%v, want %s", got, wantCode)
	}
}

// ---------------------------------------------------------------------------
// TestPipeline_EndToEnd — D-03 outcome matrix
// ---------------------------------------------------------------------------

func TestPipeline_EndToEnd(t *testing.T) {
	t.Parallel()
	fx := setupIntegration(t)
	defer fx.cleanup()

	// Common seed: prod environment authorizes team-a, allowlists
	// p1/p2 (prompts), pl1/pl2/shared@mkt-b (plugins), a1/a2 (artifacts).
	// shared@mkt-b tests the scoped-ref (name@marketplace) resolution path.
	fx.seedEnvironment("prod",
		[]string{"team-a"},
		[]string{"p1", "p2"},
		[]string{"pl1", "pl2", "pl-no-row", "shared@mkt-b"},
		[]string{"a1", "a2"},
	)
	now := time.Now().UTC()
	fx.seedPrompt("p1", &now, 600, nil, []byte("hello prompt"))
	fx.seedPlugin("pl1", &now, 600, []byte{0x1f, 0x8b, 0x08, 0x00, 0xAA, 0xBB, 0xCC})
	fx.seedArtifact("a1", "object", &now, 600, []byte("artifact-bytes"))
	// Seed the scoped marketplace plugin: (marketplace_name="mkt-b", name="shared").
	fx.seedMarketplacePlugin("mkt-b", "shared", now, 600, []byte{0x1f, 0x8b, 0x08, 0x00, 0x11, 0x22, 0x33})
	fx.seedPersonalKey("pkid_a", "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice@x.com")
	fx.seedEnvironmentKey("ekid_a", "ek-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bob@x.com", "prod")
	fx.teamsFake.setTeams("alice@x.com", []string{"team-a"})
	fx.teamsFake.setTeams("alice-team-mismatch@x.com", []string{"team-z"})

	t.Run("200 success pk_ plugin", func(t *testing.T) {
		rec := fx.doRequest("GET", "/content/plugin/pl1", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		resp := rec.Result()
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != "application/gzip" {
			t.Errorf("Content-Type=%q, want application/gzip", got)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control=%q, want no-store", got)
		}
		body, _ := io.ReadAll(resp.Body)
		if len(body) != 7 {
			t.Errorf("body len=%d, want 7", len(body))
		}
	})

	t.Run("200 success scoped marketplace plugin", func(t *testing.T) {
		rec := fx.doRequest("GET", "/content/plugin/shared@mkt-b", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("scoped plugin: got %d, want 200", rec.Code)
		}
	})

	t.Run("200 success ek_ prompt", func(t *testing.T) {
		rec := fx.doRequest("GET", "/content/prompt/p1", map[string]string{
			"x-ach-key": "ek-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		})
		resp := rec.Result()
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status=%d, want 200", resp.StatusCode)
		}
	})

	t.Run("400 missing_environment pk_", func(t *testing.T) {
		rec := fx.doRequest("GET", "/content/prompt/p1", map[string]string{
			"x-ach-key": "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		})
		requireOutcome(t, rec, 400, "missing_environment")
	})

	t.Run("400 invalid_key_format garbage", func(t *testing.T) {
		rec := fx.doRequest("GET", "/content/prompt/p1", map[string]string{
			"x-ach-key": "garbage_no_prefix",
		})
		requireOutcome(t, rec, 400, "invalid_key_format")
	})

	t.Run("400 invalid_key_format empty", func(t *testing.T) {
		rec := fx.doRequest("GET", "/content/prompt/p1", map[string]string{})
		requireOutcome(t, rec, 400, "invalid_key_format")
	})

	t.Run("401 expired_or_revoked pk_ not in DB", func(t *testing.T) {
		rec := fx.doRequest("GET", "/content/prompt/p1", map[string]string{
			"x-ach-key":         "pk-cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			"x-ach-environment": "prod",
		})
		requireOutcome(t, rec, 401, "expired_or_revoked")
	})

	t.Run("403 unauthorized_team", func(t *testing.T) {
		// Seed pk_ with mismatched team.
		fx.seedPersonalKey("pkid_z", "pk-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "alice-team-mismatch@x.com")
		// Refresh envcache (just expire it; loader will re-fetch).
		fx.mr.FlushAll()
		rec := fx.doRequest("GET", "/content/prompt/p1", map[string]string{
			"x-ach-key":         "pk-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			"x-ach-environment": "prod",
		})
		requireOutcome(t, rec, 403, "unauthorized_team")
	})

	t.Run("403 wrong_environment ek_ header mismatch", func(t *testing.T) {
		// ek-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb is bound to prod; request asks for staging.
		fx.seedEnvironment("staging", []string{"team-a"},
			[]string{"p1"}, []string{}, []string{})
		fx.mr.FlushAll()
		rec := fx.doRequest("GET", "/content/prompt/p1", map[string]string{
			"x-ach-key":         "ek-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"x-ach-environment": "staging",
		})
		requireOutcome(t, rec, 403, "wrong_environment")
	})

	t.Run("403 unauthorized_content cheaper-first", func(t *testing.T) {
		// Seed a prompt row that the environment does NOT allow-list.
		fx.seedPrompt("p-disallowed", &now, 600, nil, []byte("nope"))
		rec := fx.doRequest("GET", "/content/prompt/p-disallowed", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		// Even though the CRD projection row exists, the allowlist gate
		// fires FIRST per D-04 cheaper-first divergence → 403 not 404.
		requireOutcome(t, rec, 403, "unauthorized_content")
	})

	t.Run("404 environment_not_found", func(t *testing.T) {
		rec := fx.doRequest("GET", "/content/prompt/p1", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "does-not-exist",
		})
		requireOutcome(t, rec, 404, "environment_not_found")
	})

	t.Run("404 content_not_found in allowlist but no projection row", func(t *testing.T) {
		// pl-no-row is in env.context.plugins but has no plugins row AND
		// no marketplace_plugins row.
		rec := fx.doRequest("GET", "/content/plugin/pl-no-row", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		requireOutcome(t, rec, 404, "content_not_found")
	})

	t.Run("503 stale_cache_expired", func(t *testing.T) {
		stale := now.Add(-1 * time.Hour)
		fx.seedPrompt("p2", &stale, 60, nil, []byte("stale"))
		rec := fx.doRequest("GET", "/content/prompt/p2", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		requireOutcome(t, rec, 503, "stale_cache_expired")
	})

	t.Run("503 stale_cache_expired NULL LSR", func(t *testing.T) {
		fx.seedPlugin("pl2", nil, 600, []byte("null-lsr"))
		rec := fx.doRequest("GET", "/content/plugin/pl2", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		requireOutcome(t, rec, 503, "stale_cache_expired")
	})

	t.Run("503 litellm_unreachable", func(t *testing.T) {
		// Force teams resolver to error → 503 + litellm_unreachable Inc.
		fx.teamsFake.setErr(errors.New("connection refused"))
		defer fx.teamsFake.setErr(nil)
		// Refresh envcache so the (alice@x.com → team-a) cache doesn't
		// reuse a previously-cached value — but wait, envcache caches
		// EnvRow not teams. The teams resolver is direct each call;
		// no flush needed for teams.
		// Need to use the alice@x.com pk_ but the team cache is hit-
		// avoided because we use the resolver directly.
		rec := fx.doRequest("GET", "/content/prompt/p1", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		requireOutcome(t, rec, 503, "litellm_unreachable")
	})

	t.Run("200 ignores Range header", func(t *testing.T) {
		rec := fx.doRequest("GET", "/content/plugin/pl1", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
			"Range":             "bytes=0-2",
		})
		resp := rec.Result()
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status=%d, want 200 (Range ignored)", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if len(body) != 7 {
			t.Errorf("body len=%d, want full 7 bytes", len(body))
		}
	})

	t.Run("200 ignores If-None-Match", func(t *testing.T) {
		rec := fx.doRequest("GET", "/content/plugin/pl1", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
			"If-None-Match":     `"deadbeef"`,
		})
		resp := rec.Result()
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status=%d, want 200 (If-None-Match ignored)", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// TestPipeline_PluginPrecedence — §12.3 CTE matrix
// ---------------------------------------------------------------------------

func TestPipeline_PluginPrecedence(t *testing.T) {
	t.Parallel()
	fx := setupIntegration(t)
	defer fx.cleanup()
	now := time.Now().UTC()

	fx.seedEnvironment("prod",
		[]string{"team-a"},
		[]string{},
		[]string{"shared"},
		[]string{},
	)
	fx.seedPersonalKey("pkid_a", "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice@x.com")
	fx.teamsFake.setTeams("alice@x.com", []string{"team-a"})

	t.Run("CRD wins", func(t *testing.T) {
		fx.seedPlugin("shared", &now, 600, []byte("from-crd"))
		fx.seedMarketplacePlugin("anthropic-mkt", "shared", now, 600, []byte("from-mkt"))
		defer func() {
			_ = db.DeletePlugin(context.Background(), fx.pool, "default", "shared")
		}()
		rec := fx.doRequest("GET", "/content/plugin/shared", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		resp := rec.Result()
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 200 {
			t.Fatalf("status=%d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		// Both the CRD seed and the marketplace seed write to the same
		// on-disk path (CS-07 — marketplace_name is not in the path).
		// B2 bare-ref semantics: bare "shared" resolves ONLY via the plugins
		// (CRD) table, so the active plugins row is returned (200). The
		// marketplace row is irrelevant for bare refs. Verify a non-empty body.
		if len(body) == 0 {
			t.Errorf("got empty body")
		}
	})

	t.Run("bare name no marketplace fallback", func(t *testing.T) {
		// B2 semantics: bare ref (no @marketplace) resolves ONLY against the
		// plugins (CRD) table — no alphabetical marketplace fallback. Multiple
		// marketplace rows for "shared" exist, but a bare /content/plugin/shared
		// request with no active CRD row returns 404. Use a scoped ref
		// "shared@z-marketplace" or "shared@a-marketplace" for marketplace access.
		fx.seedMarketplacePlugin("z-marketplace", "shared", now, 600, []byte("z-mkt"))
		fx.seedMarketplacePlugin("a-marketplace", "shared", now, 600, []byte("a-mkt"))
		rec := fx.doRequest("GET", "/content/plugin/shared", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		requireOutcome(t, rec, 404, "content_not_found")
	})

	t.Run("scoped ref resolves marketplace plugin", func(t *testing.T) {
		// With a scoped ref the exact (marketplace_name, name) PK is probed.
		// "shared@a-marketplace" was seeded in the subtest above (same fixture pool).
		// Update env to allowlist the scoped ref first.
		fx.seedEnvironment("prod",
			[]string{"team-a"},
			[]string{},
			[]string{"shared", "shared@a-marketplace"},
			[]string{},
		)
		fx.mr.FlushAll()
		rec := fx.doRequest("GET", "/content/plugin/shared@a-marketplace", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("scoped marketplace ref: status=%d, want 200", rec.Code)
		}
		// Restore env for subsequent subtests.
		fx.seedEnvironment("prod",
			[]string{"team-a"},
			[]string{},
			[]string{"shared"},
			[]string{},
		)
		fx.mr.FlushAll()
	})

	t.Run("soft-deleted CRD bare ref returns 404", func(t *testing.T) {
		// B2 semantics: soft-deleted CRD plugin + bare ref → 404.
		// No marketplace fallback for bare refs.
		fx.seedPlugin("shared", &now, 600, []byte("crd-soft-deleted"))
		fx.softDeletePlugin("shared")
		defer func() {
			_ = db.DeletePlugin(context.Background(), fx.pool, "default", "shared")
		}()
		rec := fx.doRequest("GET", "/content/plugin/shared", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		requireOutcome(t, rec, 404, "content_not_found")
	})

	t.Run("no match → 404", func(t *testing.T) {
		// "another-plugin" is in env.context but has no plugin AND no
		// marketplace row.
		fx.seedEnvironment("prod",
			[]string{"team-a"},
			[]string{},
			[]string{"shared", "another-plugin"},
			[]string{},
		)
		fx.mr.FlushAll()
		rec := fx.doRequest("GET", "/content/plugin/another-plugin", map[string]string{
			"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"x-ach-environment": "prod",
		})
		requireOutcome(t, rec, 404, "content_not_found")
	})
}

// ---------------------------------------------------------------------------
// TestPipeline_InFlightReadSurvivesRename — D-02 + SC#4 inode pin
// ---------------------------------------------------------------------------

func TestPipeline_InFlightReadSurvivesRename(t *testing.T) {
	t.Parallel()
	fx := setupIntegration(t)
	defer fx.cleanup()
	now := time.Now().UTC()

	fx.seedEnvironment("prod", []string{"team-a"}, []string{},
		[]string{"big-plugin"}, []string{})
	fx.seedPersonalKey("pkid_a", "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice@x.com")
	fx.teamsFake.setTeams("alice@x.com", []string{"team-a"})
	originalBody := bytes.Repeat([]byte{0xAA}, 64*1024) // 64 KiB
	fx.seedPlugin("big-plugin", &now, 600, originalBody)

	// Trigger the request synchronously (httptest), but after the
	// handler has started the pipeline (which opens the file), we will
	// rename the on-disk file and verify the served bytes match the
	// ORIGINAL content. The pipeline opens *os.File EARLY (D-02), so
	// the inode is pinned for the response stream.
	//
	// Note: with httptest.NewRecorder there is no concurrent client
	// reading the body — the entire response is buffered before we can
	// inspect it. So the rename here must happen BETWEEN pipeline file-
	// open and io.Copy completion. We achieve that by inserting a slow
	// network-mock that pauses inside io.Copy. Instead, here we do the
	// rename test more directly: open the file in the handler, rename
	// outside, then assert the body matches original.
	//
	// Simpler approach: rename the file BEFORE the pipeline runs but
	// AFTER seedPlugin. The pipeline runs, opens the (now-renamed)
	// file (it no longer exists at that path), and either fails or
	// reads the moved file. The proper SC#4 test would interleave a
	// rename with an in-flight read, but our infrastructure makes that
	// hard without injecting hooks into the pipeline. Instead, run an
	// after-the-fact assertion: the request succeeds with the original
	// bytes when the request runs to completion before any rename
	// hits. The rename-after-completion case is the legitimate
	// invariant: a finished response is unaffected by subsequent fs
	// mutations.
	rec := fx.doRequest("GET", "/content/plugin/big-plugin", map[string]string{
		"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"x-ach-environment": "prod",
	})
	resp := rec.Result()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, originalBody) {
		t.Errorf("body does not match original: got len=%d, want len=%d", len(body), len(originalBody))
	}

	// Atomic rename simulating the Operator's refresh path.
	oldPath := filepath.Join(fx.cacheRoot, "plugin", "big-plugin.tar.gz")
	tmpPath := oldPath + ".new"
	newBody := bytes.Repeat([]byte{0xBB}, 64*1024)
	if err := os.WriteFile(tmpPath, newBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpPath, oldPath); err != nil {
		t.Fatal(err)
	}

	// Second request after rename returns the NEW bytes — confirms
	// renames between requests are honored (NOT pinned across
	// requests, only within an open FD's lifetime per D-02).
	rec2 := fx.doRequest("GET", "/content/plugin/big-plugin", map[string]string{
		"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"x-ach-environment": "prod",
	})
	resp2 := rec2.Result()
	defer func() { _ = resp2.Body.Close() }()
	body2, _ := io.ReadAll(resp2.Body)
	if !bytes.Equal(body2, newBody) {
		t.Errorf("second request after rename did not return new bytes")
	}

	// True SC#4 in-flight test: open the file directly (mimicking
	// pipeline's early-open), do the rename, then read from the open
	// FD — we should see the ORIGINAL bytes regardless. This proves
	// the kernel-level inode-pin invariant the pipeline depends on.
	fx.seedPlugin("inode-pin", &now, 600, originalBody)
	pinnedPath := filepath.Join(fx.cacheRoot, "plugin", "inode-pin.tar.gz")
	f, err := os.Open(pinnedPath) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	// Rename underneath the open FD.
	tmp2 := pinnedPath + ".new"
	if err := os.WriteFile(tmp2, newBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp2, pinnedPath); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, originalBody) {
		t.Errorf("inode-pin invariant broken: open FD read NEW bytes after rename")
	}
}

// TestPipeline_InFlightReadSurvivesRename_ServePath closes the coverage
// gap left by TestPipeline_InFlightReadSurvivesRename above: that test's
// "true SC#4" assertion (the inode-pin step) operates on a bare os.Open
// FD and therefore proves only the Linux kernel primitive, NOT that ACH's
// own serve path honors it. This test drives the REAL serve functions:
//
//  1. pipeline() runs all gates and performs the D-02 early open (gate 8),
//     returning the held *os.File in row.File — exactly what serve() does.
//  2. We then simulate the Operator's refresh: stage a NEW body under a
//     temp name and os.Rename(2) it over the published cache path while
//     our pipeline FD is still open (the §10.3 atomic-rename invariant —
//     same dir / same filesystem, mirrored from materializeExternalRef +
//     pluginmarketplace_controller).
//  3. stream() copies from the pipeline-held FD (io.Copy → *os.File.WriteTo
//     → sendfile(2) on a TCP writer; plain copy on httptest.Recorder).
//
// The served bytes MUST equal the ORIGINAL content: the rename repointed
// the directory entry at the new inode, but the FD opened in step 1 pins
// the old inode for the lifetime of the stream. A regression that re-opens
// the file by path inside stream() (instead of consuming the passed FD)
// would serve the NEW bytes and fail here — deterministically, with no
// goroutine/timing race, which is why this lives at the integration layer
// and the live-cluster e2e variant stays a documented non-goal.
func TestPipeline_InFlightReadSurvivesRename_ServePath(t *testing.T) {
	t.Parallel()
	fx := setupIntegration(t)
	defer fx.cleanup()
	now := time.Now().UTC()

	fx.seedEnvironment("prod", []string{"team-a"}, nil, []string{"big-plugin"}, nil)
	fx.seedPersonalKey("pkid_a", "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice@x.com")
	fx.teamsFake.setTeams("alice@x.com", []string{"team-a"})
	originalBody := bytes.Repeat([]byte{0xAA}, 64*1024) // 64 KiB
	fx.seedPlugin("big-plugin", &now, 600, originalBody)

	// Build a request with the chi route context "name" param populated so
	// pipeline() resolves the same way serve() would, then run the pipeline
	// directly to capture the gate-8 open FD.
	req := httptest.NewRequest("GET", "/content/plugin/big-plugin", nil)
	req.Header.Set("x-ach-key", "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	req.Header.Set("x-ach-environment", "prod")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "big-plugin")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	row, errR := pipeline(req.Context(), fx.deps, kindPlugin, req)
	if errR != nil {
		t.Fatalf("pipeline returned error: %+v", errR.errResp)
	}
	if row == nil || row.File == nil {
		t.Fatal("pipeline returned nil row or nil File")
	}
	defer func() { _ = row.File.Close() }()

	// Operator refresh: stage a NEW body and atomically rename(2) it over
	// the published cache path while our FD is still open.
	cachePath := filepath.Join(fx.cacheRoot, "plugin", "big-plugin.tar.gz")
	newBody := bytes.Repeat([]byte{0xBB}, 64*1024)
	tmpPath := cachePath + ".new"
	if err := os.WriteFile(tmpPath, newBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpPath, cachePath); err != nil {
		t.Fatal(err)
	}

	// Stream from the pipeline-held FD. Must serve ORIGINAL bytes.
	rec := httptest.NewRecorder()
	n, err := stream(rec, req, row.File, row.ContentType, row.Size)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if n != int64(len(originalBody)) {
		t.Errorf("streamed %d bytes, want %d", n, len(originalBody))
	}
	if !bytes.Equal(rec.Body.Bytes(), originalBody) {
		t.Errorf("serve path did not pin inode across rename: streamed NEW bytes (0x%02X...) — want ORIGINAL (0xAA...)",
			rec.Body.Bytes()[0])
	}
}

// ---------------------------------------------------------------------------
// TestPipeline_EmitsOneAuditEventPerRequest
// ---------------------------------------------------------------------------

func TestPipeline_EmitsOneAuditEventPerRequest(t *testing.T) {
	t.Parallel()
	fx := setupIntegration(t)
	defer fx.cleanup()
	now := time.Now().UTC()

	fx.seedEnvironment("prod", []string{"team-a"}, []string{}, []string{"plgn"}, []string{})
	fx.seedPlugin("plgn", &now, 600, []byte("ok"))
	fx.seedPersonalKey("pkid_a", "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice@x.com")
	fx.teamsFake.setTeams("alice@x.com", []string{"team-a"})

	// Success: one audit line with outcome=forwarded.
	fx.auditBuf.Reset()
	rec := fx.doRequest("GET", "/content/plugin/plgn", map[string]string{
		"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"x-ach-environment": "prod",
	})
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	lines := countAuditLines(fx.auditBuf.String())
	if lines != 1 {
		t.Errorf("audit lines=%d after success, want 1", lines)
	}
	if !strings.Contains(fx.auditBuf.String(), `"outcome":"forwarded"`) {
		t.Errorf("audit missing outcome=forwarded:\n%s", fx.auditBuf.String())
	}
	if !strings.Contains(fx.auditBuf.String(), `"target.kind":"plugin"`) {
		t.Errorf("audit missing target.kind=plugin:\n%s", fx.auditBuf.String())
	}

	// Denial: one audit line with outcome=unauthorized_team.
	fx.auditBuf.Reset()
	fx.seedPersonalKey("pkid_z", "pk-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "wrong-team@x.com")
	fx.teamsFake.setTeams("wrong-team@x.com", []string{"team-z"})
	rec = fx.doRequest("GET", "/content/plugin/plgn", map[string]string{
		"x-ach-key":         "pk-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
		"x-ach-environment": "prod",
	})
	if rec.Code != 403 {
		t.Fatalf("status=%d", rec.Code)
	}
	lines = countAuditLines(fx.auditBuf.String())
	if lines != 1 {
		t.Errorf("audit lines=%d after denial, want 1", lines)
	}
	if !strings.Contains(fx.auditBuf.String(), `"outcome":"unauthorized_team"`) {
		t.Errorf("audit missing outcome=unauthorized_team:\n%s", fx.auditBuf.String())
	}
}

func countAuditLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n++
	}
	return n
}

// ---------------------------------------------------------------------------
// TestPipeline_NoStoreHeader — drift flag #3 lockdown
// ---------------------------------------------------------------------------

func TestPipeline_NoStoreHeader(t *testing.T) {
	t.Parallel()
	fx := setupIntegration(t)
	defer fx.cleanup()
	now := time.Now().UTC()
	fx.seedEnvironment("prod", []string{"team-a"}, []string{}, []string{"plgn"}, []string{})
	fx.seedPlugin("plgn", &now, 600, []byte("ok"))
	fx.seedPersonalKey("pkid_a", "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice@x.com")
	fx.teamsFake.setTeams("alice@x.com", []string{"team-a"})

	rec := fx.doRequest("GET", "/content/plugin/plgn", map[string]string{
		"x-ach-key":         "pk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"x-ach-environment": "prod",
	})
	resp := rec.Result()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control=%q, want no-store (drift flag #3)", got)
	}
	if got := resp.Header.Get("Transfer-Encoding"); strings.EqualFold(got, "chunked") {
		t.Errorf("Transfer-Encoding=%q includes chunked (want identity transfer)", got)
	}
}

// ---------------------------------------------------------------------------
// TestPipeline_PluginContainment — path-traversal + malformed-ref regressions
// ---------------------------------------------------------------------------

// TestPipeline_PluginContainment proves the two gate-8 / gate-6 security
// defences added in the source fixes:
//
//  1. Path-traversal 404: a marketplace_plugins row whose storage_location
//     escapes cacheRoot (e.g. /etc/passwd) must return 404, not 200.
//     Proves PluginStoragePathWithinRoot fires inside pipeline gate 8.
//
//  2. Malformed-ref 404: a URL parameter whose ref is malformed ("bad@" —
//     '@' present but empty marketplace) must return 404.
//     Proves pluginref.Valid rejection inside resolveContent gate 6.
func TestPipeline_PluginContainment(t *testing.T) {
	t.Parallel()
	fx := setupIntegration(t)
	defer fx.cleanup()
	now := time.Now().UTC()

	// Personal key + team setup shared by both subtests.
	fx.seedPersonalKey("pkid_contain", "pk-containmentcontainmentcontainmentcontainmentcontainmentcontainme", "eve@x.com")
	fx.teamsFake.setTeams("eve@x.com", []string{"team-a"})

	t.Run("path-traversal storage_location returns 404", func(t *testing.T) {
		// Seed an environment that allowlists "pwn@evil-mkt" so gate 5 passes.
		fx.seedEnvironment("env-containment",
			[]string{"team-a"},
			[]string{},
			[]string{"pwn@evil-mkt"},
			[]string{},
		)
		// Seed the marketplace row with storage_location=/etc/passwd.
		// Gate 8 must detect the escape and return 404 (PluginStoragePathWithinRoot → ok=false).
		// No file is written to /etc/passwd — the containment check fires before os.Open.
		fx.seedMarketplacePluginWithLocation("evil-mkt", "pwn", "/etc/passwd", now, 86400)

		rec := fx.doRequest("GET", "/content/plugin/pwn@evil-mkt", map[string]string{
			"x-ach-key":         "pk-containmentcontainmentcontainmentcontainmentcontainmentcontainme",
			"x-ach-environment": "env-containment",
		})
		requireOutcome(t, rec, http.StatusNotFound, "content_not_found")
	})

	t.Run("malformed ref name@ returns 404", func(t *testing.T) {
		// "bad@" has '@' present but an empty marketplace segment — pluginref.Valid rejects it.
		// Allowlist "bad@" so gate 5 passes; gate 6 (resolveContent) must return 404 via
		// the pluginref.Valid check before any DB lookup.
		fx.seedEnvironment("env-badref",
			[]string{"team-a"},
			[]string{},
			[]string{"bad@"},
			[]string{},
		)
		// Seed a personal key bound to this env to cover the ek_ path cleanly.
		fx.seedEnvironmentKey("ekid_badref", "ek-badrefbadrefbadrefbadrefbadrefbadrefbadrefbadrefbadrefbadrefbadx", "eve@x.com", "env-badref")

		rec := fx.doRequest("GET", "/content/plugin/bad@", map[string]string{
			"x-ach-key": "ek-badrefbadrefbadrefbadrefbadrefbadrefbadrefbadrefbadrefbadrefbadx",
		})
		requireOutcome(t, rec, http.StatusNotFound, "content_not_found")
	})
}

// ---------------------------------------------------------------------------
// Sanity: ensure unused imports are tied to real call sites.
// ---------------------------------------------------------------------------

// Compile-time sanity: the audit / keys / litellm packages stay
// referenced by these tests even if individual subtests are filtered.
var (
	_ = audit.ActionContentGet
	_ = keys.PrefixPk
	_ = litellm.ErrNotFound
)
