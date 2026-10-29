//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Bare `ach-cli env hydrate` e2e — the workspace re-hydrate contract.
//
// Drives the host-built ach-cli against the kept kind cluster (per CLAUDE.md
// "E2E debug loop"):
//
//  1. Hydrate two fixture Environments (demo, env-valid) into a temp
//     workspace, each --target claude-code.
//  2. Bare `ach-cli env hydrate` (no <name>, no ACH_ENVIRONMENT, no --target)
//     — re-hydrates every Environment recorded under .ach/ with its recorded
//     targets, best-effort.
//  3. Assert exit 0 and that both envs report OK.
//
// Activation mirrors the Phase 7 CLI suite: phase7SuiteGuard skips cleanly
// when the cluster / binary / pk prerequisites are absent.

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// workspaceEnvs are the two fixture Environments the workspace hydrates.
// Both authorize team "default" (the SSO-minted pk's team), so both resolve
// for the seeded credential. demo carries skills+prompts+artifacts; env-valid
// carries prompts+artifacts — each writes the per-platform state file the
// bare hydrate discovers.
var workspaceEnvs = []string{"demo", "env-valid"}

// runAchCliInDir is a cwd-aware sibling of phase7RunAchCli: it sets cmd.Dir
// so the cwd-relative commands (bare `env hydrate`) operate on
// the temp workspace rather than the e2e package directory. Same env hygiene
// as phase7RunAchCliEnv (synthetic-mode vars stripped, XDG seeded,
// ACH_INSECURE=1 for the http:// local gateway).
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
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(cleanEnv(os.Environ()), "XDG_CONFIG_HOME="+xdgHome, "ACH_INSECURE=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// TestHydrateWorkspace_BareRehydratesRecordedEnvs is the bare-hydrate
// contract end-to-end.
func TestHydrateWorkspace_BareRehydratesRecordedEnvs(t *testing.T) {
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	ws := phase7Workspace(t)

	// 1. Hydrate both fixture Environments into the workspace (cwd = ws).
	for _, env := range workspaceEnvs {
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

	// 2. Bare hydrate — no <name>, no --target: envs + targets come from .ach/.
	stdout, stderr, err := runAchCliInDir(t, xdg, ws, "env", "hydrate")
	code, runErr := phase7StripExitErr(err)
	if runErr != nil {
		t.Fatalf("bare hydrate: exec error: %v\nstdout=%s\nstderr=%s", runErr, stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("bare hydrate: exit %d (want 0 — every recorded env hydrates)\nstdout=%s\nstderr=%s",
			code, stdout, stderr)
	}

	// 3. Every recorded env was re-hydrated: one verb-column row per env,
	// then "Finished N of N".
	for _, env := range workspaceEnvs {
		if row := fmt.Sprintf("\n%12s ", env); !bytes.Contains(stdout, []byte(row)) {
			t.Errorf("bare hydrate: no row for %q\nstdout=%s", env, stdout)
		}
	}
	n := len(workspaceEnvs)
	if want := fmt.Sprintf("%12s %d of %d in ", "Finished", n, n); !bytes.Contains(stdout, []byte(want)) {
		t.Errorf("bare hydrate: missing %q\nstdout=%s", want, stdout)
	}
}
