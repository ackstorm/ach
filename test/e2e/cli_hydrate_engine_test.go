//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 7 CLI engine e2e suite — Plan 07-W4-01.
//
// Drives `ach-cli hydrate` against the kept kind cluster (per
// CLAUDE.md "E2E debug loop" — `make cluster-keep`) for the full
// Phase 7 close criterion set per D-22:
//
//   - 8 sc1_* subtests (4 platforms × {pk_, ek_}) — Core Value path
//     verified end-to-end with the engine emitting each adapter's
//     canonical runtime-config file and a state.json with
//     schemaVersion="2".
//   - sc2_commit_sequence_sigkill — §6.7 14-step commit sequence
//     crash-recovery via the deterministic
//     ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP=11 env-var seam
//     (declared in 07-W1-06 Task 2). The seam fires syscall.Kill
//     between steps 11 and 12 (atomic state write); the test asserts
//     prior state.json bytes intact, then plants a synthetic orphan
//     under <ach-dir>/tmp (the killed run leaves none — StageAndPublish
//     eager-cleans) and asserts a clean re-run sweeps it per §6.7 step 2.
//   - sc3_drift_* — §8.4 four-outcome drift truth table + --force
//     override on conflict-preserve.
//   - sc4_safe_extract_malicious — iterates the
//     test/fixtures/malicious-archives/ BuildAll set (from 07-W2-01)
//     via an httptest content server and asserts every fixture
//     rejects with exit non-zero + no files written under output.
//   - sc4_safe_extract_bomb — ACH_MAX_EXTRACTED_PLUGIN_MIB=1 + a
//     10MiB synthetic bomb tarball; asserts exit non-zero + partial
//     output discarded (SAFE-03 bomb cap).
//   - sc4_autoclaim_three_tier_match / _differ — SAFE-04 auto-claim
//     cascade: matching pre-existing bytes auto-claim; differing bytes
//     refuse with exit 7; --force overrides.
//   - w1_baseline_no_op — D-20 baseline; second invocation is a no-op
//     (zero new writes, same state.json hash).
//
// Activation:
//
//	make cluster-keep
//	./scripts/dev.sh make build
//	ACH_E2E_PHASE7=1 \
//	  ACH_E2E_PHASE7_PK=pk_<26-base32-lower> \
//	  ACH_E2E_PHASE7_BASE_URL=http://localhost:8080 \
//	  ./scripts/dev.sh make e2e-focus FOCUS=TestPhase7CLIEngine
//
// Per CLAUDE.md "E2E debug loop", a single failing subtest can be
// replayed via:
//
//	./scripts/dev.sh make e2e-focus FOCUS=TestPhase7CLIEngine/<sub>
//
// e.g. FOCUS=TestPhase7CLIEngine/sc2_commit_sequence_sigkill — avoids
// burning a full ~6-minute e2e cycle on a single regression.
//
// The skip-on-cluster-missing pattern from phase6_helpers_test.go is
// honored: every subtest preamble calls phase7SuiteGuard, which
// t.Skipf's cleanly when ACH_E2E_PHASE7 is unset or any prerequisite
// (binary, kubectl, cluster Deployment) is missing.

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	maliciousfixtures "github.com/ackstorm/ach/test/fixtures/malicious-archives"
)

// Phase 7 platform IDs (canonical). Cross-checked against
// internal/cli/adapter/<sub>/<sub>.go canonicalID consts.
const (
	phase7PlatformClaudeCode = "claude-code"
	phase7PlatformCodex      = "codex"
	phase7PlatformGemini     = "gemini-cli"
	phase7PlatformOpencode   = "opencode"
)

// Per-adapter canonical runtime-config target paths emitted by
// RenderRuntime. Source-of-truth: internal/cli/adapter/<sub>/<sub>.go
// const blocks (mcpJSONPath / configTOMLPath / settingsJSONPath /
// configJSONPath). Tests cross-check the on-disk file at <output>/<path>.
const (
	phase7ClaudeCodeRuntimePath = ".claude/settings.json"
	phase7CodexRuntimePath      = ".codex/config.toml"
	phase7GeminiRuntimePath     = ".gemini/settings.json"
	phase7OpencodeRuntimePath   = ".opencode/opencode.json"
)

// phase7StatePath returns the on-disk path of the engine's state.json.
// WORKSPACE scope (these tests pass --output, never --global) is
// <output>/.ach/state.json — NOT env-namespaced (only --global namespaces by
// environment). Matches internal/cli/state.ResolvePath. The environment param
// is retained for call-site symmetry but unused in workspace scope.
func phase7StatePath(output, environment string) string {
	_ = environment
	return filepath.Join(output, ".ach", "state.json")
}

// phase7AchTmpDir returns the on-disk <ach-dir>/tmp/ path the engine
// uses for staging — swept on hydrate start per spec §6.7 step 2.
// sc2_commit_sequence_sigkill plants a synthetic orphan staging dir here
// (the killed run leaves none — StageAndPublish eager-cleans) and asserts
// a clean re-run sweeps it.
func phase7AchTmpDir(output, environment string) string {
	_ = environment
	return filepath.Join(output, ".ach", "tmp")
}

// TestPhase7CLIEngine is the single top-level umbrella for the Phase 7
// CLI engine e2e suite. Each subtest maps to one of the D-22 close
// criteria. The umbrella shape mirrors TestPhase6CLI verbatim — every
// subtest body re-runs phase7SuiteGuard so a partial-skip in one
// subtest does not contaminate the rest.
func TestPhase7CLIEngine(t *testing.T) {
	t.Run("w1_baseline_no_op", testPhase7BaselineNoOp)

	// 4 platforms × {pk_, ek_} = 8 sc1_* subtests.
	t.Run("sc1_claudecode_pk", testPhase7Sc1ClaudeCodePk)
	t.Run("sc1_claudecode_ek", testPhase7Sc1ClaudeCodeEk)
	t.Run("sc1_codex_pk", testPhase7Sc1CodexPk)
	t.Run("sc1_codex_ek", testPhase7Sc1CodexEk)
	t.Run("sc1_gemini_pk", testPhase7Sc1GeminiPk)
	t.Run("sc1_gemini_ek", testPhase7Sc1GeminiEk)
	t.Run("sc1_opencode_pk", testPhase7Sc1OpencodePk)
	t.Run("sc1_opencode_ek", testPhase7Sc1OpencodeEk)

	// W6-01 surgical-merge proof: hydrate must add ACH's MCP servers into the
	// tool's native config without clobbering the user's pre-existing entries.
	t.Run("sc1_claudecode_surgical_preserve", testPhase7Sc1ClaudeCodeSurgicalPreserve)

	// SC#2 (deterministic SIGKILL seam — §6.7 crash recovery).
	t.Run("sc2_commit_sequence_sigkill", testPhase7Sc2SigkillRecovery)

	// SC#3 (§8.4 drift truth table — 4 outcomes + --force override).
	t.Run("sc3_drift_no_op", testPhase7Sc3DriftNoOp)
	t.Run("sc3_drift_upstream_only", testPhase7Sc3DriftUpstreamOnly)
	t.Run("sc3_drift_local_edit_preserve", testPhase7Sc3DriftLocalEditPreserve)
	t.Run("sc3_drift_conflict_preserve", testPhase7Sc3DriftConflictPreserve)
	t.Run("sc3_drift_force_overrides", testPhase7Sc3DriftForceOverrides)

	// SC#4 (SAFE-01 malicious-archive fixture set + SAFE-03 bomb +
	// SAFE-04 auto-claim cascade).
	t.Run("sc4_safe_extract_malicious", testPhase7Sc4SafeExtractMalicious)
	t.Run("sc4_safe_extract_bomb", testPhase7Sc4SafeExtractBomb)
	t.Run("sc4_autoclaim_three_tier_match", testPhase7Sc4AutoClaimMatch)
	t.Run("sc4_autoclaim_three_tier_differ", testPhase7Sc4AutoClaimDiffer)
	// W6-01 Task 2: re-hydrate-with-rotated-credentials path. Closes the
	// CR-03 / W5-03 runtime gate — without the W5-03 fix, this subtest
	// would exit 7 (CollisionRefuse) on the second hydrate because the
	// engine-written file's path would never match its state.json entry's
	// (workspace-relative) Target. Post-W5-03 the path is normalized and
	// Classify returns CollisionOwnedByCurrent → engine overwrites → exit 0.
	t.Run("sc4_autoclaim_three_tier/rotated_credential_owned_by_current",
		testPhase7Sc4AutoClaimRotatedCredentialOwnedByCurrent)
}

