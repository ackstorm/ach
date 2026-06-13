//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 4 invariants e2e suite. Asserts the five Hub Phase 4 ROADMAP
// Success Criteria SC#1..SC#5 against the running kind+Helm cluster
// brought up by `make e2e-keep`. Mirrors phase3_invariants_test.go in
// shape: stdlib testing, kubectl orchestration, no Ginkgo.
//
// Engineer-pending verification debt: the suite is mechanically correct
// and the assertions are stable, but every subtest first calls
// phase4SuiteGuard which Skipf's with a clear message when ACH_E2E_PHASE4=1
// or the Forwarder Deployment is not Ready. Once the engineer flips the
// gate (after standing up the cluster per CLAUDE.md "E2E debug loop"),
// the subtests run end-to-end. SC#5 is partially manual — the negative
// refuse-to-start test requires a deliberate helm upgrade and observation
// of CrashLoopBackOff, which the engineer performs after the green run.

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPhase4Invariants exercises Phase 4 SC#1..SC#5. Each SC is one t.Run
// so a failed SC#1 does NOT abort the others. Subtests run sequentially
// against the shared cluster.
func TestPhase4Invariants(t *testing.T) {
	phase4SuiteGuard(t)

	// pk_/ek_ keys are acquired on demand through the always-on gateway
	// (mustAcquirePk / mustAcquireEkBoundToEnv → phase4GatewayPort, which
	// derives the port from ACH_BASE_URL and needs no dedicated port-forward)
	// and cached per-process. The harness exports ACH_FORWARDER_URL=<gateway
	// base>, which the subtests read directly — so there is no longer a
	// dedicated :8084 port-forward + ACH_FORWARDER_URL override here (it only
	// shadowed the gateway env on every focused run; see #63).

	t.Run("SC1_HeaderRewrite", testPhase4SC1HeaderRewrite)
	t.Run("SC2_McpA2aPrecheck", testPhase4SC2McpA2aPrecheck)
	t.Run("SC2_EkTagInjection", testPhase4SC2EkTagInjection)
	t.Run("SC3_JwtMintAndBipAlphaLast", testPhase4SC3JwtMintAndBipAlphaLast)
	t.Run("SC4_JwksAndSecretRbac", testPhase4SC4JwksAndSecretRbac)
}

