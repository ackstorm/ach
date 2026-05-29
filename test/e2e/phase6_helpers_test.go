//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 6 CLI e2e helpers — Plan 06-09.
//
// Mirrors phase3/4/5 helpers: stdlib testing, kubectl orchestration,
// no Ginkgo / Gomega / testify. All command execution is bounded by
// context timeouts; no naked polling loops.
//
// Engineer-pending verification debt: this suite is mechanically
// correct but defers prerequisite acquisition (real pk_ via Phase 3
// SSO, kept-cluster bring-up, ./bin/ach binary build) to engineer
// action. Run via:
//
//	make cluster-keep
//	./scripts/dev.sh make build
//	ACH_E2E_PHASE6=1 \
//	  ACH_E2E_PHASE6_PK=pk_<26-base32-lower> \
//	  ACH_E2E_PHASE6_BASE_URL=https://<live-platform-api> \
//	  ./scripts/dev.sh make e2e-focus RUN='TestPhase6CLI'
//
// D-18 bypass mechanism chosen: Option A — env-var-injected pk_.
// The suite does NOT shell out to `ach login` (the device-code flow
// requires a real Dex round-trip + interactive browser open). Instead,
// the test writes a synthetic config file under a temp XDG_CONFIG_HOME
// directory with `default: demo` + `deployments.demo.{url,pk}` populated
// from ACH_E2E_PHASE6_PK + ACH_E2E_PHASE6_BASE_URL. The pk_ itself is
// expected to be acquired by the operator out-of-band (the canonical
// path is scripts/uat-phase3.sh + the existing examples/hydrate-demo.sh
// flow — though hydrate-demo.sh is deleted in this plan, the underlying
// Phase 3 SSO endpoints remain reachable for one-off pk_ acquisition).
//
// The hydrate-golden-diff subtest (the headline assertion) is the
// load-bearing assertion: bytes.Equal(stdout, phase6NormalizeHydrate(
// golden, clusterHost)). The normalization helper rewrites the
// golden's "ach.local.test" host with the live cluster's externally-
// visible host, so the byte-for-byte assertion holds across cluster
// topologies. The plan locks this contract in W4 — not deferred to
// SUMMARY.

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
	// phase6Namespace where the Helm chart installs the ach Deployments.
	phase6Namespace = "ach-system"
	// phase6PlatformAPIDeployment is the Phase 3 / Helm-chart Deployment
	// name that the CLI suite drives through.
	phase6PlatformAPIDeployment = "ach-platform-api"
	// phase6BinaryPath is the compiled `ach-cli` binary the suite exec's.
	// The user-facing CLI ships as a separate binary post-split; built by
	// `./scripts/dev.sh make build` (Makefile target writes to bin/ach-cli).
	phase6BinaryPath = "../../bin/ach-cli"
	// phase6GoldenPath is the golden hydrate.json relative to the
	// test/e2e working directory (Go `go test` cwd is the package dir).
	phase6GoldenPath = "../../examples/hydrate.json"
	// phase6DefaultBaseURL is the base URL the golden (examples/hydrate.json)
	// is stored against — the literal scheme+host the standard kind+Helm
	// cluster emits (the ach-local-gateway speaks plain http on localhost:8080
	// via ACH_BASE_URL in test/e2e/values/ach.values.yaml). Also the default
	// when ACH_E2E_PHASE6_BASE_URL is unset. phase6NormalizeHydrate rewrites
	// this to the live base before the byte-for-byte compare — a no-op on the
	// standard fixture, a real rewrite only against an exotic-host cluster.
	phase6DefaultBaseURL = "http://localhost:8080"
	// phase6DemoEnvironment is the Environment name from
	// examples/04-environment-demo.yaml — the standard cluster fixture
	// that the golden was captured against.
	phase6DemoEnvironment = "demo"
)

