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
// Both opt out via ACH_SKIP_PHASE4=1, matching the existing phase4
// promotion e2e gate.

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/ackstorm/ach/internal/litellm"
)

// TestAccessGroupSynced_Demo_HappyPath asserts the demo fixture flips
// to True/Synced once the operator reconciles. Requires hydrate_litellm
// to have seeded the demo Model / MCP / A2A.
//
// Fix 1 regression gate: every other test on this branch asserts the shell
// team's sentinels against a Go fake or httptest — nothing checks that a REAL
// LiteLLM accepts object_permission on POST /team/new and stores it. This has
// already been confirmed by hand against this cluster (2026-07-21); the
// assertion below codifies that as a PASS-on-first-run regression gate, not a
// discovery test.
func TestAccessGroupSynced_Demo_HappyPath(t *testing.T) {
	if os.Getenv("ACH_SKIP_PHASE4") == "1" {
		t.Skip("§17 e2e (phase4); opt out via ACH_SKIP_PHASE4=1")
	}
	// The "demo" Environment is pre-synced by cluster.sh (reconcile_examples).
	// Tests assert against the live, already-synced cluster — they do NOT apply
	// fixtures (that would mutate shared state other specs depend on).
	if !waitForConditionTriple(t, "environment", "demo", "ach-system",
		"AccessGroupSynced", "True", "Synced", 30*time.Second) {
		dumpAGConditions(t, "demo")
		t.Fatalf("demo Environment did NOT reach AccessGroupSynced=True/Synced within 30s")
	}

	assertDemoShellTeamWiredInLiteLLM(t)
}

// assertDemoShellTeamWiredInLiteLLM reads the REAL in-cluster LiteLLM (not a
// Go fake — see startPortForward / sc5MasterKey, shared with the SC#5 e2e)
// and confirms the "demo" Environment's deny-all shell team
// (litellm.ShellTeamAlias("demo")) carries the exact sentinels ACH writes:
//
//   - models == [ShellTeamDenyAllModel]
//   - object_permission.agents == [ShellTeamDenyAllAgent], mcp_servers empty
//   - access_group_ids contains the demo access group's id — the LiteLLM-side
//     mirror that actually enforces (references/litellm-permission-model.md
//     §2), so its presence is the proof the shell is wired to the
//     Environment's grants and not merely present.
//
// GET /team/info?team_id= is the only LiteLLM read that resolves
// object_permission (references/litellm-permission-model.md §9); GET
// /v2/team/list serialises it as null, so this deliberately does not use
// that endpoint for the sentinel assertions.
func assertDemoShellTeamWiredInLiteLLM(t *testing.T) {
	t.Helper()
	llPort := startPortForward(t, sc5LiteLLMNS, sc5LiteLLMSvc, 4000)
	llURL := fmt.Sprintf("http://127.0.0.1:%d", llPort)
	client := litellm.NewRESTClient(llURL, sc5MasterKey, logr.Discard())
	ctx := context.Background()

	alias := litellm.ShellTeamAlias("demo")
	teams, err := client.ListTeamsByAlias(ctx, alias)
	if err != nil {
		t.Fatalf("ListTeamsByAlias(%s): %v", alias, err)
	}
	if len(teams) != 1 {
		t.Fatalf("ListTeamsByAlias(%s) = %d teams, want exactly 1: %+v", alias, len(teams), teams)
	}
	teamID := teams[0].TeamID

	info, err := client.GetTeamInfo(ctx, teamID)
	if err != nil {
		t.Fatalf("GetTeamInfo(%s) (team=%s): %v", teamID, alias, err)
	}
	if info == nil {
		t.Fatalf("GetTeamInfo(%s) (team=%s) returned nil", teamID, alias)
	}
	if !slices.Equal(info.Models, []string{litellm.ShellTeamDenyAllModel}) {
		t.Fatalf("shell team %s models = %v, want [%s]", alias, info.Models, litellm.ShellTeamDenyAllModel)
	}
	if info.ObjectPermission == nil {
		t.Fatalf("shell team %s: GET /team/info did not resolve object_permission", alias)
	}
	if !slices.Equal(info.ObjectPermission.Agents, []string{litellm.ShellTeamDenyAllAgent}) {
		t.Fatalf("shell team %s object_permission.agents = %v, want [%s]",
			alias, info.ObjectPermission.Agents, litellm.ShellTeamDenyAllAgent)
	}
	if len(info.ObjectPermission.MCPServers) != 0 {
		t.Fatalf("shell team %s object_permission.mcp_servers = %v, want empty", alias, info.ObjectPermission.MCPServers)
	}

	ag, err := client.GetAccessGroupByName(ctx, litellm.AccessGroupName("demo"))
	if err != nil {
		t.Fatalf("GetAccessGroupByName(%s): %v", litellm.AccessGroupName("demo"), err)
	}
	if ag == nil {
		t.Fatalf("GetAccessGroupByName(%s) returned nil — access group not found", litellm.AccessGroupName("demo"))
	}
	if !slices.Contains(info.AccessGroupIDs, ag.AccessGroupID) {
		t.Fatalf("shell team %s access_group_ids = %v, want to contain the demo access group id %s",
			alias, info.AccessGroupIDs, ag.AccessGroupID)
	}
}

