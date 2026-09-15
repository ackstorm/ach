// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestAgentRuntimeReady is the runtime-readiness evidence for stage 06, kept
// separate from the rendering gate in scripts/cluster.sh (WorkloadApplied):
// it mints a real ek_ for the demo Environment, hands it to the fixture Secret,
// and requires BOTH agents (standalone + distributed) to reach
// WorkloadReady=True — i.e. hydration, native configuration and directory
// preparation completed inside the pods against a reachable ACH origin.
// Nothing here bypasses hydration or relaxes readiness.
func TestAgentRuntimeReady(t *testing.T) {
	baseURL := phase6PlatformAPIURL(t)
	pk := phase6AcquirePk(t)
	status, body := guardrailPostKey(t, baseURL, pk, "demo")
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("mint ek for demo: status=%d body=%s", status, body)
	}
	var created struct {
		KeyID     string `json:"key_id"`
		Plaintext string `json:"plaintext"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Plaintext == "" {
		t.Fatalf("decode ek: %v body=%s", err, body)
	}
	t.Cleanup(func() { guardrailRevokeKey(t, baseURL, pk, created.KeyID) })

	// Swap the placeholder ek into the fixture Secret; the operator's salted
	// secret hash rolls both agent pods onto the new value.
	// The patch body carries the plaintext ek: on failure log only the exit
	// error, never kubectl's output (it can echo the -p argument back).
	patch := fmt.Sprintf(`{"stringData":{"ek":%q}}`, created.Plaintext)
	if err := exec.Command("kubectl", "-n", "ach-system", "patch", "secret", "e2e-agent-ek",
		"--type", "merge", "-p", patch).Run(); err != nil {
		t.Fatalf("patch secret e2e-agent-ek: %v (output withheld: contains the ek)", err)
	}

	for _, name := range []string{"e2e-agent", "e2e-agent-dist"} {
		t.Run(name+"_ready", func(t *testing.T) {
			out, err := exec.Command("kubectl", "-n", "ach-system", "wait",
				"--for=condition=WorkloadReady=true", "--timeout=300s", "achagent/"+name).CombinedOutput()
			if err != nil {
				pods, _ := exec.Command("kubectl", "-n", "ach-system", "get", "pods",
					"-l", "ach.ackstorm.ai/agent="+name, "-o", "wide").CombinedOutput()
				t.Fatalf("%s never became WorkloadReady: %v\n%s\n%s", name, err, out, pods)
			}
		})
	}

	t.Run("distributed_isolation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		const ns, deploy = "ach-system", "achagent-e2e-agent-dist"
		// Engine must not see harness-private config/state.
		if stdout, _, _ := kubectlExec(ctx, ns, deploy, "engine", "sh", "-c",
			`test ! -e /etc/ach-agent/config.json && test ! -e /tmp/ach-agent/state && echo isolated`); !strings.Contains(stdout, "isolated") {
			t.Errorf("engine can see harness-private files: %q", stdout)
		}
		// Engine env: only the forwardEnv-selected operator var; ACH_TOKEN never.
		if stdout, _, _ := kubectlExec(ctx, ns, deploy, "engine", "sh", "-c",
			`env | grep -E '^(E2E_|ACH_TOKEN)' | sort`); strings.TrimSpace(stdout) != "E2E_FORWARDED=1" {
			t.Errorf("engine env = %q, want only E2E_FORWARDED=1", stdout)
		}
		// Read-only IPC boundaries.
		if _, stderr, err := kubectlExec(ctx, ns, deploy, "harness", "touch", "/run/ach-agent/engine/.probe"); err == nil || !strings.Contains(stderr, "Read-only") {
			t.Errorf("harness must not write the engine IPC dir: err=%v stderr=%q", err, stderr)
		}
		if _, stderr, err := kubectlExec(ctx, ns, deploy, "channels", "touch", "/run/ach-agent/channels/.probe"); err == nil || !strings.Contains(stderr, "Read-only") {
			t.Errorf("channels must not write the channels IPC dir: err=%v stderr=%q", err, stderr)
		}
		// uid/gid 10001 in every role.
		for _, c := range []string{"channels", "harness", "engine"} {
			if stdout, _, _ := kubectlExec(ctx, ns, deploy, c, "sh", "-c", `echo $(id -u):$(id -g)`); strings.TrimSpace(stdout) != "10001:10001" {
				t.Errorf("%s uid:gid = %q", c, stdout)
			}
		}
	})
}
