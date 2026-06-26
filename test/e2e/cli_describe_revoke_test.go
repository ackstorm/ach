//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 6 CLI render + lifecycle e2e — drives `ach-cli env describe`,
// `keys create/list/revoke`, and the hydrate target-autodetect path against
// the kept kind cluster, asserting on the REAL server's response shape.
//
// Why this file exists: the unit tests for these renderers use synthetic
// view structs, which let a field-shape mismatch slip through (v0.5.5 shipped
// an em-dash fix that dashed the wrong runtime column because the unit test's
// fixture didn't match what /platform/hydrate actually emits — mcpServers carry
// an `id`+`endpoint` with an EMPTY `name`). These subtests close that gap: they
// run the real binary against the live cluster so the rendered output is checked
// against real data, no release required.
//
// Activation: same as TestPhase6CLI — `make e2e-run` / `make e2e-focus
// RUN=TestPhase6CLIRenderAndLifecycle` against the kept cluster.

package e2e

import (
	"strings"
	"testing"
)

// TestPhase6CLIRenderAndLifecycle groups the render + key-lifecycle assertions
// that need the live platform-api. Each subtest is self-contained (acquires its
// own pk + synthetic XDG) so a partial skip never contaminates the others.
func TestPhase6CLIRenderAndLifecycle(t *testing.T) {
	t.Run("env_describe_render", testPhase6EnvDescribeRender)
	t.Run("keys_revoke_confirmation", testPhase6KeysRevokeConfirmation)
	t.Run("hydrate_autodetect_friendly_error", testPhase6HydrateAutodetectError)
}