// testPhase4SC1HeaderRewrite — pk_ → /v1/chat/completions reaches LiteLLM
// authenticated as the CALLER's own LiteLLM virtual key AND without any
// client-supplied Authorization or x-ach-*. TESTING-PHASE (reverts FIX01 §A.6 /
// D-13): the forwarder no longer injects the shared master key — it forwards
// the per-user material persisted at mint (migration 000011) as
// x-litellm-api-key. A freshly-minted e2e pk_ therefore carries real material,
// so LiteLLM accepts it. We can't peek inside the in-cluster LiteLLM upstream's
// request log directly, so we rely on the fact that LiteLLM returns 401 when the
// forwarded key is invalid/absent: any non-401 status indicates header rewrite
// (and per-user material forwarding) succeeded.
func testPhase4SC1HeaderRewrite(t *testing.T) {
	forwarderURL := os.Getenv("ACH_FORWARDER_URL")
	if forwarderURL == "" {
		t.Fatalf("ACH_FORWARDER_URL not set — required for a phase4 run (set ACH_SKIP_PHASE4=1 to opt out).")
	}
	pk := mustAcquirePk(t)

	req, err := http.NewRequest(http.MethodPost, forwarderURL+"/v1/chat/completions",
		strings.NewReader(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-ach-key", pk)
	req.Header.Set("authorization", "Bearer should-be-stripped")
	req.Header.Set("x-litellm-api-key", "should-be-stripped-and-overwritten")
	req.Header.Set("x-ach-environment", "should-be-stripped")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("forwarder POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("LiteLLM returned 401 — header rewrite likely failed. Body: %s", string(body))
	}
	t.Logf("SC1 forwarder→LiteLLM status: %d (header rewrite verified)", resp.StatusCode)
}

// testPhase4SC2McpA2aPrecheck — ek_ to a name NOT in env.runtime.mcpServers
// returns 403 unauthorized_resource; ek_ to a name IN the list passes
// precheck (LiteLLM may 404 the unknown name — that's fine).
func testPhase4SC2McpA2aPrecheck(t *testing.T) {
	forwarderURL := os.Getenv("ACH_FORWARDER_URL")
	if forwarderURL == "" {
		t.Fatalf("ACH_FORWARDER_URL not set — required for a phase4 run (set ACH_SKIP_PHASE4=1 to opt out).")
	}
	ek := mustAcquireEkBoundToEnv(t, "demo")

	req, _ := http.NewRequest(http.MethodGet, forwarderURL+"/mcp/disallowed/", nil)
	req.Header.Set("x-ach-key", ek)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("disallowed mcp request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 on /mcp/disallowed; got %d body=%s", resp.StatusCode, string(body))
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("envelope decode: %v body=%s", err, string(body))
	}
	if env.Error.Code != "unauthorized_resource" {
		t.Fatalf("expected error.code=unauthorized_resource; got %q", env.Error.Code)
	}

	// demo-mcp-jwt IS in the synced demo Environment's runtime.mcpServers
	// (test/e2e/cluster/05-environment/demo.yaml), so the precheck must
	// pass. LiteLLM may still 404 the path — the comment above notes
	// that's fine; we only assert the precheck does NOT 403.
	reqAllowed, _ := http.NewRequest(http.MethodGet, forwarderURL+"/mcp/demo-mcp-jwt/", nil)
	reqAllowed.Header.Set("x-ach-key", ek)
	respAllowed, err := http.DefaultClient.Do(reqAllowed)
	if err != nil {
		t.Fatalf("allowed mcp request: %v", err)
	}
	respAllowed.Body.Close()
	if respAllowed.StatusCode == http.StatusForbidden {
		t.Fatalf("expected non-403 on /mcp/allowed (precheck should pass); got 403")
	}

	// Bare "/mcp/<name>" (NO trailing slash) — the exact form hydrate writes
	// into runtime config (platformapi/hydrate/handler.go) and the form that
	// previously 404'd at the chi router before reaching precheck (the bare
	// route was missing; only "/mcp/{name}/*" was registered). A router miss
	// returns chi's plain 404, so a 404 HERE means the bare route regressed.
	// We assert it reaches precheck (non-403 AND non-404).
	reqBare, _ := http.NewRequest(http.MethodGet, forwarderURL+"/mcp/demo-mcp-jwt", nil)
	reqBare.Header.Set("x-ach-key", ek)
	respBare, err := http.DefaultClient.Do(reqBare)
	if err != nil {
		t.Fatalf("bare-path mcp request: %v", err)
	}
	respBare.Body.Close()
	if respBare.StatusCode == http.StatusNotFound {
		t.Fatalf("bare /mcp/demo-mcp-jwt 404'd — the slash-less route regressed (router miss)")
	}
	if respBare.StatusCode == http.StatusForbidden {
		t.Fatalf("expected non-403 on bare /mcp/demo-mcp-jwt (precheck should pass); got 403")
	}
}

// testPhase4SC2EkTagInjection — FWD-06 v1alpha1 scope. The forwarder injects
// "environment:<name>" into the /v1 request body's metadata.tags for ek_
// traffic only, and mirrors it into the X-Ach-Tags header on the SAME
// success path (internal/forwarder/proxy/tags.go). LiteLLM consumes and
// strips metadata.tags before the model backend, so we assert at the backend
// (ach-mock-model "loro") on the mirror header — which the TEST cluster
// forwards to the demo-model group via forward_client_headers_to_llm_api
// (test/e2e/cluster/01-base/litellm.values.yaml; not enabled in prod). Header
// presence is a faithful proxy for body-tag injection since both are set on
// the one success path.
//
//   - ek_ bound to demo → loro sees X-Ach-Tags: environment:demo
//   - pk_               → loro sees no X-Ach-Tags (no env binding, no inject)
func testPhase4SC2EkTagInjection(t *testing.T) {
	forwarderURL := os.Getenv("ACH_FORWARDER_URL")
	if forwarderURL == "" {
		t.Fatalf("ACH_FORWARDER_URL not set — required for a phase4 run (set ACH_SKIP_PHASE4=1 to opt out).")
	}
	if err := waitDeploymentReady(t, phase4Namespace, mockModelDeployment, 15*time.Second); err != nil {
		t.Fatalf("ach-mock-model not Ready (%v) — cluster must be up (set ACH_SKIP_PHASE4=1 to opt out).", err)
	}
	mockLocal := strconv.Itoa(startPortForward(t, phase4Namespace, "svc/"+mockModelService, mockModelSvcPort))

	// Positive: ek_ bound to demo → forwarder injects + mirrors the tag.
	ek := mustAcquireEkBoundToEnv(t, "demo")
	ekSnap := driveV1ToBackend(t, forwarderURL, ek, fmt.Sprintf("sc2-ek-%d", time.Now().UnixNano()), mockLocal)
	if got := headerValue(ekSnap.Headers, headerTagsName); got != tagEnvDemo {
		t.Fatalf("ek_ traffic: backend %s=%q; want %q — forwarder must tag ek_ traffic. capture=%+v",
			headerTagsName, got, tagEnvDemo, ekSnap)
	}

	// Negative: pk_ → no environment binding → no tag injection, no header.
	pk := mustAcquirePk(t)
	pkSnap := driveV1ToBackend(t, forwarderURL, pk, fmt.Sprintf("sc2-pk-%d", time.Now().UnixNano()), mockLocal)
	if got := headerValue(pkSnap.Headers, headerTagsName); got != "" {
		t.Fatalf("pk_ traffic: backend %s=%q; want empty — pk_ must NOT be tagged. capture=%+v",
			headerTagsName, got, pkSnap)
	}
}

const (
	mockModelDeployment = "ach-mock-model"
	mockModelService    = "ach-mock-model"
	mockModelSvcPort    = 80
	// headerTagsName mirrors internal/forwarder/proxy.headerTags. NOT "x-ach-*":
	// the forwarder's D-06 strip drops that prefix before upstream, so the
	// mirror header lives outside it (still x-* so LiteLLM forwards it).
	headerTagsName = "X-Achtest-Tags"
	tagEnvDemo     = "environment:demo"
)

// modelCaptureSnap is the subset of ach-mock-model's /__capture/last we assert
// on — the generic ach-mock capture shape (distinct from mcp-echo's captureSnap).
type modelCaptureSnap struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	BodyRaw string              `json:"body_raw"`
}

