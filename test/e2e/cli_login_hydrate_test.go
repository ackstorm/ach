//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 6 CLI e2e suite. Drives `ach login` + `ach hydrate
// --environment demo` against the kept kind cluster (per
// CLAUDE.md "E2E debug loop" — `make cluster-keep`), then
// byte-for-byte diffs vs examples/hydrate.json (D-17, D-18).
//
// Activation: ACH_E2E_PHASE6=1 \
//   ACH_E2E_PHASE6_PK=pk_<26-base32-lower> \
//   ACH_E2E_PHASE6_BASE_URL=https://<live-platform-api> \
//   ./scripts/dev.sh make e2e-focus RUN=TestPhase6CLI
//
// Engineer-pending until the kept kind cluster is up, ./bin/ach is
// built, and the engineer has minted a real pk_ via the Phase 3 SSO
// flow (scripts/uat-phase3.sh or POST /platform/auth/login →
// /sso/callback round-trip against the live cluster). phase6SuiteGuard
// skips cleanly when any prerequisite is missing.
//
// D-18 bypass mechanism: Option A (env-var-injected pk_) — the suite
// writes a synthetic config file under a temp XDG_CONFIG_HOME with the
// pk_ pre-populated, rather than running the device-code flow. This
// avoids the real interactive browser open + Dex round-trip in the
// test path. See test/e2e/phase6_helpers_test.go file header for the
// full activation contract.
//
// Order of subtests is load-bearing:
//
//  1. login_device_code   — stages the synthetic config (Option A).
//  2. whoami_verify_pk    — asserts the staged pk_ resolves end-to-end.
//  3. env_list            — confirms the demo Environment is reachable.
//  4. env_keys_create     — mints an ek_ (one-shot CLI-04 return).
//  5. hydrate_golden_diff — the headline assertion (byte-for-byte vs
//                           the normalized golden).

package e2e

import (
	"bytes"
	"os"
	"testing"
)

// TestPhase6CLI is the single top-level umbrella for the Phase 6 CLI
// e2e suite. Each subtest maps to one of the load-bearing CLI flows
// the Phase 6 plans landed (login → whoami → env list → env-keys
// create → hydrate). The hydrate-golden-diff subtest is the headline
// invariant — the byte-for-byte assertion vs examples/hydrate.json
// (normalized for cluster host) is what the demo collapse hangs on.
func TestPhase6CLI(t *testing.T) {
	t.Run("login_device_code", testPhase6Login)
	t.Run("whoami_verify_pk", testPhase6WhoamiVerifyPk)
	t.Run("env_list", testPhase6EnvList)
	t.Run("env_keys_create", testPhase6EnvKeysCreate)
	t.Run("hydrate_golden_diff", testPhase6HydrateGoldenDiff)
}

// testPhase6Login stages the synthetic config file the rest of the
// suite consumes. Per D-18 Option A (recommended in the plan): the
// test does NOT shell out to `ach login` (the device-code flow
// requires a real Dex round-trip + interactive browser open). Instead,
// the test writes a temp XDG_CONFIG_HOME/ach/config.yaml with
// `default: demo` + `deployments.demo.{url,pk}` populated from
// ACH_E2E_PHASE6_BASE_URL + ACH_E2E_PHASE6_PK.
//
// The pk_ itself is acquired out-of-band by the engineer — typically
// via scripts/uat-phase3.sh or a manual POST /platform/auth/login →
// /sso/callback round-trip against the kept cluster's platform-api.
//
// On success: the temp XDG_CONFIG_HOME path is published via t.Setenv
// so subsequent subtests' `./bin/ach <cmd>` invocations pick up the
// synthetic config. The temp dir is auto-cleaned by t.TempDir().
//
// On failure: nothing — every subtest below also calls
// phase6SuiteGuard + phase6AcquirePk + phase6WriteTempConfig in its
// own preamble, so a partial-skip on this subtest does NOT contaminate
// later subtests with a half-populated XDG_CONFIG_HOME.
func testPhase6Login(t *testing.T) {
	t.Helper()
	phase6SuiteGuard(t)
	pk := phase6AcquirePk(t)
	baseURL := phase6PlatformAPIURL(t)
	xdg := phase6WriteTempConfig(t, baseURL, pk)
	// Publish the synthetic XDG_CONFIG_HOME so later subtests can read it
	// via t.Setenv (each subtest also recreates its own temp dir so the
	// suite is robust to subtest-skip; this publication is a courtesy).
	t.Setenv("ACH_E2E_PHASE6_XDG_CONFIG_HOME", xdg)
	t.Logf("phase6 login (Option A): synthetic config staged at %s/ach/config.yaml "+
		"(default=demo, url=%s)", xdg, baseURL)
}

