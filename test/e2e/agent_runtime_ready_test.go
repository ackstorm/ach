// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/agentrender"
)

func TestClassifyRuntimeImages(t *testing.T) {
	cases := []struct {
		name       string
		out        string
		cmdErr     error
		wantCheck  runtimeImageCheck
		wantErrSub string
	}{
		{
			name:      "both real refs",
			out:       "ghcr.io/ackstorm/ach-control:v0.1.0 ghcr.io/ackstorm/ach-execution:v0.1.0",
			wantCheck: runtimeImagesReady,
		},
		{
			name:      "control marker, execution real",
			out:       publishedControlImageMarker + " ghcr.io/ackstorm/ach-execution:v0.1.0",
			wantCheck: runtimeImagesPlaceholder,
		},
		{
			name:      "control real, execution marker",
			out:       "ghcr.io/ackstorm/ach-control:v0.1.0 " + publishedExecutionImageMarker,
			wantCheck: runtimeImagesPlaceholder,
		},
		{
			name:      "both markers",
			out:       publishedControlImageMarker + " " + publishedExecutionImageMarker,
			wantCheck: runtimeImagesPlaceholder,
		},
		{
			name:       "kubectl error",
			out:        "Error from server (NotFound): agentprofiles.ach.ackstorm.ai \"e2e-profile\" not found",
			cmdErr:     fmt.Errorf("exit status 1"),
			wantErrSub: "read e2e-profile",
		},
		{
			name:       "empty output",
			out:        "",
			wantErrSub: "malformed jsonpath result",
		},
		{
			name:       "missing execution slot",
			out:        "ghcr.io/ackstorm/ach-control:v0.1.0",
			wantErrSub: "malformed jsonpath result",
		},
		{
			name:       "missing control slot (leading space)",
			out:        " ghcr.io/ackstorm/ach-execution:v0.1.0",
			wantErrSub: "malformed jsonpath result",
		},
		{
			name:       "malformed: three tokens",
			out:        "a b c",
			wantErrSub: "malformed jsonpath result",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check, err := classifyRuntimeImages("e2e-profile", []byte(tc.out), tc.cmdErr)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("classifyRuntimeImages() err = %v, want containing %q", err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("classifyRuntimeImages() unexpected err: %v", err)
			}
			if check != tc.wantCheck {
				t.Errorf("classifyRuntimeImages() = %v, want %v", check, tc.wantCheck)
			}
		})
	}
}

// TestCheckRuntimeProfiles exercises checkRuntimeProfiles against a fake reader so the
// two-profile control flow (ach-task6-fix1-review.md: a first-profile placeholder marker
// must never hide a second-profile read/classify error behind a skip) is asserted
// directly, not just inferred from classifyRuntimeImages' own per-call cases.
func TestCheckRuntimeProfiles(t *testing.T) {
	bothReal := "ghcr.io/ackstorm/ach-control:v0.1.0 ghcr.io/ackstorm/ach-execution:v0.1.0"
	controlMarker := publishedControlImageMarker + " ghcr.io/ackstorm/ach-execution:v0.1.0"
	executionMarker := "ghcr.io/ackstorm/ach-control:v0.1.0 " + publishedExecutionImageMarker

	cases := []struct {
		name             string
		outByProfile     map[string]string
		errByProfile     map[string]error
		wantPlaceholders []string
		wantErrSub       string
		wantCalls        []string
	}{
		{
			name:         "both profiles real",
			outByProfile: map[string]string{"e2e-profile": bothReal, "e2e-profile-pvc": bothReal},
			wantCalls:    []string{"e2e-profile", "e2e-profile-pvc"},
		},
		{
			name:             "marker in profile1 control slot",
			outByProfile:     map[string]string{"e2e-profile": controlMarker, "e2e-profile-pvc": bothReal},
			wantPlaceholders: []string{"e2e-profile"},
			wantCalls:        []string{"e2e-profile", "e2e-profile-pvc"},
		},
		{
			name:             "marker in profile1 execution slot",
			outByProfile:     map[string]string{"e2e-profile": executionMarker, "e2e-profile-pvc": bothReal},
			wantPlaceholders: []string{"e2e-profile"},
			wantCalls:        []string{"e2e-profile", "e2e-profile-pvc"},
		},
		{
			name:             "marker in profile2 control slot",
			outByProfile:     map[string]string{"e2e-profile": bothReal, "e2e-profile-pvc": controlMarker},
			wantPlaceholders: []string{"e2e-profile-pvc"},
			wantCalls:        []string{"e2e-profile", "e2e-profile-pvc"},
		},
		{
			name:             "marker in profile2 execution slot",
			outByProfile:     map[string]string{"e2e-profile": bothReal, "e2e-profile-pvc": executionMarker},
			wantPlaceholders: []string{"e2e-profile-pvc"},
			wantCalls:        []string{"e2e-profile", "e2e-profile-pvc"},
		},
		{
			name:         "first marker does not hide second read error",
			outByProfile: map[string]string{"e2e-profile": controlMarker},
			errByProfile: map[string]error{"e2e-profile-pvc": fmt.Errorf("exit status 1")},
			wantErrSub:   "read e2e-profile-pvc",
			wantCalls:    []string{"e2e-profile", "e2e-profile-pvc"},
		},
		{
			name:         "first marker does not hide second malformed result",
			outByProfile: map[string]string{"e2e-profile": controlMarker, "e2e-profile-pvc": "ghcr.io/ackstorm/ach-control:v0.1.0"},
			wantErrSub:   "malformed jsonpath result",
			wantCalls:    []string{"e2e-profile", "e2e-profile-pvc"},
		},
		{
			name:         "first profile read error",
			errByProfile: map[string]error{"e2e-profile": fmt.Errorf("exit status 1")},
			wantErrSub:   "read e2e-profile",
			wantCalls:    []string{"e2e-profile"},
		},
		{
			name:         "second profile read error without any marker",
			outByProfile: map[string]string{"e2e-profile": bothReal},
			errByProfile: map[string]error{"e2e-profile-pvc": fmt.Errorf("exit status 1")},
			wantErrSub:   "read e2e-profile-pvc",
			wantCalls:    []string{"e2e-profile", "e2e-profile-pvc"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			readImages := func(profile string) ([]byte, error) {
				calls = append(calls, profile)
				return []byte(tc.outByProfile[profile]), tc.errByProfile[profile]
			}
			placeholders, err := checkRuntimeProfiles(readImages)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("checkRuntimeProfiles() err = %v, want containing %q", err, tc.wantErrSub)
				}
			} else if err != nil {
				t.Fatalf("checkRuntimeProfiles() unexpected err: %v", err)
			}
			if !reflect.DeepEqual(placeholders, tc.wantPlaceholders) {
				t.Errorf("checkRuntimeProfiles() placeholders = %v, want %v", placeholders, tc.wantPlaceholders)
			}
			if !reflect.DeepEqual(calls, tc.wantCalls) {
				t.Errorf("checkRuntimeProfiles() calls = %v, want %v", calls, tc.wantCalls)
			}
		})
	}
}