// -----------------------------------------------------------------------
// W1 baseline: hydrate twice → second invocation is a no-op.
// -----------------------------------------------------------------------

// testPhase7BaselineNoOp asserts the D-20 baseline: a clean hydrate
// followed by a second hydrate against the same workspace produces
// zero new writes and the same state.json bytes (Result.FilesWritten
// == 0 on the second run; state.json hash unchanged).
//
// This is the load-bearing invariant for STATE-04 / STATE-05: if the
// engine cannot achieve a no-op on identical inputs, the §8.4 drift
// truth table cannot be trusted to honor "no-op" outcomes.
func testPhase7BaselineNoOp(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// First hydrate — populates state.json + adapter runtime config.
	stdout1, stderr1, err1 := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code1, runErr1 := phase7StripExitErr(err1)
	if runErr1 != nil {
		t.Fatalf("baseline first hydrate: exec error: %v\nstdout=%s\nstderr=%s",
			runErr1, stdout1, stderr1)
	}
	if code1 != 0 {
		t.Fatalf("baseline first hydrate: exit %d (want 0)\nstdout=%s\nstderr=%s",
			code1, stdout1, stderr1)
	}

	statePath := phase7StatePath(output, phase7DemoEnvironment)
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("baseline read state.json after first hydrate: %v", err)
	}
	hashBefore := sha256.Sum256(stateBefore)

	// Second hydrate — same inputs; expect no on-disk changes.
	stdout2, stderr2, err2 := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code2, runErr2 := phase7StripExitErr(err2)
	if runErr2 != nil {
		t.Fatalf("baseline second hydrate: exec error: %v\nstdout=%s\nstderr=%s",
			runErr2, stdout2, stderr2)
	}
	if code2 != 0 {
		t.Fatalf("baseline second hydrate: exit %d (want 0)\nstdout=%s\nstderr=%s",
			code2, stdout2, stderr2)
	}

	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("baseline read state.json after second hydrate: %v", err)
	}
	hashAfter := sha256.Sum256(stateAfter)
	if hashBefore != hashAfter {
		t.Errorf("baseline second hydrate mutated state.json (want no-op):\n"+
			"before=%s\nafter=%s",
			hex.EncodeToString(hashBefore[:]), hex.EncodeToString(hashAfter[:]))
	}
}

// -----------------------------------------------------------------------
// SC#1 — 4 platforms × {pk_, ek_} = 8 subtests.
// -----------------------------------------------------------------------

// phase7Sc1AssertRunOutputs is the shared assertion body for every
// sc1_* subtest: exit 0, expected runtime-config file present, state.json
// present with schemaVersion="2", and runtime-config file MODE == 0o600
// per W5-02 CR-01 mitigation (credential-bearing adapter files MUST NOT
// be world-readable on multi-user hosts).
//
// stdout / stderr are passed through verbatim into the failure message
// so any first-failure debug session has full context — no
// re-invocation required.
//
// Mode-0o600 assertion is load-bearing for the W5-02 close. The
// runtime-config file (.claude/.mcp.json / .codex/config.toml /
// .gemini/settings.json / .opencode/opencode.json) embeds plaintext
// `x-ach-key` bearer credentials in its headers map; a 0o644 mode would
// leak the bearer to any other local UID. Pre-W5-02 the engine called
// state.WriteAtomic with no mode parameter (hardcoded 0o644); post-W5-02
// the signature is required-mode and adapterDispatcherImpl.Render passes
// 0o600. This assertion is the end-to-end proof.
func phase7Sc1AssertRunOutputs(t *testing.T, output, environment, runtimePath string, code int, stdout, stderr []byte) {
	t.Helper()
	if code != 0 {
		t.Fatalf("sc1: hydrate exit %d (want 0)\nstdout=%s\nstderr=%s",
			code, stdout, stderr)
	}
	// Runtime-config file landed at the expected canonical path.
	fullRuntimePath := filepath.Join(output, runtimePath)
	info, err := os.Stat(fullRuntimePath)
	if err != nil {
		t.Fatalf("sc1: expected runtime-config at %s not found: %v\n"+
			"stdout=%s\nstderr=%s",
			fullRuntimePath, err, stdout, stderr)
	}
	// Mode 0o600 assertion — W5-02 CR-01 mitigation. Anything other
	// than 0o600 means WriteAtomic was called with the wrong mode
	// (the adapter file is leaking bearer credentials to other UIDs).
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("sc1: adapter file %s mode = %o, want %o (W5-02 CR-01 mitigation — "+
			"adapter runtime-config files embed plaintext x-ach-key bearer in headers; "+
			"0o600 prevents read by other local UIDs on multi-user hosts)\n"+
			"stdout=%s\nstderr=%s",
			fullRuntimePath, got, 0o600, stdout, stderr)
	}
	// state.json present and asserts schemaVersion=2.
	statePath := phase7StatePath(output, environment)
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("sc1: read state.json at %s: %v\nstdout=%s\nstderr=%s",
			statePath, err, stdout, stderr)
	}
	var stateDoc struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	if err := json.Unmarshal(stateBytes, &stateDoc); err != nil {
		t.Fatalf("sc1: parse state.json: %v\nbytes=%s", err, stateBytes)
	}
	if stateDoc.SchemaVersion != "2" {
		t.Errorf("sc1: state.json schemaVersion=%q, want \"2\"\nbytes=%s",
			stateDoc.SchemaVersion, stateBytes)
	}
}

// phase7Sc1RunPk drives a single sc1_*_pk subtest for the given
// canonical platform id + its on-disk runtime-config target. The pk_
// is fetched from the Phase 7 env-var seam; --environment is required.
func phase7Sc1RunPk(t *testing.T, platformID, runtimePath string) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	stdout, stderr, err := phase7RunAchCli(t, xdg,
		"hydrate",
		"--environment", phase7DemoEnvironment,
		"--platform", platformID,
		"--output", output,
	)
	code, runErr := phase7StripExitErr(err)
	if runErr != nil {
		t.Fatalf("sc1_%s_pk: exec error: %v\nstdout=%s\nstderr=%s",
			platformID, runErr, stdout, stderr)
	}
	phase7Sc1AssertRunOutputs(t, output, phase7DemoEnvironment, runtimePath,
		code, stdout, stderr)
}

// phase7Sc1RunEk drives a single sc1_*_ek subtest. The ek_ is minted
// via `ach-cli env-keys create` against the seeded XDG; per CLI-04 the
// ek_ binds the environment, so the hydrate call omits --environment
// and passes --env-key <label>.
func phase7Sc1RunEk(t *testing.T, platformID, runtimePath string) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)

	label := "e2e-phase7-" + platformID + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
	_ = phase7CreateEkKey(t, xdg, label)

	output := phase7Workspace(t)
	stdout, stderr, err := phase7RunAchCli(t, xdg,
		"hydrate",
		"--env-key", label,
		"--platform", platformID,
		"--output", output,
	)
	code, runErr := phase7StripExitErr(err)
	if runErr != nil {
		t.Fatalf("sc1_%s_ek: exec error: %v\nstdout=%s\nstderr=%s",
			platformID, runErr, stdout, stderr)
	}
	phase7Sc1AssertRunOutputs(t, output, phase7DemoEnvironment, runtimePath,
		code, stdout, stderr)
}