// testPhase6WhoamiVerifyPk asserts `ach whoami --verify` exits 0
// against the live cluster's platform-api when handed a pk_. Per D-13
// + spec §5.3: pk_ → `GET /platform/environments?limit=1`.
//
// Captured stdout MUST include the deployment name AND a masked pk_
// tail (`pk_****<last-4>`) per CLI-04 / Pattern S5. The raw pk_
// MUST NOT appear in stdout or stderr (CLI-04 / OBS-02 no-leak).
func testPhase6WhoamiVerifyPk(t *testing.T) {
	t.Helper()
	phase6SuiteGuard(t)
	pk := phase6AcquirePk(t)
	baseURL := phase6PlatformAPIURL(t)
	xdg := phase6WriteTempConfig(t, baseURL, pk)

	stdout, stderr, err := phase6RunAch(t, xdg, "whoami", "--verify")
	code, runErr := phase6StripExitErr(err)
	if runErr != nil {
		t.Fatalf("ach whoami --verify: exec error: %v\nstdout=%s\nstderr=%s",
			runErr, stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("ach whoami --verify: exit %d (want 0)\nstdout=%s\nstderr=%s",
			code, stdout, stderr)
	}
	// Identity block must surface the deployment name + a masked pk_ tail.
	if !phase6Contains(stdout, "demo") {
		t.Errorf("ach whoami --verify: stdout missing deployment 'demo'; got=%s", stdout)
	}
	if !phase6Contains(stdout, "pk_****") {
		t.Errorf("ach whoami --verify: stdout missing masked pk_ tail (CLI-04); got=%s", stdout)
	}
	// No raw pk_ leak per OBS-02 / Pattern S5. The masked form (pk_****)
	// is OK; the raw bearer (full ACH_E2E_PHASE6_PK value) is not.
	if bytes.Contains(stdout, []byte(pk)) {
		t.Errorf("ach whoami --verify: raw pk_ leaked to stdout (CLI-04 no-leak)")
	}
	if bytes.Contains(stderr, []byte(pk)) {
		t.Errorf("ach whoami --verify: raw pk_ leaked to stderr (CLI-04 no-leak)")
	}
}

// testPhase6EnvList asserts `ach env list` exits 0 and the stdout
// contains the row for the demo Environment (the standard cluster
// fixture from examples/04-environment-demo.yaml).
func testPhase6EnvList(t *testing.T) {
	t.Helper()
	phase6SuiteGuard(t)
	pk := phase6AcquirePk(t)
	baseURL := phase6PlatformAPIURL(t)
	xdg := phase6WriteTempConfig(t, baseURL, pk)

	stdout, stderr, err := phase6RunAch(t, xdg, "env", "list")
	code, runErr := phase6StripExitErr(err)
	if runErr != nil {
		t.Fatalf("ach env list: exec error: %v\nstdout=%s\nstderr=%s",
			runErr, stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("ach env list: exit %d (want 0)\nstdout=%s\nstderr=%s",
			code, stdout, stderr)
	}
	if !phase6Contains(stdout, phase6DemoEnvironment) {
		t.Errorf("ach env list: stdout missing 'demo' environment row; got=%s", stdout)
	}
}

// testPhase6EnvKeysCreate asserts `ach env-keys create --environment
// demo --name e2e-test-key` exits 0 and stdout includes a freshly-minted
// ek_ plaintext (the one-time return per CLI-04 / D-07).
//
// The minted ek_ is captured for documentation in subsequent runs but
// is NOT subsequently exercised against the cluster — the W2 ek_
// asymmetric-verify path is covered by whoami's --env-key flag in
// production; folding that here would require either a second `ach
// whoami --env-key` invocation (which reads the ek from the same XDG
// config the previous step wrote to) or careful subtest-ordering, and
// either would only re-prove the W2 unit-test coverage.
//
// The persistence side-effect (D-07 always-persist) means the ek_ is
// now in the temp XDG_CONFIG_HOME's config.yaml under
// `deployments.demo.ek.e2e-test-key` — useful for cleanup/inspection
// when running with -keep or under a manual XDG export.
func testPhase6EnvKeysCreate(t *testing.T) {
	t.Helper()
	phase6SuiteGuard(t)
	pk := phase6AcquirePk(t)
	baseURL := phase6PlatformAPIURL(t)
	xdg := phase6WriteTempConfig(t, baseURL, pk)

	stdout, stderr, err := phase6RunAch(t, xdg,
		"env-keys", "create",
		"--environment", phase6DemoEnvironment,
		"--name", "e2e-test-key",
	)
	code, runErr := phase6StripExitErr(err)
	if runErr != nil {
		t.Fatalf("ach env-keys create: exec error: %v\nstdout=%s\nstderr=%s",
			runErr, stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("ach env-keys create: exit %d (want 0)\nstdout=%s\nstderr=%s",
			code, stdout, stderr)
	}
	// Mint must surface the ek_ plaintext exactly once per CLI-04.
	if !phase6Contains(stdout, "ek_") {
		t.Errorf("ach env-keys create: stdout missing minted ek_ plaintext; got=%s", stdout)
	}
}

// testPhase6HydrateGoldenDiff is the headline assertion — `ach hydrate
// --environment demo` stdout MUST byte-for-byte equal the normalized
// golden (examples/hydrate.json with "ach.local.test" rewritten to the
// live cluster's externally-visible host).
//
// Per W4 — the host-substitution decision is LOCKED here, not deferred
// to SUMMARY. The 06-06 hydrate command emits the response body
// verbatim via io.Copy (no re-encoding); the only intentional transform
// happens in phase6NormalizeHydrate.
//
// This is the demo-collapse anchor: any future change to the hydrate
// command's stdout shape OR to the server's render.JSON output WILL
// break this assertion. That's by design — the byte-for-byte parity is
// the contract the demo hangs on.
func testPhase6HydrateGoldenDiff(t *testing.T) {
	t.Helper()
	phase6SuiteGuard(t)
	pk := phase6AcquirePk(t)
	baseURL := phase6PlatformAPIURL(t)
	xdg := phase6WriteTempConfig(t, baseURL, pk)

	// D-21: pass --raw to preserve the Phase 6 byte-for-byte POST+stream
	// stdout contract under Phase 7's engine-default ach-cli binary. The
	// hidden --raw flag short-circuits BEFORE any engine call so the
	// golden-diff anchor at examples/hydrate.json continues to hold.
	stdout, stderr, err := phase6RunAch(t, xdg,
		"hydrate", "--environment", phase6DemoEnvironment,
		"--no-warnings",
		"--raw",
	)
	code, runErr := phase6StripExitErr(err)
	if runErr != nil {
		t.Fatalf("ach hydrate: exec error: %v\nstdout=%s\nstderr=%s",
			runErr, stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("ach hydrate: exit %d (want 0)\nstdout=%s\nstderr=%s",
			code, stdout, stderr)
	}

	golden, gErr := os.ReadFile(phase6GoldenPath)
	if gErr != nil {
		t.Fatalf("read golden %s: %v", phase6GoldenPath, gErr)
	}
	liveBaseURL := phase6PlatformAPIURL(t)
	expected := phase6NormalizeHydrate(golden, liveBaseURL)

	if !bytes.Equal(stdout, expected) {
		t.Errorf("ach hydrate output != golden (normalized for baseURL=%s):\n"+
			"want=%s\ngot=%s\n"+
			"NOTE: the golden uses %q as the base URL; the live cluster uses %q. "+
			"phase6NormalizeHydrate rewrites scheme+host in the golden before "+
			"compare; if the diff persists, the cluster is emitting a "+
			"structurally-different response body (Phase 7 schemaVersion bump? "+
			"new field? whitespace drift in the server's render.JSON?) — "+
			"re-capture the golden via ./bin/ach hydrate --environment demo, "+
			"rewrite the live base back to %q, and audit the diff. See CLAUDE.md "+
			"\"Common failure modes\" entry \"Hydrate output != "+
			"examples/hydrate.json\" for the gotcha documentation.",
			liveBaseURL, expected, stdout,
			phase6DefaultBaseURL, liveBaseURL, phase6DefaultBaseURL)
	}
}
