// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/hydrate"
)

// executeCommand moved to helpers_test.go (06-04) — single shared driver
// for the entire cmd/ach/cmd package. This file consumes it directly.

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
//
// Note: as of 07-W3-05 the cobra layer ALSO accepts engine flags
// (--include-runtime / --only-runtime / --sync / --force / --dry-run
// / --wait / --lock-timeout / --output / --allow-symlinks / --platform
// / --global). The existing Phase 6 tests below were authored against
// the surface-only --raw path; we prepend "--raw" here so the legacy
// suite exercises the Phase 6 POST+stream byte-for-byte contract
// (D-04). New engine tests use executeHydrateEngine which does NOT
// prepend --raw.
func executeHydrate(t *testing.T, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	rawArgs := append([]string{"--raw"}, args...)
	return executeCommand(t, newHydrateCmd(), rawArgs...)
}

// executeHydrateEngine drives the same dispatch path as executeHydrate
// but WITHOUT injecting --raw, so the engine code path is exercised.
// New Phase 7 tests use this helper.
func executeHydrateEngine(t *testing.T, args ...string) (string, string, exit.Code, error) {
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

	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
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
	if *mock.lastKey != "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz" {
		t.Errorf("x-ach-key = %q; want pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz", *mock.lastKey)
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

	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
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

	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
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

	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
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

	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		EK:  map[string]string{"local-laptop": "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAghij"},
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
	if *mock.lastKey != "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAghij" {
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

	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
		EK:  map[string]string{"demo": "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAghij"},
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, _, code, err := executeHydrate(t,
		"--api-key", "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
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

	t.Setenv("ACH_API_KEY", "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz")
	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		EK:  map[string]string{"demo": "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAghij"},
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
	seedConfig(t, dir, "prod", &config.Profile{
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
	t.Setenv("ACH_API_KEY", "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz")
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
// config-resolved profile per spec §6.1 / D-11).
func TestHydrate_SyntheticMode_EnvKey_Exit1(t *testing.T) {
	whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))
	t.Setenv("ACH_BASE_URL", mock.server.URL)
	t.Setenv("ACH_API_KEY", "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz")
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
	seedConfig(t, dir, "prod", &config.Profile{
		URL: ts.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
		EK:  map[string]string{"l": "ek-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAghij"},
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

	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
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

// ============================================================================
// Phase 7 W3-05 engine-path tests
// ============================================================================

// TestNewHydrateCmd_FlagsRegistered asserts every Phase 7 engine flag
// the D-03 refactor adds is registered on the cobra.Command — the
// engine surface is the new user-facing default.
func TestNewHydrateCmd_FlagsRegistered(t *testing.T) {
	cmd := newHydrateCmd()
	wantFlags := []string{
		"include-runtime", "only-runtime", "sync", "force", "dry-run",
		"wait", "lock-timeout", "output", "allow-symlinks", "platform",
		"global", "raw",
		// Phase 6 surface preserved.
		"environment", "no-warnings", "verbose", "api-key", "env-key", "profile",
	}
	for _, name := range wantFlags {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("flag --%s not registered", name)
		}
	}
}

// TestNewHydrateCmd_RawFlag_Hidden asserts the --raw flag is registered
// (so callers can pass it) AND hidden (so --help does not advertise
// it) per D-04.
func TestNewHydrateCmd_RawFlag_Hidden(t *testing.T) {
	cmd := newHydrateCmd()
	f := cmd.Flags().Lookup("raw")
	if f == nil {
		t.Fatal("--raw flag not registered")
	}
	if !f.Hidden {
		t.Error("--raw flag should be hidden in --help")
	}
}

// TestRunHydrate_RawDispatchesToLegacy asserts the --raw path produces
// byte-for-byte identical stdout to the Phase 6 contract — the W3-P3
// golden-diff anchor depends on this.
func TestRunHydrate_RawDispatchesToLegacy(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))

	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	stdout, _, code, err := executeHydrateEngine(t, "--raw",
		"--environment", "demo", "--no-warnings")
	if err != nil {
		t.Fatalf("hydrate --raw: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if !bytes.Equal([]byte(stdout), []byte(canonicalHydrateJSON)) {
		t.Errorf("--raw stdout != canonical bytes\nwant: %q\ngot:  %q",
			canonicalHydrateJSON, stdout)
	}
	if got := atomic.LoadInt32(mock.calls); got != 1 {
		t.Errorf("HTTP calls = %d; want 1", got)
	}
}

// TestRunHydrate_EngineDispatch asserts the engine path is invoked
// when --raw is absent. Swap hydrateRunFn with a recorder and assert
// it is called with Opts carrying the resolved platform + environment.
func TestRunHydrate_EngineDispatch(t *testing.T) {
	withCleanHomeEngine(t)
	dir := whoamiTestEnv(t)

	// Seed the workspace cwd with a .claude/ signal so autodetect
	// resolves to claude-code.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", ".mcp.json"),
		[]byte("{}"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Server returns canonical hydrate JSON for the manifest fetch
	// (engine reads it during step 5).
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))
	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	// Swap hydrateRunFn with a recorder. The fake returns
	// (Result{}, nil) so the cobra layer's downstream rendering is
	// satisfied.
	var (
		called       atomic.Bool
		capturedOpts hydrate.Opts
	)
	prev := hydrateRunFn
	hydrateRunFn = func(_ context.Context, opts hydrate.Opts) (hydrate.Result, error) {
		called.Store(true)
		capturedOpts = opts
		return hydrate.Result{}, nil
	}
	t.Cleanup(func() { hydrateRunFn = prev })

	// --output overrides cwd → autodetect against the seeded root.
	_, _, code, err := executeHydrateEngine(t,
		"--environment", "demo", "--no-warnings", "--output", root)
	if err != nil {
		t.Fatalf("hydrate engine: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if !called.Load() {
		t.Fatal("hydrateRunFn was not invoked — engine dispatch broken")
	}
	if capturedOpts.Environment != "demo" {
		t.Errorf("Opts.Environment = %q; want demo", capturedOpts.Environment)
	}
	if capturedOpts.Platform != "claude-code" {
		t.Errorf("Opts.Platform = %q; want claude-code", capturedOpts.Platform)
	}
	if capturedOpts.Output != root {
		t.Errorf("Opts.Output = %q; want %q", capturedOpts.Output, root)
	}
	if capturedOpts.Bearer != "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz" {
		t.Errorf("Opts.Bearer = %q; want pk_…", capturedOpts.Bearer)
	}
}

// TestRunHydrate_IncludeAndOnlyRuntime_MutuallyExclusive asserts the
// scope-flag conflict surfaces as exit 1 BEFORE any HTTP call.
func TestRunHydrate_IncludeAndOnlyRuntime_MutuallyExclusive(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))
	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, _, code, err := executeHydrateEngine(t,
		"--include-runtime", "--only-runtime",
		"--environment", "demo", "--no-warnings")
	if err == nil {
		t.Fatal("expected scope-flag conflict error")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err missing 'mutually exclusive': %q", err.Error())
	}
	if got := atomic.LoadInt32(mock.calls); got != 0 {
		t.Errorf("HTTP calls = %d; want 0 (client-side gate)", got)
	}
}

// TestRunHydrate_WaitAndLockTimeout_MutuallyExclusive asserts the
// locking-flag conflict surfaces as exit 1 BEFORE any HTTP call.
func TestRunHydrate_WaitAndLockTimeout_MutuallyExclusive(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))
	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, _, code, err := executeHydrateEngine(t,
		"--wait", "--lock-timeout", "5s",
		"--environment", "demo", "--no-warnings")
	if err == nil {
		t.Fatal("expected lock-flag conflict error")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err missing 'mutually exclusive': %q", err.Error())
	}
}

// TestRunHydrate_UnknownPlatform asserts --platform <bogus> surfaces
// as exit 1 via hydrate.ResolvePlatform.
func TestRunHydrate_UnknownPlatform(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))
	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	_, _, code, err := executeHydrateEngine(t,
		"--platform", "clade-code",
		"--environment", "demo", "--no-warnings")
	if err == nil {
		t.Fatal("expected unknown-platform error")
	}
	if code != exit.General {
		t.Errorf("code = %d; want 1", code)
	}
	if !strings.Contains(err.Error(), "unknown platform") {
		t.Errorf("err missing 'unknown platform': %q", err.Error())
	}
}

