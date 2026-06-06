//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Per-adapter projection + lifecycle e2e matrix — Plan 06-02 (VER-02).
//
// Drives `ach-cli hydrate` + `ach-cli uninstall` against the kept kind cluster
// (per CLAUDE.md "E2E debug loop" — `make e2e-full` keeps the cluster) for the
// full projection lifecycle of the demo Environment's caveman plugin, for every
// canonical adapter id:
//
//	claude-code · codex · gemini-cli · opencode · pimono
//
// The existing Phase 7 CLI-engine suite (cli_hydrate_engine_test.go) proves
// RUNTIME-config emission + drift/SIGKILL/safe-extract, but NO existing e2e
// asserts that a PLUGIN tree is PROJECTED into the adapter-native resource
// dirs, that drops are warned, that state records the plugins, or that
// uninstall removes them while preserving co-owned user keys. This file closes
// VER-02 by gating that flow end-to-end against a real cluster.
//
// Per subtest (after phase7SuiteGuard skip-on-cluster-missing preamble):
//
//	1. Acquire pk_ + seed XDG (phase7AcquirePk / phase7SeedXdgConfig), wait
//	   the demo Environment Available, allocate a fresh phase7Workspace.
//	2. hydrate --environment demo --platform <id> --output <ws>  (CONTEXT-slice
//	   projection: NOT --only-runtime, so plugin projection runs). Assert exit 0.
//	3. assertProjectedNativeDirs — projected resources in native dirs, NOT at
//	   verbatim source paths (SC2).
//	4. assertDropsWarned — pimono drops `agents`; the others drop no projected
//	   kind (caveman ships none of their drop kinds).
//	5. assertStateRecordsPlugins — state.json v2 Plugins[] records the targets.
//	6. Pre-seed the adapter's co-owned deep-merge file with a USER key, re-run
//	   hydrate (ACH keys land alongside), then `uninstall --include-runtime`.
//	7. After removal: assertCoOwnedUserKeyPreserved (user key survives) AND
//	   assertFileOwnedResourcesGone (the projected native-dir files are gone).
//
// Activation (mirrors cli_hydrate_engine_test.go):
//
//	make e2e-full                         # cluster-up + e2e binaries, cluster KEPT
//	make e2e-focus RUN='TestProjectionLifecycle'
//
// Replay one adapter:  make e2e-focus RUN='TestProjectionLifecycle/pimono'
//
// Every subtest re-runs phase7SuiteGuard so a partial skip (cluster/pk gate
// unset) does not contaminate the rest — never a silent pass without the guard.
// Where caveman does not exercise a kind, the per-adapter descriptor (see
// projection_helpers_test.go) scopes the assertions to the kinds the demo
// Environment actually ships; this file does NOT fabricate fixture files the
// cluster does not serve.

package e2e

import (
	"testing"
)

// TestProjectionLifecycle is the single top-level umbrella for the per-adapter
// projection + lifecycle e2e matrix. One subtest per canonical adapter id.
func TestProjectionLifecycle(t *testing.T) {
	for _, d := range projectionDescriptors {
		d := d
		t.Run(d.platformID, func(t *testing.T) {
			runProjectionLifecycle(t, d)
		})
	}
}

// runProjectionLifecycle executes the full hydrate → assert → uninstall flow
// for a single adapter descriptor against the kept cluster.
func runProjectionLifecycle(t *testing.T, d projectionDescriptor) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// (1b) Pre-seed a USER key into the adapter's co-owned deep-merge file
	// BEFORE the (single) hydrate. The hydrate's runtime leg then deep-merges
	// ACH's keys ALONGSIDE the user's into the same file — no drift, because
	// the engine records the merged file's hash as its own. Seeding AFTER a
	// hydrate would instead present a "local edit" on the next run
	// (LocalEditPreserve, exit 2) — the wrong path to exercise here. The
	// post-uninstall assertion proves the inverse-merge subtracts only ACH's
	// keys and leaves the user key intact.
	userKey := seedCoOwnedUserKey(t, output, d)

	// (2) Hydrate (default scope: context plugins + runtime co-owned file).
	// Plugin projection runs (NOT --only-runtime).
	stdout, stderr, err := phase7RunAchCli(t, xdg,
		"hydrate",
		"--environment", phase7DemoEnvironment,
		"--platform", d.platformID,
		"--output", output,
	)
	code, runErr := phase7StripExitErr(err)
	if runErr != nil {
		t.Fatalf("%s: hydrate exec error: %v\nstdout=%s\nstderr=%s",
			d.platformID, runErr, stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("%s: hydrate exit %d (want 0)\nstdout=%s\nstderr=%s",
			d.platformID, code, stdout, stderr)
	}

	// (3) Native-dir projection + SC2 no-verbatim-leak.
	assertProjectedNativeDirs(t, output, d)

	// (4) WIRE-03 drop warning (pimono drops `agents`; others drop no
	// projected kind).
	assertDropsWarned(t, d.platformID, stderr, d)

	// (5) state.json v2 records the projected plugin targets.
	statePath := phase7StatePath(output, phase7DemoEnvironment)
	assertStateRecordsPlugins(t, statePath, d)

	// (6) uninstall --include-runtime: tears down context (projected plugin
	// resources) AND runtime (co-owned MCP file inverse-merge).
	stdoutU, stderrU, errU := phase7RunAchCli(t, xdg,
		"uninstall",
		"--environment", phase7DemoEnvironment,
		"--platform", d.platformID,
		"--include-runtime",
		"--output", output,
	)
	codeU, runErrU := phase7StripExitErr(errU)
	if runErrU != nil {
		t.Fatalf("%s: uninstall exec error: %v\nstdout=%s\nstderr=%s",
			d.platformID, runErrU, stdoutU, stderrU)
	}
	if codeU != 0 {
		t.Fatalf("%s: uninstall exit %d (want 0)\nstdout=%s\nstderr=%s",
			d.platformID, codeU, stdoutU, stderrU)
	}

	// (7) After removal: user key survives the inverse-merge; file-owned
	// projected resources are gone.
	assertCoOwnedUserKeyPreserved(t, output, d, userKey)
	assertFileOwnedResourcesGone(t, output, d)
}
