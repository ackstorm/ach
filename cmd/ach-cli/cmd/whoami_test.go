// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// whoamiTestEnv resets XDG_CONFIG_HOME → t.TempDir() and clears
// synthetic-mode + verbose env vars so each test runs hermetically.
func whoamiTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("ACH_BASE_URL", "")
	t.Setenv("ACH_API_KEY", "")
	t.Setenv("ACH_ENV_KEY", "")
	t.Setenv("ACH_PROFILE", "")
	return dir
}

// seedConfig writes a config.yaml under the test XDG home with the
// supplied profile, returning the resolved config path.
func seedConfig(t *testing.T, dir, name string, dep *config.Profile) string {
	t.Helper()
	path := filepath.Join(dir, "ach", "config.yaml")
	f := &config.File{
		Default:  name,
		Profiles: map[string]*config.Profile{name: dep},
	}
	if err := config.Save(path, f); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return path
}

// executeWhoami runs newWhoamiCmd with args and returns stdout,
// stderr, exit code, raw error.
func executeWhoami(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	cmd := newWhoamiCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		return outBuf.String(), errBuf.String(), exit.OK, nil
	}
	var sErr *httpclient.ServerError
	if errors.As(err, &sErr) {
		return outBuf.String(), errBuf.String(), exit.MapServerError(sErr), err
	}
	var cErr *exit.CodedError
	if errors.As(err, &cErr) {
		return outBuf.String(), errBuf.String(), cErr.Code, err
	}
	return outBuf.String(), errBuf.String(), exit.General, err
}

// TestWhoami_NoNet_PrintsIdentityBlock is Test 1: no --verify reads
// on-disk config only, prints identity block, makes ZERO HTTP calls.
func TestWhoami_NoNet_PrintsIdentityBlock(t *testing.T) {
	dir := whoamiTestEnv(t)
	seedConfig(t, dir, "prod", &config.Profile{
		URL: "https://hub.example",
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
	})

	var calls int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	stdout, _, code, err := executeWhoami(t)
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if code != exit.OK {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(stdout, "prod") {
		t.Errorf("missing profile name 'prod'; stdout: %s", stdout)
	}
	if !strings.Contains(stdout, "https://hub.example") {
		t.Errorf("missing URL; stdout: %s", stdout)
	}
	if !strings.Contains(stdout, "pk-****wxyz") {
		t.Errorf("missing masked pk tail; stdout: %s", stdout)
	}
	if !strings.Contains(stdout, "no remote check") {
		t.Errorf("missing '(no remote check)' marker; stdout: %s", stdout)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("HTTP calls = %d; want 0 (no --verify)", got)
	}
}

// TestWhoami_Verify_PK_Calls_Environments is Test 2: pk_ → GET
// /platform/environments?limit=1 with x-ach-key header.
func TestWhoami_Verify_PK_Calls_Environments(t *testing.T) {
	dir := whoamiTestEnv(t)

	var sawPath, sawAchKey string
	var sawMethod string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path + "?" + r.URL.RawQuery
		sawMethod = r.Method
		sawAchKey = r.Header.Get("x-ach-key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []any{}})
	}))
	defer ts.Close()

	seedConfig(t, dir, "prod", &config.Profile{
		URL: ts.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAbcde",
	})
	swapHTTPClientForTest(t, ts.Client())

	stdout, _, code, err := executeWhoami(t, "--verify")
	if err != nil {
		t.Fatalf("whoami --verify: %v", err)
	}
	if code != exit.OK {
		t.Errorf("exit code = %d; want 0", code)
	}
	if sawMethod != http.MethodGet {
		t.Errorf("method = %q; want GET", sawMethod)
	}
	if sawPath != "/platform/environments?limit=1" {
		t.Errorf("path = %q; want /platform/environments?limit=1", sawPath)
	}
	if sawAchKey != "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAbcde" {
		t.Errorf("x-ach-key = %q; want pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAbcde", sawAchKey)
	}
	if !strings.Contains(stdout, "Verified: yes") {
		t.Errorf("stdout missing 'Verified: yes'; stdout: %s", stdout)
	}
}