// testPhase6EnvDescribeRender asserts `ach env describe demo` against the live
// cluster renders the two contract fixes:
//   - Context table has NO ID column (it always duplicated NAME) → the header
//     row is exactly KIND / NAME / DOWNLOADURL.
//   - Every empty Runtime cell renders as an em dash. The demo Environment's
//     mcpServers come back from /platform/hydrate with an empty `name`, so the
//     mcpServer rows MUST show "—" in the NAME column (the cell v0.5.5 missed).
func testPhase6EnvDescribeRender(t *testing.T) {
	t.Helper()
	phase6SuiteGuard(t)
	pk := phase6AcquirePk(t)
	baseURL := phase6PlatformAPIURL(t)
	xdg := phase6WriteTempConfig(t, baseURL, pk)

	stdout, stderr, err := phase6RunAch(t, xdg, "env", "describe", phase6DemoEnvironment)
	code, runErr := phase6StripExitErr(err)
	if runErr != nil {
		t.Fatalf("ach env describe: exec error: %v\nstdout=%s\nstderr=%s", runErr, stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("ach env describe: exit %d (want 0)\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}

	lines := strings.Split(string(stdout), "\n")

	// #2 — Context header must be exactly KIND/NAME/DOWNLOADURL (no ID column).
	// tabwriter pads with spaces, so compare the field set, not raw spacing.
	var ctxHeader []string
	for _, ln := range lines {
		if strings.Contains(ln, "DOWNLOADURL") {
			ctxHeader = strings.Fields(ln)
			break
		}
	}
	if ctxHeader == nil {
		t.Fatalf("ach env describe: no Context header (DOWNLOADURL) found:\n%s", stdout)
	}
	if got, want := strings.Join(ctxHeader, " "), "KIND NAME DOWNLOADURL"; got != want {
		t.Errorf("Context header = %q, want %q (the redundant ID column must be gone):\n%s", got, want, stdout)
	}

	// #3 — at least one mcpServer runtime row must em-dash its empty NAME cell.
	// Row field shape: [mcpServer, <name-or-em-dash>, <id>, <endpoint>].
	sawMcp, sawDashedName := false, false
	for _, ln := range lines {
		f := strings.Fields(ln)
		if len(f) >= 2 && f[0] == "mcpServer" {
			sawMcp = true
			if f[1] == "—" {
				sawDashedName = true
			}
		}
	}
	if !sawMcp {
		t.Fatalf("ach env describe: no mcpServer runtime rows found (demo fixture should have them):\n%s", stdout)
	}
	if !sawDashedName {
		t.Errorf("ach env describe: mcpServer empty NAME not rendered as em dash '—':\n%s", stdout)
	}
}

// testPhase6KeysRevokeConfirmation asserts the full self-service key lifecycle:
// create an ek_ → look up its ekid_ via `keys list` → revoke it → the revoke
// prints a "Revoked <id>" confirmation (the v0.5.5 fix; previously silent).
func testPhase6KeysRevokeConfirmation(t *testing.T) {
	t.Helper()
	phase6SuiteGuard(t)
	pk := phase6AcquirePk(t)
	baseURL := phase6PlatformAPIURL(t)
	xdg := phase6WriteTempConfig(t, baseURL, pk)

	const label = "e2e-revoke-test"

	// 1. Create a throwaway ek_ for the demo environment.
	stdout, stderr, err := phase6RunAch(t, xdg, "keys", "create", phase6DemoEnvironment, "--name", label)
	if code, runErr := phase6StripExitErr(err); runErr != nil || code != 0 {
		t.Fatalf("keys create: exit=%d err=%v\nstdout=%s\nstderr=%s", code, runErr, stdout, stderr)
	}
	if !phase6Contains(stdout, "ek-") {
		t.Fatalf("keys create: no ek_ plaintext in stdout:\n%s", stdout)
	}

	// 2. Find the ekid_ for that label via `keys list --type ek`.
	listOut, listErr, err := phase6RunAch(t, xdg, "keys", "list", "--type", "ek")
	if code, runErr := phase6StripExitErr(err); runErr != nil || code != 0 {
		t.Fatalf("keys list: exit=%d err=%v\nstdout=%s\nstderr=%s", code, runErr, listOut, listErr)
	}
	var ekid string
	for _, ln := range strings.Split(string(listOut), "\n") {
		if strings.Contains(ln, label) {
			if f := strings.Fields(ln); len(f) > 0 && strings.HasPrefix(f[0], "ekid_") {
				ekid = f[0]
				break
			}
		}
	}
	if ekid == "" {
		t.Fatalf("keys list: could not find ekid_ for label %q:\n%s", label, listOut)
	}

	// 3. Revoke it — stdout must carry the "Revoked <id>" confirmation.
	revOut, revErr, err := phase6RunAch(t, xdg, "keys", "revoke", ekid, "--yes")
	code, runErr := phase6StripExitErr(err)
	if runErr != nil {
		t.Fatalf("keys revoke: exec error: %v\nstdout=%s\nstderr=%s", runErr, revOut, revErr)
	}
	if code != 0 {
		t.Fatalf("keys revoke: exit %d (want 0)\nstdout=%s\nstderr=%s", code, revOut, revErr)
	}
	if !phase6Contains(revOut, "Revoked "+ekid) {
		t.Errorf("keys revoke: stdout missing 'Revoked %s' confirmation:\n%s", ekid, revOut)
	}
}

// testPhase6HydrateAutodetectError asserts that `ach env hydrate` against an
// EMPTY workspace (no agent adapter present) fails with the friendly
// "no agent target detected" prompt naming the closed set — rather than a
// false "multiple agent targets" match or an opaque error. Driving --output at
// a fresh empty dir scopes autodetection there (and exercises the same
// root-relative detection that the #4 $HOME-bleed fix corrected).
func testPhase6HydrateAutodetectError(t *testing.T) {
	t.Helper()
	phase6SuiteGuard(t)
	pk := phase6AcquirePk(t)
	baseURL := phase6PlatformAPIURL(t)
	xdg := phase6WriteTempConfig(t, baseURL, pk)

	emptyWorkspace := t.TempDir()
	stdout, stderr, err := phase6RunAch(t, xdg,
		"env", "hydrate", phase6DemoEnvironment, "--output", emptyWorkspace)
	code, runErr := phase6StripExitErr(err)
	if runErr != nil {
		t.Fatalf("env hydrate (autodetect): exec error: %v\nstdout=%s\nstderr=%s", runErr, stdout, stderr)
	}
	if code == 0 {
		t.Fatalf("env hydrate (autodetect): exit 0, want non-zero on no-target\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	combined := string(stdout) + string(stderr)
	if !strings.Contains(combined, "no agent target detected") {
		t.Errorf("env hydrate (autodetect): missing friendly 'no agent target detected' prompt:\nstdout=%s\nstderr=%s", stdout, stderr)
	}
}