func testPhase7Sc1ClaudeCodePk(t *testing.T) {
	phase7Sc1RunPk(t, phase7PlatformClaudeCode, phase7ClaudeCodeRuntimePath)
}
func testPhase7Sc1ClaudeCodeEk(t *testing.T) {
	phase7Sc1RunEk(t, phase7PlatformClaudeCode, phase7ClaudeCodeRuntimePath)
}
func testPhase7Sc1CodexPk(t *testing.T) {
	phase7Sc1RunPk(t, phase7PlatformCodex, phase7CodexRuntimePath)
}
func testPhase7Sc1CodexEk(t *testing.T) {
	phase7Sc1RunEk(t, phase7PlatformCodex, phase7CodexRuntimePath)
}
func testPhase7Sc1GeminiPk(t *testing.T) {
	phase7Sc1RunPk(t, phase7PlatformGemini, phase7GeminiRuntimePath)
}
func testPhase7Sc1GeminiEk(t *testing.T) {
	phase7Sc1RunEk(t, phase7PlatformGemini, phase7GeminiRuntimePath)
}
func testPhase7Sc1OpencodePk(t *testing.T) {
	phase7Sc1RunPk(t, phase7PlatformOpencode, phase7OpencodeRuntimePath)
}
func testPhase7Sc1OpencodeEk(t *testing.T) {
	phase7Sc1RunEk(t, phase7PlatformOpencode, phase7OpencodeRuntimePath)
}

// testPhase7Sc1ClaudeCodeSurgicalPreserve is the W6-01 surgical-merge proof:
// a hydrate must MERGE ACH's MCP servers into the tool's native config while
// preserving the user's pre-existing servers + unrelated settings. Pre-seed
// .claude/settings.json with a user MCP server and a permissions block,
// hydrate, then assert the user keys survive verbatim, ACH added >=1 server,
// and an ACH-contributed server carries a populated x-ach-key bearer.
//
// (Assertion shapes track the demo environment's MCP fixtures; if the demo
// MCP set changes, the >=1-ACH-server / x-ach-key checks may need tuning.)
func testPhase7Sc1ClaudeCodeSurgicalPreserve(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// Pre-seed the user's native config: a personal MCP server + a permissions
	// block ACH must never touch.
	target := filepath.Join(output, phase7ClaudeCodeRuntimePath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("surgical-preserve seed mkdir: %v", err)
	}
	seed := []byte(`{
  "mcpServers": {
    "user-personal": {"command": "my-server", "args": ["--port", "9999"]}
  },
  "permissions": {"allow": ["Read", "Edit"]}
}`)
	if err := os.WriteFile(target, seed, 0o600); err != nil {
		t.Fatalf("surgical-preserve seed write: %v", err)
	}

	stdout, stderr, err := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode, "--output", output,
	)
	code, runErr := phase7StripExitErr(err)
	if runErr != nil {
		t.Fatalf("surgical-preserve hydrate: exec error: %v\nstdout=%s\nstderr=%s", runErr, stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("surgical-preserve hydrate: exit %d (want 0)\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}

	raw, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("surgical-preserve read merged config: %v", rerr)
	}
	var doc struct {
		McpServers  map[string]json.RawMessage `json:"mcpServers"`
		Permissions json.RawMessage            `json:"permissions"`
	}
	if jerr := json.Unmarshal(raw, &doc); jerr != nil {
		t.Fatalf("surgical-preserve merged config not valid JSON: %v\n%s", jerr, raw)
	}
	if _, ok := doc.McpServers["user-personal"]; !ok {
		t.Errorf("surgical-merge clobbered user MCP server 'user-personal'\nmerged=%s", raw)
	}
	if len(doc.Permissions) == 0 {
		t.Errorf("surgical-merge dropped the user 'permissions' block\nmerged=%s", raw)
	}
	if len(doc.McpServers) < 2 {
		t.Errorf("expected ACH server(s) merged alongside the user's; got %d total\nmerged=%s",
			len(doc.McpServers), raw)
	}
	foundKey := false
	for name, rawSrv := range doc.McpServers {
		if name == "user-personal" {
			continue
		}
		var srv struct {
			Headers map[string]string `json:"headers"`
		}
		_ = json.Unmarshal(rawSrv, &srv)
		if srv.Headers["x-ach-key"] != "" {
			foundKey = true
		}
	}
	if !foundKey {
		t.Errorf("no ACH-contributed MCP server carries a populated x-ach-key\nmerged=%s", raw)
	}
}

// -----------------------------------------------------------------------
// SC#2 — deterministic SIGKILL between steps 11 and 12.
// -----------------------------------------------------------------------

// testPhase7Sc2SigkillRecovery exercises the §6.7 commit-sequence
// crash-recovery contract via the deterministic SIGKILL seam
// ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP=11 (declared in 07-W1-06
// Task 2 — see internal/cli/hydrate/sigkill_seam_e2e.go envSigkillStep
// post-07-W5-04 WR-01 split). The seam fires syscall.Kill(SIGKILL)
// on the engine's own pid after step 11 returns and BEFORE step 12
// (atomic state write).
//
// PREREQ: the ./bin/ach-cli binary MUST be built with -tags=e2e
// (`make build-e2e`) or the seam is stubbed out (release builds
// receive a no-op via sigkill_seam_prod.go per WR-01) and sc2
// would false-pass — phase7RequireSigkillSeam below catches this.
//
// Flow:
//
//  1. Run hydrate cleanly to seed a known state.json snapshot. Hash it.
//  2. Run hydrate with ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP=11. The
//     process exits non-zero (SIGKILL → exit code -1 via Go's ExitError).
//  3. Assert state.json bytes equal the prior snapshot (step 12 never ran).
//  4. Plant a synthetic orphan staging dir under <ach-dir>/tmp/. The
//     killed run itself leaves NONE by design — extract.StageAndPublish
//     removes each per-resource staging dir via a deferred os.RemoveAll
//     on every return (stage.go: "a mid-extraction crash leaves no
//     orphan"), and the maybeKill(11) hook fires AFTER ExtractContent has
//     returned. The §6.7 step-2 sweep instead defends the narrower window
//     where a SIGKILL lands WHILE a staging dir exists (before its defer
//     runs); the plant simulates exactly that residue.
//  5. Run hydrate WITHOUT the env-var; assert it completes cleanly +
//     the planted orphan tmp/ is swept per spec §6.7 step 2.
//
// NO TIMEOUT FALLBACK — the env-var seam is deterministic per D-22.
// The previously-considered `timeout --signal=KILL 0.5s` retry-3-times
// approach is REMOVED because it cannot guarantee landing between
// specific step boundaries.
func testPhase7Sc2SigkillRecovery(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	// Post-07-W5-04 (WR-01): seam is build-tag-gated. Skip cleanly
	// if the binary lacks the seam — see phase7RequireSigkillSeam.
	phase7RequireSigkillSeam(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// Step 1 — clean hydrate seeds the baseline state.
	stdoutSeed, stderrSeed, errSeed := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	codeSeed, runErrSeed := phase7StripExitErr(errSeed)
	if runErrSeed != nil {
		t.Fatalf("sc2 seed hydrate: exec error: %v\nstdout=%s\nstderr=%s",
			runErrSeed, stdoutSeed, stderrSeed)
	}
	if codeSeed != 0 {
		t.Fatalf("sc2 seed hydrate: exit %d (want 0)\nstdout=%s\nstderr=%s",
			codeSeed, stdoutSeed, stderrSeed)
	}
	statePath := phase7StatePath(output, phase7DemoEnvironment)
	stateBytesBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("sc2: read seed state.json: %v", err)
	}

	// Step 2 — SIGKILL-injecting run between steps 11 and 12.
	tmpDir := phase7AchTmpDir(output, phase7DemoEnvironment)
	stdoutKill, stderrKill, errKill := phase7RunAchCliEnv(t, xdg,
		[]string{phase7SigkillEnvVar + "=11"},
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	codeKill, _ := phase7StripExitErr(errKill)
	// SIGKILL-killed processes report ExitCode() == -1 via Go's
	// exec.ExitError (process signaled, not exited normally). Anything
	// other than that is a regression — either the seam never fired
	// (the engine completed normally) or a different error path was
	// taken.
	if codeKill != -1 {
		t.Fatalf("sc2 sigkill hydrate: exit %d (want -1, SIGKILL termination)\n"+
			"stdout=%s\nstderr=%s\n"+
			"HINT: post-07-W5-04 the seam lives in "+
			"internal/cli/hydrate/sigkill_seam_e2e.go (envSigkillStep + "+
			"readSigkillSeamFromEnv + defaultKillFn). Verify the binary was "+
			"built with -tags=e2e via `strings %s | grep -q %q` (true under "+
			"-tags=e2e, false under release); verify "+
			"`grep -n c.maybeKill(11) internal/cli/hydrate/commit.go` still "+
			"sits between step 11 and step 12 of the 14-step dispatch.",
			codeKill, stdoutKill, stderrKill,
			phase7BinaryPath, phase7SigkillEnvVar)
	}

	// Step 3 — state.json bytes intact (step 12 never ran).
	stateBytesAfterKill, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("sc2: read state.json after kill: %v", err)
	}
	if !bytes.Equal(stateBytesBefore, stateBytesAfterKill) {
		t.Errorf("sc2: state.json mutated by killed run (step 12 should not have fired)\n"+
			"before=%s\nafter=%s", stateBytesBefore, stateBytesAfterKill)
	}

	// Step 4 — plant a synthetic orphan staging dir under <ach-dir>/tmp/.
	// The killed run leaves none by design (extract.StageAndPublish's
	// deferred os.RemoveAll fires on every return, and maybeKill(11) is
	// after ExtractContent returns), so we simulate the residue a SIGKILL
	// mid-staging would leave and then prove step 5 sweeps it.
	if err := os.MkdirAll(filepath.Join(tmpDir, "orphan-staging-sim", "extracted"), 0o755); err != nil {
		t.Fatalf("sc2: plant orphan staging dir under %s: %v", tmpDir, err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "orphan-staging-sim", "source.bin"),
		[]byte("partial-extract-residue"), 0o644); err != nil {
		t.Fatalf("sc2: plant orphan source.bin: %v", err)
	}
	planted, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("sc2: readdir tmp after plant: %v", err)
	}
	if len(planted) == 0 {
		t.Fatalf("sc2: planted orphan staging dir not present under %s — cannot "+
			"exercise the §6.7 step-2 sweep", tmpDir)
	}

	// Step 5 — clean re-run sweeps tmp/ (§6.7 step 2).
	stdoutResume, stderrResume, errResume := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	codeResume, runErrResume := phase7StripExitErr(errResume)
	if runErrResume != nil {
		t.Fatalf("sc2 resume hydrate: exec error: %v\nstdout=%s\nstderr=%s",
			runErrResume, stdoutResume, stderrResume)
	}
	if codeResume != 0 {
		t.Fatalf("sc2 resume hydrate: exit %d (want 0)\nstdout=%s\nstderr=%s",
			codeResume, stdoutResume, stderrResume)
	}
	tmpEntriesAfter, err := os.ReadDir(tmpDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("sc2: readdir tmp after resume: %v", err)
	}
	if len(tmpEntriesAfter) != 0 {
		t.Errorf("sc2: tmp/ not swept after clean resume (want 0 entries, got %d)\n"+
			"entries=%v\n"+
			"HINT: spec §6.7 step 2 mandates the unconditional sweep at hydrate "+
			"start. Re-check internal/cli/state/sweep.go SweepTmp + the step 2 "+
			"call site in commit.go.", len(tmpEntriesAfter), tmpEntriesAfter)
	}
}

