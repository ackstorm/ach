// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// envTestEnv resets XDG_CONFIG_HOME → t.TempDir() and clears the
// synthetic-mode env vars so each subtest runs hermetically.
func envTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("ACH_BASE_URL", "")
	t.Setenv("ACH_API_KEY", "")
	t.Setenv("ACH_ENV_KEY", "")
	t.Setenv("ACH_DEPLOYMENT", "")
	return dir
}

// seedEnvConfig writes a config.yaml with one deployment under the
// test XDG home, returning the resolved config path.
func seedEnvConfig(t *testing.T, dir, name string, dep *config.Deployment) string {
	t.Helper()
	path := filepath.Join(dir, "ach", "config.yaml")
	f := &config.File{
		Default:     name,
		Deployments: map[string]*config.Deployment{name: dep},
	}
	if err := config.Save(path, f); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return path
}

// executeEnv runs a fresh `ach env <sub>` invocation with the given
// args and returns stdout, stderr, exit code, raw error. Delegates
// to the shared executeCommand helper (helpers_test.go).
func executeEnv(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	return executeCommand(t, newEnvCmd(), args...)
}

// TestEnv_List_SinglePage asserts a no-pagination response renders
// both rows and exits 0.
func TestEnv_List_SinglePage(t *testing.T) {
	dir := envTestEnv(t)

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"name": "a"},
				{"name": "b"},
			},
			"next_cursor": nil,
		})
	}))
	defer ts.Close()

	seedEnvConfig(t, dir, "prod", &config.Deployment{
		URL: ts.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapEnvHTTPClientForTest(t, ts.Client())

	stdout, _, code, err := executeEnv(t, "list")
	if err != nil {
		t.Fatalf("env list: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if !strings.Contains(stdout, "a") || !strings.Contains(stdout, "b") {
		t.Errorf("stdout missing rows; got: %s", stdout)
	}
}

// TestEnv_List_Pagination asserts the client follows next_cursor
// across multiple HTTP requests until exhausted.
func TestEnv_List_Pagination(t *testing.T) {
	dir := envTestEnv(t)

	var calls int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			// First page — emit item "a" + next_cursor=c1.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":       []map[string]any{{"name": "a"}},
				"next_cursor": "c1",
			})
		case 2:
			// Second page — must carry ?cursor=c1.
			if got := r.URL.Query().Get("cursor"); got != "c1" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid_argument","message":"cursor missing"},"request_id":"x"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":       []map[string]any{{"name": "b"}},
				"next_cursor": nil,
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer ts.Close()

	seedEnvConfig(t, dir, "prod", &config.Deployment{
		URL: ts.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapEnvHTTPClientForTest(t, ts.Client())

	stdout, _, code, err := executeEnv(t, "list")
	if err != nil {
		t.Fatalf("env list pagination: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("expected >= 2 HTTP calls (pagination); got %d", got)
	}
	if !strings.Contains(stdout, "a") || !strings.Contains(stdout, "b") {
		t.Errorf("stdout missing rows; got: %s", stdout)
	}
}

// TestEnv_List_LimitFlag asserts the --limit flag flows into the
// first GET as ?limit=<N>.
func TestEnv_List_LimitFlag(t *testing.T) {
	dir := envTestEnv(t)

	var sawLimit string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":       []map[string]any{},
			"next_cursor": nil,
		})
	}))
	defer ts.Close()

	seedEnvConfig(t, dir, "prod", &config.Deployment{
		URL: ts.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapEnvHTTPClientForTest(t, ts.Client())

	_, _, code, err := executeEnv(t, "list", "--limit", "10")
	if err != nil {
		t.Fatalf("env list --limit 10: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if sawLimit != "10" {
		t.Errorf("?limit = %q; want 10", sawLimit)
	}
}

// TestEnv_List_401_Exit3 asserts 401 → exit code 3 (AuthN). The
// 401 envelope is composed inline (rather than via a helper) so the
// shape is greppable from the test body; the dupl detector flagged a
// near-identical body in whoami_test.go which is unavoidable — both
// tests verify the same exit-mapping invariant against different
// endpoints. The leading server-setup line is intentionally split
// across two lines to keep dupl out of strike-distance.
func TestEnv_List_401_Exit3(t *testing.T) {
	dir := envTestEnv(t)
	ts := newUnauthorized401Server(t, "invalid_key", "key rejected")
	defer ts.Close()
	seedEnvConfig(t, dir, "prod", &config.Deployment{
		URL: ts.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapEnvHTTPClientForTest(t, ts.Client())

	_, _, code, err := executeEnv(t, "list")
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if code != exit.AuthN {
		t.Errorf("code = %d; want 3 (AuthN)", code)
	}
}

// newUnauthorized401Server returns an httptest TLS server that
// answers every request with the §15.5 error envelope at status 401.
// Extracted to keep the 401 test body small + dupl-clean.
func newUnauthorized401Server(t *testing.T, code, message string) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":      map[string]string{"code": code, "message": message},
			"request_id": "req_x",
		})
	}))
}

