//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// ek_ state model e2e — AC-07..AC-14 (console-phase2). Exercises the live
// /platform/keys lifecycle (create/suspend/resume/revoke/expiry), the
// forwarder/hydrate/content-service enforcement of a suspended, expired,
// revoked, or access-lost ek_ (D-30), and the orphan-cleanup loop's
// managed-set discipline (D-23: a non-revoked row survives a tick; a
// revoked row's lingering LiteLLM key is reaped; a foreign key is never
// touched).
//
// Admin-allowlist caveat: the ONLY Dex mock user (kilgore@kilgore.trout,
// test/e2e/cluster/02-ach/ach.values.yaml) is on the platform-api admin
// allowlist. GET /platform/keys never derives status="invalid" or a
// "no_access" reason for an admin caller (D-20 grants everywhere), and
// ResumeHandler skips the owner-access check entirely for an admin
// (internal/platformapi/envkeys/suspend.go: `if !kc.IsAdmin { ... }`). So
// AC-09's list-derived "invalid"/"no_access" assertions and AC-07/AC-09's
// "resume without access -> 403" CANNOT be exercised live with this user —
// they are covered by the envkeys unit tests
// (internal/platformapi/envkeys/*_test.go). This suite instead asserts what
// IS observable for an admin owner: the forwarder's EkOwnerGate and the
// hydrate/content-service D-30 gate have no admin bypass, so a key whose
// owner lost Environment access is denied AT USE (403 unauthorized_team)
// and recovers once membership is restored.
//
// AC-12 (Environment-delete finalizer revokes its keys) is NOT exercised
// live here — a throwaway Environment would need a real reconcile (the
// operator is scaled to 0 for large stretches of this test's own AC-14/
// AC-13 orphan ticks) plus a second wait-bound on top of an already
// ~5-6 minute suite. It is covered by the Task 5 envtest finalizer seam
// test and the DB drain test (internal/controller, D-23/AC-12).
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/orphan"
)

const (
	// ekStateUserEmail is the sole Dex mock user (see the admin-allowlist
	// caveat above) — the owner of every key this suite creates.
	ekStateUserEmail = "kilgore@kilgore.trout"
	// demoAuthorizedTeamAlias is the LiteLLM team the `demo` Environment
	// fixture authorizes (test/e2e/cluster/05-environment/demo.yaml
	// spec.authorizedTeams); the SSO mock user joins it at provision.
	demoAuthorizedTeamAlias = "default"
	// demoPromptName is the Prompt CR name the `demo` Environment's
	// context.prompts references (test/e2e/cluster/04-objects/
	// prompt-claude-code.yaml metadata.name), served at
	// /content/prompt/{name}.
	demoPromptName = "claude-code-system-prompt"
)

// keysAPI is a thin client over /platform/keys as the OAuth user — every
// call authenticates with `Authorization: Bearer <jwt>` (the JWT resolves
// to the caller's purpose='oauth' pk_ row; middleware.go resolves a
// Bearer token shaped like a JWS as ACH's own OAuth credential with no
// declared credential-header slot needed).
type keysAPI struct {
	t    *testing.T
	base string
	jwt  string
}