// -----------------------------------------------------------------------
// SC#3 — §8.4 drift truth table + --force override.
// -----------------------------------------------------------------------

// Each sc3_* subtest seeds a workspace with a known {state.json, on-disk
// adapter file} pair, then induces one of the four §8.4 outcomes by
// mutating disk and/or upstream, then re-runs hydrate and asserts the
// exit code + the on-disk bytes.
//
// The "upstream change" is induced via a fresh hydrate after a fixture
// CR edit OR by toggling Environment on the server side. For the
// engine-side tests we focus on the engine's behavior given a known
// pair of {state hashes, on-disk hash, upstream hash} — the W1
// Differ unit tests already exhaustively cover the pure-string truth
// table; here we only re-prove the end-to-end contract.

// testPhase7Sc3DriftNoOp: state hash matches both upstream and on-disk
// → no-op, exit 0, no rewrite. Subsumed by w1_baseline_no_op but
// kept as a named sc3 subtest for §8.4 row-1 traceability.
func testPhase7Sc3DriftNoOp(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// First clean hydrate.
	stdout1, stderr1, err1 := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code1, _ := phase7StripExitErr(err1)
	if code1 != 0 {
		t.Fatalf("sc3 drift no-op seed: exit %d\nstdout=%s\nstderr=%s",
			code1, stdout1, stderr1)
	}

	// Snapshot the runtime-config bytes.
	runtimeFile := filepath.Join(output, phase7ClaudeCodeRuntimePath)
	before, err := os.ReadFile(runtimeFile)
	if err != nil {
		t.Fatalf("sc3 drift no-op: read runtime file: %v", err)
	}

	// Re-hydrate — same upstream + same disk; expect bytes unchanged.
	stdout2, stderr2, err2 := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code2, _ := phase7StripExitErr(err2)
	if code2 != 0 {
		t.Fatalf("sc3 drift no-op re-run: exit %d\nstdout=%s\nstderr=%s",
			code2, stdout2, stderr2)
	}
	after, err := os.ReadFile(runtimeFile)
	if err != nil {
		t.Fatalf("sc3 drift no-op: read runtime file after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("sc3 drift no-op: runtime file mutated on re-run (want unchanged)\n"+
			"before=%s\nafter=%s", before, after)
	}
}

