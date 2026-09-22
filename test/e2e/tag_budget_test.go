//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	// costedMcpServer is the demo MCP server this test temporarily prices.
	// It is already in the `demo` Environment's runtime.mcpServers, so an
	// ek_ passes precheck against it; a brand-new server name would 403.
	costedMcpServer = "demo-mcp-nojwt"

	// mcpQueryCost is booked per tools/call while the cost is attached.
	mcpQueryCost = 0.25

	// tagBudgetCacheWindow bounds LiteLLM's tag-budget cache: a budget
	// change was measured taking effect in ≈10 s on an idle proxy
	// (references/litellm-permission-model.md §15), but a RAISED ceiling
	// took longer than 30 s to unblock on the e2e box, so this is generous
	// on purpose — the assertion is that the change lands, not how fast.
	tagBudgetCacheWindow = 150 * time.Second

	// tagSpendWindow bounds how long LiteLLM may take to BOOK the spend that
	// pushes a tag over budget. Spend is written on a batched spend-log
	// flush, not synchronously with the request, so this is much longer than
	// the cache window — and it is why the test waits on the 429 itself
	// rather than on a spend figure.
	tagSpendWindow = 180 * time.Second
)

// TestTagBudgets — the three ceilings are live and independent:
//
//  1. a per-ek budget set through PATCH /platform/keys/{id}/budget refuses
//     that key's traffic once its spend exceeds it — the 429 IS the proof
//     that the forwarder stamped `key:<id>` and that LiteLLM booked spend
//     against it, with no dependence on any spend projection;
//  2. the owner's pk_ keeps working meanwhile — independent counters, no
//     hierarchy;
//  3. raising the ek budget unblocks it within the cache window;
//  4. revoke reaps the tag and its budget object.
//
// Spend is generated through a COSTED MCP server, not /v1: the e2e mock LLM
// answers every completion with `usage {0,0,0}`, so demo-model traffic costs
// exactly 0 no matter how much of it you send, and `spend > max_budget` can
// never be true — not even against a ceiling of 0. An MCP server carrying
// `mcp_info.mcp_server_cost_info.default_cost_per_query` books that amount
// per tools/call to the key AND to every tag on the request, which is the
// only priced path this cluster has. The cost is attached to the existing
// demo-mcp-nojwt registration (already in demo's runtime.mcpServers, so
// precheck passes) and removed again in cleanup.
//
// What this test deliberately does NOT do is poll /tag/info for a spend
// figure: that reports LiteLLM_TagTable.spend, which is NOT the enforcement
// counter (LiteLLM_DailyTagSpend is), and the tag row itself only appears on
// LiteLLM's batched flush. Waiting on it fails slowly for no signal.
func TestTagBudgets(t *testing.T) {
	phase4SuiteGuard(t)
	base := "http://" + phase4GatewayAuthority(t)
	jwt := oauthLogin(t, base, "").Access

	llPort := startPortForward(t, sc5LiteLLMNS, sc5LiteLLMSvc, 4000)
	llURL := fmt.Sprintf("http://127.0.0.1:%d", llPort)

	k := keysAPI{t: t, base: base, jwt: jwt}
	keyID, plaintext := k.create("demo", fmt.Sprintf("budget-%d", time.Now().UnixNano()), nil)
	t.Cleanup(func() { _ = k.revoke(keyID) })

	// A freshly minted key_id has never been seen, so its tag must not
	// exist yet — anything else means we are reading a previous run's row.
	keyTag := "key:" + keyID
	if entry, ok := tagInfo(t, llURL, keyTag); ok && entry != nil {
		t.Fatalf("tag %s existed before the key was ever used: %+v", keyTag, entry)
	}

	withMcpQueryCost(t, llURL, costedMcpServer, mcpQueryCost)

	// 1. Spend some, then cap the key below what it already spent. The PATCH
	// creates and binds the tag's budget object; the spend that crosses it
	// lands on LiteLLM's own flush schedule, which tagSpendWindow allows for.
	for i := 0; i < 2; i++ {
		if code, body := mcpCall(t, base, plaintext, costedMcpServer); code != http.StatusOK {
			t.Fatalf("costed MCP call %d: status=%d body=%s, want 200", i, code, body)
		}
	}
	if code, resp := k.do("PATCH", "/platform/keys/"+keyID+"/budget",
		map[string]any{"max_budget": 0}); code != http.StatusNoContent {
		t.Fatalf("PATCH budget to 0: status=%d body=%v, want 204", code, resp)
	}
	// Each poll sends another costed call, which both re-tests the verdict
	// and adds spend — so this converges rather than merely waiting.
	var lastBody string
	within(t, tagSpendWindow, "the ek_ to be refused over budget", func() bool {
		code, body := mcpCall(t, base, plaintext, costedMcpServer)
		lastBody = body
		return code == http.StatusTooManyRequests
	})
	// The refusal must name the tag that blocked: that is the whole contract
	// — the forwarder stamped key:<id>, LiteLLM booked spend to it, and the
	// ceiling ACH wrote is the one being compared against.
	if !strings.Contains(lastBody, "Budget has been exceeded") || !strings.Contains(lastBody, keyTag) {
		t.Fatalf("over-budget body = %s; want it to name %s", lastBody, keyTag)
	}

	// 2. The owner's pk_ is capped by user:<email>, which has no budget in
	// this deployment — independent counters, so it keeps working.
	if code, body := mcpCall(t, base, mustAcquirePk(t), costedMcpServer); code != http.StatusOK {
		t.Fatalf("pk_ call while the ek_ is over budget: status=%d body=%s, want 200 "+
			"(the key: ceiling must not reach the owner's own traffic)", code, body)
	}

	// 3. Raising the ceiling unblocks the same key.
	if code, _ := k.do("PATCH", "/platform/keys/"+keyID+"/budget",
		map[string]any{"max_budget": 1000, "budget_duration": "30d"}); code != http.StatusNoContent {
		t.Fatalf("PATCH budget to 1000: status=%d, want 204", code)
	}
	within(t, tagBudgetCacheWindow, "the ek_ to be served again", func() bool {
		code, _ := mcpCall(t, base, plaintext, costedMcpServer)
		return code == http.StatusOK
	})

	// 4. The budget object carries ACH's friendly id — /budget/list reads as
	// key:<id>, never a uuid.
	entry, ok := tagInfo(t, llURL, keyTag)
	if !ok || entry == nil || entry.Budget == nil {
		t.Fatalf("tag %s has no budget object: %+v (ok=%v)", keyTag, entry, ok)
	}
	if entry.Budget.BudgetID != keyTag || entry.Budget.MaxBudget != 1000 {
		t.Fatalf("budget object = %+v, want {budget_id:%s max_budget:1000}", entry.Budget, keyTag)
	}

	// 5. Revoke reaps both the tag and its budget object.
	if code := k.revoke(keyID); code != http.StatusNoContent {
		t.Fatalf("revoke: status=%d, want 204", code)
	}
	within(t, tagBudgetCacheWindow, "the key tag to be gone", func() bool {
		entry, ok := tagInfo(t, llURL, keyTag)
		return ok && entry == nil
	})

	t.Run("environment_ceiling", func(t *testing.T) { testEnvironmentBudget(t, llURL) })
}