// do round-trips one /platform/keys JSON request and decodes the response
// body into a map (empty map on a bodyless 204). Never fails the test on a
// non-2xx status — callers assert the status code themselves so a 400/403/
// 409 path can be exercised.
func (k keysAPI) do(method, path string, body any) (int, map[string]any) {
	k.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			k.t.Fatalf("keysAPI.do: marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, k.base+path, reader)
	if err != nil {
		k.t.Fatalf("keysAPI.do: new request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+k.jwt)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		k.t.Fatalf("keysAPI.do: %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// create POSTs /platform/keys and fails the test unless it succeeds —
// every scenario needs a live key before it can exercise a failure path,
// so a create failure is always a hard stop, not an assertion under test.
func (k keysAPI) create(env, name string, expiresAt *time.Time) (keyID, plaintext string) {
	k.t.Helper()
	body := map[string]any{"environment": env, "name": name}
	if expiresAt != nil {
		body["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	}
	code, resp := k.do("POST", "/platform/keys", body)
	if code != http.StatusOK {
		k.t.Fatalf("keysAPI.create(%s,%s): status=%d body=%v", env, name, code, resp)
	}
	keyID, _ = resp["key_id"].(string)
	plaintext, _ = resp["plaintext"].(string)
	if keyID == "" || plaintext == "" {
		k.t.Fatalf("keysAPI.create(%s,%s): missing key_id/plaintext in %v", env, name, resp)
	}
	return keyID, plaintext
}

// state GETs /platform/keys (environment=demo, one page) and returns the
// caller-scoped status + reasons for keyID. Fails the test if keyID is not
// found on the page — the list is caller-scoped and demo-scoped, so every
// key this suite creates must appear.
func (k keysAPI) state(keyID string) (status string, reasons []string) {
	k.t.Helper()
	code, resp := k.do("GET", "/platform/keys?environment=demo&limit=500", nil)
	if code != http.StatusOK {
		k.t.Fatalf("keysAPI.state: GET /platform/keys status=%d body=%v", code, resp)
	}
	items, _ := resp["items"].([]any)
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || item["key_id"] != keyID {
			continue
		}
		status, _ = item["status"].(string)
		if rs, ok := item["reasons"].([]any); ok {
			for _, r := range rs {
				if s, ok := r.(string); ok {
					reasons = append(reasons, s)
				}
			}
		}
		return status, reasons
	}
	k.t.Fatalf("keysAPI.state: key_id %s not found in GET /platform/keys?environment=demo", keyID)
	return "", nil
}

func (k keysAPI) suspend(keyID string) int {
	k.t.Helper()
	code, _ := k.do("POST", "/platform/keys/"+keyID+"/suspend", nil)
	return code
}

func (k keysAPI) resume(keyID string) int {
	k.t.Helper()
	code, _ := k.do("POST", "/platform/keys/"+keyID+"/resume", nil)
	return code
}

func (k keysAPI) revoke(keyID string) int {
	k.t.Helper()
	code, _ := k.do("DELETE", "/platform/keys/"+keyID, nil)
	return code
}

// forwarderStatus POSTs a minimal chat-completion through the gateway's
// /v1 route with the given ek_/pk_ as x-ach-key. demo-model is backed by
// the in-cluster "parrot" mock LLM (scripts/cluster.sh), so a 200 comes
// back fast without a real upstream call.
func forwarderStatus(t *testing.T, base, key string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/chat/completions",
		strings.NewReader(`{"model":"demo-model","messages":[{"role":"user","content":"ek-state e2e probe"}]}`))
	if err != nil {
		t.Fatalf("forwarderStatus: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-ach-key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("forwarderStatus: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// hydrateStatus POSTs /platform/hydrate with the given key as x-ach-key.
func hydrateStatus(t *testing.T, base, key, env string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"environment": env})
	req, err := http.NewRequest(http.MethodPost, base+"/platform/hydrate", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("hydrateStatus: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-ach-key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("hydrateStatus: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// contentStatus GETs /content/prompt/{name} with the given key as
// x-ach-key and env as x-ach-environment, through the gateway (which
// routes /content/ to content-service — internal/gateway/routes.go).
func contentStatus(t *testing.T, base, key, env, name string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/content/prompt/"+name, nil)
	if err != nil {
		t.Fatalf("contentStatus: new request: %v", err)
	}
	req.Header.Set("x-ach-key", key)
	req.Header.Set("x-ach-environment", env)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("contentStatus: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// within polls fn every 2s until it returns true or d elapses, in which
// case it fails the test. Bounded — never an unbounded loop.
func within(t *testing.T, d time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for: %s", d, what)
		}
		time.Sleep(2 * time.Second)
	}
}

// teamIDByAlias resolves a LiteLLM team_id by team_alias over the sc5
// port-forward (master-key admin call).
func teamIDByAlias(t *testing.T, ll *litellm.RESTClient, alias string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	teams, err := ll.ListTeamsByAlias(ctx, alias)
	if err != nil {
		t.Fatalf("teamIDByAlias(%s): %v", alias, err)
	}
	if len(teams) == 0 || teams[0].TeamID == "" {
		t.Fatalf("teamIDByAlias(%s): no team found", alias)
	}
	return teams[0].TeamID
}

// litellmMasterPost issues a master-key-authenticated POST against the
// in-cluster LiteLLM (over the sc5 port-forward) and returns the status +
// raw body for the caller to interpret — team membership calls are
// idempotent-with-caveats at LiteLLM (a duplicate add/remove 4xx's), so the
// caller decides what is fatal.
func litellmMasterPost(t *testing.T, llURL, path string, body []byte) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, llURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("litellmMasterPost %s: build request: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+sc5MasterKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("litellmMasterPost %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// removeMember POSTs /team/member_delete for the given team + email.
func removeMember(t *testing.T, llURL, teamID, email string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"team_id": teamID, "user_id": email})
	if status, out := litellmMasterPost(t, llURL, "/team/member_delete", body); status >= 300 {
		t.Fatalf("removeMember(%s,%s): status=%d body=%s", teamID, email, status, out)
	}
}

// addMember POSTs /team/member_add for the given team + email ("user"
// role). Tolerant of LiteLLM's 4xx-on-duplicate-add response (the same
// idempotency contract internal/litellm.RESTClient.TeamMemberAdd
// documents) — callers may re-add a member that is already on the team
// (e.g. a mid-test recovery step followed by a t.Cleanup restore).
func addMember(t *testing.T, llURL, teamID, email string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"team_id": teamID,
		"member":  map[string]string{"user_id": email, "role": "user"},
	})
	if status, out := litellmMasterPost(t, llURL, "/team/member_add", body); status >= 500 {
		t.Fatalf("addMember(%s,%s): status=%d body=%s", teamID, email, status, out)
	}
}

// seedLiteLLMKey mints a raw LiteLLM key over the sc5MasterKey admin API
// under userID, stamping the given metadata (nil → no ach_key_id → a
// foreign key the orphan loop's ownership gate never touches). Mirrors
// createLiteLLMKey (phase2_sc5_orphan_test.go) with caller-supplied
// metadata instead of the SC#5 fixture's hardcoded ach_issuer. Returns the
// plaintext key (LiteLLM's `.key`).
func seedLiteLLMKey(t *testing.T, ctx context.Context, baseURL, userID string, metadata map[string]string) string {
	t.Helper()
	meta, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("seedLiteLLMKey: marshal metadata: %v", err)
	}
	body := strings.NewReader(fmt.Sprintf(`{"user_id":%q,"duration":"24h","metadata":%s}`, userID, meta))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/key/generate", body)
	if err != nil {
		t.Fatalf("seedLiteLLMKey: build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+sc5MasterKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("seedLiteLLMKey POST /key/generate: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("seedLiteLLMKey /key/generate HTTP %d: %s", resp.StatusCode, b)
	}
	var parsed struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("seedLiteLLMKey: decode /key/generate: %v", err)
	}
	if parsed.Key == "" {
		t.Fatalf("seedLiteLLMKey: /key/generate response missing .key")
	}
	return parsed.Key
}

// runOrphanTickInProcess drives exactly one orphan.Runnable.TickOnce
// against the live in-cluster LiteLLM (litellmURL, already port-forwarded)
// + ACH Postgres, scoped to issuer (must match metadata.ach_issuer as
// platform-api stamps it — ACH_BASE_URL). Mirrors the
// testSC5OrphanReapLive recipe (phase2_sc5_orphan_test.go:159-205): scale
// the operator to 0 so its own ticker cannot race this call, tick once,
// restore via t.Cleanup (LIFO at the end of the calling subtest) so later
// subtests get the operator back.
func runOrphanTickInProcess(t *testing.T, litellmURL, issuer string) {
	t.Helper()
	if out, err := runCmd("kubectl", "scale", "-n", namespace,
		"deployment/ach-operator", "--replicas=0"); err != nil {
		t.Fatalf("ekstate: scale operator to 0: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "scale", "-n", namespace,
			"deployment/ach-operator", "--replicas=1")
		_, _ = runCmdLonger(120*time.Second, "kubectl", "rollout", "status", "-n", namespace,
			"deployment/ach-operator", "--timeout=120s")
	})
	if out, err := runCmdLonger(60*time.Second, "kubectl", "wait", "-n", namespace,
		"--for=delete", "pods", "-l", "app.kubernetes.io/name=ach-operator",
		"--timeout=60s"); err != nil {
		t.Logf("ekstate: wait operator pods deleted (best-effort): %v\n%s", err, out)
	}

	achPgPort := startPortForward(t, namespace, sc5ACHPgSvc, 5432)
	achDBURL := fmt.Sprintf("postgres://ach:ach@127.0.0.1:%d/ach?sslmode=disable", achPgPort)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, achDBURL)
	if err != nil {
		t.Fatalf("ekstate: pgxpool.New: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ekstate: ach db ping: %v", err)
	}

	client := litellm.NewRESTClient(litellmURL, sc5MasterKey, logr.Discard())
	r := orphan.NewRunnable(client, pool, audit.NewLogger(io.Discard), 5*time.Minute, false, orphan.DefaultMaxRevoke, issuer, logr.Discard())
	r.TickOnce(ctx)
}

// assertReaperReapsRevokedAndKeepsForeign backs AC-13: seeds one LiteLLM
// key stamped with the just-revoked ek_'s key_id as metadata.ach_key_id
// (simulating the crash-between-calls a finalizer/revoke can strand — the
// row is revoked so it has left the managed set, but its LiteLLM key
// lingers) plus one foreign key (no ach_key_id) for the same user,
// backdates both past the 10-minute orphan floor, runs one in-process
// tick, and asserts the seeded orphan is gone while the foreign key
// survives untouched.
func assertReaperReapsRevokedAndKeepsForeign(t *testing.T, base, litellmURL, llDBURL, revokedKeyID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	foreignMarker := "ekstate-foreign-" + revokedKeyID
	orphanPlain := seedLiteLLMKey(t, ctx, litellmURL, ekStateUserEmail, map[string]string{
		"ach_key_id":   revokedKeyID,
		"ach_key_type": "ek",
		"ach_issuer":   base,
	})
	t.Cleanup(func() { _ = deleteLiteLLMKey(litellmURL, sc5MasterKey, orphanPlain) }) // best-effort: a no-op if the tick already reaped it
	foreignPlain := seedLiteLLMKey(t, ctx, litellmURL, ekStateUserEmail, map[string]string{"test_marker": foreignMarker})
	t.Cleanup(func() { _ = deleteLiteLLMKey(litellmURL, sc5MasterKey, foreignPlain) })

	// WHERE user_id = ekStateUserEmail also backdates every OTHER LiteLLM
	// key this user owns — harmless: a non-revoked ACH row is excluded from
	// candidacy by managed-set membership alone (internal/orphan/runnable.go),
	// never by age, so an older timestamp on an active/suspended key changes
	// nothing.
	backdateLiteLLMKeyByUser(t, ctx, llDBURL, ekStateUserEmail, 11)

	runOrphanTickInProcess(t, litellmURL, base)

	// Probe each seeded key directly by its own plaintext (GET /key/info?key=)
	// rather than listing the user's keys: GET /key/list has no page/size
	// param wired through internal/litellm.RESTClient.ListUserKeys, so it
	// only ever returns LiteLLM's default first page — on this shared,
	// repeatedly-reused kept cluster ekStateUserEmail accumulates keys
	// across runs (>50 seen in practice), which silently truncated the
	// list-based check before this fix and made the foreign key look
	// "gone" when it had simply fallen off page 1. /key/info is a
	// single-key lookup and immune to that.
	if litellmKeyExists(t, litellmURL, orphanPlain) {
		t.Fatalf("AC-13: LiteLLM key stamped ach_key_id=%s (revoked ACH row) survived the reaper tick", revokedKeyID)
	}
	if !litellmKeyExists(t, litellmURL, foreignPlain) {
		t.Fatalf("AC-13: foreign LiteLLM key (marker=%s, no ach_key_id) did not survive the reaper tick", foreignMarker)
	}
}

// litellmKeyExists probes ONE specific LiteLLM key by its own plaintext via
// GET /key/info?key=<plaintext> (master-key admin call) — 200 if it still
// exists, 404 if it is gone. Unlike GET /key/list (ListUserKeys), this is a
// single-key lookup and is immune to LiteLLM's default first-page-only
// pagination on a user with many keys.
func litellmKeyExists(t *testing.T, litellmURL, plaintext string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		litellmURL+"/key/info?key="+url.QueryEscape(plaintext), nil)
	if err != nil {
		t.Fatalf("litellmKeyExists: build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+sc5MasterKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("litellmKeyExists: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		return true
	case http.StatusNotFound:
		return false
	default:
		t.Fatalf("litellmKeyExists: GET /key/info unexpected status=%d", resp.StatusCode)
		return false
	}
}

// TestEkStateModel exercises AC-07..AC-14 live on the kind cluster: the
// ek_ state machine (expiry, suspend/resume, revoke), forwarder/hydrate/
// content-service enforcement of a non-active or access-lost ek_, and the
// orphan reaper's managed-set discipline. See the package doc comment
// above for the admin-allowlist caveat this suite works around.
func TestEkStateModel(t *testing.T) {
	phase4SuiteGuard(t)
	base := "http://" + phase4GatewayAuthority(t)
	jwt := oauthLogin(t, base, "").Access

	llPort := startPortForward(t, sc5LiteLLMNS, sc5LiteLLMSvc, 4000)
	llURL := fmt.Sprintf("http://127.0.0.1:%d", llPort)
	team := teamIDByAlias(t, litellm.NewRESTClient(llURL, sc5MasterKey, logr.Discard()), demoAuthorizedTeamAlias)

	t.Run("AC-10_expiry", func(t *testing.T) {
		k := keysAPI{t: t, base: base, jwt: jwt} // keysAPI.do calls t.Fatalf on the goroutine it's given (Go testing: FailNow must run on the goroutine of the *testing.T that owns it) — rebind per subtest, never close over the outer t.
		if code, resp := k.do("POST", "/platform/keys",
			map[string]any{"environment": "demo", "name": "past", "expires_at": "2020-01-01T00:00:00Z"}); code != http.StatusBadRequest {
			t.Fatalf("create with past expires_at: status=%d body=%v", code, resp)
		}

		exp := time.Now().Add(75 * time.Second)
		id, key := k.create("demo", "short", &exp)
		if got := forwarderStatus(t, base, key); got != http.StatusOK {
			t.Fatalf("fresh expiring key must work: forwarder status=%d", got)
		}
		within(t, 2*time.Minute, "expiry enforced with a warm cache", func() bool {
			return forwarderStatus(t, base, key) == http.StatusUnauthorized
		})
		if s, r := k.state(id); s != "expired" {
			t.Fatalf("state after expiry: status=%s reasons=%v want=expired", s, r)
		}
		if code := k.resume(id); code != http.StatusConflict {
			t.Fatalf("resume of expired key: status=%d want=409", code)
		}

		id2, key2 := k.create("demo", "perpetual", nil)
		if got := forwarderStatus(t, base, key2); got != http.StatusOK {
			t.Fatalf("perpetual key: forwarder status=%d", got)
		}
		_ = id2
	})

	t.Run("AC-08_suspend_bound_AC-14_reaper_survival_resume", func(t *testing.T) {
		k := keysAPI{t: t, base: base, jwt: jwt} // see AC-10_expiry: rebind per subtest.
		id, key := k.create("demo", "susp", nil)
		if got := forwarderStatus(t, base, key); got != http.StatusOK {
			t.Fatalf("warm the resolver cache: forwarder status=%d", got)
		}
		if code := k.suspend(id); code != http.StatusNoContent {
			t.Fatalf("suspend: status=%d want=204", code)
		}
		start := time.Now()
		within(t, 70*time.Second, "denied after suspend", func() bool {
			return forwarderStatus(t, base, key) == http.StatusUnauthorized
		})
		if took := time.Since(start); took > 62*time.Second {
			t.Fatalf("suspend enforcement took %v, want <= 60s bound", took)
		}
		if code := k.suspend(id); code != http.StatusNoContent {
			t.Fatalf("suspend idempotent: status=%d want=204", code)
		}
		if s, r := k.state(id); s != "suspended" || !contains(r, "suspended") {
			t.Fatalf("state after suspend: status=%s reasons=%v", s, r)
		}

		// AC-14: a reaper tick must leave the suspended (non-revoked, hence
		// managed) key's LiteLLM backing untouched.
		runOrphanTickInProcess(t, llURL, base)

		if code := k.resume(id); code != http.StatusNoContent {
			t.Fatalf("resume: status=%d want=204", code)
		}
		within(t, 70*time.Second, "usable after resume", func() bool {
			return forwarderStatus(t, base, key) == http.StatusOK
		})
	})

	t.Run("AC-09_access_loss_invalid_recovery_AC-07_suspended_no_access", func(t *testing.T) {
		k := keysAPI{t: t, base: base, jwt: jwt} // see AC-10_expiry: rebind per subtest.
		id, key := k.create("demo", "inv", nil)
		idS, _ := k.create("demo", "inv-susp", nil)
		if code := k.suspend(idS); code != http.StatusNoContent {
			t.Fatalf("pre-suspend inv-susp: status=%d want=204", code)
		}

		removeMember(t, llURL, team, ekStateUserEmail)
		t.Cleanup(func() { addMember(t, llURL, team, ekStateUserEmail) })

		within(t, 70*time.Second, "forwarder denies on access loss", func() bool {
			return forwarderStatus(t, base, key) == http.StatusForbidden
		})
		if got := hydrateStatus(t, base, key, "demo"); got != http.StatusForbidden {
			t.Fatalf("hydrate must deny on access loss: status=%d want=403", got)
		}
		if got := contentStatus(t, base, key, "demo", demoPromptName); got != http.StatusForbidden {
			t.Fatalf("content-service must deny on access loss: status=%d want=403", got)
		}

		// AC-07/AC-09 list-derived assertions (status="invalid",
		// reasons contains "no_access", resume->403 without access) are NOT
		// exercised here — kilgore@kilgore.trout is the platform-api admin
		// (see the package doc comment): GET /platform/keys never derives
		// Invalid for an admin caller and ResumeHandler skips the access
		// check for one. Covered by internal/platformapi/envkeys/state_test.go
		// and suspend_test.go. Suspend itself is allowed without access
		// (no access check on that path):
		if code := k.suspend(id); code != http.StatusNoContent {
			t.Fatalf("suspend is allowed without access: status=%d want=204", code)
		}
		// Admin bypass (see package doc comment): resume succeeds immediately
		// even with access still lost — ResumeHandler skips the owner-access
		// check for kilgore@kilgore.trout. The forwarder's EkOwnerGate is NOT
		// admin-aware though: it independently re-derives the owner's real
		// team membership, so the key stays blocked until access is
		// genuinely restored — the observable half of AC-09's recovery path.
		if code := k.resume(id); code != http.StatusNoContent {
			t.Fatalf("resume (admin bypass, access still lost): status=%d want=204", code)
		}
		if got := forwarderStatus(t, base, key); got != http.StatusForbidden {
			t.Fatalf("forwarder must still deny — ACH row active but access genuinely lost: status=%d want=403", got)
		}

		addMember(t, llURL, team, ekStateUserEmail)
		within(t, 70*time.Second, "same key recovers after access is restored", func() bool {
			return forwarderStatus(t, base, key) == http.StatusOK
		})
	})

	t.Run("AC-11_revoke_idempotent_AC-13_reaper_reaps_revoked", func(t *testing.T) {
		k := keysAPI{t: t, base: base, jwt: jwt} // see AC-10_expiry: rebind per subtest.
		id, key := k.create("demo", "rev", nil)
		if code := k.suspend(id); code != http.StatusNoContent {
			t.Fatalf("pre-suspend: status=%d want=204", code)
		}
		if code := k.revoke(id); code != http.StatusNoContent {
			t.Fatalf("revoke: status=%d want=204", code)
		}
		if code := k.revoke(id); code != http.StatusNoContent {
			t.Fatalf("revoke idempotent: status=%d want=204", code)
		}
		if s, _ := k.state(id); s != "revoked" {
			t.Fatalf("state after revoke: status=%s want=revoked", s)
		}
		if code := k.resume(id); code != http.StatusConflict {
			t.Fatalf("resume after revoke: status=%d want=409", code)
		}
		within(t, 70*time.Second, "revoked key denied", func() bool {
			return forwarderStatus(t, base, key) == http.StatusUnauthorized
		})

		llDBPort := startPortForward(t, sc5LiteLLMNS, sc5LiteLLMDBSvc, 5432)
		llDBURL := fmt.Sprintf("postgres://litellm:litellm@127.0.0.1:%d/litellm?sslmode=disable", llDBPort)
		assertReaperReapsRevokedAndKeepsForeign(t, base, llURL, llDBURL, id)
	})
}