// publishedControlImageMarker/publishedExecutionImageMarker are the exact committed D3
// placeholder images (examples/agent-runtime/profile.yaml,
// test/e2e/cluster/06-agent/{profile,profile-pvc}.yaml) — the only strings that justify
// phase6RequireRealRuntimeImages skipping instead of failing.
const (
	publishedControlImageMarker   = "REPLACE_WITH_PUBLISHED_CONTROL_IMAGE"
	publishedExecutionImageMarker = "REPLACE_WITH_PUBLISHED_EXECUTION_IMAGE"
)

// runtimeImageCheck is classifyRuntimeImages' verdict for one profile's control+execution
// image pair.
type runtimeImageCheck int

const (
	runtimeImagesReady runtimeImageCheck = iota
	runtimeImagesPlaceholder
)

// classifyRuntimeImages interprets one `kubectl get agentprofile <profile> -o
// jsonpath={.spec.achagent.image} {.spec.execution.image}` result. It returns
// (runtimeImagesPlaceholder, nil) only when the command succeeded AND produced exactly
// two non-empty space-separated image references AND at least one of them is the exact
// committed D3 marker — the only condition that justifies a skip (ach-task6-review.md
// I4). A kubectl error, or a result that is empty, has a blank slot, or does not split
// into exactly two tokens, is always an error the caller must fail on, never skip: a
// profile-read failure is not evidence D3 is unresolved.
func classifyRuntimeImages(profile string, out []byte, cmdErr error) (runtimeImageCheck, error) {
	if cmdErr != nil {
		return 0, fmt.Errorf("read %s control/execution images: %w\n%s", profile, cmdErr, out)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, fmt.Errorf("%s control/execution images: malformed jsonpath result %q (want exactly two non-empty image references)", profile, string(out))
	}
	control, execution := fields[0], fields[1]
	if control == publishedControlImageMarker || execution == publishedExecutionImageMarker {
		return runtimeImagesPlaceholder, nil
	}
	return runtimeImagesReady, nil
}

// checkRuntimeProfiles checks e2e-profile and e2e-profile-pvc's control/execution images
// via readImages+classifyRuntimeImages, always reading BOTH profiles before deciding: a
// placeholder marker on the first profile never short-circuits the second profile's read,
// so a read or classify error on either one is never hidden behind a skip of the other
// (ach-task6-fix1-review.md review focus 1; an error always wins over a skip — a read
// failure is never evidence D3 is unresolved, ach-task6-review.md I4). Returns the names
// of the profiles still carrying a placeholder marker (nil if both are real) only once
// both reads succeed.
func checkRuntimeProfiles(readImages func(string) ([]byte, error)) ([]string, error) {
	var placeholders []string
	for _, profile := range []string{"e2e-profile", "e2e-profile-pvc"} {
		out, cmdErr := readImages(profile)
		check, err := classifyRuntimeImages(profile, out, cmdErr)
		if err != nil {
			return nil, err
		}
		if check == runtimeImagesPlaceholder {
			placeholders = append(placeholders, profile)
		}
	}
	return placeholders, nil
}

