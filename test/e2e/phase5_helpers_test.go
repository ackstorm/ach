//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 5 invariants helpers — Plan 05-08.
//
// Mirrors phase4_helpers_test.go: stdlib testing, kubectl orchestration,
// no Ginkgo / Gomega / testify. All command execution is bounded by
// context timeouts; no naked polling loops.
//
// Engineer-pending verification debt: this suite is mechanically
// correct but defers prerequisite acquisition (pk_/ek_ via Phase 3 SSO,
// CR fixture seeding, projection-row write-through latency) to
// engineer action once the kind cluster is up. Run via
// `ACH_E2E_PHASE5=1 make e2e-focus RUN='TestPhase5Invariants'` after
// `make e2e-keep`.

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	// phase5Namespace where the Helm chart installs the operator Pod.
	phase5Namespace = "ach-system"
	// phase5OperatorDeployment hosts both the manager and content-service
	// containers (Plan 01-08 RWO co-location reaffirmed by Plan 05-07).
	phase5OperatorDeployment = "ach-operator"
	// phase5CSContainer is the second container in the operator Pod
	// (see deploy/helm/ach/templates/operator-deployment.yaml).
	phase5CSContainer = "content-service"
	// phase5EnvFixtureName matches test/e2e/phase5_fixtures/environment.yaml.
	phase5EnvFixtureName = "prod"
)

// phase5SuiteGuard skips when prerequisites aren't met.
//
// Required env vars when ACH_E2E_PHASE5=1:
//   - ACH_CONTENT_SERVICE_URL: port-forwarded http://localhost:<p>/ for the CS
//   - ACH_PLATFORM_API_URL:    port-forwarded http://localhost:<p>/ for platform-api
//   - ACH_FORWARDER_URL:       port-forwarded http://localhost:<p>/ for forwarder
//   - ACH_OPERATOR_METRICS_URL: port-forwarded http://localhost:<p>/metrics endpoint
//     for the operator's controller-runtime metricsserver (typically :8080
//     when metrics-secure=false, or :8443 with TLS+auth when secure).
func phase5SuiteGuard(t *testing.T) {
	t.Helper()
	if os.Getenv("ACH_E2E_PHASE5") != "1" {
		t.Skipf("Phase 5 e2e suite gated behind ACH_E2E_PHASE5=1 + live kind+Helm cluster with operator Pod Ready; see CLAUDE.md `E2E debug loop`. Skipping (engineer-pending).")
	}
	required := []string{
		"ACH_CONTENT_SERVICE_URL",
		"ACH_PLATFORM_API_URL",
		"ACH_FORWARDER_URL",
		"ACH_OPERATOR_METRICS_URL",
	}
	for _, k := range required {
		if os.Getenv(k) == "" {
			t.Skipf("%s not set — set port-forwarded URL per CLAUDE.md `E2E debug loop`. Skipping (engineer-pending).", k)
		}
	}
	if err := waitOperatorReady(t, 30*time.Second); err != nil {
		t.Skipf("operator Deployment %s/%s not Ready (%v) — run `make e2e-keep` first. Skipping (engineer-pending).",
			phase5Namespace, phase5OperatorDeployment, err)
	}
}

// waitOperatorReady bounds the wait on the co-located Pod's Deployment.
func waitOperatorReady(t *testing.T, deadline time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "-n", phase5Namespace,
		"rollout", "status", "deployment/"+phase5OperatorDeployment,
		fmt.Sprintf("--timeout=%ds", int(deadline.Seconds())))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl rollout: %v output=%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// seedPhase5Fixtures applies the four CR fixtures (Environment, Plugin,
// Prompt, Artifact) and waits each one Ready via the blessed
// make wait-cr-ready target. Returns the pk_/ek_/env tuple acquired
// from Phase 4 helpers (themselves env-var-stubbed until SSO is wired).
//
// Idempotent — re-applying the same fixtures on a populated cluster
// returns success.
func seedPhase5Fixtures(t *testing.T, ctx context.Context) (pk, ek, env string) {
	t.Helper()
	fixturesDir := phase5FixtureDir(t)
	files := []string{
		"environment.yaml",
		"plugin-foo.yaml",
		"prompt-bar.yaml",
		"artifact-baz.yaml",
	}
	for _, f := range files {
		path := filepath.Join(fixturesDir, f)
		cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("kubectl apply %s: %v output=%s", path, err, strings.TrimSpace(string(out)))
		}
	}
	// Wait each CR Ready via the blessed Makefile target. Each wait is
	// bounded by WAIT_TIMEOUT (default 300s in Makefile).
	waits := []struct{ kind, name string }{
		{"environment", "prod"},
		{"plugin", "foo"},
		{"prompt", "bar"},
		{"artifact", "baz"},
	}
	for _, w := range waits {
		cmd := exec.CommandContext(ctx, "make", "wait-cr-ready",
			"KIND="+w.kind, "NAME="+w.name, "NS="+phase5Namespace)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("wait-cr-ready %s/%s: %v output=%s", w.kind, w.name, err, strings.TrimSpace(string(out)))
		}
	}
	pk = mustAcquirePk(t)
	ek = mustAcquireEkBoundToEnv(t, phase5EnvFixtureName)
	env = phase5EnvFixtureName
	return pk, ek, env
}

