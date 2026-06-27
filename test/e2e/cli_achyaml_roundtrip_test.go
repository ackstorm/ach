//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// ach.yaml project-manifest round-trip e2e — the "clone-and-go" contract.
//
// Drives the host-built ach-cli against the kept kind cluster (per CLAUDE.md
// "E2E debug loop") through the full committed-manifest lifecycle:
//
//  1. Hydrate two fixture Environments (demo, env-valid) into a temp
//     workspace, each --target claude-code.
//  2. `ach-cli env save` — derive a committed ach.yaml from the realized
//     .ach/<env>/ state; assert it lists BOTH envs with explicit targets.
//  3. Simulate a fresh clone: keep only the committed ach.yaml, delete .ach/
//     AND the projected adapter dir (.claude/) so nothing but the manifest
//     survives.
//  4. Bare `ach-cli env hydrate` (no <name>, no ACH_ENVIRONMENT) — reads
//     ach.yaml and re-materializes every listed Environment best-effort.
//  5. Assert exit 0 and that both envs' per-platform state files are
//     re-created — proving the manifest alone reproduces the workspace.
//
// Activation mirrors the Phase 7 CLI suite: phase7SuiteGuard skips cleanly
// when the cluster / binary / pk prerequisites are absent.

package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// achRoundTripEnvs are the two fixture Environments the round-trip hydrates.
// Both authorize team "default" (the SSO-minted pk's team), so both resolve
// for the seeded credential. demo carries skills+prompts+artifacts; env-valid
// carries prompts+artifacts — each writes a per-platform state file the
// manifest derivation reads.
var achRoundTripEnvs = []string{"demo", "env-valid"}

// runAchCliInDir is a cwd-aware sibling of phase7RunAchCli: it sets cmd.Dir
// so the cwd-relative commands (`env save`, bare `env hydrate`) operate on
// the temp workspace rather than the e2e package directory. Same env hygiene
// as phase7RunAchCliEnv (synthetic-mode vars stripped, XDG seeded,
// ACH_INSECURE=1 for the http:// local gateway) and the same
// --include-runtime injection for hydrate calls.
func runAchCliInDir(t *testing.T, xdgHome, dir string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// phase7BinaryPath is relative to the test package cwd; resolve it to an
	// absolute path BEFORE setting cmd.Dir, or exec would resolve the relative
	// path against cmd.Dir (the temp workspace) and fail with "no such file".
	bin, err := filepath.Abs(phase7BinaryPath)
	if err != nil {
		t.Fatalf("runAchCliInDir: resolve binary path %q: %v", phase7BinaryPath, err)
	}
	cmd := exec.CommandContext(ctx, bin, phase7ArgsWithRuntime(args)...)
	cmd.Dir = dir
	cmd.Env = append(cleanEnv(os.Environ()), "XDG_CONFIG_HOME="+xdgHome, "ACH_INSECURE=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// TestAchYamlRoundTrip is the clone-and-go contract end-to-end.
func TestAchYamlRoundTrip(t *testing.T) {
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	ws := phase7Workspace(t)

	// 1. Hydrate both fixture Environments into the workspace (cwd = ws).
	for _, env := range achRoundTripEnvs {
		stdout, stderr, err := runAchCliInDir(t, xdg, ws,
			"env", "hydrate", env, "--target", phase7PlatformClaudeCode)
		code, runErr := phase7StripExitErr(err)
		if runErr != nil {
			t.Fatalf("seed hydrate %q: exec error: %v\nstdout=%s\nstderr=%s", env, runErr, stdout, stderr)
		}
		if code != 0 {
			t.Fatalf("seed hydrate %q: exit %d (want 0)\nstdout=%s\nstderr=%s", env, code, stdout, stderr)
		}
	}

	// 2. env save → committed ach.yaml listing both envs with explicit targets.
	stdout, stderr, err := runAchCliInDir(t, xdg, ws, "env", "save")
	code, runErr := phase7StripExitErr(err)
	if runErr != nil {
		t.Fatalf("env save: exec error: %v\nstdout=%s\nstderr=%s", runErr, stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("env save: exit %d (want 0)\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	manifestRaw, mErr := os.ReadFile(filepath.Join(ws, "ach.yaml"))
	if mErr != nil {
		t.Fatalf("env save: ach.yaml not written: %v\nstdout=%s", mErr, stdout)
	}
	for _, env := range achRoundTripEnvs {
		if !bytes.Contains(manifestRaw, []byte(env)) {
			t.Errorf("ach.yaml missing env %q:\n%s", env, manifestRaw)
		}
	}
	// Explicit targets are required for a no-adapter-dir restore (a fresh clone
	// has nothing to autodetect from); the adapter always records Adapter.ID.
	if !bytes.Contains(manifestRaw, []byte(phase7PlatformClaudeCode)) {
		t.Errorf("ach.yaml missing explicit %q targets:\n%s", phase7PlatformClaudeCode, manifestRaw)
	}

	// 3. Simulate a fresh clone: keep only the committed ach.yaml, drop the
	// hydrate state AND the projected adapter dir.
	for _, d := range []string{".ach", ".claude"} {
		if err := os.RemoveAll(filepath.Join(ws, d)); err != nil {
			t.Fatalf("clone-sim: rm %s: %v", d, err)
		}
	}

	// 4. Bare hydrate — reads ach.yaml, hydrates every listed env best-effort.
	bStdout, bStderr, bErr := runAchCliInDir(t, xdg, ws, "env", "hydrate")
	bCode, bRunErr := phase7StripExitErr(bErr)
	if bRunErr != nil {
		t.Fatalf("bare hydrate: exec error: %v\nstdout=%s\nstderr=%s", bRunErr, bStdout, bStderr)
	}
	if bCode != 0 {
		t.Fatalf("bare hydrate: exit %d (want 0 — all manifest envs hydrate)\nstdout=%s\nstderr=%s",
			bCode, bStdout, bStderr)
	}

	// 5. Both envs re-materialized from the manifest alone.
	for _, env := range achRoundTripEnvs {
		sp := phase7StatePath(ws, env, phase7PlatformClaudeCode)
		if _, err := os.Stat(sp); err != nil {
			t.Errorf("bare hydrate: env %q not re-hydrated (state file %s absent): %v\nstderr=%s",
				env, sp, err, bStderr)
		}
	}
}
