//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 7 CLI engine e2e helpers — Plan 07-W4-01.
//
// Mirrors phase6_helpers_test.go: stdlib testing, kubectl orchestration,
// no Ginkgo / Gomega / testify. All command execution is bounded by
// context timeouts; no naked polling loops.
//
// The normal path is `make e2e-run`: it builds the e2e-tagged CLI
// binary and self-mints a pk_ through the mock SSO flow when no
// ACH_E2E_PHASE7_PK override is supplied. Focused runs can be driven
// after the same prerequisites:
//
//	make cluster-up
//	make build-e2e   # MUST use -tags=e2e for sc2 (07-W5-04)
//	make e2e-focus FOCUS=TestPhase7CLIEngine
//
// IMPORTANT (07-W5-04 WR-01): the binary at ./bin/ach-cli MUST be
// built with -tags=e2e for sc2_commit_sequence_sigkill to fire. A
// release-tagged binary stubs out the SIGKILL seam (env var is
// never read; syscall.Kill is never invoked) so a release binary
// will run to completion instead of crashing mid-step. The
// phase7SuiteGuard skips sc2 cleanly with a descriptive message
// when the binary's strings do not contain the seam env-var
// literal — see the guard's binary-introspection block.
//
// D-18 bypass mechanism (carried forward from Phase 6): the suite stages
// a synthetic XDG_CONFIG_HOME/ach/config.yaml with `default: demo` +
// `deployments.demo.{url,pk}` populated from ACH_E2E_PHASE7_PK +
// ACH_E2E_PHASE7_BASE_URL when supplied, or from the standard gateway URL
// plus a self-minted pk_ otherwise.
//
// Subtest harness contract:
//   - phase7SuiteGuard:        prerequisite gate (env + binary + cluster).
//   - phase7BinaryPath:        compiled `./bin/ach-cli` target const.
//   - phase7RunAchCli:         exec the CLI under XDG_CONFIG_HOME.
//   - phase7RunAchCliEnv:      env-augmented variant (sc2 SIGKILL seam,
//                              sc4 bomb ACH_MAX_EXTRACTED_PLUGIN_MIB).
//   - phase7SeedXdgConfig:     write the synthetic config + return paths.
//   - phase7CreateEkKey:       mint an ek_ via `ach-cli keys create`.
//   - phase7DemoEnvironmentReady: kubectl wait for the demo Environment.
//   - phase7Workspace:         t.TempDir + .claude/ scaffold.
//   - phase7BaseURL:           ACH_E2E_PHASE7_BASE_URL or localhost:8080.
//
// The TEST-ONLY SIGKILL seam consumed by sc2 is:
//
//	ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP=<N>
//
// declared in internal/cli/hydrate/commit.go (envSigkillStep const,
// landed by 07-W1-06 Task 2). Setting it makes the engine call
// syscall.Kill(SIGKILL) on its own pid after step N returns, giving the
// e2e a deterministic mid-commit-sequence crash point — no timeout race.

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	// phase7Namespace where the Helm chart installs the ach Deployments.
	phase7Namespace = "ach-system"
	// phase7PlatformAPIDeployment is the Phase 3 / Helm-chart Deployment
	// name that the CLI engine suite drives through.
	phase7PlatformAPIDeployment = "ach-platform-api"
	// phase7BinaryPath is the compiled `ach-cli` binary the suite exec's.
	// The user-facing CLI ships as a separate binary post-split; built by
	// `make e2e-run` / `make build-e2e` (writes to bin/ach-cli).
	phase7BinaryPath = "../../bin/ach-cli"
	// phase7DefaultBaseURL is the externally-visible platform-api base
	// URL the standard kind+Helm fixture emits (the ach-local-gateway
	// speaks plain http on localhost:8080 via ACH_BASE_URL in
	// test/e2e/values/ach.values.yaml). Override with
	// ACH_E2E_PHASE7_BASE_URL for an exotic-host cluster.
	phase7DefaultBaseURL = "http://localhost:8080"
	// phase7DemoEnvironment is the Environment name from
	// examples/04-environment-demo.yaml — the standard cluster fixture
	// every sc1_* subtest drives against.
	phase7DemoEnvironment = "demo"
	// phase7SigkillEnvVar is the TEST-ONLY SIGKILL injection seam read
	// by internal/cli/hydrate/commit.go at newCommit() entry. Setting it
	// to a step index N (1..13) makes the engine call syscall.Kill on
	// its own pid after step N returns — deterministic mid-commit-
	// sequence crash for sc2_commit_sequence_sigkill. Default 0 = off.
	// See 07-W1-06 Task 2 for the seam declaration.
	phase7SigkillEnvVar = "ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP"
)