// TestWhoami_Verify_EK_Calls_Hydrate is Test 3: ek_ → POST
// /platform/hydrate {} with Accept-Encoding: gzip header.
func TestWhoami_Verify_EK_Calls_Hydrate(t *testing.T) {
	dir := whoamiTestEnv(t)

	var sawPath, sawMethod, sawAcceptEncoding, sawBody string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawMethod = r.Method
		sawAcceptEncoding = r.Header.Get("Accept-Encoding")
		b := make([]byte, 1024)
		n, _ := r.Body.Read(b)
		sawBody = string(b[:n])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"environment":{"name":"demo"}}`))
	}))
	defer ts.Close()

	seedConfig(t, dir, "prod", &config.Profile{
		URL: ts.URL,
		EK:  map[string]string{"demo": "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAghij"},
	})
	swapHTTPClientForTest(t, ts.Client())

	stdout, _, code, err := executeWhoami(t, "--verify", "--env-key", "demo")
	if err != nil {
		t.Fatalf("whoami --verify --env-key demo: %v", err)
	}
	if code != exit.OK {
		t.Errorf("exit code = %d; want 0", code)
	}
	if sawMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", sawMethod)
	}
	if sawPath != "/platform/hydrate" {
		t.Errorf("path = %q; want /platform/hydrate", sawPath)
	}
	if sawAcceptEncoding != "gzip" {
		t.Errorf("Accept-Encoding = %q; want gzip (CLI-11)", sawAcceptEncoding)
	}
	if strings.TrimSpace(sawBody) != "{}" {
		t.Errorf("body = %q; want '{}' (empty struct for CLI-11)", sawBody)
	}
	if !strings.Contains(stdout, "Verified: yes") {
		t.Errorf("stdout missing 'Verified: yes'; stdout: %s", stdout)
	}
}

// TestWhoami_Verify_401_Exit3 is Test 4: 401 → exit code 3 (AuthN).
func TestWhoami_Verify_401_Exit3(t *testing.T) {
	dir := whoamiTestEnv(t)

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":      map[string]string{"code": "invalid_key", "message": "key rejected"},
			"request_id": "req_x",
		})
	}))
	defer ts.Close()

	seedConfig(t, dir, "prod", &config.Profile{
		URL: ts.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
	})
	swapHTTPClientForTest(t, ts.Client())

	_, _, code, err := executeWhoami(t, "--verify")
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if code != exit.AuthN {
		t.Errorf("exit code = %d; want 3 (AuthN)", code)
	}
}

// TestWhoami_Verify_NetworkRefused_Exit6 is Test 5: connection
// refused → exit code 6 (Network).
func TestWhoami_Verify_NetworkRefused_Exit6(t *testing.T) {
	dir := whoamiTestEnv(t)
	// Pre-create the server, capture its URL, then close it so the
	// dial fails. (Loopback https URL with no listener.)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	closedURL := ts.URL
	ts.Close()

	seedConfig(t, dir, "prod", &config.Profile{
		URL: closedURL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
	})
	// Restore the package-level HTTPClient seam to nil so the call
	// uses the default client (we want a real connection-refused).
	previous := whoamiHTTPClient
	whoamiHTTPClient = nil
	t.Cleanup(func() { whoamiHTTPClient = previous })

	_, _, code, err := executeWhoami(t, "--verify")
	if err == nil {
		t.Fatal("expected network error")
	}
	if code != exit.Network {
		t.Errorf("exit code = %d; want 6 (Network); err: %v", code, err)
	}
}

// TestWhoami_NoConfig_Exit1 is Test 7: NO config file + NO synthetic
// env → exit 1.
func TestWhoami_NoConfig_Exit1(t *testing.T) {
	whoamiTestEnv(t)
	// Do NOT seed config — XDG_CONFIG_HOME points at empty dir.

	_, _, code, err := executeWhoami(t)
	if err == nil {
		t.Fatal("expected error on missing config")
	}
	if code != exit.General {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "ach login") {
		t.Errorf("err missing 'ach login' hint: %q", err.Error())
	}
}

// TestWhoami_Verbose_RedactsKey is Test 8: --verbose stderr dump
// shows x-ach-key: pk_*** (redacted form per CLI-04).
func TestWhoami_Verbose_RedactsKey(t *testing.T) {
	dir := whoamiTestEnv(t)

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []any{}})
	}))
	defer ts.Close()

	seedConfig(t, dir, "prod", &config.Profile{
		URL: ts.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
	})
	swapHTTPClientForTest(t, ts.Client())

	_, stderr, code, err := executeWhoami(t, "--verify", "--verbose")
	if err != nil {
		t.Fatalf("whoami --verify --verbose: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	// httpclient.HeaderDump canonicalizes header to X-Ach-Key.
	if !strings.Contains(stderr, "X-Ach-Key: pk-***") {
		t.Errorf("stderr missing redacted X-Ach-Key: pk-***; stderr: %s", stderr)
	}
	// CLI-04: the full pk plaintext MUST NOT appear in stderr.
	if strings.Contains(stderr, "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz") {
		t.Errorf("CLI-04 leak in stderr: %s", stderr)
	}
}
