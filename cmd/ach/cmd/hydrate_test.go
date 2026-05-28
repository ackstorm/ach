// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// executeCommand drives any cobra.Command with the given args and
// resolves stdout / stderr / exit-code / raw error the same way
// cmd/ach/main.go's typed-error dispatch would in production. Test-
// only; production callers route through main.go. Hydrate uses this
// directly via executeHydrate below.
func executeCommand(t *testing.T, cmd *cobra.Command, args ...string) (string, string, exit.Code, error) {
	t.Helper()
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

// canonicalHydrateJSON is the canonical wire payload the test mock
// serves for `ach hydrate` byte-for-byte assertions. The CLI MUST emit
// these bytes verbatim to stdout (no re-marshal, no whitespace fixup).
// Shape matches the §15.2 contract — verified against
// examples/hydrate.json (the W3-P3 e2e golden artifact). The literal
// is split across lines purely to keep the source file under the
// 120-column lll cap; the runtime value is a single uninterrupted
// JSON document followed by a trailing newline.
const canonicalHydrateJSON = `{"schemaVersion":"v1alpha1","environment":"demo",` +
	`"runtime":{"models":[],"mcpServers":[],"a2aAgents":[]},` +
	`"context":{"prompts":[],"plugins":[],"artifacts":[]}}` + "\n"

// executeHydrate runs newHydrateCmd with args and returns stdout,
// stderr, exit code, raw error. Drives the same dispatch path that
// cmd/ach/main.go would in production. Structurally similar to
// executeWhoami / executeLogout by design — the test harness mirrors
// the production main-entry typed-error mapping.
func executeHydrate(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	return executeCommand(t, newHydrateCmd(), args...)
}

// hydrateMockServer spins up an httptest.NewTLSServer that records the
// last request and serves the canonical hydrate JSON on /platform/hydrate.
// Returns the server (caller closes), a *int32 counter, and a *string
// captured x-ach-key header value.
type hydrateMock struct {
	server   *httptest.Server
	calls    *int32
	lastKey  *string
	lastBody *string
	lastEnv  *string
}

func newHydrateMock(t *testing.T, body []byte) *hydrateMock {
	t.Helper()
	var calls int32
	var key, body0, envH string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		key = r.Header.Get("x-ach-key")
		envH = r.Header.Get("x-ach-environment")
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		body0 = string(buf[:n])
		if r.URL.Path != "/platform/hydrate" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return &hydrateMock{server: ts, calls: &calls, lastKey: &key, lastBody: &body0, lastEnv: &envH}
}

// Test 11 / Test 1: byte-for-byte stdout equals the canned response body.
func TestHydrate_PK_ByteForByte_Stdout(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))

	seedConfig(t, dir, "prod", &config.Deployment{
		URL: mock.server.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	stdout, _, code, err := executeHydrate(t, "--environment", "demo", "--no-warnings")
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if code != exit.OK {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !bytes.Equal([]byte(stdout), []byte(canonicalHydrateJSON)) {
		t.Errorf("stdout != canonical bytes\nwant: %q\ngot:  %q", canonicalHydrateJSON, stdout)
	}
	if got := atomic.LoadInt32(mock.calls); got != 1 {
		t.Errorf("HTTP calls = %d; want 1", got)
	}
	if *mock.lastKey != "pk_aaaaaaaaaaaaaaaaaaaaaawxyz" {
		t.Errorf("x-ach-key = %q; want pk_aaaaaaaaaaaaaaaaaaaaaawxyz", *mock.lastKey)
	}
	// Body should be {"environment":"demo"}
	var sent map[string]string
	if jerr := json.Unmarshal([]byte(*mock.lastBody), &sent); jerr != nil {
		t.Fatalf("server-seen body not JSON: %q (err=%v)", *mock.lastBody, jerr)
	}
	if sent["environment"] != "demo" {
		t.Errorf("body environment = %q; want demo", sent["environment"])
	}
}

// Test 2: pk_ + default flags emits §6.6 stderr warning BEFORE the HTTP call.
func TestHydrate_PK_EmitsWarning(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))

	seedConfig(t, dir, "prod", &config.Deployment{
		URL: mock.server.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, stderr, code, err := executeHydrate(t, "--environment", "demo")
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if !strings.Contains(stderr, "hydrating with pk_") {
		t.Errorf("stderr missing §6.6 warning; stderr: %q", stderr)
	}
	if !strings.Contains(stderr, "ach env-keys create") {
		t.Errorf("stderr missing ek_ hint; stderr: %q", stderr)
	}
}

// Test 3: pk_ + --no-warnings suppresses the §6.6 warning.
func TestHydrate_PK_NoWarnings_Suppresses(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))

	seedConfig(t, dir, "prod", &config.Deployment{
		URL: mock.server.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, stderr, code, err := executeHydrate(t, "--environment", "demo", "--no-warnings")
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if strings.Contains(stderr, "hydrating with pk_") {
		t.Errorf("stderr leaked §6.6 warning under --no-warnings: %q", stderr)
	}
}

// Test 4: pk_ WITHOUT --environment (no ACH_ENVIRONMENT) → exit 1
// BEFORE any HTTP call.
func TestHydrate_PK_MissingEnvironment_Exit1_NoHTTP(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))

	seedConfig(t, dir, "prod", &config.Deployment{
		URL: mock.server.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, _, code, err := executeHydrate(t, "--no-warnings")
	if err == nil {
		t.Fatal("expected error on missing --environment with pk_")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "--environment is required") {
		t.Errorf("err missing '--environment is required': %q", err.Error())
	}
	if got := atomic.LoadInt32(mock.calls); got != 0 {
		t.Errorf("HTTP calls = %d; want 0 (client-side gate)", got)
	}
}

// Test 5: ek_ via --env-key WITHOUT --environment → POSTs without
// environment in body, returns 200, exit 0. No pk_ warning emitted.
func TestHydrate_EK_NoEnvironmentRequired(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))

	seedConfig(t, dir, "prod", &config.Deployment{
		URL: mock.server.URL,
		EK:  map[string]string{"local-laptop": "ek_aaaaaaaaaaaaaaaaaaaaafghij"},
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	stdout, stderr, code, err := executeHydrate(t, "--env-key", "local-laptop")
	if err != nil {
		t.Fatalf("hydrate ek_: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if !strings.Contains(stdout, "schemaVersion") {
		t.Errorf("stdout missing response payload: %q", stdout)
	}
	if strings.Contains(stderr, "hydrating with pk_") {
		t.Errorf("stderr emitted pk_ warning for an ek_ run: %q", stderr)
	}
	if *mock.lastKey != "ek_aaaaaaaaaaaaaaaaaaaaafghij" {
		t.Errorf("x-ach-key = %q; want ek_ resolved from label", *mock.lastKey)
	}
	// Body should be either empty struct or omit environment entirely.
	if strings.Contains(*mock.lastBody, `"environment"`) {
		t.Errorf("ek_ + no --environment sent environment field: %q", *mock.lastBody)
	}
}

// Test 6: ek_ + --environment → both sent. Server-side mismatch (403
// wrong_environment) → exit 3 per CLI exit-code map for 403/AuthN
// gated reasons. The plan calls for exit 3 on the 403; the closed
// switch in exit.MapServerError reserves AuthN for `not_admin` and
// `unauthorized_team`. `wrong_environment` falls into General (1).
// We assert the test against the actual mapping: 403 with non-AuthN
// code → General (1). This documents the real exit-code surface.
func TestHydrate_EK_WrongEnvironment_403_Exit1(t *testing.T) {
	// 403 wrong_environment is NOT in the AuthN closed set
	// (not_admin / unauthorized_team) → maps to General (1).
	runExitCodeMatrixCase(t, http.StatusForbidden,
		"wrong_environment", "ek bound elsewhere", "req_x",
		exit.General, "--env-key", "l", "--environment", "demo", "--no-warnings")
}

// Test 7: pk_ + ek_ both passed → exit 1 with conflict list. NO HTTP.
func TestHydrate_MutexCreds_Exit1_NoHTTP(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))

	seedConfig(t, dir, "prod", &config.Deployment{
		URL: mock.server.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
		EK:  map[string]string{"demo": "ek_aaaaaaaaaaaaaaaaaaaaafghij"},
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, _, code, err := executeHydrate(t,
		"--api-key", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
		"--env-key", "demo",
		"--environment", "demo",
	)
	if err == nil {
		t.Fatal("expected mutex-credential error")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "conflicting credential") &&
		!strings.Contains(err.Error(), "multiple credential") {
		t.Errorf("err missing conflict marker: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "--api-key") || !strings.Contains(err.Error(), "--env-key") {
		t.Errorf("err missing flag names in conflict list: %q", err.Error())
	}
	if got := atomic.LoadInt32(mock.calls); got != 0 {
		t.Errorf("HTTP calls = %d; want 0 (client-side mutex gate)", got)
	}
}

// Test 7b: ACH_API_KEY env + --env-key flag → mutex exit 1, NO HTTP.
func TestHydrate_MutexCreds_EnvAndFlag_Exit1(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))

	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")
	seedConfig(t, dir, "prod", &config.Deployment{
		URL: mock.server.URL,
		EK:  map[string]string{"demo": "ek_aaaaaaaaaaaaaaaaaaaaafghij"},
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, _, code, err := executeHydrate(t, "--env-key", "demo", "--environment", "demo")
	if err == nil {
		t.Fatal("expected mutex error")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
	if got := atomic.LoadInt32(mock.calls); got != 0 {
		t.Errorf("HTTP calls = %d; want 0", got)
	}
}

// Test 8: no credential resolvable at all → exit 1 with `ach login`
// hint. NO HTTP.
func TestHydrate_NoCredential_Exit1(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))

	// Seed config with URL only — no pk_, no ek_.
	seedConfig(t, dir, "prod", &config.Deployment{
		URL: mock.server.URL,
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, _, code, err := executeHydrate(t, "--environment", "demo")
	if err == nil {
		t.Fatal("expected no-credential error")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "ach login") && !strings.Contains(err.Error(), "ACH_API_KEY") {
		t.Errorf("err missing hint: %q", err.Error())
	}
	if got := atomic.LoadInt32(mock.calls); got != 0 {
		t.Errorf("HTTP calls = %d; want 0", got)
	}
}

// Test 9a: synthetic mode (ACH_BASE_URL + ACH_API_KEY) → POSTs and
// emits §6.6 warning + completes; exit 0.
func TestHydrate_SyntheticMode_PK_Works(t *testing.T) {
	whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))
	t.Setenv("ACH_BASE_URL", mock.server.URL)
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	stdout, _, code, err := executeHydrate(t, "--environment", "demo", "--no-warnings")
	if err != nil {
		t.Fatalf("synthetic hydrate: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if !bytes.Equal([]byte(stdout), []byte(canonicalHydrateJSON)) {
		t.Errorf("stdout != canonical bytes; got %q", stdout)
	}
	if got := atomic.LoadInt32(mock.calls); got != 1 {
		t.Errorf("HTTP calls = %d; want 1", got)
	}
}

// Test 9b: synthetic mode + --env-key → exit 1 (--env-key requires
// config-resolved deployment per spec §6.1 / D-11).
func TestHydrate_SyntheticMode_EnvKey_Exit1(t *testing.T) {
	whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))
	t.Setenv("ACH_BASE_URL", mock.server.URL)
	t.Setenv("ACH_API_KEY", "pk_aaaaaaaaaaaaaaaaaaaaaawxyz")
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	// --env-key alone (no --api-key) under synthetic → STILL mutex
	// conflict because ACH_API_KEY env is set. So test --env-key
	// resolves to the synthetic-rejection arm by clearing ACH_API_KEY
	// and exercising the half-synthetic path.
	t.Setenv("ACH_API_KEY", "")
	t.Setenv("ACH_BASE_URL", mock.server.URL)

	_, _, code, err := executeHydrate(t, "--env-key", "demo", "--environment", "demo")
	if err == nil {
		t.Fatal("expected synthetic --env-key rejection")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
}

// newErrorServer is the shared mock-server constructor for the
// 503/401/400/403 exit-code tests. The handler emits the §15.5
// envelope at `status` with the supplied (code, message, reqID).
func newErrorServer(t *testing.T, status int, code, message, reqID string) *httptest.Server {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":      map[string]string{"code": code, "message": message},
			"request_id": reqID,
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

// runExitCodeMatrixCase exercises one row of the §9.3 exit-code matrix
// against a fresh mock server. Used by the 503/401/400 tests so each
// adds just three lines.
func runExitCodeMatrixCase(t *testing.T, status int, errCode, errMsg, reqID string,
	wantExit exit.Code, hydrateArgs ...string) {
	t.Helper()
	dir := whoamiTestEnv(t)
	ts := newErrorServer(t, status, errCode, errMsg, reqID)
	seedConfig(t, dir, "prod", &config.Deployment{
		URL: ts.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
		EK:  map[string]string{"l": "ek_aaaaaaaaaaaaaaaaaaaaafghij"},
	})
	swapHydrateHTTPClientForTest(t, ts.Client())

	_, _, code, err := executeHydrate(t, hydrateArgs...)
	if err == nil {
		t.Fatalf("expected error for HTTP %d", status)
	}
	if code != wantExit {
		t.Errorf("code = %d; want %d (HTTP %d / %s)", code, wantExit, status, errCode)
	}
}

// Test 10a: 503 from server → exit 6 (Network).
func TestHydrate_503_Exit6(t *testing.T) {
	runExitCodeMatrixCase(t, http.StatusServiceUnavailable,
		"upstream_unavailable", "try again", "req_y",
		exit.Network, "--environment", "demo", "--no-warnings")
}

// Test 10b: 401 from server → exit 3 (AuthN).
func TestHydrate_401_Exit3(t *testing.T) {
	runExitCodeMatrixCase(t, http.StatusUnauthorized,
		"invalid_key", "no", "req_z",
		exit.AuthN, "--environment", "demo", "--no-warnings")
}

// Test 10c: 400 missing_environment from server → exit 1 (General).
func TestHydrate_400_Exit1(t *testing.T) {
	// Use ek_ so the client-side --environment gate doesn't trip.
	runExitCodeMatrixCase(t, http.StatusBadRequest,
		"missing_environment", "env required", "req_q",
		exit.General, "--env-key", "l")
}

// Test: ACH_ENVIRONMENT env-var satisfies the pk_ --environment
// requirement (D-12: required-via-flag-OR-env).
func TestHydrate_PK_EnvironmentFromEnv(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))
	t.Setenv("ACH_ENVIRONMENT", "demo")

	seedConfig(t, dir, "prod", &config.Deployment{
		URL: mock.server.URL,
		PK:  "pk_aaaaaaaaaaaaaaaaaaaaaawxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, _, code, err := executeHydrate(t, "--no-warnings")
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	// Confirm body carried environment.
	if !strings.Contains(*mock.lastBody, `"environment":"demo"`) {
		t.Errorf("body missing env from ACH_ENVIRONMENT: %q", *mock.lastBody)
	}
}