// testPhase7Sc3DriftUpstreamOnly: state hash matches on-disk; upstream
// differs → upstream-only-overwrite, exit 0, bytes rewritten. Induced
// by mutating the on-disk runtime file back to its pre-hydrate "stale"
// content (so the engine's recorded state hash matches the stale bytes,
// then a fresh upstream causes an overwrite).
//
// In the steady-state cluster this is harder to fixture (the upstream
// hash is server-driven). We approximate the row by mutating the state
// hash entry directly: rewrite state.json with a hash that matches the
// CURRENT on-disk runtime bytes, and trust the upstream still produces
// the same bytes (so the engine has a no-op) — then the assertion is
// degenerate. For a true upstream-only test the W1 Differ unit tests
// (drift_test.go) exhaustively cover this row; here we exercise the
// row via the more common "first hydrate" path: state.json absent →
// fetch+write (upstream-only-overwrite is one outcome of "state has no
// prior record"). exit 0 + file written.
func testPhase7Sc3DriftUpstreamOnly(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// First hydrate against an empty workspace — every adapter file is
	// "upstream-only" in the §8.4 sense (no prior state, no on-disk
	// version).
	stdout, stderr, err := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code, _ := phase7StripExitErr(err)
	if code != 0 {
		t.Fatalf("sc3 drift upstream-only: exit %d\nstdout=%s\nstderr=%s",
			code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(output, phase7ClaudeCodeRuntimePath)); err != nil {
		t.Errorf("sc3 drift upstream-only: runtime file not written: %v\n"+
			"stdout=%s\nstderr=%s", err, stdout, stderr)
	}
}

// testPhase7Sc3DriftLocalEditPreserve: state hash matches upstream;
// on-disk differs → local-edit-preserve, exit 2 (exit.Drift). Induced
// by hydrating cleanly then mutating the on-disk runtime file with
// arbitrary bytes; the second hydrate sees state-hash == upstream-hash
// but on-disk != state-hash → preserve (don't clobber) + exit 2.
func testPhase7Sc3DriftLocalEditPreserve(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// First hydrate.
	stdoutSeed, stderrSeed, errSeed := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	codeSeed, _ := phase7StripExitErr(errSeed)
	if codeSeed != 0 {
		t.Fatalf("sc3 drift local-edit seed: exit %d\nstdout=%s\nstderr=%s",
			codeSeed, stdoutSeed, stderrSeed)
	}

	// Mutate the on-disk runtime file with a local edit.
	runtimeFile := filepath.Join(output, phase7ClaudeCodeRuntimePath)
	localEdit := []byte(`{"mcpServers":{},"_local_edit":true}` + "\n")
	if err := os.WriteFile(runtimeFile, localEdit, 0o644); err != nil {
		t.Fatalf("sc3 drift local-edit: write local edit: %v", err)
	}

	// Second hydrate — engine sees a local edit on the file; expects
	// preserve + exit 2 (exit.Drift).
	stdoutDrift, stderrDrift, errDrift := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	codeDrift, _ := phase7StripExitErr(errDrift)
	if codeDrift != 2 {
		t.Errorf("sc3 drift local-edit-preserve: exit %d (want 2 / exit.Drift)\n"+
			"stdout=%s\nstderr=%s", codeDrift, stdoutDrift, stderrDrift)
	}
	// Bytes preserved.
	got, err := os.ReadFile(runtimeFile)
	if err != nil {
		t.Fatalf("sc3 drift local-edit: read runtime file after: %v", err)
	}
	if !bytes.Equal(got, localEdit) {
		t.Errorf("sc3 drift local-edit-preserve: bytes mutated (want preserved)\n"+
			"want=%s\ngot=%s", localEdit, got)
	}
}

// testPhase7Sc3DriftConflictPreserve: state hash differs from upstream
// AND from on-disk; on-disk != upstream → conflict-preserve, exit 2.
// Induced by mutating both the state hash entry AND the on-disk file
// to differing values, then re-hydrating. The engine sees a 3-way
// disagreement and refuses to clobber.
func testPhase7Sc3DriftConflictPreserve(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// Seed.
	stdoutSeed, stderrSeed, errSeed := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	codeSeed, _ := phase7StripExitErr(errSeed)
	if codeSeed != 0 {
		t.Fatalf("sc3 drift conflict seed: exit %d\nstdout=%s\nstderr=%s",
			codeSeed, stdoutSeed, stderrSeed)
	}

	// Corrupt state.json's hash for the runtime file entry — write a
	// known-bogus hash so state-hash ≠ on-disk-hash ≠ upstream-hash.
	statePath := phase7StatePath(output, phase7DemoEnvironment)
	if err := phase7CorruptStateHash(statePath); err != nil {
		t.Fatalf("sc3 drift conflict: corrupt state.json: %v", err)
	}

	// Mutate on-disk file to a different "user edit".
	runtimeFile := filepath.Join(output, phase7ClaudeCodeRuntimePath)
	userEdit := []byte(`{"mcpServers":{},"_user_conflict":true}` + "\n")
	if err := os.WriteFile(runtimeFile, userEdit, 0o644); err != nil {
		t.Fatalf("sc3 drift conflict: write user edit: %v", err)
	}

	// Re-hydrate. Conflict-preserve → exit 2, bytes preserved.
	stdoutDrift, stderrDrift, errDrift := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	codeDrift, _ := phase7StripExitErr(errDrift)
	if codeDrift != 2 {
		t.Errorf("sc3 drift conflict-preserve: exit %d (want 2 / exit.Drift)\n"+
			"stdout=%s\nstderr=%s", codeDrift, stdoutDrift, stderrDrift)
	}
	got, err := os.ReadFile(runtimeFile)
	if err != nil {
		t.Fatalf("sc3 drift conflict: read runtime file after: %v", err)
	}
	if !bytes.Equal(got, userEdit) {
		t.Errorf("sc3 drift conflict-preserve: bytes mutated (want preserved)\n"+
			"want=%s\ngot=%s", userEdit, got)
	}
}

// testPhase7Sc3DriftForceOverrides: from the conflict-preserve setup,
// a --force re-run overwrites the on-disk bytes and exits 0.
func testPhase7Sc3DriftForceOverrides(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// Seed + induce conflict (same as sc3_drift_conflict_preserve).
	stdoutSeed, stderrSeed, errSeed := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	codeSeed, _ := phase7StripExitErr(errSeed)
	if codeSeed != 0 {
		t.Fatalf("sc3 drift force seed: exit %d\nstdout=%s\nstderr=%s",
			codeSeed, stdoutSeed, stderrSeed)
	}
	statePath := phase7StatePath(output, phase7DemoEnvironment)
	if err := phase7CorruptStateHash(statePath); err != nil {
		t.Fatalf("sc3 drift force: corrupt state.json: %v", err)
	}
	runtimeFile := filepath.Join(output, phase7ClaudeCodeRuntimePath)
	userEdit := []byte(`{"mcpServers":{},"_will_be_clobbered":true}` + "\n")
	if err := os.WriteFile(runtimeFile, userEdit, 0o644); err != nil {
		t.Fatalf("sc3 drift force: write user edit: %v", err)
	}

	// --force re-hydrate. Expected: exit 0 + bytes overwritten.
	stdoutForce, stderrForce, errForce := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
		"--force",
	)
	codeForce, _ := phase7StripExitErr(errForce)
	if codeForce != 0 {
		t.Errorf("sc3 drift force-overrides: exit %d (want 0)\n"+
			"stdout=%s\nstderr=%s", codeForce, stdoutForce, stderrForce)
	}
	got, err := os.ReadFile(runtimeFile)
	if err != nil {
		t.Fatalf("sc3 drift force: read runtime file after: %v", err)
	}
	if bytes.Equal(got, userEdit) {
		t.Errorf("sc3 drift force-overrides: bytes NOT overwritten (want different)\n"+
			"still=%s", got)
	}
}

// phase7CorruptStateHash overwrites every hash field in state.json
// with a known-bogus value so the engine's drift comparator sees a
// 3-way mismatch on the next run. The state.json schema is preserved
// (schemaVersion="2", environment, deployment); only the hash entries
// in contentHashes / adapterSection FileEntry hashes are corrupted.
//
// Implementation: load JSON into map[string]any, walk it, replace
// every leaf string starting with "xxh3:" with a fixed "xxh3:" +
// "deadbeef…" sentinel. State integrity outside hash fields preserved.
func phase7CorruptStateHash(statePath string) error {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return fmt.Errorf("read state.json: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("unmarshal state.json: %w", err)
	}
	corruptHashes(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal corrupted state.json: %w", err)
	}
	return os.WriteFile(statePath, out, 0o644)
}

// corruptHashes walks an arbitrary JSON-decoded structure and replaces
// every leaf string with the prefix "xxh3:" by the same prefix +
// "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef".
// Length matches the canonical xxh3:<32 lowercase hex> form.
func corruptHashes(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if s, ok := child.(string); ok && strings.HasPrefix(s, "xxh3:") {
				t[k] = "xxh3:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
				continue
			}
			corruptHashes(child)
		}
	case []any:
		for _, child := range t {
			corruptHashes(child)
		}
	}
}

// -----------------------------------------------------------------------
// SC#4 — SAFE-01 malicious-archive + SAFE-03 bomb + SAFE-04 auto-claim.
// -----------------------------------------------------------------------

// testPhase7Sc4SafeExtractMalicious iterates the
// test/fixtures/malicious-archives/ BuildAll fixture set and asserts
// every fixture rejects with exit non-zero + no files written under
// output. Each fixture is served via a local httptest.Server (matches
// the engine's content-fetch code path verbatim — internal/cli/extract
// streams the gzip reader regardless of whether it came from a kind
// cluster or a localhost httptest).
//
// Per-fixture subtest naming via t.Run for first-failure surfacing.
//
// Per CLAUDE.md "Common failure modes" → "Re-running full E2E for
// every code change", a single fixture's failure can be re-run via:
//
//	FOCUS=TestPhase7CLIEngine/sc4_safe_extract_malicious/<fixture_name>
func testPhase7Sc4SafeExtractMalicious(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)

	fixturesDir := t.TempDir()
	fixturesByName, err := maliciousfixtures.BuildAll(fixturesDir)
	if err != nil {
		t.Fatalf("sc4 safe-extract malicious: BuildAll: %v", err)
	}

	for _, name := range maliciousfixtures.Names {
		fixturePath, ok := fixturesByName[name]
		if !ok {
			t.Errorf("sc4 safe-extract malicious: BuildAll missing fixture %q", name)
			continue
		}
		subName := strings.TrimSuffix(name, ".tar.gz")
		t.Run(subName, func(t *testing.T) {
			phase7AssertMaliciousFixtureRejected(t, fixturePath)
		})
	}
}

