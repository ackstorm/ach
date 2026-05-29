//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Package e2e is the build-tag-gated end-to-end suite (Plan 01-11 Task 5).
// It targets a kind cluster, applies the full config/default kustomization,
// and asserts the five Phase 1 ROADMAP Success Criteria. Hidden behind the
// `e2e` build tag so `go test ./...` and `make test` skip it — the kind
// dependency would otherwise break developers without a Docker daemon.
//
// The suite uses stdlib `testing` (no Ginkgo) so it mirrors the envtest
// pass in idiom — one TestMain bootstrap, sub-T's per assertion, shell-out
// to kubectl/psql for observable state.
//
// Activation: ./scripts/dev.sh make test-e2e — sets KIND_CLUSTER and the
// OPERATOR_IMAGE env vars; defaults are ach-e2e + ach-operator:latest.
package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Defaults for kind-cluster name, namespace, and image tags. Overridable
// via env vars so a CI matrix can re-tag the images and reuse the suite.
const (
	defaultKindCluster   = "ach-e2e"
	defaultNamespace     = "ach-system"
	defaultOperatorImage = "ach-operator:latest"
)

// Suite-global state populated by TestMain and read by every subtest.
var (
	kindCluster   string
	namespace     string
	operatorImage string
	keepCluster   bool
)

// TestMain spins up (or reuses) a kind cluster, loads the five ACH
// container images, applies the full manifest set, and waits for the
// Operator Pod to become Ready. After m.Run() it optionally deletes
// the cluster (controlled by E2E_KEEP_CLUSTER=1).
//
// If `kind` is not on PATH inside the devtools container, TestMain
// exits 0 with a SKIP message — this lets `make test-e2e` run on
// developer machines without kind installed (CI runs the full path).
func TestMain(m *testing.M) {
	kindCluster = envOr("E2E_KIND_CLUSTER", defaultKindCluster)
	namespace = envOr("E2E_NAMESPACE", defaultNamespace)
	operatorImage = envOr("OPERATOR_IMAGE", defaultOperatorImage)
	keepCluster = os.Getenv("E2E_KEEP_CLUSTER") == "1"

	// When invoked from scripts/cluster.sh (make e2e / e2e-full / e2e-keep),
	// the orchestrator has already created the cluster and applied
	// config/e2e. Skip setupCluster()/teardownCluster() entirely so the
	// overlay patches (replicas, env injection, Secret URL) survive — a
	// re-apply of config/default would revert them.
	if os.Getenv("E2E_SKIP_SETUP") == "1" {
		fmt.Fprintln(os.Stderr, "E2E_SKIP_SETUP=1 — orchestrator owns cluster lifecycle")
		os.Exit(m.Run())
	}

	if _, err := exec.LookPath("kind"); err != nil {
		fmt.Fprintln(os.Stderr, "SKIP: kind not on PATH — e2e suite requires a Docker-backed kind binary")
		os.Exit(0)
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		fmt.Fprintln(os.Stderr, "SKIP: kubectl not on PATH — e2e suite requires kubectl")
		os.Exit(0)
	}

	if err := setupCluster(); err != nil {
		fmt.Fprintf(os.Stderr, "setup cluster: %v\n", err)
		os.Exit(1)
	}

	rc := m.Run()

	if !keepCluster {
		teardownCluster()
	}
	os.Exit(rc)
}

// setupCluster creates the kind cluster if missing, loads the five
// container images (build them via `make docker-build` first if not
// already present), applies config/default, and waits for the Operator
// Pod to become Ready.
func setupCluster() error {
	// 1. Cluster exists?
	if !clusterExists(kindCluster) {
		out, err := runCmd("kind", "create", "cluster",
			"--name", kindCluster,
			"--config", "../../scripts/kind-config.yaml",
		)
		if err != nil {
			return fmt.Errorf("kind create cluster: %v\n%s", err, out)
		}
	}

	// 2. Load images. Each `kind load docker-image` is a no-op when the
	// image is already loaded — safe to rerun.
	for _, img := range []string{
		operatorImage,
		envOr("PLATFORM_API_IMAGE", "ach-platform-api:latest"),
		envOr("FORWARDER_IMAGE", "ach-forwarder:latest"),
		envOr("CONTENT_SERVICE_IMAGE", "ach-content-service:latest"),
	} {
		if out, err := runCmd("kind", "load", "docker-image", img, "--name", kindCluster); err != nil {
			return fmt.Errorf("kind load %s: %v\n%s", img, err, out)
		}
	}

	// 3. Apply the full manifest set.
	if out, err := runCmd("kubectl", "apply", "-k", "../../config/default"); err != nil {
		return fmt.Errorf("kubectl apply -k config/default: %v\n%s", err, out)
	}

	// 4. Wait for the Operator Deployment to roll out.
	if out, err := runCmd("kubectl", "rollout", "status", "deployment/ach-operator",
		"-n", namespace, "--timeout=2m",
	); err != nil {
		return fmt.Errorf("rollout ach-operator: %v\n%s", err, out)
	}

	return nil
}

// teardownCluster deletes the kind cluster so rerunning the suite
// starts from a clean slate. Suppress errors — this is best-effort.
func teardownCluster() {
	out, err := runCmd("kind", "delete", "cluster", "--name", kindCluster)
	if err != nil {
		fmt.Fprintf(os.Stderr, "teardown kind cluster: %v\n%s\n", err, out)
	}
}

// clusterExists shells out to `kind get clusters` and checks for an
// exact name match.
func clusterExists(name string) bool {
	out, err := runCmd("kind", "get", "clusters")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// runCmd is the shared exec.Cmd shell helper. Returns combined stdout +
// stderr as a string plus the underlying error. 60-second per-command
// timeout via context — slow kubectl calls fail fast rather than hanging
// the suite.
func runCmd(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runCmdLonger runs a command with a configurable timeout — for kubectl
// wait / kubectl exec into Postgres which legitimately take >60s during
// Pod warmup.
func runCmdLonger(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// envOr returns the env var or a default when unset/empty.
func envOr(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}