// phase7SuiteGuard skips when prerequisites aren't met.
//
// Optional env vars:
//   - ACH_E2E_PHASE7_PK:       pk_<26-base32-lower> override. When unset,
//     the suite self-mints via the mock SSO flow against the kept cluster.
//   - ACH_E2E_PHASE7_BASE_URL: externally-visible platform-api URL
//     (default: http://localhost:8080).
//
// Also requires:
//   - ./bin/ach-cli binary exists + is executable (built by
//     `make e2e-run` / `make build-e2e`).
//   - kubectl can reach the kept cluster's platform-api Deployment
//     (`kubectl get deploy ach-platform-api -n ach-system` succeeds).
//
// On any prerequisite miss, the subtest is t.Skipf'd with a descriptive
// message pointing at the activation contract — not failed. This is the
// same skip-on-cluster-missing pattern as phase6SuiteGuard so
// `make e2e-focus FOCUS=TestPhase7CLIEngine` on a missing-cluster run is
// a clean skip, not a fail.
func phase7SuiteGuard(t *testing.T) {
	t.Helper()

	if os.Getenv("ACH_SKIP_PHASE7") == "1" {
		t.Skipf(
			"Phase 7 CLI engine e2e suite opted out via ACH_SKIP_PHASE7=1 (default: runs); needs a live " +
				"kind+Helm cluster + ./bin/ach-cli. " +
				"Run: make e2e-run, or make cluster-up && make build-e2e && " +
				"make e2e-focus FOCUS=TestPhase7CLIEngine. " +
				"See CLAUDE.md \"E2E debug loop\" + test/e2e/phase7_helpers_test.go " +
				"file header for the full activation contract. " +
				"NOTE (07-W5-04): make build-e2e supersedes make build for this suite — " +
				"-tags=e2e is required for sc2_commit_sequence_sigkill.",
		)
		return
	}

	if _, err := os.Stat(phase7BinaryPath); err != nil {
		t.Skipf(
			"Phase 7 suite guard: %s not found (build it via "+
				"`make e2e-run` or `make build-e2e`): %v",
			phase7BinaryPath, err,
		)
		return
	}

	out, err := runCmd("kubectl", "get", "deploy", phase7PlatformAPIDeployment,
		"-n", phase7Namespace, "--no-headers")
	if err != nil {
		t.Skipf(
			"Phase 7 suite guard: kubectl get deploy %s -n %s failed "+
				"(cluster up? Helm chart applied? — run `make cluster-up`): "+
				"%v\n%s",
			phase7PlatformAPIDeployment, phase7Namespace, err, out)
		return
	}
}

// phase7RequireSigkillSeam introspects the ./bin/ach-cli binary for
// the ACH_E2E_PHASE7_INJECT_SIGKILL_AFTER_STEP env-var literal and
// skips the calling subtest cleanly with a descriptive message when
// the literal is absent. This is the post-07-W5-04 (WR-01) guard:
// the SIGKILL injection seam is build-tag-gated behind //go:build
// e2e, so a binary built without -tags=e2e (i.e. `make build`
// instead of `make build-e2e`) compiles in the no-op stub from
// sigkill_seam_prod.go — the env var is never read and sc2 would
// false-pass (the engine completes normally; no SIGKILL ever
// fires).
//
// Symbol introspection is intentionally string-based rather than
// `nm`-based: the env-var literal is preserved verbatim in the
// .rodata section under -tags=e2e (via the envSigkillStep const
// in sigkill_seam_e2e.go) and is absent under -tags=!e2e (the
// readSigkillSeamFromEnv stub never references it). A simple byte-
// containment check on the binary's file bytes is sufficient and
// avoids the dependency on platform-specific nm/objdump tooling.
//
// Called by testPhase7Sc2SigkillRecovery before the SIGKILL-
// injecting hydrate run. Other Phase 7 subtests (sc1_*, sc3_*,
// sc4_*) do not depend on the seam and do not call this helper.
func phase7RequireSigkillSeam(t *testing.T) {
	t.Helper()
	bin, err := os.ReadFile(phase7BinaryPath)
	if err != nil {
		t.Fatalf("phase7RequireSigkillSeam: read %s: %v "+
			"(precondition: phase7SuiteGuard should have caught a missing binary)",
			phase7BinaryPath, err)
	}
	if !bytes.Contains(bin, []byte(phase7SigkillEnvVar)) {
		t.Skipf(
			"phase7RequireSigkillSeam: %s does not contain the SIGKILL seam env-var "+
				"literal %q — binary was built without -tags=e2e (07-W5-04 WR-01: "+
				"the seam is build-tag-gated; sc2 cannot fire against a release-tagged "+
				"binary). Rebuild via `make build-e2e` and re-run.",
			phase7BinaryPath, phase7SigkillEnvVar)
	}
}

