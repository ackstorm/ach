//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Per-adapter byte-identical re-hydrate idempotence + auto-claim ownership e2e
// matrix — Plan 06-03 (VER-03).
//
// The Phase 7 baseline (cli_hydrate_engine_test.go testPhase7BaselineNoOp)
// proves the RUNTIME-config no-op for claude-code, and the hermetic dispatcher
// test (internal/cli/hydrate/projection_rehydrate_test.go) proves the
// single-adapter passthrough PROJECTION byte-no-op. The VER-03 gap this file
// closes is the PROJECTION-path byte-no-op as a per-adapter matrix that
// INCLUDES the format-converting adapters — codex (TOML) and opencode (JSON) —
// whose conversion determinism is exactly what FMT-05 (sorted-key / stable
// encode) guarantees, plus the auto-claim-ownership path on a re-hydrate over
// byte-matching pre-existing owned files (the second hydrate MUST exit 0 — an
// auto-claim, NOT a CollisionRefuse / exit 7).
//
// Per subtest (after phase7SuiteGuard skip-on-cluster-missing preamble):
//
//	1. Acquire pk_ + seed XDG, wait the demo Environment Available, allocate a
//	   fresh phase7Workspace.
//	2. First `hydrate --environment demo --platform <id> --output <ws>`; assert
//	   exit 0. snapshotProjectedFiles → before; sha256(state.json) → stateBefore.
//	3. Second hydrate, identical inputs + SAME workspace; assert exit 0 — the
//	   auto-claim over the byte-matching pre-existing owned files (NOT a drift
//	   refusal). snapshotProjectedFiles → after; sha256(state.json) → stateAfter.
//	4. assertSnapshotsByteIdentical(before, after) — every projected native file
//	   byte-identical, no churn; stateBefore == stateAfter (state.json byte-no-op,
//	   mirroring testPhase7BaselineNoOp).
//
// The codex + opencode subtests are the load-bearing FMT-05 determinism proofs:
// their projected output passes through a TOML / JSON re-encode, so a
// non-deterministic map-iteration-order encode would surface as a run1≠run2
// byte diff right here. The verbatim-passthrough adapters (claude-code,
// gemini-cli, pimono) prove the copy path is equally stable.
//
// Activation (mirrors cli_hydrate_engine_test.go / projection_lifecycle_test.go):
//
//	make e2e-full                         # cluster-up + e2e binaries, cluster KEPT
//	make e2e-focus RUN='TestProjectionIdempotence'
//
// Replay one adapter:  make e2e-focus RUN='TestProjectionIdempotence/codex'
//
// Every subtest re-runs phase7SuiteGuard so a partial skip (cluster/pk gate
// unset) never silently passes. Per-adapter assertions are scoped to the demo
// plugin's actual kinds via the shared 06-02 descriptor table — this file does
// NOT fabricate fixture files the cluster does not serve, and only READS the
// demo Environment + writes to per-test phase7Workspace temp dirs (no
// test/e2e/cluster/ synced fixture is touched — T-06-06).

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/ackstorm/ach/internal/featuregate"
)

// TestProjectionIdempotence is the single top-level umbrella for the per-adapter
// re-hydrate idempotence + auto-claim matrix. One subtest per canonical adapter
// id; order mirrors the 06-02 projectionDescriptors table / TestProjectionLifecycle.
func TestProjectionIdempotence(t *testing.T) {
	// Idempotence is proven over the demo Environment's `caveman` PLUGIN
	// projection (the snapshotProjectedFiles byte-no-op + Plugins[] state). With
	// plugins disabled there is no plugin projection to re-hydrate. The Skill-CR
	// projection path stays covered LIVE by
	// TestPhase7CLIEngine/sc5_skill_projection. Flip featuregate.PluginsEnabled
	// to re-activate.
	if !featuregate.PluginsEnabled {
		t.Skip("plugins disabled via featuregate.PluginsEnabled (caveman plugin projection idempotence); Skill-CR projection stays covered by sc5_skill_projection")
	}
	for _, d := range projectionDescriptors {
		d := d
		t.Run(d.platformID, func(t *testing.T) {
			runProjectionIdempotence(t, d)
		})
	}
}

