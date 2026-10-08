//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration coverage for the list/get admin rules: the list is
// team-scoped for EVERY caller, ?all=true is the admin-only full
// inventory, and GET /{name} keeps its admin bypass. Deps.Store is a
// concrete *store.Store over pgx, so these run against a real Postgres
// (same per-package testcontainers setup as internal/platformapi/admin).
//
// Run with `make test-integration-pkg PKG=./internal/platformapi/environments`.

package environments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/store"
)

const testNs = "ach-test"

func setupPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test requires Docker; -short specified")
	}
	ctx := context.Background()
	pgC, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("ach_env_test"),
		tcpostgres.WithUsername("ach_env_test"),
		tcpostgres.WithPassword("ach_env_test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("docker required: %v", err)
	}
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })
	conn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}
	migPath, err := filepath.Abs("../../../db/migrations")
	if err != nil {
		t.Fatalf("abs migrations path: %v", err)
	}
	if err := db.Migrate(conn, migPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, conn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fakeTeams answers LookupCallerTeams and counts the calls, so the
// ?all=true path can prove it never reaches LiteLLM.
type fakeTeams struct {
	litellm.NoopClient
	teams []string
	calls int
}

func (f *fakeTeams) UserInfoByEmail(context.Context, string) (*litellm.UserInfo, error) {
	f.calls++
	return &litellm.UserInfo{Teams: f.teams}, nil
}

// newEnvRouter seeds two Environments (own: team "run"; other: team
// "agents") and mounts the handlers behind a pk_ caller in team "run".
func newEnvRouter(t *testing.T, isAdmin bool) (chi.Router, *fakeTeams) {
	t.Helper()
	pool := setupPostgres(t)
	ctx := context.Background()
	for name, team := range map[string]string{"own": "run", "other": "agents"} {
		// Array columns are NOT NULL: every list must be non-nil.
		e := []string{}
		row := db.EnvironmentRow{
			Namespace: testNs, Name: name, AuthorizedTeams: []string{team},
			ContextPrompts: e, ContextPlugins: e, ContextArtifacts: e, ContextSkills: e,
			RuntimeModels: e, RuntimeMCPServers: e, RuntimeA2AAgents: e, RuntimeGuardrails: e,
		}
		if err := db.UpsertEnvironment(ctx, pool, row); err != nil {
			t.Fatalf("seed %s: %v", row.Name, err)
		}
	}
	ll := &fakeTeams{teams: []string{"run"}}
	deps := Deps{Store: store.New(pool, testNs, logr.Discard()), LiteLLM: ll}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := middleware.WithRequestID(r.Context(), "test-req-id")
			info := &keystore.KeyInfo{KeyID: "pkid_test", KeyType: keys.PrefixPk, OwnerEmail: "me@x.example"}
			next.ServeHTTP(w, r.WithContext(middleware.WithKeyContext(ctx, info, isAdmin)))
		})
	})
	r.Route("/platform/environments", Mount(deps))
	return r, ll
}

func get(t *testing.T, r http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func listNames(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var body struct {
		Items []store.EnvironmentView `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := make([]string, 0, len(body.Items))
	for _, it := range body.Items {
		names = append(names, it.Name)
	}
	sort.Strings(names)
	return names
}

func TestList_AdminWithoutAll_IsTeamScoped(t *testing.T) {
	r, _ := newEnvRouter(t, true)
	if got := listNames(t, get(t, r, "/platform/environments")); len(got) != 1 || got[0] != "own" {
		t.Fatalf("admin without all must see only own team's envs, got %v", got)
	}
}

func TestList_AdminWithAll_SeesEveryRowWithoutLiteLLM(t *testing.T) {
	r, ll := newEnvRouter(t, true)
	got := listNames(t, get(t, r, "/platform/environments?all=true"))
	if len(got) != 2 || got[0] != "other" || got[1] != "own" {
		t.Fatalf("admin with all=true must see every env, got %v", got)
	}
	if ll.calls != 0 {
		t.Fatalf("all=true must skip the LiteLLM team lookup, got %d calls", ll.calls)
	}
}

func TestList_NonAdminWithAll_Is403NotAdmin(t *testing.T) {
	r, _ := newEnvRouter(t, false)
	rec := get(t, r, "/platform/environments?all=true")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "not_admin" {
		t.Fatalf("want error code not_admin, body = %s", rec.Body)
	}
}

func TestList_NonAdminWithoutAll_IsTeamScoped(t *testing.T) {
	r, _ := newEnvRouter(t, false)
	if got := listNames(t, get(t, r, "/platform/environments")); len(got) != 1 || got[0] != "own" {
		t.Fatalf("non-admin must see only own team's envs, got %v", got)
	}
}

func TestGet_AdminBypassUnchanged(t *testing.T) {
	r, _ := newEnvRouter(t, true)
	if rec := get(t, r, "/platform/environments/other"); rec.Code != http.StatusOK {
		t.Fatalf("admin get outside own teams: status = %d, want 200; body = %s", rec.Code, rec.Body)
	}
	r, _ = newEnvRouter(t, false)
	if rec := get(t, r, "/platform/environments/other"); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin get outside own teams: status = %d, want 403", rec.Code)
	}
}
