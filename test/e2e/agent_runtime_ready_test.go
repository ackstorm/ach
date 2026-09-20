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
// and requires ALL THREE agents (standalone ephemeral, distributed, standalone
// persistent) to reach WorkloadReady=True — i.e. hydration, native
// configuration and directory preparation completed inside the pods against a
// reachable ACH origin. Nothing here bypasses hydration or relaxes readiness.
func TestAgentRuntimeReady(t *testing.T) {
	baseURL := phase6PlatformAPIURL(t)
	pk := phase6AcquirePk(t).Access
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

	for _, name := range []string{"e2e-agent", "e2e-agent-dist", "e2e-agent-pvc"} {
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

	// Persistent standalone: the PVC must be writable by the image uid. A fresh
	// root-owned EBS PVC was not on v0.8.11 (no fsGroup); kind's local-path dirs
	// are 0777 so this cannot reproduce that, but it pins the pod running as
	// 10001 and writing under the mountPath the harness already prepared.
	t.Run("persistent_standalone_writable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		stdout, stderr, err := kubectlExec(ctx, "ach-system", "achagent-e2e-agent-pvc", "agent", "sh", "-c",
			`touch /var/lib/ach-agent/.e2e-probe && rm /var/lib/ach-agent/.e2e-probe && echo $(id -u):$(id -g)`)
		if err != nil || strings.TrimSpace(stdout) != "10001:10001" {
			t.Errorf("PVC write as image uid failed: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
	})

	// PVC placement transition (ach-agent v0.16.5 contract): populate the standalone
	// PVC, flip ONLY spec.placement to distributed, and require the same PVC, the
	// same absolute workspace path (base/home/workspace) with its contents, the
	// harness confined to that nested dir (no access to the rest of engine home),
	// and a writable nested workspace as uid 10001. Native session continuity and a
	// second invocation need a live model + webhook and are validated by ach-agent.
	t.Run("pvc_placement_transition", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		const ns, agent, deploy = "ach-system", "e2e-agent-pvc", "achagent-e2e-agent-pvc"
		const base = "/var/lib/ach-agent"
		kc := func(args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, "kubectl", append([]string{"-n", ns}, args...)...).CombinedOutput()
			return strings.TrimSpace(string(out)), err
		}
		pvBefore, err := kc("get", "pvc", deploy, "-o", "jsonpath={.spec.volumeName}")
		if err != nil || pvBefore == "" {
			t.Fatalf("pvc volumeName: %v %q", err, pvBefore)
		}
		// Standalone must already keep the workspace under home (the layout the
		// transition preserves); populate both the workspace and engine home.
		if stdout, stderr, err := kubectlExec(ctx, ns, deploy, "agent", "sh", "-c",
			`test -d `+base+`/home/workspace && echo ws > `+base+`/home/workspace/.e2e-ws && echo home > `+base+`/home/.e2e-home && echo populated`); err != nil || !strings.Contains(stdout, "populated") {
			t.Fatalf("populate standalone PVC (home/workspace must exist): err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		t.Cleanup(func() {
			// Back to standalone so the fixture is idempotent for re-runs; the
			// markers stay harmless but are removed anyway.
			cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer ccancel()
			_ = exec.CommandContext(cctx, "kubectl", "-n", ns, "patch", "achagent", agent, "--type", "json",
				"-p", `[{"op":"remove","path":"/spec/placement"}]`).Run()
			_ = exec.CommandContext(cctx, "kubectl", "-n", ns, "wait", "--timeout=120s",
				`--for=jsonpath={.spec.template.spec.containers[?(@.name=="agent")].name}=agent`, "deploy/"+deploy).Run()
			_ = exec.CommandContext(cctx, "kubectl", "-n", ns, "rollout", "status", "--timeout=300s", "deploy/"+deploy).Run()
			_, _, _ = kubectlExec(cctx, ns, deploy, "agent", "rm", "-f", base+"/home/workspace/.e2e-ws", base+"/home/.e2e-home")
		})
		if out, err := kc("patch", "achagent", agent, "--type", "merge", "-p", `{"spec":{"placement":"distributed"}}`); err != nil {
			t.Fatalf("patch placement: %v %s", err, out)
		}
		for _, w := range [][]string{
			{"wait", "--timeout=120s", `--for=jsonpath={.spec.template.spec.containers[?(@.name=="engine")].name}=engine`, "deploy/" + deploy},
			{"rollout", "status", "--timeout=300s", "deploy/" + deploy},
			{"wait", "--for=condition=WorkloadReady=true", "--timeout=300s", "achagent/" + agent},
		} {
			if out, err := kc(w...); err != nil {
				pods, _ := kc("get", "pods", "-l", "ach.ackstorm.ai/agent="+agent, "-o", "wide")
				t.Fatalf("%v: %v\n%s\n%s", w, err, out, pods)
			}
		}
		if pvAfter, _ := kc("get", "pvc", deploy, "-o", "jsonpath={.spec.volumeName}"); pvAfter != pvBefore {
			t.Errorf("PV changed across the flip: %q → %q", pvBefore, pvAfter)
		}
		// Harness: same workspace path + contents, writable, nothing else of home.
		if stdout, stderr, err := kubectlExec(ctx, ns, deploy, "harness", "sh", "-c",
			`cat `+base+`/home/workspace/.e2e-ws && touch `+base+`/home/workspace/.e2e-h && rm `+base+`/home/workspace/.e2e-h && test ! -e `+base+`/home/.e2e-home && ls `+base+`/home`); err != nil || strings.TrimSpace(stdout) != "ws\nworkspace" {
			t.Errorf("harness workspace view: err=%v stdout=%q stderr=%q (want marker + only 'workspace' under home)", err, stdout, stderr)
		}
		// Engine: whole home, workspace included, at the same path.
		if stdout, _, err := kubectlExec(ctx, ns, deploy, "engine", "sh", "-c",
			`cat `+base+`/home/.e2e-home `+base+`/home/workspace/.e2e-ws`); err != nil || strings.TrimSpace(stdout) != "home\nws" {
			t.Errorf("engine home view: err=%v stdout=%q", err, stdout)
		}
	})

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
