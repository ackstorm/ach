// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// whoamiTestEnv is credTestEnv, kept under this name for the hydrate tests.
func whoamiTestEnv(t *testing.T) string {
	t.Helper()
	return credTestEnv(t)
}

// seedConfig writes a config.yaml under the test XDG home with the
// supplied profile (the default), returning the config path.
func seedConfig(t *testing.T, dir, name string, dep *config.Profile) string {
	t.Helper()
	path := filepath.Join(dir, "ach", "config.yaml")
	if err := config.Save(path, &config.File{Default: name, Profiles: map[string]*config.Profile{name: dep}}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return path
}

func executeWhoami(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	return executeCommand(t, newWhoamiCmd(), args...)
}

// bootstrapServer serves GET /platform/console/bootstrap with body and
// records the x-ach-key it saw.
func bootstrapServer(t *testing.T, body map[string]any, sawKey *string) *httptest.Server {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/platform/console/bootstrap" {
			http.NotFound(w, r)
			return
		}
		if sawKey != nil {
			*sawKey = r.Header.Get("x-ach-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(ts.Close)
	swapHTTPClientForTest(t, &whoamiHTTPClient, ts.Client())
	return ts
}

// fields maps each whoami line's label to its (space-normalized) value.
func fields(out string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(l)
		if len(f) > 0 {
			m[f[0]] = strings.Join(f[1:], " ")
		}
	}
	return m
}

func TestWhoami_Session(t *testing.T) {
	dir := whoamiTestEnv(t)
	var saw string
	ts := bootstrapServer(t, map[string]any{
		"email": "jc@example.com", "keys_used": 3, "max_keys": 5,
		"budget": map[string]any{"spend": 12.4, "max_budget": 100, "budget_duration": "30d"},
	}, &saw)
	seedConfig(t, dir, "default", oauthProfile(ts.URL))

	out, _, code, err := executeWhoami(t)
	if err != nil || code != exit.OK {
		t.Fatalf("code=%d err=%v", code, err)
	}
	got := fields(out)
	want := map[string]string{
		"Profile": "default", "URL": ts.URL, "User": "jc@example.com", "Auth": "session",
		"Budget": "12.40 / 100.00 USD (30d)", "Keys": "3 / 5",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q; want %q\n%s", k, got[k], v, out)
		}
	}
	if !strings.HasPrefix(out, "Profile  default\n") {
		t.Errorf("want two-space aligned layout, got:\n%s", out)
	}
	if saw != "h.p.s" {
		t.Errorf("bootstrap called with %q; want the session token", saw)
	}
}

func TestWhoami_KeyOverrideAndDegradedFields(t *testing.T) {
	dir := whoamiTestEnv(t)
	var saw string
	ts := bootstrapServer(t, map[string]any{
		"email": "jc@example.com", "keys_used": nil, "max_keys": nil,
		"budget": map[string]any{"spend": 12.4, "max_budget": nil, "budget_duration": nil},
	}, &saw)
	seedConfig(t, dir, "default", oauthProfile(ts.URL))

	out, _, _, err := executeWhoami(t, "--key", testEK)
	if err != nil {
		t.Fatal(err)
	}
	got := fields(out)
	if got["Auth"] != "key ek-****abcd" || got["Budget"] != "12.40 USD (no ceiling)" || got["Keys"] != "unavailable" {
		t.Fatalf("got:\n%s", out)
	}
	if saw != testEK {
		t.Errorf("bootstrap must be called with the --key override")
	}

	ts2 := bootstrapServer(t, map[string]any{"email": "jc@example.com", "budget": nil}, nil)
	seedConfig(t, dir, "default", &config.Profile{URL: ts2.URL, Key: testEK})
	out, _, _, err = executeWhoami(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := fields(out); got["Auth"] != "key ek-****abcd" || got["Budget"] != "unavailable" {
		t.Fatalf("key profile:\n%s", out)
	}
}

func TestWhoami_401_Exit3(t *testing.T) {
	dir := whoamiTestEnv(t)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "invalid_key", "message": "key rejected"}, "request_id": "req_x",
		})
	}))
	defer ts.Close()
	swapHTTPClientForTest(t, &whoamiHTTPClient, ts.Client())
	seedConfig(t, dir, "prod", &config.Profile{URL: ts.URL, Key: testEK})

	if _, _, code, err := executeWhoami(t); err == nil || code != exit.AuthN {
		t.Fatalf("code=%d err=%v; want 3", code, err)
	}
}

func TestWhoami_NetworkRefused_Exit6(t *testing.T) {
	dir := whoamiTestEnv(t)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := ts.URL
	ts.Close()
	swapHTTPClientForTest(t, &whoamiHTTPClient, nil)
	seedConfig(t, dir, "prod", &config.Profile{URL: closedURL, Key: testEK})

	if _, _, code, err := executeWhoami(t); err == nil || code != exit.Network {
		t.Fatalf("code=%d err=%v; want 6", code, err)
	}
}

func TestWhoami_NoConfig_Exit1(t *testing.T) {
	whoamiTestEnv(t)
	_, _, code, err := executeWhoami(t)
	if err == nil || code != exit.General || !strings.Contains(err.Error(), "ach-cli login") {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestWhoami_Verbose_RedactsKey(t *testing.T) {
	dir := whoamiTestEnv(t)
	ts := bootstrapServer(t, map[string]any{"email": "jc@example.com"}, nil)
	seedConfig(t, dir, "prod", &config.Profile{URL: ts.URL, Key: testEK})

	_, stderr, _, err := executeWhoami(t, "--verbose")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "X-Ach-Key: ek-***") || strings.Contains(stderr, testEK) {
		t.Errorf("stderr must carry the redacted key only:\n%s", stderr)
	}
}

func TestWhoami_RemovedFlags(t *testing.T) {
	whoamiTestEnv(t)
	for _, f := range []string{"--verify", "--api-key=x", "--env-key=x"} {
		if _, _, _, err := executeWhoami(t, f); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s: err = %v; want unknown flag", f, err)
		}
	}
}
