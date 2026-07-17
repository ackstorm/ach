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
// `make e2e-full`.

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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
	// phase5EnvFixtureName matches the synced Environment fixture
	// test/e2e/cluster/05-environment/env-valid.yaml.
	phase5EnvFixtureName = "env-valid"
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
	if os.Getenv("ACH_SKIP_PHASE5") == "1" {
		t.Skipf("Phase 5 e2e suite opted out via ACH_SKIP_PHASE5=1 (default: runs against the synced cluster).")
	}
	required := []string{
		"ACH_CONTENT_SERVICE_URL",
		"ACH_PLATFORM_API_URL",
		"ACH_FORWARDER_URL",
		"ACH_OPERATOR_METRICS_URL",
	}
	for _, k := range required {
		if os.Getenv(k) == "" {
			t.Fatalf("%s not set — required for a phase5 run (set ACH_SKIP_PHASE5=1 to opt out).", k)
		}
	}
	if err := waitOperatorReady(t, 30*time.Second); err != nil {
		t.Fatalf("operator Deployment %s/%s not Ready (%v) — cluster must be up (set ACH_SKIP_PHASE5=1 to opt out).",
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
func seedPhase5Fixtures(t *testing.T, _ context.Context) (pk, ek, env string) {
	t.Helper()
	// The fixtures this helper used to apply (Environment prod + Plugin
	// foo / Prompt bar / Artifact baz) are now part of the SYNCED set
	// (test/e2e/cluster/{04-objects,05-environment}): scripts/cluster.sh
	// applies them at bring-up and verify_all gates them healthy before
	// any test runs. The suite asserts against that synced state rather
	// than hydrating its own copies, so this helper only mints the
	// pk_/ek_ identity the content-service assertions need.
	pk = mustAcquirePk(t)
	ek = mustAcquireEkBoundToEnv(t, phase5EnvFixtureName)
	env = phase5EnvFixtureName
	return pk, ek, env
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
// Returns false only when strace ran and observed no sendfile; the caller
// t.Fatal's on false. When the assertion cannot be performed at all (opt-out
// unset, or the kubectl-debug infra is unavailable) this t.Skip's rather than
// returning true — returning true reported "verified" for a check that never
// ran. Call it from a subtest so the skip does not swallow the caller's
// remaining assertions.
func straceCSSendfile(t *testing.T, ctx context.Context, contentPath, pk, env string) bool {
	t.Helper()
	// Opt-in gate: strace needs an ephemeral debug container (kubectl debug
	// v1.23+, and the kind image must allow it). t.Skip (not `return true`) so
	// an unset var reports NOT VERIFIED instead of a green sendfile assertion —
	// ACH_E2E_PHASE5_STRACE is currently set nowhere in CI, so `return true`
	// meant this check had never actually run. Integration coverage for the
	// same discipline: Plan 05-05 Task 4 (direct io.Copy assertion).
	if os.Getenv("ACH_E2E_PHASE5_STRACE") != "1" {
		t.Skipf("ACH_E2E_PHASE5_STRACE != 1 — sendfile syscall assertion NOT verified here (integration coverage: Plan 05-05 Task 4)")
	}
	// Run strace in background via kubectl debug.
	straceCmd := exec.CommandContext(ctx, "kubectl", "-n", phase5Namespace,
		"debug", "deploy/"+phase5OperatorDeployment,
		"--image=nicolaka/netshoot",
		"--target="+phase5CSContainer,
		"--", "strace", "-p", "1", "-e", "trace=sendfile,sendfile64",
		"-f", "-e", "signal=none", "-o", "/tmp/strace.out")
	if err := straceCmd.Start(); err != nil {
		t.Skipf("kubectl debug strace start: %v — sendfile syscall assertion NOT verified", err)
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
		t.Skipf("cat strace.out: %v output=%s — sendfile syscall assertion NOT verified", err, strings.TrimSpace(string(out)))
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
	// postgres runs as a StatefulSet (ach-postgres-0), NOT a Deployment.
	// Exec into it and supply the e2e fixture password via PGPASSWORD (psql
	// prompts and fails non-interactively otherwise). The query is passed as
	// a positional arg ($1) to sh -c so its embedded quotes/semicolons need
	// no extra escaping.
	args := []string{"-n", phase5Namespace, "exec", "statefulset/ach-postgres",
		"--request-timeout=30s", "--",
		"sh", "-c", `PGPASSWORD=ach psql -U ach -d ach -t -A -c "$1"`, "sh", query}
	c := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return stdout.String(), stderr.String(), err
}