// phase7BaseURL returns the externally-visible platform-api base URL the
// CLI engine path targets. Sourced from ACH_E2E_PHASE7_BASE_URL when set,
// else falls back to the standard kind+Helm fixture URL
// (http://localhost:8080).
func phase7BaseURL() string {
	if v := os.Getenv("ACH_E2E_PHASE7_BASE_URL"); v != "" {
		return v
	}
	return phase7DefaultBaseURL
}

// phase7AcquirePk returns the pk_ the test should use to authenticate
// CLI invocations. Sources, in order:
//   - ACH_E2E_PHASE7_PK env var (explicit override).
//   - else SELF-MINT via the Dex mockCallback SSO flow against the kept
//     cluster (ssoMintPK), so the Phase 7 engine suite runs unattended
//     inside the e2e container instead of skipping on a missing pk_.
//
// The mint is the same Go port of references/local-testing-gateway.md §3
// used by TestPhase7AllPlatformsProjection. Standard kind+Helm fixture only
// (the mock connector returns the static kilgore@kilgore.trout user).
func phase7AcquirePk(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("ACH_E2E_PHASE7_PK"); v != "" {
		return v
	}
	return ssoMintPK(t, phase7BaseURL())
}

// phase7SeedXdgConfig writes a synthetic ~/.config/ach/config.yaml under
// a temp XDG_CONFIG_HOME directory with `default: demo` +
// `deployments.demo.{url,pk}` populated from baseURL + pk. Returns the
// temp XDG_CONFIG_HOME path so the caller can pass it to
// phase7RunAchCli / phase7RunAchCliEnv.
//
// The yaml shape mirrors `internal/cli/config.File` (Hub §15.4 verbatim).
// Mode 0600 on the config file + 0700 on the directory match the
// production discipline enforced by `internal/cli/config.Save`.
//
// This is the D-18 Option A bypass — the test stages a pre-minted pk_
// rather than running through the device-code flow. The pk_ plaintext
// is never logged via t.Logf (CLI-04 no-leak / OBS-02).
func phase7SeedXdgConfig(t *testing.T, baseURL, pk string) string {
	t.Helper()
	if baseURL == "" {
		t.Fatalf("phase7SeedXdgConfig: baseURL must be non-empty")
	}
	if pk == "" {
		t.Fatalf("phase7SeedXdgConfig: pk must be non-empty")
	}
	tmp := t.TempDir()
	achDir := filepath.Join(tmp, "ach")
	if err := os.MkdirAll(achDir, 0o700); err != nil {
		t.Fatalf("phase7SeedXdgConfig: mkdir %s: %v", achDir, err)
	}
	cfgPath := filepath.Join(achDir, "config.yaml")
	contents := fmt.Sprintf(""+
		"default: demo\n"+
		"profiles:\n"+
		"    demo:\n"+
		"        url: %s\n"+
		"        pk: %s\n",
		baseURL, pk,
	)
	if err := os.WriteFile(cfgPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("phase7SeedXdgConfig: write %s: %v", cfgPath, err)
	}
	return tmp
}