// phase6RequireRealRuntimeImages skips with a concrete, checkable reason when either
// synced profile (e2e-profile, e2e-profile-pvc — both exercised by this test) still
// carries a committed REPLACE_WITH_PUBLISHED_* authoring marker on its control or
// execution image (D3: no real control/execution image coordinates exist yet; the
// markers are a substitution requirement, never a real one this test can boot). This is
// a real precondition on the actual fixture state, not a vacuous always-skip: once D3 is
// resolved and both fixtures are re-applied with real images, this check passes and the
// rest of the test runs unmodified. A kubectl error or a malformed/absent image slot
// fails the test instead of skipping it (ach-task6-review.md I4): that is not evidence
// D3 is unresolved, and masking it as a skip would hide a real fixture/readability
// regression.
func phase6RequireRealRuntimeImages(t *testing.T) {
	t.Helper()
	readImages := func(profile string) ([]byte, error) {
		return exec.Command("kubectl", "-n", "ach-system", "get", "agentprofile", profile,
			"-o", "jsonpath={.spec.achagent.image} {.spec.execution.image}").CombinedOutput()
	}
	placeholders, err := checkRuntimeProfiles(readImages)
	if err != nil {
		t.Fatalf("TestAgentRuntimeReady: %v", err)
	}
	if len(placeholders) > 0 {
		t.Skipf("TestAgentRuntimeReady: D3 (real control/execution image coordinates) is not resolved — %s still carries a placeholder image; no real image to boot in kind", strings.Join(placeholders, ", "))
	}
}

// controlStatefulSetRef is the contract §11 control workload (agent-<agent name>,
// agentrender.ControlName) as a kubectl workload ref ("statefulset/...") suitable for
// `kubectl exec`/`kubectl wait`.
func controlStatefulSetRef(agentName string) string {
	return "statefulset/" + agentrender.ControlName(agentName)
}

// kubectlExecWorkload wraps `kubectl exec -n <ns> <workloadRef> -c <container> -- <cmd...>`.
// Unlike the shared kubectlExec helper (phase5_helpers_test.go), which hardcodes
// `deploy/<name>`, the control workload is a StatefulSet — workloadRef carries its own
// resource kind (see controlStatefulSetRef).
func kubectlExecWorkload(ctx context.Context, namespace, workloadRef, container string, cmd ...string) (string, string, error) {
	args := []string{"-n", namespace, "exec", workloadRef, "-c", container, "--request-timeout=30s", "--"}
	args = append(args, cmd...)
	c := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return stdout.String(), stderr.String(), err
}

// TestAgentRuntimeReady is the runtime-readiness evidence for stage 06, kept separate
// from the rendering gate in scripts/cluster.sh (WorkloadApplied): it mints a real ek_
// for the demo Environment, hands it to the fixture Secret, and requires both agents
// (ephemeral, persistent) to reach WorkloadReady=True — i.e. hydration, native
// configuration and directory preparation completed inside the pods against a reachable
// ACH origin. Nothing here bypasses hydration or relaxes readiness. The retired
// standalone/distributed placement-flip and distributed-isolation subtests are gone with
// placement itself (contract §11 scope reset, 0.1.0) — this is not a reduced-coverage
// substitute for a Workspace lifecycle test, which does not exist yet.
func TestAgentRuntimeReady(t *testing.T) {
	phase6RequireRealRuntimeImages(t)

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

	for _, name := range []string{"e2e-agent", "e2e-agent-pvc"} {
		t.Run(name+"_ready", func(t *testing.T) {
			out, err := exec.Command("kubectl", "-n", "ach-system", "wait",
				"--for=condition=WorkloadReady=true", "--timeout=300s", "achagent/"+name).CombinedOutput()
			if err != nil {
				pods, _ := exec.Command("kubectl", "-n", "ach-system", "get", "pods",
					"-l", "ach.ackstorm.ai/agent="+name+",ach.ackstorm.ai/component=control", "-o", "wide").CombinedOutput()
				t.Fatalf("%s never became WorkloadReady: %v\n%s\n%s", name, err, out, pods)
			}
		})
	}

	// Persistent standalone: the PVC must be writable by the image uid. A fresh
	// root-owned EBS PVC was not on v0.8.11 (no fsGroup); kind's local-path dirs
	// are 0777 so this cannot reproduce that, but it pins the control pod running
	// as 10001 and writing under the mountPath the harness already prepared.
	t.Run("persistent_standalone_writable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		ref := controlStatefulSetRef("e2e-agent-pvc")
		stdout, stderr, err := kubectlExecWorkload(ctx, "ach-system", ref, "agent", "sh", "-c",
			`touch /var/lib/ach-agent/.e2e-probe && rm /var/lib/ach-agent/.e2e-probe && echo $(id -u):$(id -g)`)
		if err != nil || strings.TrimSpace(stdout) != "10001:10001" {
			t.Errorf("PVC write as image uid failed: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
	})
}