// testEnvironmentBudget — the operator half: Environment.spec.budget writes
// the "environment:<name>" tag budget and reports BudgetSynced.
//
// The ceiling applied is deliberately enormous: this patches the SHARED
// `demo` Environment on a kept cluster, so a ceiling that could ever be
// crossed would turn every other suite's spend into a shared resource. The
// assertion is that the operator wrote the budget object with ACH's friendly
// id, not that it blocks — blocking is already proven by the key: ceiling
// above, and LiteLLM enforces all three tags through the same code path.
func testEnvironmentBudget(t *testing.T, llURL string) {
	const envName = "demo"
	envTag := "environment:" + envName

	patch := func(body string) {
		t.Helper()
		if out, err := runCmd("kubectl", "patch", "environment", envName, "-n", namespace,
			"--type", "merge", "-p", body); err != nil {
			t.Fatalf("kubectl patch environment %s %s: %v\n%s", envName, body, err, out)
		}
	}
	t.Cleanup(func() {
		// Restore the fixture: no budget. The tag + budget object survive
		// (nothing deletes them until the Environment is deleted), which is
		// the documented behaviour — §15 Lifecycle.
		_, _ = runCmd("kubectl", "patch", "environment", envName, "-n", namespace,
			"--type", "json", "-p", `[{"op":"remove","path":"/spec/budget"}]`)
	})

	patch(`{"spec":{"budget":{"maxBudget":1000000,"budgetDuration":"30d"}}}`)
	waitForCondition(t, "environment", envName, "BudgetSynced", "True", 60*time.Second)
	if got := getConditionField(t, "environment", envName, "BudgetSynced", "reason"); got != "Synced" {
		t.Fatalf("BudgetSynced.reason = %q, want Synced", got)
	}

	within(t, tagBudgetCacheWindow, "the environment budget object", func() bool {
		entry, ok := tagInfo(t, llURL, envTag)
		return ok && entry != nil && entry.Budget != nil &&
			entry.Budget.BudgetID == envTag && entry.Budget.MaxBudget == 1000000
	})
}