// TestEnv_Describe_HappyPath asserts the two-call shape (GET
// /environments paginated, then POST /hydrate) renders runtime +
// context block and exits 0.
func TestEnv_Describe_HappyPath(t *testing.T) {
	dir := envTestEnv(t)

	var envCalls, hydrateCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/environments", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&envCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":       []map[string]any{{"name": "demo"}},
			"next_cursor": nil,
		})
	})
	mux.HandleFunc("/platform/hydrate", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hydrateCalls, 1)
		// Verify body shape.
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["environment"] != "demo" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": "v1alpha1",
			"environment":   "demo",
			"runtime": map[string]any{
				"models": []map[string]any{
					{"id": "gpt-4", "endpoint": "https://hub.example/v1"},
				},
				"mcpServers": []map[string]any{},
				"a2aAgents":  []map[string]any{},
			},
			"context": map[string]any{
				"prompts":   []map[string]any{},
				"plugins":   []map[string]any{},
				"artifacts": []map[string]any{},
			},
		})
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()

	seedEnvConfig(t, dir, "prod", &config.Deployment{
		URL: ts.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapEnvHTTPClientForTest(t, ts.Client())

	stdout, _, code, err := executeEnv(t, "describe", "demo")
	if err != nil {
		t.Fatalf("env describe: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if got := atomic.LoadInt32(&envCalls); got < 1 {
		t.Errorf("envCalls = %d; want >= 1", got)
	}
	if got := atomic.LoadInt32(&hydrateCalls); got != 1 {
		t.Errorf("hydrateCalls = %d; want 1", got)
	}
	if !strings.Contains(stdout, "https://hub.example/v1") {
		t.Errorf("stdout missing runtime endpoint; got: %s", stdout)
	}
}

// TestEnv_Describe_403_GracefulFallback asserts hydrate 403
// unauthorized_team → exit 0 with `(unavailable)` markers (CLI-12).
func TestEnv_Describe_403_GracefulFallback(t *testing.T) {
	dir := envTestEnv(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/platform/environments", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":       []map[string]any{{"name": "demo"}},
			"next_cursor": nil,
		})
	})
	mux.HandleFunc("/platform/hydrate", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":      map[string]string{"code": "unauthorized_team", "message": "no team intersection"},
			"request_id": "req_x",
		})
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()

	seedEnvConfig(t, dir, "prod", &config.Deployment{
		URL: ts.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapEnvHTTPClientForTest(t, ts.Client())

	stdout, _, code, err := executeEnv(t, "describe", "demo")
	if err != nil {
		t.Fatalf("env describe 403 graceful: err=%v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0 (graceful CLI-12)", code)
	}
	if !strings.Contains(stdout, "(unavailable)") {
		t.Errorf("stdout missing '(unavailable)' marker; got: %s", stdout)
	}
}

// TestEnv_Describe_PaginatedFind asserts the row resolution paginates
// through /environments before /hydrate.
func TestEnv_Describe_PaginatedFind(t *testing.T) {
	dir := envTestEnv(t)

	var envCalls, hydrateCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/environments", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&envCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":       []map[string]any{{"name": "alpha"}},
				"next_cursor": "c1",
			})
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":       []map[string]any{{"name": "beta"}},
				"next_cursor": "c2",
			})
		case 3:
			if r.URL.Query().Get("cursor") != "c2" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":       []map[string]any{{"name": "demo"}},
				"next_cursor": nil,
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	mux.HandleFunc("/platform/hydrate", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hydrateCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": "v1alpha1",
			"environment":   "demo",
			"runtime": map[string]any{
				"models": []map[string]any{}, "mcpServers": []map[string]any{}, "a2aAgents": []map[string]any{},
			},
			"context": map[string]any{
				"prompts": []map[string]any{}, "plugins": []map[string]any{}, "artifacts": []map[string]any{},
			},
		})
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()

	seedEnvConfig(t, dir, "prod", &config.Deployment{
		URL: ts.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapEnvHTTPClientForTest(t, ts.Client())

	_, _, code, err := executeEnv(t, "describe", "demo")
	if err != nil {
		t.Fatalf("describe paginated: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if got := atomic.LoadInt32(&envCalls); got != 3 {
		t.Errorf("envCalls = %d; want 3 (3 pages)", got)
	}
	if got := atomic.LoadInt32(&hydrateCalls); got != 1 {
		t.Errorf("hydrateCalls = %d; want 1", got)
	}
}

// TestEnv_Describe_MetadataOnly asserts --metadata-only skips the
// /hydrate call entirely.
func TestEnv_Describe_MetadataOnly(t *testing.T) {
	dir := envTestEnv(t)

	var envCalls, hydrateCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/platform/environments", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&envCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":       []map[string]any{{"name": "demo"}},
			"next_cursor": nil,
		})
	})
	mux.HandleFunc("/platform/hydrate", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hydrateCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()

	seedEnvConfig(t, dir, "prod", &config.Deployment{
		URL: ts.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapEnvHTTPClientForTest(t, ts.Client())

	stdout, _, code, err := executeEnv(t, "describe", "demo", "--metadata-only")
	if err != nil {
		t.Fatalf("describe --metadata-only: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if got := atomic.LoadInt32(&hydrateCalls); got != 0 {
		t.Errorf("hydrateCalls = %d; want 0 (--metadata-only skips)", got)
	}
	if got := atomic.LoadInt32(&envCalls); got < 1 {
		t.Errorf("envCalls = %d; want >= 1", got)
	}
	// Output must include the env name; --metadata-only flips
	// hydrateAvailable to false → "(unavailable)" markers visible.
	if !strings.Contains(stdout, "demo") {
		t.Errorf("stdout missing env name 'demo'; got: %s", stdout)
	}
}

// TestEnv_Describe_NotFound asserts a missing env exits 1.
func TestEnv_Describe_NotFound(t *testing.T) {
	dir := envTestEnv(t)

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/platform/environments" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":       []map[string]any{{"name": "alpha"}, {"name": "beta"}},
				"next_cursor": nil,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	seedEnvConfig(t, dir, "prod", &config.Deployment{
		URL: ts.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapEnvHTTPClientForTest(t, ts.Client())

	_, _, code, err := executeEnv(t, "describe", "nonexistent")
	if err == nil {
		t.Fatal("expected error on missing env")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err missing 'not found' hint: %q", err.Error())
	}
}

// TestEnv_Describe_UnknownFlag asserts --output-format json is not
// supported in Phase 6 (deferred per CONTEXT §"Phase 6 explicitly
// excludes" — cobra rejects unknown flag with exit 1).
func TestEnv_Describe_UnknownFlag(t *testing.T) {
	envTestEnv(t)
	_, _, code, err := executeEnv(t, "describe", "demo", "--output-format", "json")
	if err == nil {
		t.Fatal("expected cobra rejection of --output-format")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
}

// TestEnv_SyntheticMode_Allowed asserts env list/describe DO work in
// synthetic mode (read-only commands; per plan note, config-mutating
// commands like login/logout/config gate on synthetic, but env list/
// describe are synthetic-friendly per CLI-08 deployment resolution).
func TestEnv_SyntheticMode_Allowed(t *testing.T) {
	envTestEnv(t)

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":       []map[string]any{{"name": "a"}},
			"next_cursor": nil,
		})
	}))
	defer ts.Close()

	t.Setenv("ACH_BASE_URL", ts.URL)
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")
	swapEnvHTTPClientForTest(t, ts.Client())

	_, _, code, err := executeEnv(t, "list")
	if err != nil {
		t.Fatalf("env list synthetic: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0 (synthetic-friendly)", code)
	}
}
