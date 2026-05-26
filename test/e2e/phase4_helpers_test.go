//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 4 invariants helpers — Plan 04-09.
//
// Shared helpers used by phase4_invariants_test.go. Mirrors the Phase 3
// pattern: stdlib testing, kubectl orchestration, no Ginkgo.
//
// Engineer-pending verification debt: the suite is mechanically correct
// but defers prerequisite acquisition (pk_/ek_ keys via Phase 3 SSO,
// LiteLLM mock provisioning, BIP fixture seeding) to engineer action
// once the kind cluster is up per CLAUDE.md "E2E debug loop".

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	// phase4Namespace is where the Helm chart installs ach-forwarder.
	phase4Namespace = "default"
	// phase4ForwarderDeployment is the Deployment name created by the Helm
	// chart (matches deploy/helm/ach/templates/forwarder-deployment.yaml).
	phase4ForwarderDeployment = "ach-forwarder"
)

// phase4SuiteGuard skips when prerequisites aren't met.
func phase4SuiteGuard(t *testing.T) {
	t.Helper()
	if os.Getenv("ACH_E2E_PHASE4") != "1" {
		t.Skipf("Phase 4 e2e suite gated behind ACH_E2E_PHASE4=1 + live kind+Helm cluster with Forwarder Ready; see CLAUDE.md `E2E debug loop`. Skipping (engineer-pending).")
	}
	if err := waitForwarderReady(t, 15*time.Second); err != nil {
		t.Skipf("Forwarder Deployment %s/%s not Ready (%v) — run `make e2e-keep` first. Skipping (engineer-pending).",
			phase4Namespace, phase4ForwarderDeployment, err)
	}
}

// waitForwarderReady polls the Forwarder Deployment until it has
// status.readyReplicas > 0 or the deadline expires. Uses kubectl rollout
// status under a bounded timeout — never an unbounded polling loop.
func waitForwarderReady(t *testing.T, deadline time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "-n", phase4Namespace,
		"rollout", "status", "deployment/"+phase4ForwarderDeployment,
		fmt.Sprintf("--timeout=%ds", int(deadline.Seconds())))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl rollout: %v output=%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// mustAcquirePk acquires a pk_ via the Phase 3 SSO flow.
// Engineer-pending: stub returning a Skip until the in-cluster SSO mock
// is wired into the Phase 4 e2e harness.
func mustAcquirePk(t *testing.T) string {
	t.Helper()
	pk := os.Getenv("ACH_E2E_PK_FIXTURE")
	if pk == "" {
		t.Skipf("ACH_E2E_PK_FIXTURE not set — provision a pk_ via Phase 3 SSO then export it. Skipping (engineer-pending).")
	}
	return pk
}

// mustAcquireEkBoundToEnv acquires an ek_ bound to the given Environment.
// Engineer-pending: stub returning a Skip until the env-keys provisioning
// fixture is wired.
func mustAcquireEkBoundToEnv(t *testing.T, env string) string {
	t.Helper()
	key := os.Getenv("ACH_E2E_EK_FIXTURE_" + strings.ToUpper(env))
	if key == "" {
		t.Skipf("ACH_E2E_EK_FIXTURE_%s not set — provision an ek_ bound to env %q. Skipping (engineer-pending).",
			strings.ToUpper(env), env)
	}
	return key
}

// phase4AssertSecretRbacNegative verifies that a non-forwarder
// ServiceAccount cannot get the ach-jwt-signing-keys Secret. Uses
// `kubectl auth can-i` rather than performing a real Get (the negative
// path doesn't need real RBAC enforcement to be exercised; can-i
// inspects the same authz pipeline).
func phase4AssertSecretRbacNegative(t *testing.T) error {
	t.Helper()
	// Use the platform-api ServiceAccount as the negative-test subject —
	// it MUST NOT have read on ach-jwt-signing-keys (only ach-forwarder does).
	cmd := exec.Command("kubectl", "-n", phase4Namespace,
		"auth", "can-i", "get", "secret/ach-jwt-signing-keys",
		"--as=system:serviceaccount:"+phase4Namespace+":ach-platform-api")
	out, err := cmd.CombinedOutput()
	verdict := strings.TrimSpace(string(out))
	if verdict == "yes" {
		return fmt.Errorf("platform-api ServiceAccount unexpectedly CAN read ach-jwt-signing-keys; RBAC carve-out missing")
	}
	if err != nil && verdict != "no" {
		// kubectl auth can-i returns exit 1 on "no" — only error if output
		// is neither "yes" nor "no".
		return fmt.Errorf("kubectl auth can-i: %v output=%s", err, verdict)
	}
	return nil
}

// _ ensures errors stays referenced — helpers may grow into wrapping.
var _ = errors.New
