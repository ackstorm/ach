//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Live-cluster assertion for the P0-A guardrail provisioning barrier.
//
// It asserts the ONE thing that is actually enforced: POST /platform/keys is
// refused with 503 not_ready while a declared guardrail does not resolve.
//
// It deliberately does NOT assert that hydrate is refused. The hydrate handler
// reads no Environment conditions (internal/platformapi/hydrate/handler.go) and
// returns the manifest regardless — an earlier draft of this plan asserted
// otherwise and would have failed. Nor does it assert anything about forwarded
// traffic or existing keys: neither is gated. See the plan's P0-A box.
//
// The suite's LiteLLM has no Enterprise licence. That is fine here — an
// unresolved guardrail makes reconcileAccessGroup bail BEFORE ensureShellTeam
// attempts the premium-gated write, so this path never 403s.

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	// Applied by scripts/cluster.sh stage 05 and gated by verify_all — this
	// test never applies it. See references/repo-layout.md.
	guardrailEnvName = "guardrail-unresolved"
	guardrailBadName = "this-guardrail-does-not-exist"
	guardrailHealthy = "demo"
	guardrailTestNS  = "ach-system"
)

func TestGuardrailUnresolvedBlocksEkMint(t *testing.T) {
	phase6SuiteGuard(t)

	pk := phase6AcquirePk(t)
	baseURL := phase6PlatformAPIURL(t)

	// ── Precondition ────────────────────────────────────────────────────
	// verify_all already blocked on ExecutionResourcesResolved=false for this
	// fixture, and the operator writes all three conditions in ONE
	// Status().Update (environment_controller.go:305-330), so both are settled
	// by now. No retry loop: if this ever flakes, the verify_all gate from
	// Step 1 is missing, and a sleep here would only hide that.
	ag := guardrailCondition(t, guardrailEnvName, "AccessGroupSynced")
	if ag.Status != "False" || ag.Reason != "UnresolvedReferences" {
		t.Fatalf("AccessGroupSynced = %s/%s, want False/UnresolvedReferences; message=%q",
			ag.Status, ag.Reason, ag.Message)
	}
	if !strings.Contains(ag.Message, guardrailBadName) {
		t.Errorf("AccessGroupSynced message does not name the bad guardrail: %q", ag.Message)
	}

	// Named execCond, not exec: `exec` would shadow the os/exec import.
	execCond := guardrailCondition(t, guardrailEnvName, "ExecutionResourcesResolved")
	if execCond.Status != "False" || execCond.Reason != "ResourceUnresolved" {
		t.Fatalf("ExecutionResourcesResolved = %s/%s, want False/ResourceUnresolved; message=%q",
			execCond.Status, execCond.Reason, execCond.Message)
	}
	if !strings.Contains(execCond.Message, "guardrails=1") {
		t.Errorf("ExecutionResourcesResolved message missing guardrails=1: %q", execCond.Message)
	}

	// ── The barrier ─────────────────────────────────────────────────────
	t.Run("ek_mint_refused_503_not_ready", func(t *testing.T) {
		status, body := guardrailPostKey(t, baseURL, pk, guardrailEnvName)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("POST /platform/keys for %s: status = %d, want 503; body=%s",
				guardrailEnvName, status, body)
		}
		var decoded struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode error envelope: %v; body=%s", err, body)
		}
		if decoded.Error.Code != "not_ready" {
			t.Errorf("error code = %q, want not_ready; body=%s", decoded.Error.Code, body)
		}
	})

	// ── Control ─────────────────────────────────────────────────────────
	// Without this, a key endpoint broken for ANY reason would make the
	// assertion above pass for the wrong reason.
	t.Run("healthy_environment_still_mints", func(t *testing.T) {
		status, body := guardrailPostKey(t, baseURL, pk, guardrailHealthy)
		if status != http.StatusOK && status != http.StatusCreated {
			t.Fatalf("control: POST /platform/keys for %s: status = %d, want 200/201; body=%s",
				guardrailHealthy, status, body)
		}
		var created struct {
			KeyID string `json:"key_id"`
		}
		if err := json.Unmarshal(body, &created); err != nil {
			t.Fatalf("control: decode create response: %v; body=%s", err, body)
		}
		if created.KeyID == "" {
			t.Fatalf("control: empty key_id in %s", body)
		}
		t.Cleanup(func() { guardrailRevokeKey(t, baseURL, pk, created.KeyID) })
	})
}

// guardrailConditionView is one entry of status.conditions.
type guardrailConditionView struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// guardrailCondition reads one condition off a live Environment. Read-only —
// the fixture is synced, so the test must never apply or mutate it.
func guardrailCondition(t *testing.T, env, condType string) guardrailConditionView {
	t.Helper()
	out, err := exec.Command("kubectl", "-n", guardrailTestNS,
		"get", "environment", env, "-o", "json").Output()
	if err != nil {
		t.Fatalf("kubectl get environment/%s: %v", env, err)
	}
	var obj struct {
		Status struct {
			Conditions []guardrailConditionView `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("decode environment/%s: %v", env, err)
	}
	for _, c := range obj.Status.Conditions {
		if c.Type == condType {
			return c
		}
	}
	t.Fatalf("environment/%s has no %s condition; conditions=%+v",
		env, condType, obj.Status.Conditions)
	return guardrailConditionView{}
}

// guardrailPostKey issues the ek_ mint and returns the raw status + body so the
// caller can assert on either the success or the error envelope.
func guardrailPostKey(t *testing.T, baseURL, pk, env string) (int, []byte) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"environment": env,
		// Unique per run: the suite keeps the cluster up across invocations.
		"name": fmt.Sprintf("e2e-guardrail-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("marshal key request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/platform/keys", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-ach-key", pk)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("POST /platform/keys: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// guardrailRevokeKey cleans up the control arm's key. Failure is logged, not
// fatal: a leaked test key must not redden an otherwise-passing run, and the
// cluster is torn down by `make cluster-down`.
func guardrailRevokeKey(t *testing.T, baseURL, pk, keyID string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, baseURL+"/platform/keys/"+keyID, nil)
	if err != nil {
		t.Logf("cleanup: build revoke request: %v", err)
		return
	}
	req.Header.Set("x-ach-key", pk)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Logf("cleanup: revoke %s: %v", keyID, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Logf("cleanup: revoke %s: status %d: %s", keyID, resp.StatusCode, body)
	}
}