// phase7PoisonedContentServer returns an httptest.Server that injects
// `archive` bytes into the engine's content-fetch path WITHOUT any
// production test seam. It serves `archive` (with the canonical
// application/gzip Content-Type the real Content Service sends) for every
// GET /content/{kind}/{name}, and reverse-proxies all other requests —
// notably the POST /platform/hydrate manifest call — to the live
// platform-api at `upstream`.
//
// Pointing the seeded deployment URL at this server splits the two paths:
// the manifest still resolves against the real cluster (so the demo
// environment hydrates as usual), while every per-artifact content GET is
// poisoned with the malicious / oversized archive. The engine's content
// URL is `client.BaseURL + /content/{kind}/{name}` (extract.FetchContent),
// so it always targets this server regardless of manifest contents.
//
// The canonical Content-Type matters: http.ServeFile would sniff the gzip
// magic and send "application/x-gzip", which the engine's isGzip dispatch
// (correctly, per spec) does NOT treat as gzip — the archive would then be
// written verbatim and the SAFE-01/03 tar policy would never run.
func phase7PoisonedContentServer(t *testing.T, upstream string, archive []byte) *httptest.Server {
	t.Helper()
	target, err := url.Parse(upstream)
	if err != nil {
		t.Fatalf("phase7PoisonedContentServer: parse upstream %q: %v", upstream, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/content/") {
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(archive)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// phase7AssertMaliciousFixtureRejected points the engine's content-fetch
// path at a poisoned reverse-proxy server (phase7PoisonedContentServer)
// serving the malicious fixture, and runs a real hydrate. The engine's
// extract package must reject the archive on the first SAFE-01 violation,
// exit non-zero, and write zero files under output. The /platform/hydrate
// manifest is proxied to the live platform-api, so this exercises the
// engine's true fetch → extract integration end-to-end — no production
// test seam, no skip fallback.
func phase7AssertMaliciousFixtureRejected(t *testing.T, fixturePath string) {
	t.Helper()

	archive, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("sc4 malicious fixture: read %s: %v", fixturePath, err)
	}

	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	srv := phase7PoisonedContentServer(t, baseURL, archive)
	xdg := phase7SeedXdgConfig(t, srv.URL, pk)
	output := phase7Workspace(t)

	stdout, stderr, err := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code, runErr := phase7StripExitErr(err)
	if runErr != nil {
		t.Fatalf("sc4 malicious fixture: exec error: %v\nstdout=%s\nstderr=%s",
			runErr, stdout, stderr)
	}
	// SAFE-01 contract: any malicious-archive entry → non-zero exit.
	if code == 0 {
		t.Errorf("sc4 malicious fixture %s: exit 0 (want non-zero — SAFE-01 rejection)\n"+
			"stdout=%s\nstderr=%s", filepath.Base(fixturePath), stdout, stderr)
	}
	// No files under output (partial-output-discarded invariant per
	// 07-W2-02 staging layer).
	if entries := phase7CountFiles(output); entries > 0 {
		t.Errorf("sc4 malicious fixture %s: %d files written under %s (want 0)",
			filepath.Base(fixturePath), entries, output)
	}
}

// phase7CountFiles returns the number of regular files under root,
// recursively. Excludes the .ach/ control plane subtree (state.json +
// tmp/) because those are written by step 12 before extract is reached
// — they are not extraction outputs. A successful SAFE-01 rejection
// fires before any adapter file is materialized, so .claude/.mcp.json
// and friends should NOT exist; only .ach/ entries (if any) survive.
func phase7CountFiles(root string) int {
	var n int
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(rel, ".ach"+string(filepath.Separator)) {
			return nil
		}
		n++
		return nil
	})
	return n
}

// testPhase7Sc4SafeExtractBomb sets ACH_MAX_EXTRACTED_PLUGIN_MIB=1 and
// supplies a 10MiB synthetic bomb tar.gz via a localhost
// httptest.Server. The engine's bomb cap (07-W2-01 capWriter) MUST
// fire before any bytes hit the output dir; exit non-zero + partial
// output discarded.
func testPhase7Sc4SafeExtractBomb(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	output := phase7Workspace(t)

	// Build a 10MiB synthetic tarball (entry size = 10*1024*1024) in
	// memory, gzip-wrap, serve via the poisoned reverse-proxy server (the
	// manifest still resolves against the live platform-api).
	bomb, err := buildBombTarGz(10 * 1024 * 1024)
	if err != nil {
		t.Fatalf("sc4 bomb: build tarball: %v", err)
	}
	srv := phase7PoisonedContentServer(t, baseURL, bomb)
	xdg := phase7SeedXdgConfig(t, srv.URL, pk)

	stdout, stderr, err := phase7RunAchCliEnv(t, xdg,
		[]string{
			"ACH_MAX_EXTRACTED_PLUGIN_MIB=1",
		},
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code, runErr := phase7StripExitErr(err)
	if runErr != nil {
		t.Fatalf("sc4 bomb: exec error: %v\nstdout=%s\nstderr=%s",
			runErr, stdout, stderr)
	}
	if code == 0 {
		t.Errorf("sc4 bomb: exit 0 (want non-zero — SAFE-03 bomb cap)\n"+
			"stdout=%s\nstderr=%s", stdout, stderr)
	}
	if entries := phase7CountFiles(output); entries > 0 {
		t.Errorf("sc4 bomb: %d files written under %s (want 0 — partial output discarded)",
			entries, output)
	}
}

// buildBombTarGz returns a gzip-wrapped tar archive with one regular
// entry of `size` bytes. The entry content is a repeated NUL — gzip
// compresses it very small in the wire form (a few hundred bytes), so
// the test fixture is cheap to ship across the test harness, but
// uncompressed it expands to `size` bytes for the bomb-cap test.
func buildBombTarGz(size int64) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name:     "bomb.bin",
		Mode:     0o644,
		Size:     size,
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0).UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	// Stream `size` NUL bytes through the tar writer. 64KiB chunk
	// keeps memory bounded.
	const chunk = 64 * 1024
	zero := make([]byte, chunk)
	written := int64(0)
	for written < size {
		n := int64(chunk)
		if rem := size - written; rem < n {
			n = rem
		}
		if _, err := tw.Write(zero[:n]); err != nil {
			return nil, err
		}
		written += n
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// testPhase7Sc4AutoClaimMatch: seed the final adapter-output path with
// bytes that match what the adapter would emit; hydrate; expect exit 0
// + bytes unchanged + state.json claims the file (auto-claim cascade
// Tier-1 = eager match).
//
// The expected adapter bytes are not available pre-hydrate without
// duplicating RenderRuntime logic in the test. We approximate the row
// by running hydrate to a temp dir, capturing the bytes, then re-using
// them as the seed in a fresh workspace. The second hydrate sees a
// pre-existing match and auto-claims.
func testPhase7Sc4AutoClaimMatch(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)

	// Pre-run to capture canonical adapter bytes.
	prerun := phase7Workspace(t)
	stdoutPre, stderrPre, errPre := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", prerun,
	)
	codePre, _ := phase7StripExitErr(errPre)
	if codePre != 0 {
		t.Fatalf("sc4 autoclaim match prerun: exit %d\nstdout=%s\nstderr=%s",
			codePre, stdoutPre, stderrPre)
	}
	canonical, err := os.ReadFile(filepath.Join(prerun, phase7ClaudeCodeRuntimePath))
	if err != nil {
		t.Fatalf("sc4 autoclaim match: read canonical bytes: %v", err)
	}

	// Fresh workspace; seed final path with canonical bytes.
	output := phase7Workspace(t)
	target := filepath.Join(output, phase7ClaudeCodeRuntimePath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("sc4 autoclaim match: mkdir final path parent: %v", err)
	}
	if err := os.WriteFile(target, canonical, 0o644); err != nil {
		t.Fatalf("sc4 autoclaim match: seed final path: %v", err)
	}

	stdout, stderr, err := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code, _ := phase7StripExitErr(err)
	if code != 0 {
		t.Errorf("sc4 autoclaim match: exit %d (want 0 — Tier-1 eager match)\n"+
			"stdout=%s\nstderr=%s", code, stdout, stderr)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("sc4 autoclaim match: read final path after: %v", err)
	}
	if !bytes.Equal(got, canonical) {
		t.Errorf("sc4 autoclaim match: bytes mutated on claim\nwant=%s\ngot=%s",
			canonical, got)
	}
}

