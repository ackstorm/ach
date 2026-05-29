//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// E2E coverage for issue #17 — Access Group /v1 migration.
//
// Two specs:
//   - HappyPath: examples/04-environment-demo.yaml reaches
//     AccessGroupSynced=True/Synced within 30s (relies on
//     hydrate_litellm seeding demo-model / demo-mcp / demo-agent).
//   - UnresolvedReferences: examples/04b-environment-unresolved.yaml
//     reaches AccessGroupSynced=False/UnresolvedReferences within 30s.
//
// Both gated behind ACH_E2E_PHASE9=1, matching the existing phase4
// promotion e2e gate.

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestAccessGroupSynced_Demo_HappyPath asserts the demo fixture flips
// to True/Synced once the operator reconciles. Requires hydrate_litellm
// to have seeded the demo Model / MCP / A2A.
func TestAccessGroupSynced_Demo_HappyPath(t *testing.T) {
	if os.Getenv("ACH_E2E_PHASE9") != "1" {
		t.Skip("§17 e2e gated behind ACH_E2E_PHASE9=1")
	}
	// The "demo" Environment is pre-synced by cluster.sh (reconcile_examples).
	// Tests assert against the live, already-synced cluster — they do NOT apply
	// fixtures (that would mutate shared state other specs depend on).
	if !waitForConditionTriple(t, "environment", "demo", "ach-system",
		"AccessGroupSynced", "True", "Synced", 30*time.Second) {
		dumpAGConditions(t, "demo")
		t.Fatalf("demo Environment did NOT reach AccessGroupSynced=True/Synced within 30s")
	}
}

// TestAccessGroupSynced_DemoUnresolved_FlipsToUnresolvedReferences
// asserts the negative-path fixture flips to False/UnresolvedReferences.
func TestAccessGroupSynced_DemoUnresolved_FlipsToUnresolvedReferences(t *testing.T) {
	if os.Getenv("ACH_E2E_PHASE9") != "1" {
		t.Skip("§17 e2e gated behind ACH_E2E_PHASE9=1")
	}
	// The "demo-unresolved" Environment is pre-synced by cluster.sh
	// (reconcile_examples). Assert against the live cluster; do not apply.
	if !waitForConditionTriple(t, "environment", "demo-unresolved", "ach-system",
		"AccessGroupSynced", "False", "UnresolvedReferences", 30*time.Second) {
		dumpAGConditions(t, "demo-unresolved")
		t.Fatalf("demo-unresolved Environment did NOT reach AccessGroupSynced=False/UnresolvedReferences within 30s")
	}
}

// waitForConditionTriple polls until a condition matches (type, status,
// reason) all three, or the deadline expires. Returns true on success.
func waitForConditionTriple(t *testing.T, kind, name, ns, condType, status, reason string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command("kubectl", "-n", ns, "get", kind, name, "-o", "json").Output()
		if err == nil {
			var obj struct {
				Status struct {
					Conditions []struct {
						Type, Status, Reason string
					}
				}
			}
			if jerr := json.Unmarshal(out, &obj); jerr == nil {
				for _, c := range obj.Status.Conditions {
					if c.Type == condType && c.Status == status && c.Reason == reason {
						return true
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// dumpAGConditions prints the Environment's status.conditions for debug.
func dumpAGConditions(t *testing.T, name string) {
	t.Helper()
	out, _ := exec.Command("kubectl", "-n", "ach-system", "get",
		"environment", name, "-o", "jsonpath={.status.conditions}").Output()
	t.Logf("environment/%s conditions: %s", name, out)
}