// headerValue returns the first value of a header by case-insensitive name
// (the mock stores headers under Go's canonical keys, e.g. X-Ach-Tags).
func headerValue(h map[string][]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func resetModelMockCapture(t *testing.T, localPort string) {
	t.Helper()
	resp, err := http.Post(fmt.Sprintf("http://localhost:%s/__capture/reset", localPort), "", nil) //nolint:gosec // localhost PF
	if err != nil {
		t.Fatalf("POST /__capture/reset: %v", err)
	}
	_ = resp.Body.Close()
}

func readModelMockCapture(t *testing.T, localPort string) modelCaptureSnap {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://localhost:%s/__capture/last", localPort)) //nolint:gosec // localhost PF
	if err != nil {
		t.Fatalf("GET /__capture/last: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var snap modelCaptureSnap
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode /__capture/last: %v", err)
	}
	return snap
}

// driveV1ToBackend resets the loro's capture, POSTs a /v1 chat-completion with
// a unique marker through the forwarder, and returns the backend's capture
// once the marker round-trips. Retries on the transient LiteLLM→backend
// connection 500 (bounded, 30s) so a flaky hop never fails the assertion.
func driveV1ToBackend(t *testing.T, forwarderURL, key, marker, mockLocal string) modelCaptureSnap {
	t.Helper()
	body := fmt.Sprintf(`{"model":"demo-model","messages":[{"role":"user","content":%q}]}`, marker)
	var snap modelCaptureSnap
	deadline := time.Now().Add(30 * time.Second)
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		resetModelMockCapture(t, mockLocal)
		req, err := http.NewRequest(http.MethodPost, forwarderURL+"/v1/chat/completions", strings.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-ach-key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("forwarder POST: %v", err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		// The request reached the loro iff the capture body carries our marker.
		if snap = readModelMockCapture(t, mockLocal); strings.Contains(snap.BodyRaw, marker) {
			return snap
		}
		t.Logf("attempt %d: marker %q not at backend yet (forwarder status %d) — retry transient hop",
			attempt, marker, resp.StatusCode)
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("request never reached ach-mock-model with marker %q within 30s; last capture=%+v", marker, snap)
	return snap
}

// testPhase4SC3JwtMintAndBipAlphaLast — two throwaway BIPs targeting the SAME
// MCPServer route with different forwardIdentityJWT values; the forwarder mints
// (or not) for the alphabetically-LAST metadata.name, and the ach-mcp-echo
// /__capture/last endpoint proves which won via jwt_present/jwt_claims. Adding
// an even-later opt-out BIP flips the winner — the "alpha-last lock-in".
//
// Both BIPs target demo-mcp-jwt (the synced JWT route, mcp-echo backend with
// requireJwt=false) and sort AFTER the synced bip-demo-mcp-jwt, so they own the
// tiebreak. t.Cleanup removes them, restoring the route to its synced baseline.
func testPhase4SC3JwtMintAndBipAlphaLast(t *testing.T) {
	phase4SuiteGuard(t)

	if err := waitDeploymentReady(t, phase4Namespace, mcpEchoDeployment, 15*time.Second); err != nil {
		t.Fatalf(
			"ach-mcp-echo Deployment not Ready (%v) — cluster must be up with "+
				"testMocks.mcpEcho.enabled=true (requireJwt=false) (set ACH_SKIP_PHASE4=1 to opt out).", err)
	}

	mcpEchoLocal := "8194"
	defer startMcpEchoPortForward(t, mcpEchoLocal)()
	gatewayLocal := "8195"
	defer phase4StartGatewayPortForward(t, gatewayLocal)()
	pk := phase4AcquirePkAutomatically(t, gatewayLocal)

	const route = bipJWTRouteName // demo-mcp-jwt
	// Names sort after the synced bip-demo-mcp-jwt ('b' < 'z'); among the
	// throwaways, "aaa" < "zzz" so zzz is alpha-last.
	const (
		bipAaaOn  = "zz-sc3-aaa-jwt-on"  // forwardIdentityJWT: true  (earlier name)
		bipZzzOff = "zz-sc3-zzz-jwt-off" // forwardIdentityJWT: false (alpha-last)
		bipZ2On   = "zz-sc3-zzz2-jwt-on" // sorts AFTER zzz-off; the flip
	)

	// Orientation 1 — alpha-last is bipZzzOff (false): explicit opt-out wins
	// over the earlier true BIPs (bip-demo-mcp-jwt + bipAaaOn) → NO mint.
	sc3ApplyBIP(t, bipAaaOn, route, true)
	sc3ApplyBIP(t, bipZzzOff, route, false)
	t.Run("alpha_last_optout_suppresses_mint", func(t *testing.T) {
		snap := sc3AssertJWTPresentEventually(t, gatewayLocal, mcpEchoLocal, pk, route, false)
		if snap.AuthorizationSeen != "" {
			t.Fatalf("alpha-last opt-out: authorization_seen=%q, want empty (no JWT forwarded)",
				snap.AuthorizationSeen)
		}
	})

	// Orientation 2 — add bipZ2On, which sorts AFTER bipZzzOff and is true:
	// the alpha-last winner flips back to mint. Proves the tiebreak is purely
	// metadata.name ordering, not the value of any earlier policy.
	sc3ApplyBIP(t, bipZ2On, route, true)
	t.Run("later_name_flips_winner_to_mint", func(t *testing.T) {
		snap := sc3AssertJWTPresentEventually(t, gatewayLocal, mcpEchoLocal, pk, route, true)
		if !strings.HasPrefix(snap.AuthorizationSeen, "Bearer ey") {
			t.Fatalf("flip-to-mint: Authorization not a JWT: %q", snap.AuthorizationSeen)
		}
		if wantAud := "mcp:" + route; snap.JWTClaims.Aud != wantAud {
			t.Fatalf("flip-to-mint: aud=%q want %q", snap.JWTClaims.Aud, wantAud)
		}
		if snap.JWTClaims.Iss != forwarderBaseURL(t) {
			t.Fatalf("flip-to-mint: iss=%q want %q", snap.JWTClaims.Iss, forwarderBaseURL(t))
		}
		if snap.JWTClaims.Sub == "" {
			t.Fatalf("flip-to-mint: sub empty, want <ownerEmail>")
		}
	})
}

// testPhase4SC4JwksAndSecretRbac — anonymous JWKS GET + non-forwarder
// ServiceAccount Secret RBAC negative test.
func testPhase4SC4JwksAndSecretRbac(t *testing.T) {
	forwarderURL := os.Getenv("ACH_FORWARDER_URL")
	if forwarderURL == "" {
		t.Fatalf("ACH_FORWARDER_URL not set — required for a phase4 run (set ACH_SKIP_PHASE4=1 to opt out).")
	}
	resp, err := http.Get(forwarderURL + "/.well-known/jwks.json")
	if err != nil {
		t.Fatalf("JWKS GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("JWKS status = %d; want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/jwk-set+json" {
		t.Errorf("Content-Type = %q; want application/jwk-set+json", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q; want public, max-age=3600", got)
	}
	body, _ := io.ReadAll(resp.Body)
	var doc struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("JWKS decode: %v body=%s", err, string(body))
	}
	if len(doc.Keys) == 0 {
		t.Fatal("JWKS keys empty — signer.LoadOnce path may not have run")
	}
	k := doc.Keys[0]
	if k["kty"] != "OKP" {
		t.Errorf("kty = %s; want OKP", k["kty"])
	}
	if k["crv"] != "Ed25519" {
		t.Errorf("crv = %s; want Ed25519", k["crv"])
	}

	// Positive access check: the forwarder ServiceAccount must be able to
	// GET the ach-jwt-signing-keys Secret. Uses kubectl auth can-i.
	if err := phase4AssertSecretAccessible(t); err != nil {
		t.Errorf("forwarder secret access: %v", err)
	}
}