// testPhase7Sc4AutoClaimDiffer exercises the per-key auto-claim contract
// against a file ACH did NOT previously write and has NO prior state for —
// the case sc3 never reaches (sc3 always seeds via a prior ACH hydrate, so
// the file is already owned). The surgical-merge redesign replaced the old
// whole-file collision cascade (exit 7 CollisionRefuse on a differing
// pre-existing file) with per-key coexistence: ACH claims the file by
// merging its managed keys (mcpServers) in and PRESERVING every unmanaged
// key. No refuse, exit 0.
//
// Two arms:
//  1. No --force: ACH merges into the unowned file. Exit 0; the unmanaged
//     keys survive AND the ACH-managed mcpServers block appears.
//  2. --force re-run: still exit 0, and the unmanaged keys are STILL
//     preserved — --force overrides per-key DRIFT on managed keys, it does
//     NOT nuke unmanaged content. This is the load-bearing guard that the
//     surgical-merge contract holds even under --force.
func testPhase7Sc4AutoClaimDiffer(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)

	output := phase7Workspace(t)
	target := filepath.Join(output, phase7ClaudeCodeRuntimePath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("sc4 autoclaim differ: mkdir final path parent: %v", err)
	}
	// A valid JSON object ACH did NOT author — no mcpServers, no prior
	// state.json. Contains an unmanaged sentinel key + a permissions block
	// ACH must never touch.
	const sentinelKey = "_unowned_pre_existing"
	seed := []byte(`{
  "` + sentinelKey + `": "must_survive_auto_claim",
  "permissions": {"allow": ["Read", "Edit"]}
}`)
	if err := os.WriteFile(target, seed, 0o644); err != nil {
		t.Fatalf("sc4 autoclaim differ: seed final path: %v", err)
	}

	assertClaimed := func(t *testing.T, arm string, raw []byte) {
		t.Helper()
		var doc struct {
			McpServers  map[string]json.RawMessage `json:"mcpServers"`
			Permissions json.RawMessage            `json:"permissions"`
			Sentinel    string                     `json:"_unowned_pre_existing"`
		}
		if jerr := json.Unmarshal(raw, &doc); jerr != nil {
			t.Fatalf("sc4 autoclaim differ (%s): merged config not valid JSON: %v\n%s", arm, jerr, raw)
		}
		if doc.Sentinel != "must_survive_auto_claim" {
			t.Errorf("sc4 autoclaim differ (%s): auto-claim clobbered the unmanaged %q key\nmerged=%s",
				arm, sentinelKey, raw)
		}
		if len(doc.Permissions) == 0 {
			t.Errorf("sc4 autoclaim differ (%s): auto-claim dropped the user 'permissions' block\nmerged=%s", arm, raw)
		}
		if len(doc.McpServers) == 0 {
			t.Errorf("sc4 autoclaim differ (%s): no ACH-managed mcpServers merged into the claimed file\nmerged=%s", arm, raw)
		}
	}

	// Arm 1 — no --force: merge-claim, exit 0.
	stdout, stderr, err := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code, runErr := phase7StripExitErr(err)
	if runErr != nil {
		t.Fatalf("sc4 autoclaim differ: hydrate exec error: %v\nstdout=%s\nstderr=%s", runErr, stdout, stderr)
	}
	if code != 0 {
		t.Errorf("sc4 autoclaim differ: exit %d (want 0 — per-key auto-claim merges, never refuses)\n"+
			"stdout=%s\nstderr=%s", code, stdout, stderr)
	}
	merged, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("sc4 autoclaim differ: read after claim: %v", rerr)
	}
	assertClaimed(t, "no-force", merged)

	// Arm 2 — --force re-run: still exit 0, unmanaged keys STILL preserved.
	stdoutForce, stderrForce, errForce := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
		"--force",
	)
	codeForce, _ := phase7StripExitErr(errForce)
	if codeForce != 0 {
		t.Errorf("sc4 autoclaim differ force: exit %d (want 0)\n"+
			"stdout=%s\nstderr=%s", codeForce, stdoutForce, stderrForce)
	}
	forced, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("sc4 autoclaim differ force: read after force: %v", rerr)
	}
	assertClaimed(t, "force", forced)
}