// phase6SuiteGuard skips when prerequisites aren't met.
//
// Required env vars when ACH_E2E_PHASE6=1:
//   - ACH_E2E_PHASE6_PK:        pk_<26-base32-lower> minted out-of-band
//     (e.g. via the Phase 3 SSO endpoints / scripts/uat-phase3.sh).
//
// Optional env vars (defaulted to the standard kind+Helm fixture):
//   - ACH_E2E_PHASE6_BASE_URL:  externally-visible platform-api URL
//     (default: https://ach.local.test). Doubles as the hydrate-golden
//     normalization target — phase6NormalizeHydrate rewrites the golden's
//     canonical https://ach.local.test base to this scheme+host.
//
// Also requires:
//   - ./bin/ach binary exists + is executable (built by
//     `./scripts/dev.sh make build`).
//   - examples/hydrate.json file is readable (working-dir sanity check).
//   - kubectl can reach the kept cluster's platform-api Deployment
//     (`kubectl get deploy ach-platform-api -n ach-system` succeeds).
func phase6SuiteGuard(t *testing.T) {
	t.Helper()

	if os.Getenv("ACH_E2E_PHASE6") != "1" {
		t.Skipf(
			"Phase 6 CLI e2e suite gated behind ACH_E2E_PHASE6=1 + live " +
				"kind+Helm cluster + ./bin/ach built (engineer-pending). " +
				"Run: make cluster-keep && ./scripts/dev.sh make build && " +
				"ACH_E2E_PHASE6=1 ACH_E2E_PHASE6_PK=pk_<26-base32-lower> " +
				"./scripts/dev.sh make e2e-focus RUN='TestPhase6CLI'. " +
				"See CLAUDE.md \"E2E debug loop\" + test/e2e/phase6_helpers_test.go " +
				"file header for the full activation contract.",
		)
		return
	}

	if _, err := os.Stat(phase6BinaryPath); err != nil {
		t.Skipf(
			"Phase 6 suite guard: %s not found (build it via "+
				"`./scripts/dev.sh make build`): %v",
			phase6BinaryPath, err,
		)
		return
	}

	if _, err := os.Stat(phase6GoldenPath); err != nil {
		t.Skipf(
			"Phase 6 suite guard: %s not found (working dir issue?): %v",
			phase6GoldenPath, err,
		)
		return
	}

	out, err := runCmd("kubectl", "get", "deploy", phase6PlatformAPIDeployment,
		"-n", phase6Namespace, "--no-headers")
	if err != nil {
		t.Skipf(
			"Phase 6 suite guard: kubectl get deploy %s -n %s failed "+
				"(cluster up? Helm chart applied? — run `make cluster-keep`): "+
				"%v\n%s",
			phase6PlatformAPIDeployment, phase6Namespace, err, out)
		return
	}
}

// phase6PlatformAPIURL returns the externally-visible platform-api URL
// the CLI subcommands should target. Sourced from
// ACH_E2E_PHASE6_BASE_URL when set, else falls back to the standard
// fixture URL (https://ach.local.test).
func phase6PlatformAPIURL(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("ACH_E2E_PHASE6_BASE_URL"); v != "" {
		return v
	}
	return phase6DefaultBaseURL
}

// phase6NormalizeHydrate substitutes every occurrence of the golden's
// stored base URL (phase6DefaultBaseURL = "http://localhost:8080") with the
// live cluster's base URL. The golden is stored against the literal host the
// standard kind+Helm fixture emits, so this is a no-op there; it only does
// real work against an exotic-host cluster (override ACH_E2E_PHASE6_BASE_URL).
// Replacing the full scheme://host prefix (not just the host) keeps it
// correct across an http↔https topology change. The hydrate command emits
// the response body verbatim via io.Copy (no re-encoding); this rewrite is
// the only intentional transform. Idempotent when liveBaseURL ==
// phase6DefaultBaseURL.
//
// See CLAUDE.md "Common failure modes" → "Hydrate output != hydrate.json".
func phase6NormalizeHydrate(golden []byte, liveBaseURL string) []byte {
	return bytes.ReplaceAll(golden,
		[]byte(phase6DefaultBaseURL), []byte(liveBaseURL))
}