// TestAccessGroupSynced_DemoUnresolved_FlipsToUnresolvedReferences
// asserts the negative-path fixture flips to False/UnresolvedReferences.
func TestAccessGroupSynced_DemoUnresolved_FlipsToUnresolvedReferences(t *testing.T) {
	if os.Getenv("ACH_SKIP_PHASE4") == "1" {
		t.Skip("§17 e2e (phase4); opt out via ACH_SKIP_PHASE4=1")
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

// TestAccessGroupSynced_PkUserShell_ReachesEntitlements is the Task 6 e2e
// gate for the per-user deny-all shell team
// (docs/superpowers/plans/2026-07-22-pk-user-shell-team.md): a pk_ minted
// through SSO is capped by ach-user-<email>, and after the operator's next
// reconcile of an entitled Environment, that shell carries exactly that
// Environment's access group — no more, no less.
//
// Scope note: the plan's verification checklist also asks for an MCP-catalog
// union comparison and a cross-agent A2A denial. The fixture cluster ships
// exactly one MCP pair and one A2A agent (both wired to "demo"), so there is
// no second isolated resource to prove a DENIAL against without inventing
// new cluster fixtures out of scope for this plan — the sibling #159 (env-key
// shell) plan hit the identical constraint and deliberately left that leg as
// a human verification recipe rather than automated e2e (see its Task 8 step
// 4). This test follows the same precedent: what IS fully mechanical here —
// shell sentinels, access-group attachment, entitled/unentitled model
// access, key duration — is asserted below against the real in-cluster
// LiteLLM; the MCP/A2A legs are documented in Task 7's live-verify step.
func TestAccessGroupSynced_PkUserShell_ReachesEntitlements(t *testing.T) {
	if os.Getenv("ACH_SKIP_PHASE4") == "1" {
		t.Skip("§17 e2e (phase4); opt out via ACH_SKIP_PHASE4=1")
	}
	forwarderURL := os.Getenv("ACH_FORWARDER_URL")
	if forwarderURL == "" {
		t.Fatalf("ACH_FORWARDER_URL not set — required for a phase4 run (set ACH_SKIP_PHASE4=1 to opt out).")
	}
	pk := mustAcquirePk(t)
	// Static mock-Dex identity every SSO e2e mint resolves to
	// (references/local-testing-gateway.md §3); provisionUser joins it to
	// `default`, which is demo's authorizedTeams entry.
	const ownerEmail = "kilgore@kilgore.trout"

	llPort := startPortForward(t, sc5LiteLLMNS, sc5LiteLLMSvc, 4000)
	llURL := fmt.Sprintf("http://127.0.0.1:%d", llPort)
	client := litellm.NewRESTClient(llURL, sc5MasterKey, logr.Discard())
	ctx := context.Background()

	shellAlias := litellm.UserShellAlias(ownerEmail)
	shells, err := client.ListTeamsByAlias(ctx, shellAlias)
	if err != nil {
		t.Fatalf("ListTeamsByAlias(%s): %v", shellAlias, err)
	}
	if len(shells) != 1 {
		t.Fatalf("ListTeamsByAlias(%s) = %d teams, want exactly 1: %+v", shellAlias, len(shells), shells)
	}
	shellID := shells[0].TeamID

	// Force a "demo" reconcile so the operator's next pass attaches this
	// shell to demo's access group — a brand-new shell is fail-closed until
	// that happens (the documented one-time window, references/troubleshooting.md).
	if out, aerr := exec.Command("kubectl", "-n", "ach-system", "annotate", "environment/demo",
		"ach.ackstorm.ai/e2e-poke="+time.Now().UTC().Format(time.RFC3339Nano), "--overwrite").CombinedOutput(); aerr != nil {
		t.Fatalf("kubectl annotate environment/demo: %v\n%s", aerr, out)
	}

	ag, err := client.GetAccessGroupByName(ctx, litellm.AccessGroupName("demo"))
	if err != nil {
		t.Fatalf("GetAccessGroupByName(%s): %v", litellm.AccessGroupName("demo"), err)
	}
	if ag == nil {
		t.Fatalf("GetAccessGroupByName(%s) returned nil — access group not found", litellm.AccessGroupName("demo"))
	}

	var info *litellm.TeamListEntry
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		info, err = client.GetTeamInfo(ctx, shellID)
		if err != nil {
			t.Fatalf("GetTeamInfo(%s): %v", shellID, err)
		}
		if info != nil && slices.Contains(info.AccessGroupIDs, ag.AccessGroupID) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if info == nil || !slices.Contains(info.AccessGroupIDs, ag.AccessGroupID) {
		t.Fatalf("shell %s did not pick up demo's access group %s within 30s (last read: %+v)",
			shellAlias, ag.AccessGroupID, info)
	}

	// Sentinels — same shape asserted for the env shell in
	// assertDemoShellTeamWiredInLiteLLM.
	if !slices.Equal(info.Models, []string{litellm.ShellTeamDenyAllModel}) {
		t.Fatalf("shell %s models = %v, want [%s]", shellAlias, info.Models, litellm.ShellTeamDenyAllModel)
	}
	if info.ObjectPermission == nil {
		t.Fatalf("shell %s: GET /team/info did not resolve object_permission", shellAlias)
	}
	if !slices.Equal(info.ObjectPermission.Agents, []string{litellm.ShellTeamDenyAllAgent}) {
		t.Fatalf("shell %s object_permission.agents = %v, want [%s]",
			shellAlias, info.ObjectPermission.Agents, litellm.ShellTeamDenyAllAgent)
	}
	if len(info.ObjectPermission.MCPServers) != 0 {
		t.Fatalf("shell %s object_permission.mcp_servers = %v, want empty", shellAlias, info.ObjectPermission.MCPServers)
	}

	// Entitled model → 200 (demo's runtime.models grants it via the shell's
	// newly-attached access group).
	if code := callViaForwarderChatCompletion(t, forwarderURL, pk, "demo-model"); code != http.StatusOK {
		t.Errorf("entitled model demo-model → status %d, want 200", code)
	}

	// Unentitled model → denied. Registered ephemeral (never referenced by
	// any Environment, so no access group ever grants it); deleted on cleanup.
	unentitled, modelID := registerThrowawayModel(t, ctx, client, llURL, sc5MasterKey)
	t.Cleanup(func() { deleteModel(t, llURL, sc5MasterKey, modelID) })
	if code := callViaForwarderChatCompletion(t, forwarderURL, pk, unentitled); code == http.StatusOK {
		t.Errorf("unentitled model %s → status 200, want denial (pk_ shell must not reach a model no entitled Environment granted)",
			unentitled)
	}

	// Key duration: the minted pk_ must carry a non-null LiteLLM expiry
	// (durationString fix, T1/T3) — before it, pk_ keys minted with
	// expires:None outlived the ACH personal_keys row.
	assertPkKeyHasExpiry(t, llURL, sc5MasterKey, ownerEmail)
}

// callViaForwarderChatCompletion POSTs a minimal /v1/chat/completions
// request through the forwarder with the pk_ bearer, and returns the
// response status code.
func callViaForwarderChatCompletion(t *testing.T, forwarderURL, pk, model string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, forwarderURL+"/v1/chat/completions",
		strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, model)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-ach-key", pk)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("forwarder POST /v1/chat/completions model=%s: %v", model, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	t.Logf("chat/completions model=%s -> %d body=%s", model, resp.StatusCode, truncate(body, 300))
	return resp.StatusCode
}

// registerThrowawayModel POSTs /model/new (master key) for a throwaway model
// no Environment ever references — the negative control for the pk_ shell's
// model ceiling. Mirrors scripts/cluster.sh's demo-model seed shape (points
// at the same ach-mock-model echo backend, so a mistaken 200 would still be
// diagnosable). Returns the model name and its LiteLLM-internal model_info.id
// (resolved via the typed ListModels, not guessed from the POST response
// shape) for cleanup via deleteModel.
func registerThrowawayModel(t *testing.T, ctx context.Context, client litellm.Client, llURL, masterKey string) (name, id string) {
	t.Helper()
	name = fmt.Sprintf("e2e-pk-shell-denied-%d", time.Now().UnixNano())
	body := fmt.Sprintf(
		`{"model_name":%q,"litellm_params":{"model":"openai/%s","api_base":"http://ach-mock-model.ach-system.svc/v1","api_key":"sk-mock"}}`,
		name, name)
	req, err := http.NewRequest(http.MethodPost, llURL+"/model/new", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+masterKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /model/new: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /model/new: status %d body=%s", resp.StatusCode, raw)
	}

	models, err := client.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels (resolving %s's id): %v", name, err)
	}
	for _, m := range models {
		if m.ModelName == name {
			return name, m.ModelInfo.ID
		}
	}
	t.Fatalf("ListModels: %s not found after POST /model/new", name)
	return "", ""
}

// deleteModel POSTs /model/delete (master key) — best-effort cleanup, logged
// not failed, so a cleanup hiccup never masks the test's real assertions.
func deleteModel(t *testing.T, llURL, masterKey, modelID string) {
	t.Helper()
	if modelID == "" {
		return
	}
	req, err := http.NewRequest(http.MethodPost, llURL+"/model/delete",
		strings.NewReader(fmt.Sprintf(`{"id":%q}`, modelID)))
	if err != nil {
		t.Logf("cleanup: new request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+masterKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("cleanup: POST /model/delete(%s): %v", modelID, err)
		return
	}
	_ = resp.Body.Close()
}

// assertPkKeyHasExpiry asserts the caller's newest pkid_-aliased LiteLLM key
// carries a non-null expiry — GET /key/list is the only read that resolves
// `expires` (the typed litellm.UserKeyInfo does not carry it, so this reads
// the wire response directly).
func assertPkKeyHasExpiry(t *testing.T, llURL, masterKey, ownerEmail string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		llURL+"/key/list?user_id="+url.QueryEscape(ownerEmail)+"&return_full_object=true&include_team_keys=false", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+masterKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /key/list: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /key/list: status %d body=%s", resp.StatusCode, raw)
	}
	var decoded struct {
		Keys []struct {
			KeyAlias  string  `json:"key_alias"`
			CreatedAt string  `json:"created_at"`
			Expires   *string `json:"expires"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode /key/list: %v body=%s", err, raw)
	}
	var newestAt string
	var newestExpires *string
	found := false
	for _, k := range decoded.Keys {
		if !strings.HasPrefix(k.KeyAlias, "pkid_") {
			continue
		}
		found = true
		if k.CreatedAt > newestAt {
			newestAt = k.CreatedAt
			newestExpires = k.Expires
		}
	}
	if !found {
		t.Fatalf("GET /key/list(user_id=%s): no pkid_-aliased key found among %d keys", ownerEmail, len(decoded.Keys))
	}
	if newestExpires == nil || *newestExpires == "" {
		t.Fatalf("newest pk_ (created_at=%s) carries no expiry — durationString regression (expires:None)", newestAt)
	}
}