// testPhase7Sc4AutoClaimRotatedCredentialOwnedByCurrent is the W6-01
// Task 2 runtime gate for W5-03 (CR-03 path-normalization closure),
// reframed for the per-key surgical-merge model. It exercises the
// re-hydrate-with-rotated-credentials path:
//
//  1. First hydrate writes the runtime-config file + state.json. The
//     adapter file's recorded hash for that target lands in
//     state.adapter.files (asserted below — composition guard).
//  2. Simulate a credential rotation by mutating an ACH-MANAGED key
//     (a server body inside mcpServers) on-disk, keeping the file valid
//     JSON. The managed key now drifts from state.json's recorded hash.
//  3. Second hydrate (no --force) → exit 2 (LocalEditPreserve), on-disk
//     bytes preserved.
//  4. --force re-run → exit 0, the managed key restored to canonical,
//     mode 0o600 preserved.
//
// CR-03 guard (per-key): pre-W5-03 Classify compared `entry.Target`
// (workspace-relative) against an absolute finalPath; the strings never
// matched → the engine failed to find the owned entry. In the per-key
// model that manifests as a SILENT exit 0 (the drift is merge-claimed as
// if unowned) instead of the correct exit 2 drift-preserve. Post-W5-03
// Classify normalizes entry.Target against achDir via filepath.Join, the
// comparison succeeds, the engine finds the owned entry and classifies
// per-key drift. So an exit 2 at step 3 is the load-bearing proof that
// the path-normalization fix is effective end-to-end.
//
// Approach: Approach B from the W6-01 plan (documented test-seam
// state-mutation). Approach A (real credential rotation via a platform-api
// admin endpoint) is not feasible in the current kind fixture —
// `grep -rn "refresh-pk\|RefreshPK" internal/platformapi/ cmd/ach/cmd/`
// confirms no such endpoint exists. The mutation edits a managed key so
// the engine sees an "owned-by-current but drifted" file on the second
// invocation.
//
// t.Fatal fallback: if the first hydrate didn't write a state.json entry
// (or the demo env no longer renders mcpServers), the test fails with a
// "blocker follow-up plan required" message. t.Skip is FORBIDDEN — a
// silent skip would falsely green the suite.
func testPhase7Sc4AutoClaimRotatedCredentialOwnedByCurrent(t *testing.T) {
	t.Helper()
	phase7SuiteGuard(t)
	pk := phase7AcquirePk(t)
	baseURL := phase7BaseURL()
	xdg := phase7SeedXdgConfig(t, baseURL, pk)
	phase7DemoEnvironmentReady(t)
	output := phase7Workspace(t)

	// --- Step 1: first hydrate seeds state.json + adapter file. ---
	stdout1, stderr1, err1 := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code1, runErr1 := phase7StripExitErr(err1)
	if runErr1 != nil {
		t.Fatalf("sc4 autoclaim rotate: first hydrate exec error: %v\nstdout=%s\nstderr=%s",
			runErr1, stdout1, stderr1)
	}
	if code1 != 0 {
		t.Fatalf("sc4 autoclaim rotate: first hydrate exit %d (want 0)\nstdout=%s\nstderr=%s\n"+
			"HINT: re-check phase7DemoEnvironmentReady — the demo Environment must be "+
			"Available=True before any sc4 subtest runs.",
			code1, stdout1, stderr1)
	}

	// Capture the first-hydrate bytes and confirm state.json has an
	// adapter.files entry for the runtime-config target. Without that
	// entry the Classify path-match would have nothing to find post-
	// W5-03 — the failure mode would look like CR-03 but be a wholly
	// different bug (engine didn't write state.json at all).
	target := filepath.Join(output, phase7ClaudeCodeRuntimePath)
	canonical, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("sc4 autoclaim rotate: read first-hydrate target %s: %v",
			target, err)
	}
	statePath := phase7StatePath(output, phase7DemoEnvironment)
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("sc4 autoclaim rotate: read first-hydrate state.json %s: %v",
			statePath, err)
	}
	var seedState struct {
		SchemaVersion string `json:"schemaVersion"`
		Adapter       struct {
			ID    string `json:"id,omitempty"`
			Files []struct {
				Target string `json:"target"`
				Hash   string `json:"hash"`
			} `json:"files,omitempty"`
		} `json:"adapter,omitempty"`
	}
	if err := json.Unmarshal(stateBytes, &seedState); err != nil {
		t.Fatalf("sc4 autoclaim rotate: parse first-hydrate state.json: %v\nbytes=%s",
			err, stateBytes)
	}
	if seedState.SchemaVersion != "2" {
		t.Fatalf("sc4 autoclaim rotate: blocker follow-up plan required — "+
			"first hydrate produced state.json with schemaVersion=%q (want \"2\"); "+
			"the W5-03 path-comparison gate cannot be exercised without a v2 state.json. "+
			"This is not a sc4 regression — surface it to the verifier.",
			seedState.SchemaVersion)
	}
	var found bool
	for _, fe := range seedState.Adapter.Files {
		if fe.Target == phase7ClaudeCodeRuntimePath {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("sc4 autoclaim rotate: blocker follow-up plan required — "+
			"first hydrate did not record an adapter.files entry for target %q; "+
			"the W5-03 path-comparison gate has no state entry to match against. "+
			"Likely cause: W5-01 wiring regression — the engine wrote .mcp.json but "+
			"did not persist its hash to state.adapter.files. SAFE-04 W5-03 closeout "+
			"cannot be verified end-to-end against this state — file a blocker.\n"+
			"state.json=%s",
			phase7ClaudeCodeRuntimePath, stateBytes)
	}

	// --- Step 2: simulate credential rotation by mutating an ACH-MANAGED
	// key on-disk (a server body inside mcpServers — the same observable
	// state the engine would see if the bearer/header in a managed server
	// had rotated between invocations). The mutation keeps the file valid
	// JSON (per-key surgical merge parses it on re-hydrate) and is confined
	// to a managed key, so it is genuine §8.4 drift — NOT an unmanaged-key
	// edit (which would be preserved silently). ---
	const driftMarker = "DRIFTED-rotated-credential"
	var canonDoc map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &canonDoc); err != nil {
		t.Fatalf("sc4 autoclaim rotate: canonical settings.json not valid JSON: %v\n%s", err, canonical)
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(canonDoc["mcpServers"], &servers); err != nil || len(servers) == 0 {
		t.Fatalf("sc4 autoclaim rotate: blocker follow-up plan required — canonical "+
			"settings.json has no mcpServers block to drift (err=%v). The rotated-"+
			"credential scenario needs an ACH-managed server to mutate; if the demo "+
			"environment no longer renders an mcpServers block this test's premise is "+
			"stale.\n%s", err, canonical)
	}
	// Drift a key the adapter ACTUALLY renders (here "url") rather than
	// injecting a new key. mergeForward's deepMergeInto only overwrites keys
	// present in OUR canonical contribution, so a brand-new key would be
	// preserved as unmanaged content (correct surgical behavior) and --force
	// could never restore it. Mutating an existing rendered field gives a
	// genuine §8.4 managed-key drift that --force overwrites back.
	var drifted bool
	for name := range servers {
		var sObj map[string]any
		if err := json.Unmarshal(servers[name], &sObj); err != nil {
			t.Fatalf("sc4 autoclaim rotate: decode server %q: %v", name, err)
		}
		if _, ok := sObj["url"].(string); !ok {
			continue // need a rendered string field to drift
		}
		sObj["url"] = "http://" + driftMarker + ".invalid/"
		b, merr := json.Marshal(sObj)
		if merr != nil {
			t.Fatalf("sc4 autoclaim rotate: re-marshal server %q: %v", name, merr)
		}
		servers[name] = b
		drifted = true
		break
	}
	if !drifted {
		t.Fatalf("sc4 autoclaim rotate: blocker follow-up plan required — no ACH-"+
			"rendered mcpServer carries a 'url' field to drift; the demo environment's "+
			"MCP render shape changed. Pick another rendered key.\n%s", canonical)
	}
	sm, err := json.Marshal(servers)
	if err != nil {
		t.Fatalf("sc4 autoclaim rotate: re-marshal drifted mcpServers: %v", err)
	}
	canonDoc["mcpServers"] = sm
	mutated, err := json.Marshal(canonDoc)
	if err != nil {
		t.Fatalf("sc4 autoclaim rotate: re-marshal mutated settings.json: %v", err)
	}
	if bytes.Equal(mutated, canonical) {
		t.Fatalf("sc4 autoclaim rotate: blocker follow-up plan required — mutation " +
			"produced identical bytes; the managed-key drift could not be applied.")
	}
	if err := os.WriteFile(target, mutated, 0o600); err != nil {
		t.Fatalf("sc4 autoclaim rotate: mutate on-disk bytes at %s: %v", target, err)
	}

	// --- Step 3: second hydrate WITHOUT --force. The engine sees an
	// existing file whose path matches a state.adapter.files Target, with a
	// managed key drifted from the recorded hash. POST per-key model:
	// LocalEditPreserve → exit 2 (exit.Drift), on-disk bytes preserved.
	//
	// This is the load-bearing CR-03 path-normalization guard reframed for
	// the per-key model: exit 2 proves Classify FOUND the owned state entry
	// (achDir-relative Target normalized against the absolute finalPath) and
	// classified per-key drift. A silent exit 0 here would mean the path
	// comparison regressed — the engine failed to find the owned entry and
	// merge-claimed the drift as if unowned. ---
	stdout2, stderr2, err2 := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
	)
	code2, runErr2 := phase7StripExitErr(err2)
	if runErr2 != nil {
		t.Fatalf("sc4 autoclaim rotate: second hydrate exec error: %v\nstdout=%s\nstderr=%s",
			runErr2, stdout2, stderr2)
	}
	if code2 != 2 {
		t.Fatalf("sc4 autoclaim rotate: second hydrate exit %d (want 2 / exit.Drift — "+
			"LocalEditPreserve on a drifted managed key).\n"+
			"exit 0 → per-key drift detection regressed on the surgical-merge "+
			"path: the drifted user value for OUR key %q was not caught. Check "+
			"internal/cli/hydrate/wiring.go publishRuntimeFile + findAdapterEntry "+
			"(line ~371) + the §8.4 LocalEditPreserve branch in the per-key "+
			"NewDiffer().Compare dispatch.\n"+
			"stdout=%s\nstderr=%s",
			code2, phase7ClaudeCodeRuntimePath, stdout2, stderr2)
	}
	// Drift-preserve must NOT have overwritten — the marker survives.
	mid, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("sc4 autoclaim rotate: read after drift-preserve: %v", err)
	}
	if !bytes.Contains(mid, []byte(driftMarker)) {
		t.Errorf("sc4 autoclaim rotate: LocalEditPreserve (exit 2) must NOT overwrite the "+
			"drifted managed key, but the drift marker is gone.\nfile=%s", mid)
	}

	// --- Step 4: --force re-run. The engine overwrites the drifted managed
	// key back to canonical; exit 0. The marker disappears and the W5-02
	// mode-0o600 contract holds on the force-overwrite path. ---
	stdout3, stderr3, err3 := phase7RunAchCli(t, xdg,
		"hydrate", "--environment", phase7DemoEnvironment,
		"--platform", phase7PlatformClaudeCode,
		"--output", output,
		"--force",
	)
	code3, runErr3 := phase7StripExitErr(err3)
	if runErr3 != nil {
		t.Fatalf("sc4 autoclaim rotate: force hydrate exec error: %v\nstdout=%s\nstderr=%s",
			runErr3, stdout3, stderr3)
	}
	if code3 != 0 {
		t.Fatalf("sc4 autoclaim rotate: force hydrate exit %d (want 0)\nstdout=%s\nstderr=%s",
			code3, stdout3, stderr3)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("sc4 autoclaim rotate: read after force: %v", err)
	}
	if bytes.Contains(after, []byte(driftMarker)) {
		t.Errorf("sc4 autoclaim rotate: --force exited 0 but did NOT restore the drifted "+
			"managed key to canonical — the drift marker is still present.\n"+
			"Re-check the surgical-merge WriteAtomic path under --force "+
			"(internal/cli/hydrate/wiring.go).\nfile=%s", after)
	}

	// Re-assert mode 0o600 on the force-overwrite path — WriteAtomic must be
	// called with the same 0o600 mode as the initial write; a non-WriteAtomic
	// rewrite would silently drop to 0o644 (T-07-W5-02-05 / CR-01 regression).
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("sc4 autoclaim rotate: stat after force: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("sc4 autoclaim rotate: force overwrite produced mode %o (want %o) — "+
			"W5-02 CR-01 mitigation regressed on the rotate/force path.",
			got, 0o600)
	}
}
