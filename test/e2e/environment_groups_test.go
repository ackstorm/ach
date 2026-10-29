//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestEnvironmentGroups — env-groups names NO model or MCP server, only the
// e2e-grp tag scripts/cluster.sh seeds on demo.demo-flash (model_info
// .access_groups) and demo-mcp-nojwt (mcp_access_groups). An ek_ for it
// reaches both: the MCP server because the operator expanded the tag into
// the projection the forwarder precheck reads AND into the access group; the
// model because LiteLLM expanded the tag passed through in
// access_model_names. An untagged model stays refused.
func TestEnvironmentGroups(t *testing.T) {
	phase4SuiteGuard(t)
	base := "http://" + phase4GatewayAuthority(t)
	jwt := oauthLogin(t, base, "").Access

	out, err := exec.Command("kubectl", "-n", phase4Namespace, "get", "environment", "env-groups",
		"-o", "jsonpath={.status.expandedRuntime.mcpServers}").Output()
	if err != nil {
		t.Fatalf("read env-groups status: %v", err)
	}
	if !strings.Contains(string(out), costedMcpServer) {
		t.Fatalf("status.expandedRuntime.mcpServers = %s, want %s", out, costedMcpServer)
	}

	k := keysAPI{t: t, base: base, jwt: jwt}
	keyID, ek := k.create("env-groups", fmt.Sprintf("groups-%d", time.Now().UnixNano()), nil)
	t.Cleanup(func() { _ = k.revoke(keyID) })

	if code, body := mcpCall(t, base, ek, costedMcpServer); code != http.StatusOK {
		t.Fatalf("MCP via mcpServerGroups: status=%d body=%s, want 200", code, body)
	}
	if code := callViaForwarderChatCompletion(t, base, ek, "demo.demo-flash"); code != http.StatusOK {
		t.Fatalf("chat on a model granted via modelGroups: status=%d, want 200", code)
	}
	if code := callViaForwarderChatCompletion(t, base, ek, "demo-model"); code == http.StatusOK {
		t.Fatalf("chat on an untagged model: status=200, want refused")
	}
}