// phase7CreateEkKey runs `ach-cli keys create --environment demo
// --name <label>` against the seeded XDG_CONFIG_HOME and returns the
// minted ek_ plaintext. Used by the sc1_*_ek subtests to exercise the
// ek_ credential path — pk_-only subtests do NOT call this helper.
//
// The minted ek_ is also persisted into the config.yaml under the
// supplied label (D-07 always-persist), so subsequent `ach-cli hydrate
// --env-key <label>` invocations resolve it from the same XDG.
//
// On any non-zero exit or unparseable stdout, the subtest fails fast
// with the captured stdout + stderr in the error message — no silent
// failure modes.
func phase7CreateEkKey(t *testing.T, xdgHome, label string) string {
	t.Helper()
	if xdgHome == "" {
		t.Fatalf("phase7CreateEkKey: xdgHome must be non-empty")
	}
	if label == "" {
		t.Fatalf("phase7CreateEkKey: label must be non-empty")
	}
	stdout, stderr, err := phase7RunAchCli(t, xdgHome,
		"keys", "create",
		"--environment", phase7DemoEnvironment,
		"--name", label,
	)
	code, runErr := phase7StripExitErr(err)
	if runErr != nil {
		t.Fatalf("phase7CreateEkKey: exec error: %v\nstdout=%s\nstderr=%s",
			runErr, stdout, stderr)
	}
	if code != 0 {
		t.Fatalf("phase7CreateEkKey: ach-cli keys create exit %d (want 0)\n"+
			"stdout=%s\nstderr=%s", code, stdout, stderr)
	}
	ek := phase7ParseEkPlaintext(stdout)
	if ek == "" {
		t.Fatalf("phase7CreateEkKey: stdout missing ek_ plaintext (CLI-04 one-shot return)\n"+
			"stdout=%s\nstderr=%s", stdout, stderr)
	}
	return ek
}

// phase7ParseEkPlaintext extracts the ek_<...> token from `ach-cli
// keys create` stdout. The CLI prints a multi-line block including
// a freshly-minted ek_ plaintext; this helper locates the first
// whitespace-delimited token starting with "ek_" and returns it.
// Returns "" when no token is found — the caller fails the subtest.
func phase7ParseEkPlaintext(out []byte) string {
	for _, field := range strings.Fields(string(out)) {
		field = strings.TrimRight(field, ",.;:\n\r\t ")
		if strings.HasPrefix(field, "ek-") {
			return field
		}
	}
	return ""
}

// phase7DemoEnvironmentReady waits up to 5m for the demo Environment to
// be Available=True. Uses kubectl wait with a bounded timeout — no
// naked polling loop. Mirrors the `make wait-cr-ready` pattern from
// CLAUDE.md "Waiting for state" without taking the make-target
// dependency (would require shell-out to make + the devtools image).
//
// On timeout or kubectl-error, the subtest fails fast with the kubectl
// output captured for debug. Common cause of a fail here is
// AccessGroupSynced=False reason=UnresolvedReferences — see CLAUDE.md
// "Environment stuck in AccessGroupSynced=False" for the remediation.
func phase7DemoEnvironmentReady(t *testing.T) {
	t.Helper()
	out, err := runCmdLonger(5*time.Minute, "kubectl",
		"wait", "--for=condition=Available",
		"environment/"+phase7DemoEnvironment,
		"-n", phase7Namespace,
		"--timeout=5m",
	)
	if err != nil {
		t.Fatalf("phase7DemoEnvironmentReady: kubectl wait for Environment/%s "+
			"in %s did not reach Available=True within 5m: %v\n%s\n"+
			"NOTE: AccessGroupSynced=False reason=UnresolvedReferences is a "+
			"common cause — see CLAUDE.md \"Environment stuck in "+
			"AccessGroupSynced=False\" for the remediation.",
			phase7DemoEnvironment, phase7Namespace, err, out)
	}
}

// phase7Workspace returns a freshly-allocated workspace root (t.TempDir
// + an empty .claude/ scaffold). Used by sc1_*_pk subtests as the
// `--output` flag value: the hydrate engine writes its state.json +
// adapter runtime config under <workspace>/.ach/<env>/ and the adapter
// outputs (e.g. .claude/.mcp.json) under <workspace>/.claude/.
//
// The .claude/ pre-create is intentional: it gives claudecode's
// adapter.Detect a Medium-confidence signal so multi-adapter autodetect
// is deterministic in the sc1_claudecode_* subtests (the platform is
// explicitly --platform-overridden but the autodetect path is still
// touched as part of resolvePlatformOrAutodetect — keeping the disk
// state consistent prevents a spurious "did you mean ..." stderr).
func phase7Workspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatalf("phase7Workspace: mkdir .claude/: %v", err)
	}
	return root
}