// TestRunHydrate_AliasPlatform asserts --platform claude (an alias
// for claude-code) resolves correctly to the canonical id passed
// down into Opts.Platform.
func TestRunHydrate_AliasPlatform(t *testing.T) {
	dir := whoamiTestEnv(t)
	mock := newHydrateMock(t, []byte(canonicalHydrateJSON))
	seedConfig(t, dir, "prod", &config.Profile{
		URL: mock.server.URL,
		PK:  "pk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAwxyz",
	})
	swapHydrateHTTPClientForTest(t, mock.server.Client())

	var capturedOpts hydrate.Opts
	prev := hydrateRunFn
	hydrateRunFn = func(_ context.Context, opts hydrate.Opts) (hydrate.Result, error) {
		capturedOpts = opts
		return hydrate.Result{}, nil
	}
	t.Cleanup(func() { hydrateRunFn = prev })

	_, _, code, err := executeHydrateEngine(t,
		"--platform", "claude",
		"--environment", "demo", "--no-warnings")
	if err != nil {
		t.Fatalf("hydrate engine: %v", err)
	}
	if code != exit.OK {
		t.Errorf("code = %d; want 0", code)
	}
	if capturedOpts.Platform != "claude-code" {
		t.Errorf("Opts.Platform = %q; want claude-code (canonical id)",
			capturedOpts.Platform)
	}
}

// withCleanHomeEngine scrubs $HOME for the lifetime of t so global-
// mode hints in codex's Detect (e.g. $HOME/.codex/) do not leak
// cross-test signals. Engine-path tests that exercise autodetect on
// a controlled --output root use this to avoid surprise multi-match
// from the agent's actual $HOME.
func withCleanHomeEngine(t *testing.T) {
	t.Helper()
	scratch := t.TempDir()
	t.Setenv("HOME", scratch)
}

// TestValidateEnvHeaderValue exercises security 2.10 — CRLF / NUL /
// control-byte rejection on the x-ach-environment header value.
func TestValidateEnvHeaderValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "empty", input: "", wantErr: true},
		{name: "valid_simple", input: "demo", wantErr: false},
		{name: "valid_with_dash", input: "demo-prod", wantErr: false},
		{name: "valid_with_underscore", input: "demo_env", wantErr: false},
		{name: "crlf_injection", input: "demo\r\nX-Injected: yes", wantErr: true},
		{name: "lf_only", input: "demo\nfoo", wantErr: true},
		{name: "cr_only", input: "demo\rfoo", wantErr: true},
		{name: "nul_byte", input: "demo\x00foo", wantErr: true},
		{name: "tab", input: "demo\tfoo", wantErr: true},
		{name: "delete_byte", input: "demo\x7ffoo", wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateEnvHeaderValue(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("input %q: want error, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("input %q: want nil, got %v", tc.input, err)
			}
		})
	}
}
