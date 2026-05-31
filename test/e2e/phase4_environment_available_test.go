//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// E2E acceptance for TODO §9: kubectl wait --for=condition=Available
// returns within 60s for an Environment whose required sub-conditions
// reach True. Mirrors TODO §16 line 505 verbatim.
//
// Engineer-pending status — opt out via ACH_SKIP_PHASE4=1. The
// example fixture examples/04-environment-demo.yaml references 5
// LiteLLM resources (gemini.gemini-flash-latest, openai.gpt-5-mini,
// vmcp-dev, vmcp-aws, test-noop-agent) that today's seed LiteLLM
// catalogue does NOT contain (only gpt-3.5-turbo + fake-openai-endpoint
// per the §16 note in TODO.md). Until §16 lands a seeded UAT
// LiteLLM, ExecutionResourcesResolved stays False and Available stays
// False; the wait correctly times out. The test is skipped to keep
// the suite green; set ACH_SKIP_PHASE4=1 once §16's seed lands.

package e2e

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestEnvironmentAvailableE2E asserts the §9 acceptance contract: with
// a fully-resolved Environment, `kubectl wait --for=condition=Available
// environment/<name>` exits 0 within 60s. Maps to TODO §16's
// post-§7+§9 validation gate.
func TestEnvironmentAvailableE2E(t *testing.T) {
	if os.Getenv("ACH_SKIP_PHASE4") == "1" {
		t.Skip("§9 e2e (phase4); opt out via ACH_SKIP_PHASE4=1. The synced cluster seeds the demo LiteLLM resources.")
	}

	const (
		// The synced "demo" Environment lives in ach-system (see
		// test/e2e/cluster/05-environment/demo.yaml); the suite asserts
		// against the synced fixture rather than applying its own.
		namespace = "ach-system"
		envName   = "demo"
	)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx,
		"kubectl", "wait",
		"--for=condition=Available",
		"environment/"+envName,
		"-n", namespace,
		"--timeout=60s",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl wait --for=condition=Available environment/%s -n %s failed: %v\noutput: %s",
			envName, namespace, err, strings.TrimSpace(string(out)))
	}
	t.Logf("OK: %s", strings.TrimSpace(string(out)))
}