// phase6WriteTempConfig writes a synthetic ~/.config/ach/config.yaml
// under a temp XDG_CONFIG_HOME directory with `default: demo` +
// `deployments.demo.{url,pk}` populated from the supplied URL + pk.
// Returns the temp directory path (which the caller exports as
// XDG_CONFIG_HOME via t.Setenv).
//
// The yaml shape mirrors `internal/cli/config.File` (Hub §15.4 verbatim).
// Mode 0600 on the config file + 0700 on the directory match the
// production discipline enforced by `internal/cli/config.Save`.
//
// This is the D-18 Option A bypass — the test stages a pre-minted pk_
// rather than running through the device-code flow.
func phase6WriteTempConfig(t *testing.T, baseURL, pk string) string {
	t.Helper()
	if baseURL == "" {
		t.Fatalf("phase6WriteTempConfig: baseURL must be non-empty")
	}
	if pk == "" {
		t.Fatalf("phase6WriteTempConfig: pk must be non-empty")
	}
	tmp := t.TempDir()
	achDir := filepath.Join(tmp, "ach")
	if err := os.MkdirAll(achDir, 0o700); err != nil {
		t.Fatalf("phase6WriteTempConfig: mkdir %s: %v", achDir, err)
	}
	cfgPath := filepath.Join(achDir, "config.yaml")
	contents := fmt.Sprintf(""+
		"default: demo\n"+
		"deployments:\n"+
		"    demo:\n"+
		"        url: %s\n"+
		"        pk: %s\n",
		baseURL, pk,
	)
	if err := os.WriteFile(cfgPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("phase6WriteTempConfig: write %s: %v", cfgPath, err)
	}
	return tmp
}

// phase6AcquirePk returns the pk_ the test should use to authenticate
// CLI invocations. Sources:
//   - ACH_E2E_PHASE6_PK env var (engineer-pending — operator mints
//     it out-of-band via the Phase 3 SSO endpoints).
//
// When unset, the calling subtest skips with an engineer-pending
// message pointing at the canonical acquisition path.
func phase6AcquirePk(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("ACH_E2E_PHASE6_PK"); v != "" {
		return v
	}
	t.Skipf(
		"phase6AcquirePk: ACH_E2E_PHASE6_PK unset. Acquire a real pk_ via " +
			"the Phase 3 SSO endpoints (POST /platform/auth/login → " +
			"/sso/callback) against the kept cluster, then re-export. " +
			"Engineer-pending verification debt — mirror of phase3SuiteGuard.",
	)
	return ""
}

// phase6RunAch exec's the ./bin/ach binary with the supplied args.
// XDG_CONFIG_HOME is set to xdgHome so the binary picks up the
// synthetic config written by phase6WriteTempConfig.
//
// Returns the stdout bytes, stderr bytes, and the underlying error.
// Test-bounded 60s context timeout — CLI subcommands are
// request-response (no long-running streams).
func phase6RunAch(t *testing.T, xdgHome string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, phase6BinaryPath, args...)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdgHome)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// phase6StripExitErr reduces an *exec.ExitError to a synthesized
// non-zero exit code, or returns 0 on nil err. Non-ExitError errors
// (e.g. start failures) are returned via the second value so the test
// can fail-fast instead of misinterpreting an OS-level error as a CLI
// non-zero exit.
func phase6StripExitErr(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if !asExitError(err, &exitErr) {
		return -1, err
	}
	return exitErr.ExitCode(), nil
}

// asExitError is a tiny stdlib-only `errors.As` wrapper. Mirrors the
// idiom in cmd/ach/cmd/whoami.go without adding a phase6-package
// internal dependency.
func asExitError(err error, target **exec.ExitError) bool {
	if err == nil {
		return false
	}
	// stdlib errors.As is the canonical path; keep this helper here so
	// the test file stays import-light.
	for cur := err; cur != nil; {
		if ee, ok := cur.(*exec.ExitError); ok {
			*target = ee
			return true
		}
		type wrap interface{ Unwrap() error }
		w, ok := cur.(wrap)
		if !ok {
			return false
		}
		cur = w.Unwrap()
	}
	return false
}

// phase6TrimSpaceASCII is a tiny helper for stdout substring checks —
// keeps the per-subtest contains/equals assertions readable without
// pulling in strings.TrimSpace's "let's also normalize unicode"
// semantics. Tests assert against ASCII CLI output only.
func phase6TrimSpaceASCII(b []byte) []byte {
	return bytes.TrimSpace(b)
}

// phase6Contains is a tiny substring-search helper used by the
// per-subtest assertions to keep the test bodies readable. Equivalent
// to strings.Contains(string(b), needle) but avoids the string-copy
// when the result is only used as a boolean.
func phase6Contains(b []byte, needle string) bool {
	return strings.Contains(string(b), needle)
}
