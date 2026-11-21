//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestAuditCallEvents — the LiteLLM callback examples/litellm/ach_audit.py
// (mounted by scripts/cluster.sh) writes one audit line per model call and
// per MCP tool call made through the forwarder, a call LiteLLM refuses
// included. The line names the caller, the ACH key and the Environment, and
// carries no request content.
func TestAuditCallEvents(t *testing.T) {
	phase4SuiteGuard(t)
	base := "http://" + phase4GatewayAuthority(t)
	k := keysAPI{t: t, base: base, jwt: oauthLogin(t, base, "").Access}
	keyID, ek := k.create("env-groups", fmt.Sprintf("audit-%d", time.Now().UnixNano()), nil)
	t.Cleanup(func() { _ = k.revoke(keyID) })

	if code, body := mcpCall(t, base, ek, costedMcpServer); code != http.StatusOK {
		t.Fatalf("MCP call: status=%d body=%s, want 200", code, body)
	}
	if code := callViaForwarderChatCompletion(t, base, ek, "demo.demo-flash"); code != http.StatusOK {
		t.Fatalf("chat: status=%d, want 200", code)
	}
	// env-groups does not grant demo-model: LiteLLM refuses the call.
	if code := callViaForwarderChatCompletion(t, base, ek, "demo-model"); code == http.StatusOK {
		t.Fatalf("chat on a model outside the Environment: status=200, want refused")
	}

	want := []string{ // action, outcome, target.name
		"litellm.tool.call success " + costedMcpServer,
		"litellm.model.call success demo.demo-flash",
		"litellm.model.call failed demo-model",
	}
	// The callback runs after the response is sent.
	deadline := time.Now().Add(30 * time.Second)
	for {
		got := map[string]map[string]any{}
		for _, line := range auditLinesForKey(t, keyID) {
			got[fmt.Sprintf("%v %v %v", line["action"], line["outcome"], line["target.name"])] = line
		}
		missing := ""
		for _, w := range want {
			if _, ok := got[w]; !ok {
				missing = w
			}
		}
		if missing == "" {
			for key, line := range got {
				if actor, _ := line["actor"].(string); line["environment"] != "env-groups" || !strings.Contains(actor, "@") {
					t.Errorf("%s: environment, actor = %v, %v; want env-groups and the caller's email", key, line["environment"], line["actor"])
				}
			}
			if code := got[want[2]]["error.code"]; code != "403" {
				t.Errorf("refused call: error.code = %v, want 403", code)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no audit line %q for %s after 30s; got %v", missing, keyID, got)
		}
		time.Sleep(time.Second)
	}
}

// auditLinesForKey returns the LiteLLM audit lines that name keyID. It fails
// the test when one carries the MCP call's argument text.
func auditLinesForKey(t *testing.T, keyID string) []map[string]any {
	t.Helper()
	out, err := exec.Command("kubectl", "-n", "litellm-system", "logs", "deploy/litellm", "-c", "litellm", "--since=5m").Output()
	if err != nil {
		t.Fatalf("read litellm logs: %v", err)
	}
	var lines []map[string]any
	for _, raw := range strings.Split(string(out), "\n") {
		if !strings.Contains(raw, `"audit":true`) || !strings.Contains(raw, keyID) {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("audit line is not JSON: %v: %s", err, raw)
		}
		if strings.Contains(raw, "tag-budget") {
			t.Fatalf("audit line carries the tool arguments: %s", raw)
		}
		lines = append(lines, line)
	}
	return lines
}