// tagBudgetEntry is the /tag/info shape this test reads.
type tagBudgetEntry struct {
	Spend  float64 `json:"spend"`
	Budget *struct {
		BudgetID  string  `json:"budget_id"`
		MaxBudget float64 `json:"max_budget"`
	} `json:"litellm_budget_table"`
}

// mcpCall POSTs one tools/call for the echo tool through the gateway →
// forwarder → LiteLLM chain and returns the status AND body. Unlike
// postMCPViaForwarder it never fails the test on a non-200: the 429 IS the
// assertion here.
func mcpCall(t *testing.T, base, key, server string) (int, string) {
	t.Helper()
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s.echo","arguments":{"text":"tag-budget"}}}`,
		server)
	req, err := http.NewRequest(http.MethodPost, base+"/mcp/"+server+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("mcpCall: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("x-ach-key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mcpCall: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// withMcpQueryCost prices an EXISTING MCP server registration for the life
// of the test, restoring it afterwards. LiteLLM's PUT /v1/mcp/server updates
// in place (same server_id), so the Environment's resolved access group —
// which binds servers by id — is untouched.
//
// This is the only way to make e2e traffic cost anything: the mock LLM
// reports usage {0,0,0}, so no amount of /v1 traffic ever crosses a ceiling.
func withMcpQueryCost(t *testing.T, llURL, server string, cost float64) {
	t.Helper()
	id := mcpServerID(t, llURL, server)
	put := func(info map[string]any) {
		body, _ := json.Marshal(map[string]any{
			"server_id": id, "server_name": server, "transport": "http",
			"url":                          "http://ach-mcp-echo.ach-system.svc",
			"extra_headers":                []string{"authorization"},
			"allow_all_keys":               true,
			"auth_type":                    "none",
			"available_on_public_internet": true,
			"mcp_info":                     info,
		})
		if status, out := litellmMasterDo(t, llURL, http.MethodPut, "/v1/mcp/server", body); status >= 300 {
			t.Fatalf("PUT /v1/mcp/server %s: status=%d body=%s", server, status, out)
		}
	}
	t.Cleanup(func() { put(map[string]any{"server_name": server}) })
	put(map[string]any{
		"server_name":          server,
		"mcp_server_cost_info": map[string]any{"default_cost_per_query": cost},
	})
}

// mcpServerID resolves an MCP server's id by name over the master API.
func mcpServerID(t *testing.T, llURL, server string) string {
	t.Helper()
	status, raw := litellmMasterDo(t, llURL, http.MethodGet, "/v1/mcp/server", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/mcp/server: status=%d body=%s", status, raw)
	}
	var servers []struct {
		ServerID   string `json:"server_id"`
		ServerName string `json:"server_name"`
	}
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		t.Fatalf("decode /v1/mcp/server: %v (%s)", err, raw)
	}
	for _, s := range servers {
		if s.ServerName == server {
			return s.ServerID
		}
	}
	t.Fatalf("MCP server %q not registered in LiteLLM", server)
	return ""
}

// litellmMasterDo is litellmMasterPost widened to any method — the MCP
// registration read + update need GET and PUT.
func litellmMasterDo(t *testing.T, llURL, method, path string, body []byte) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, llURL+path, reader)
	if err != nil {
		t.Fatalf("litellmMasterDo %s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+sc5MasterKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("litellmMasterDo %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// tagInfo POSTs /tag/info as master and returns the named tag's entry, or
// (nil, true) when LiteLLM does not know the tag. ok is false only when the
// call itself failed — the caller is always inside a bounded `within`, so a
// transient failure retries rather than failing the test outright.
//
// An unknown tag is NOT a clean 200 with an empty map: LiteLLM answers
// HTTP 500 with `{"detail":"404: Tags not found: [...]"}` (measured; the
// same shape internal/litellm.tagAbsent handles). That is "absent", not a
// failure.
func tagInfo(t *testing.T, llURL, name string) (*tagBudgetEntry, bool) {
	t.Helper()
	body, _ := json.Marshal(map[string][]string{"names": {name}})
	status, raw := litellmMasterPost(t, llURL, "/tag/info", body)
	if status != http.StatusOK {
		if strings.Contains(strings.ToLower(raw), "not found") {
			return nil, true
		}
		t.Logf("tagInfo(%s): status=%d body=%s", name, status, raw)
		return nil, false
	}
	var resp map[string]tagBudgetEntry
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Logf("tagInfo(%s): decode %s: %v", name, raw, err)
		return nil, false
	}
	entry, present := resp[name]
	if !present {
		return nil, true
	}
	return &entry, true
}
