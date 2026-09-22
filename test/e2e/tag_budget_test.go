//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// tagBudgetCacheWindow bounds LiteLLM's tag-budget cache: a /tag/update
// takes effect in ≈10 s (measured 2026-09-22, references/
// litellm-permission-model.md §15). Doubled for headroom on a loaded box.
const tagBudgetCacheWindow = 30 * time.Second

// TestTagBudgets — the three ceilings are live and independent:
//
//  1. every forwarded request carries the user:/environment:/key: tags —
//     asserted at LiteLLM, which only knows a tag because the forwarder
//     stamped it (LiteLLM auto-creates the row from x-litellm-tags);
//  2. a per-ek budget set through PATCH /platform/keys/{id}/budget refuses
//     that key's traffic once its spend exceeds it, while the owner's pk_
//     keeps working — independent counters, no hierarchy;
//  3. raising the ek budget unblocks it within the cache window.
//
// Spend is generated with ordinary /v1 traffic: the e2e demo-model IS
// priced (scripts/cluster.sh seeds input/output_cost_per_token), so each
// call books a few micro-dollars to every tag on the request. That is all
// the enforcement needs — the comparison is `spend > max_budget`, so a
// max_budget of 0 blocks as soon as any spend lands.
func TestTagBudgets(t *testing.T) {
	phase4SuiteGuard(t)
	base := "http://" + phase4GatewayAuthority(t)
	jwt := oauthLogin(t, base, "").Access

	llPort := startPortForward(t, sc5LiteLLMNS, sc5LiteLLMSvc, 4000)
	llURL := fmt.Sprintf("http://127.0.0.1:%d", llPort)

	k := keysAPI{t: t, base: base, jwt: jwt}
	keyID, plaintext := k.create("demo", fmt.Sprintf("budget-%d", time.Now().UnixNano()), nil)
	t.Cleanup(func() { _ = k.revoke(keyID) })

	// 1. Traffic through the forwarder stamps the key's own tag, and LiteLLM
	// books this call's spend to it. A freshly minted key_id has never been
	// seen before, so the tag existing at all proves the forwarder stamped
	// it — and the spend proves LiteLLM counted it, which is the counter the
	// per-ek ceiling is compared against. (The user:/environment: tags
	// pre-exist on a kept cluster, so asserting their mere existence would
	// prove nothing; SC2 in phase4_invariants_test.go covers the full
	// three-tag stamp at the backend.)
	keyTag := "key:" + keyID
	if entry, ok := tagInfo(t, llURL, keyTag); ok && entry != nil {
		t.Fatalf("tag %s existed before the key was ever used: %+v", keyTag, entry)
	}
	if code := forwarderStatus(t, base, plaintext); code != http.StatusOK {
		t.Fatalf("first ek_ call: status=%d, want 200", code)
	}
	within(t, tagBudgetCacheWindow, "spend on "+keyTag, func() bool {
		entry, ok := tagInfo(t, llURL, keyTag)
		return ok && entry != nil && entry.Spend > 0
	})

	// 2. A zero ceiling on the key's own tag blocks THAT key only.
	if code, _ := k.do("PATCH", "/platform/keys/"+keyID+"/budget",
		map[string]any{"max_budget": 0}); code != http.StatusNoContent {
		t.Fatalf("PATCH budget to 0: status=%d, want 204", code)
	}
	within(t, tagBudgetCacheWindow, "the ek_ to be refused over budget", func() bool {
		return forwarderStatus(t, base, plaintext) == http.StatusTooManyRequests
	})
	// The owner's pk_ is capped by user:<email>, which has no budget in
	// this deployment — independent counters, so it keeps working.
	if code := forwarderStatus(t, base, mustAcquirePk(t)); code != http.StatusOK {
		t.Fatalf("pk_ call while the ek_ is over budget: status=%d, want 200 "+
			"(the key: ceiling must not reach the owner's own traffic)", code)
	}

	// 3. Raising the ceiling unblocks the same key.
	if code, _ := k.do("PATCH", "/platform/keys/"+keyID+"/budget",
		map[string]any{"max_budget": 1000, "budget_duration": "30d"}); code != http.StatusNoContent {
		t.Fatalf("PATCH budget to 1000: status=%d, want 204", code)
	}
	within(t, tagBudgetCacheWindow, "the ek_ to be served again", func() bool {
		return forwarderStatus(t, base, plaintext) == http.StatusOK
	})

	// The budget object carries ACH's friendly id — /budget/list reads as
	// key:<id>, never a uuid.
	entry, ok := tagInfo(t, llURL, keyTag)
	if !ok || entry == nil || entry.Budget == nil {
		t.Fatalf("tag %s has no budget object: %+v", keyTag, entry)
	}
	if entry.Budget.BudgetID != keyTag || entry.Budget.MaxBudget != 1000 {
		t.Fatalf("budget object = %+v, want {budget_id:%s max_budget:1000}", entry.Budget, keyTag)
	}

	// 4. Revoke reaps both the tag and its budget object.
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

// tagInfo POSTs /tag/info as master and returns the named tag's entry, or
// (nil, true) when LiteLLM does not know the tag. ok is false only when the
// call itself failed — the caller is always inside a bounded `within`, so a
// transient failure retries rather than failing the test outright.
func tagInfo(t *testing.T, llURL, name string) (*tagBudgetEntry, bool) {
	t.Helper()
	body, _ := json.Marshal(map[string][]string{"names": {name}})
	status, raw := litellmMasterPost(t, llURL, "/tag/info", body)
	if status != http.StatusOK {
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