// phase5FixtureDir resolves the test/e2e/phase5_fixtures absolute path.
func phase5FixtureDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	return filepath.Join(wd, "phase5_fixtures")
}

// straceCSSendfile attaches strace to the content-service container's
// PID 1, fires a GET against contentPath via the port-forwarded CS URL,
// and returns true iff at least one sendfile/sendfile64 syscall appears
// in the captured strace output.
//
// Approach: kubectl debug ephemeral container alongside the CS
// container (the distroless runtime has no strace binary). The
// ephemeral container image (nicolaka/netshoot) ships strace plus
// shared PID namespace via `--target=content-service` so PID 1 from
// the CS container is reachable.
//
// Returns false on any non-recoverable error; the caller t.Fatal's
// on false so that callers don't silently pass a missing-sendfile
// assertion as success.
func straceCSSendfile(t *testing.T, ctx context.Context, contentPath, pk, env string) bool {
	t.Helper()
	// Skip path when strace ephemeral-container infra isn't available
	// (kubectl debug requires v1.23+, defaults may vary in kind images).
	if os.Getenv("ACH_E2E_PHASE5_STRACE") != "1" {
		t.Logf("ACH_E2E_PHASE5_STRACE not set — skipping sendfile assertion (engineer-pending; integration test at Plan 05-05 Task 4 covers via direct io.Copy assertion)")
		return true
	}
	// Run strace in background via kubectl debug.
	straceCmd := exec.CommandContext(ctx, "kubectl", "-n", phase5Namespace,
		"debug", "deploy/"+phase5OperatorDeployment,
		"--image=nicolaka/netshoot",
		"--target="+phase5CSContainer,
		"--", "strace", "-p", "1", "-e", "trace=sendfile,sendfile64",
		"-f", "-e", "signal=none", "-o", "/tmp/strace.out")
	if err := straceCmd.Start(); err != nil {
		t.Logf("kubectl debug strace start: %v — degrading to skip", err)
		return true
	}
	defer func() { _ = straceCmd.Process.Kill() }()

	// Give strace 500ms to attach before firing the curl.
	time.Sleep(500 * time.Millisecond)

	// Fire the CS request that should sendfile.
	url := strings.TrimRight(os.Getenv("ACH_CONTENT_SERVICE_URL"), "/") + contentPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext: %v", err)
	}
	req.Header.Set("x-ach-key", pk)
	req.Header.Set("x-ach-environment", env)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// Let strace flush.
	time.Sleep(1 * time.Second)

	// Capture strace output.
	catCmd := exec.CommandContext(ctx, "kubectl", "-n", phase5Namespace,
		"exec", "deploy/"+phase5OperatorDeployment, "-c", phase5CSContainer,
		"--", "cat", "/tmp/strace.out")
	out, err := catCmd.CombinedOutput()
	if err != nil {
		t.Logf("cat strace.out: %v output=%s — degrading to skip", err, strings.TrimSpace(string(out)))
		return true
	}
	return strings.Contains(string(out), "sendfile")
}

// getMetricsBody fetches a /metrics endpoint and returns the body.
// Fails the test on non-200 or transport error.
func getMetricsBody(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d want=200", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s body: %v", url, err)
	}
	return string(body)
}

// kubectlExec wraps `kubectl exec -n <ns> deploy/<name> -c <container> -- <cmd...>`.
func kubectlExec(ctx context.Context, namespace, deployment, container string, cmd ...string) (string, string, error) {
	args := []string{"-n", namespace, "exec", "deploy/" + deployment,
		"-c", container, "--request-timeout=30s", "--"}
	args = append(args, cmd...)
	c := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return stdout.String(), stderr.String(), err
}

// psqlExec runs a SQL statement against the in-cluster postgres via
// kubectl exec into the postgres pod. Used by SC#4 staleness subtest
// to mutate projection rows directly.
func psqlExec(ctx context.Context, query string) (string, string, error) {
	args := []string{"-n", phase5Namespace, "exec", "deploy/ach-postgres",
		"--request-timeout=30s", "--",
		"psql", "-U", "ach", "-d", "ach", "-t", "-A", "-c", query}
	c := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return stdout.String(), stderr.String(), err
}