// runProjectionIdempotence drives the two-hydrate byte-no-op + auto-claim proof
// for a single adapter descriptor against the kept cluster.
//
// FMT-05 note: when d.platformID is codex or opencode this subtest is the
// format-converting determinism proof — the projected output is produced by a
// TOML/JSON re-encode, so step (4)'s byte-identical assertion is what catches a
// non-deterministic map encode. The verbatim-passthrough adapters exercise the
// same gate over a straight copy.
func runProjectionIdempotence(t *testing.T, d projectionDescriptor) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// (2) First hydrate — populates the projected native dirs + state.json.
	// Default scope (NOT --only-runtime) so plugin projection runs.
	stdout1, stderr1, err1 := phase7RunAchCli(t, xdg,
		"env", "hydrate", phase7DemoEnvironment,
		"--target", d.platformID,
		"--output", output,
	)
	code1, runErr1 := phase7StripExitErr(err1)
	if runErr1 != nil {
		t.Fatalf("%s: first hydrate exec error: %v\nstdout=%s\nstderr=%s",
			d.platformID, runErr1, stdout1, stderr1)
	}
	if code1 != 0 {
		t.Fatalf("%s: first hydrate exit %d (want 0)\nstdout=%s\nstderr=%s",
			d.platformID, code1, stdout1, stderr1)
	}

	// Guard against an empty projection silently passing the byte-no-op: the
	// descriptor's native dirs MUST hold projected files after run 1 (a
	// vacuous before==after over two empty maps would otherwise "pass").
	assertProjectedNativeDirs(t, output, d)

	before := snapshotProjectedFiles(t, output, d)
	if len(before) == 0 {
		t.Fatalf("%s: snapshot after first hydrate is empty — no projected files to gate idempotence on",
			d.platformID)
	}

	statePath := phase7StatePath(output, phase7DemoEnvironment, d.platformID)
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("%s: read state.json after first hydrate at %s: %v", d.platformID, statePath, err)
	}
	hashBefore := sha256.Sum256(stateBefore)

	// (3) Second hydrate — identical inputs + SAME workspace. MUST exit 0: the
	// re-hydrate auto-claims the byte-matching pre-existing owned files (the
	// SAFE-04-style Tier-1 eager match / CollisionOwnedByCurrent path), NOT a
	// drift refusal (exit 7). An exit 7 here would mean the engine failed to
	// recognize its own prior output as owned.
	stdout2, stderr2, err2 := phase7RunAchCli(t, xdg,
		"env", "hydrate", phase7DemoEnvironment,
		"--target", d.platformID,
		"--output", output,
	)
	code2, runErr2 := phase7StripExitErr(err2)
	if runErr2 != nil {
		t.Fatalf("%s: second hydrate exec error: %v\nstdout=%s\nstderr=%s",
			d.platformID, runErr2, stdout2, stderr2)
	}
	if code2 != 0 {
		t.Fatalf("%s: second hydrate exit %d (want 0 — auto-claim over byte-matching owned files, "+
			"NOT a collision refusal / exit 7)\nstdout=%s\nstderr=%s",
			d.platformID, code2, stdout2, stderr2)
	}

	after := snapshotProjectedFiles(t, output, d)

	// (4a) Every projected native file byte-identical, no churn. For codex/
	// opencode this is the FMT-05 deterministic-encode proof: a non-deterministic
	// TOML/JSON map encode would diff here.
	assertSnapshotsByteIdentical(t, before, after)

	// (4b) state.json byte-no-op (mirrors testPhase7BaselineNoOp).
	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("%s: read state.json after second hydrate: %v", d.platformID, err)
	}
	hashAfter := sha256.Sum256(stateAfter)
	if hashBefore != hashAfter {
		t.Errorf("%s: second hydrate mutated state.json (want byte-no-op):\nbefore=%s\nafter=%s",
			d.platformID,
			hex.EncodeToString(hashBefore[:]), hex.EncodeToString(hashAfter[:]))
	}
}
