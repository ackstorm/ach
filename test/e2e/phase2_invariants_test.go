//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 2 invariants e2e suite.
//
// Phase 02 ROADMAP Success Criteria #1–#3 covered Plugin publish +
// PluginMarketplace three-stage materialize + same-plugin-name across two
// marketplaces. Those kinds are now disabled behind
// featuregate.PluginsEnabled=false and their CRDs are no longer shipped in the
// Helm chart, so the e2e suite cannot apply or assert against Plugin /
// PluginMarketplace CRs. The reconciler logic stays covered by envtest (which
// loads the CRDs from config/crd/bases). The Phase 2 plugin/marketplace
// subtests were therefore removed; only the shared condition helpers below
// remain (phase4_* and others still call them).

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ─── Helpers ───────────────────────────────────────────────────────────
//
// waitForCondition / getConditionField / dumpOperatorLogs are shared across
// the e2e suite (phase4_* also call waitForCondition and dumpOperatorLogs).
// They live here for historical reasons; keep them even if the Phase 2
// subtests stop using one.

// waitForCondition polls until the named CR has the given condition.type
// matching the expected status. t.Fatalf on timeout.
func waitForCondition(t *testing.T, kind, name, condType, expectedStatus string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	jsonpath := fmt.Sprintf(`jsonpath={.status.conditions[?(@.type=="%s")].status}`, condType)
	for time.Now().Before(deadline) {
		out, err := runCmd("kubectl", "get", kind, name, "-n", namespace, "-o", jsonpath)
		if err == nil && strings.TrimSpace(out) == expectedStatus {
			return
		}
		time.Sleep(3 * time.Second)
	}
	dumpOperatorLogs(t)
	t.Fatalf("waitForCondition %s/%s type=%s want %q: timeout after %s",
		kind, name, condType, expectedStatus, timeout)
}

// getConditionField reads a single jsonpath-selectable field off the named
// condition on the CR. Returns the trimmed string; t.Fatalf if kubectl errors.
func getConditionField(t *testing.T, kind, name, condType, field string) string {
	t.Helper()
	jsonpath := fmt.Sprintf(`jsonpath={.status.conditions[?(@.type=="%s")].%s}`, condType, field)
	out, err := runCmd("kubectl", "get", kind, name, "-n", namespace, "-o", jsonpath)
	if err != nil {
		t.Fatalf("getConditionField %s/%s type=%s field=%s: %v\n%s", kind, name, condType, field, err, out)
	}
	return strings.TrimSpace(out)
}

// dumpOperatorLogs prints recent operator logs for postmortem visibility.
// Best-effort — never fails the test from this helper.
func dumpOperatorLogs(t *testing.T) {
	t.Helper()
	out, err := runCmd("kubectl", "logs", "-n", namespace,
		"deployment/ach-operator", "-c", "manager", "--tail=200")
	if err == nil {
		t.Logf("=== operator logs (tail=200) ===\n%s\n=== end operator logs ===", out)
	}
}