// phase7RunAchCli exec's the ./bin/ach-cli binary with the supplied
// args. XDG_CONFIG_HOME is set to xdgHome so the binary picks up the
// synthetic config written by phase7SeedXdgConfig.
//
// Returns the stdout bytes, stderr bytes, and the underlying error.
// Test-bounded 60s context timeout — CLI subcommands are
// request-response (no long-running streams).
//
// Mirrors phase6RunAch with the post-split binary name (ach-cli).
func phase7RunAchCli(t *testing.T, xdgHome string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	return phase7RunAchCliEnv(t, xdgHome, nil, args...)
}

// phase7RunAchCliEnv is the env-augmented variant of phase7RunAchCli.
// extraEnv is appended to the inherited os.Environ() AFTER the
// XDG_CONFIG_HOME setter so callers can override XDG if needed
// (sc2_commit_sequence_sigkill uses the same XDG, just appends the
// SIGKILL seam; sc4_safe_extract_bomb appends ACH_MAX_EXTRACTED_PLUGIN_MIB).
//
// Each subtest constructs extraEnv from a closed list of KEY=VALUE
// pairs — no string interpolation from untrusted sources. The 60s
// context timeout matches phase7RunAchCli's bound.
func phase7RunAchCliEnv(t *testing.T, xdgHome string, extraEnv []string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, phase7BinaryPath, phase7ArgsWithRuntime(args)...)
	// Strip the synthetic-mode env vars the e2e harness exports (E2E_RUN_ENV
	// sets ACH_BASE_URL): they would flip the CLI into synthetic mode and
	// ignore the seeded XDG disk-config credential (half-set-erroring when no
	// ACH_API_KEY is present). These tests authenticate via the seeded config.
	// ACH_INSECURE=1: the kind+Helm gateway is http://localhost:8080 and the
	// CLI now refuses plaintext http:// by default (G19, decision B), so the
	// e2e opt-in is mandatory for the local fixture.
	env := append(cleanEnv(os.Environ()), "XDG_CONFIG_HOME="+xdgHome, "ACH_INSECURE=1")
	env = append(env, extraEnv...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// phase7ArgsWithRuntime keeps Phase 7 focused on the runtime-aware hydrate
// contract. Production hydrate defaults to context-only; these tests assert
// adapter runtime-config merge/drift semantics, so every hydrate invocation
// goes through --include-runtime unless the caller already supplied it.
func phase7ArgsWithRuntime(args []string) []string {
	if len(args) < 2 || args[0] != "env" || args[1] != "hydrate" {
		return args
	}
	for _, arg := range args[2:] {
		if arg == "--include-runtime" {
			return args
		}
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, args[0], args[1], "--include-runtime")
	out = append(out, args[2:]...)
	return out
}

// syntheticModeEnvVars are the env vars that flip the CLI into "synthetic
// mode" (spec §3.3 / CLI-07): ACH_BASE_URL + a resolved credential. The e2e
// harness (E2E_RUN_ENV) exports ACH_BASE_URL into the test process, so these
// are stripped before exec'ing the CLI — the Phase 7 suite authenticates via
// the seeded XDG disk config, not synthetic mode.
var syntheticModeEnvVars = []string{"ACH_BASE_URL", "ACH_API_KEY", "ACH_ENV_KEY"}

// cleanEnv returns env with the synthetic-mode vars removed.
func cleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		drop := false
		for _, k := range syntheticModeEnvVars {
			if strings.HasPrefix(kv, k+"=") {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// phase7StripExitErr reduces an *exec.ExitError to its underlying exit
// code (or returns 0 on nil err). Non-ExitError errors (e.g. start
// failures, SIGKILL-style termination where the process has no exit
// code) are returned via the second value so the test can fail-fast
// instead of misinterpreting an OS-level error as a CLI non-zero exit.
//
// Note for sc2_commit_sequence_sigkill: a SIGKILL'd process reports
// ExitCode() = -1 via Go's exec.ExitError (the process was signaled,
// not exited). The caller asserts on this signed sentinel rather than
// a positive exit code.
func phase7StripExitErr(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if !asExitError(err, &exitErr) {
		return -1, err
	}
	return exitErr.ExitCode(), nil
}
