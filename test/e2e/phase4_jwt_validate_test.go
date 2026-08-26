//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// TestPhase4JWTValidate exercises the full JWT trust path
// (FWD-07/08/09/10) end-to-end with a backend that cryptographically
// verifies the ACH-signed JWT. Two subtests:
//
//   - Direct: reads the cluster's ach-jwt-signing-keys Secret, mints
//     a JWT in-process with crypto/ed25519, and POSTs straight to
//     ach-mcp-echo. Isolates the mcp-echo verifier code path from the
//     Forwarder + LiteLLM intermediaries — fast, deterministic, and
//     the most precise regression guard for the #35 verifier.
//
//   - ViaForwarder: drives the full chain client → ach-local-gateway →
//     ach-forwarder → LiteLLM (with extra_headers: ["authorization"])
//     → ach-mcp-echo. Asserts that the JWT survives the LiteLLM hop
//     and that the verified claims show up in mcp-echo's capture
//     surface. Reuses phase4AcquirePkAutomatically for SSO mint.
//
// Activation: requires testMocks.enabled=true AND
// testMocks.mcpEcho.enabled=true on the kept-cluster Helm install. The
// `extra_headers: ["authorization"]` opt-in on the LiteLLM MCP server
// registration (seeded by scripts/cluster.sh reconcile_litellm) is
// REQUIRED for the ViaForwarder subtest.
//
// Run via:
//
//	./scripts/dev.sh make e2e-focus FOCUS=TestPhase4JWTValidate

package e2e

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

const (
	mcpEchoDeployment = "ach-mcp-echo"
	mcpEchoService    = "ach-mcp-echo"
	mcpEchoSvcPort    = "80"
	// mcpEchoBareName is the JWT-validated route into the shared ach-mcp-echo
	// backend (BIP bip-demo-mcp-jwt, forwardIdentityJWT: true). The nojwt
	// sibling route (demo-mcp-nojwt) is exercised by TestPhase4BIPClosedLoop.
	mcpEchoBareName = "demo-mcp-jwt"
	// LiteLLM prefixes tool names with `<server>.` when proxying through
	// the MCP gateway, so the wire name for the echo tool is
	// `<server>.<tool>`.
	mcpEchoToolFQN = "demo-mcp-jwt.echo"

	jwtSecretName = "ach-jwt-signing-keys"
)

// jwtClaimsCapture mirrors the field shapes the mcp-echo backend writes
// into /__capture/last (echojwt.Verified → json keys are PascalCase by
// default, see test/e2e/mcp-echo/capture.go).
type jwtClaimsCapture struct {
	Iss    string   `json:"Iss"`
	Sub    string   `json:"Sub"`
	Aud    string   `json:"Aud"`
	Kid    string   `json:"Kid"`
	Iat    int64    `json:"Iat"`
	Exp    int64    `json:"Exp"`
	Groups []string `json:"Groups"`
}

type captureSnap struct {
	Method            string           `json:"method"`
	Path              string           `json:"path"`
	AuthorizationSeen string           `json:"authorization_seen"`
	JWTPresent        bool             `json:"jwt_present"`
	JWTClaims         jwtClaimsCapture `json:"jwt_claims"`
}

func TestPhase4JWTValidate(t *testing.T) {
	phase4SuiteGuard(t)

	// mcp-echo must be deployed (testMocks.mcpEcho.enabled=true).
	if err := waitDeploymentReady(t, phase4Namespace, mcpEchoDeployment, 15*time.Second); err != nil {
		t.Skipf(
			"ach-mcp-echo Deployment not Ready (%v) — run `make cluster-up "+
				"HELM_EXTRA_ARGS='--set testMocks.enabled=true "+
				"--set testMocks.mcpEcho.enabled=true'`. Skipping.", err)
	}

	// One port-forward to mcp-echo for /__capture/{reset,last}, shared
	// across both subtests.
	mcpEchoLocal := "8186"
	mcpEchoCleanup := startMcpEchoPortForward(t, mcpEchoLocal)
	defer mcpEchoCleanup()

	t.Run("Direct_MintAndPostToMcpEcho", func(t *testing.T) {
		testJWTValidateDirect(t, mcpEchoLocal)
	})

	t.Run("ViaForwarder_PkRoundTrip", func(t *testing.T) {
		testJWTValidateViaForwarder(t, mcpEchoLocal)
	})
}

// testJWTValidateDirect mints an ACH JWT from the cluster signing
// material and POSTs it directly to ach-mcp-echo, bypassing the
// Forwarder + LiteLLM. The whole point is to isolate the mcp-echo
// verifier code so a regression there fails this test even if the
// upstream chain is broken or in flux.
func testJWTValidateDirect(t *testing.T, mcpEchoLocal string) {
	kid, seed := readJWTSigningMaterial(t)
	expectedIss := mcpEchoExpectedIss(t)
	expectedAud := mcpEchoExpectedAud(t)

	now := time.Now().Unix()
	tok := mintAchJWT(t, kid, seed, jwtv5.MapClaims{
		"iss":    expectedIss,
		"sub":    "ach-system/e2e-direct@test",
		"aud":    expectedAud,
		"iat":    now,
		"exp":    now + 120,
		"groups": []string{"default"}, // mirrors the demo Environment's sole authorizedTeams entry, per assertCapturedClaims
	})

	resetMcpEchoCapture(t, mcpEchoLocal)

	// 1. initialize — mcp-go's Streamable-HTTP requires it before any
	//    tools/* call and replies with the Mcp-Session-Id header.
	sessionID := mcpInitialize(t, mcpEchoLocal, tok)

	// 2. tools/call echo with the same JWT (within the 120s window) and
	//    the session id.
	echoed := "hola-from-direct-test"
	body := callEchoTool(t, mcpEchoLocal, tok, sessionID, "echo", echoed)
	if !strings.Contains(body, echoed) {
		t.Fatalf("tools/call result did not echo back %q: %s", echoed, body)
	}

	// 3. Capture surface should show the JWT we just minted.
	snap := readMcpEchoCapture(t, mcpEchoLocal)
	assertCapturedClaims(t, snap, expectedIss, expectedAud, kid)
}

// testJWTValidateViaForwarder mints a pk_ via SSO and drives a real
// /mcp/demo-mcp-echo/ request through the Forwarder + LiteLLM. The
// JWT we assert on is minted by the Forwarder (FWD-07 path) — not by
// the test — so this catches BIP cache, signer wiring, and the
// LiteLLM extra_headers:["authorization"] propagation in one test.
func testJWTValidateViaForwarder(t *testing.T, mcpEchoLocal string) {
	gatewayLocal := "8187"
	gwCleanup := phase4StartGatewayPortForward(t, gatewayLocal)
	defer gwCleanup()

	pk := phase4AcquirePkAutomatically(t, gatewayLocal)

	resetMcpEchoCapture(t, mcpEchoLocal)

	// MCP through the LiteLLM gateway is stateless from the client's
	// perspective — LiteLLM owns the upstream MCP session lifecycle and
	// proxies tools/* directly without exposing Mcp-Session-Id to the
	// caller. We POST tools/call straight; LiteLLM hands the request to
	// mcp-echo (with Authorization preserved via extra_headers, §3 of
	// docs/developer-guide/jwt-forwarder.md).
	gatewayMCP := fmt.Sprintf("http://localhost:%s/mcp/%s/", gatewayLocal, mcpEchoBareName)
	echoed := "hola-from-forwarder-test"
	callBody := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+
			`{"name":"%s","arguments":{"text":%q}}}`,
		mcpEchoToolFQN, echoed)
	resp := postMCPViaForwarder(t, gatewayMCP, pk, callBody)
	if !strings.Contains(resp, echoed) {
		t.Fatalf("tools/call result did not echo back %q: %s", echoed, resp)
	}

	expectedIss := forwarderBaseURL(t)
	expectedAud := "mcp:" + mcpEchoBareName
	snap := readMcpEchoCapture(t, mcpEchoLocal)
	assertCapturedClaims(t, snap, expectedIss, expectedAud, "" /* any kid */)
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

func startMcpEchoPortForward(t *testing.T, localPort string) func() {
	t.Helper()
	cmd := exec.Command("kubectl", "port-forward",
		"-n", phase4Namespace,
		"svc/"+mcpEchoService,
		localPort+":"+mcpEchoSvcPort,
	)
	if err := cmd.Start(); err != nil {
		t.Skipf("startMcpEchoPortForward: cannot start port-forward: %v", err)
		return func() {}
	}
	// Bounded readiness probe — never an unbounded polling loop.
	healthURL := fmt.Sprintf("http://localhost:%s/healthz", localPort)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL) //nolint:gosec // localhost PF
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return func() { _ = cmd.Process.Kill() }
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Skipf("startMcpEchoPortForward: /healthz never reached 200 within 15s")
	return func() {}
}

func waitDeploymentReady(t *testing.T, ns, name string, deadline time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "-n", ns,
		"rollout", "status", "deployment/"+name,
		fmt.Sprintf("--timeout=%ds", int(deadline.Seconds())))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl rollout: %v output=%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func readJWTSigningMaterial(t *testing.T) (kid string, seed []byte) {
	t.Helper()
	// kubectlGetSecretBytes returns the post-base64-decode raw bytes.
	// For current.kid this is the ASCII identifier; for current.seed it
	// is the 32 raw bytes of the Ed25519 seed (NOT base64-encoded any
	// further — that's the cluster contract; cluster.sh seeds via
	// `openssl rand 32 > seedfile` + `--from-file=`, so the secret data
	// holds the raw bytes b64-encoded once by kubectl).
	kidRaw := kubectlGetSecretBytes(t, jwtSecretName, "current.kid")
	seed = kubectlGetSecretBytes(t, jwtSecretName, "current.seed")
	if len(seed) != ed25519.SeedSize {
		t.Fatalf("current.seed: got %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	return string(kidRaw), seed
}

func kubectlGetSecretBytes(t *testing.T, name, key string) []byte {
	t.Helper()
	// kubectl jsonpath escaping: '.' in key names must be \\. so the
	// shell preserves the literal dot in the JSONPath expression.
	jp := fmt.Sprintf(`jsonpath={.data.%s}`, strings.ReplaceAll(key, ".", `\.`))
	cmd := exec.Command("kubectl", "-n", phase4Namespace, "get", "secret", name, "-o", jp)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("kubectl get secret %s key %s: %v", name, key, err)
	}
	rawB64 := strings.TrimSpace(string(out))
	if rawB64 == "" {
		t.Fatalf("kubectl get secret %s key %s: empty value", name, key)
	}
	raw, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		t.Fatalf("decode secret %s key %s: %v", name, key, err)
	}
	return raw
}

func mintAchJWT(t *testing.T, kid string, seed []byte, claims jwtv5.MapClaims) string {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(seed)
	tok := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, claims)
	tok.Header["kid"] = kid
	tok.Header["typ"] = "JWT"
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signed
}

// mcpEchoExpectedIss / mcpEchoExpectedAud read what the deployed
// mcp-echo Pod is actually configured to expect. Test-side overrides
// remain available via env so engineers can probe non-default
// deployments without redeploying.
func mcpEchoExpectedIss(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("ACH_E2E_EXPECTED_ISS"); v != "" {
		return v
	}
	return kubectlReadDeployEnv(t, mcpEchoDeployment, "mcp-echo", "ACH_EXPECTED_ISS")
}

func mcpEchoExpectedAud(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("ACH_E2E_EXPECTED_AUD"); v != "" {
		return v
	}
	// ACH_EXPECTED_AUD is comma-separated; take the first entry.
	raw := kubectlReadDeployEnv(t, mcpEchoDeployment, "mcp-echo", "ACH_EXPECTED_AUD")
	if i := strings.Index(raw, ","); i >= 0 {
		return strings.TrimSpace(raw[:i])
	}
	return raw
}

func forwarderBaseURL(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("ACH_BASE_URL"); v != "" {
		return v
	}
	// Read the live container env — same approach as mcpEchoExpectedIss.
	return kubectlReadDeployEnv(t, "ach-forwarder", "forwarder", "ACH_BASE_URL")
}

func kubectlReadDeployEnv(t *testing.T, deploy, container, varName string) string {
	t.Helper()
	jp := fmt.Sprintf(
		`jsonpath={.spec.template.spec.containers[?(@.name=="%s")].env[?(@.name=="%s")].value}`,
		container, varName)
	cmd := exec.Command("kubectl", "-n", phase4Namespace, "get", "deploy", deploy, "-o", jp)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read deploy/%s env %s: %v", deploy, varName, err)
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		t.Fatalf("read deploy/%s env %s: empty (container=%s)", deploy, varName, container)
	}
	return v
}

func resetMcpEchoCapture(t *testing.T, localPort string) {
	t.Helper()
	url := fmt.Sprintf("http://localhost:%s/__capture/reset", localPort)
	resp, err := http.Post(url, "", nil) //nolint:gosec // localhost PF
	if err != nil {
		t.Fatalf("POST /__capture/reset: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /__capture/reset: status %d", resp.StatusCode)
	}
}

func readMcpEchoCapture(t *testing.T, localPort string) captureSnap {
	t.Helper()
	url := fmt.Sprintf("http://localhost:%s/__capture/last", localPort)
	resp, err := http.Get(url) //nolint:gosec // localhost PF
	if err != nil {
		t.Fatalf("GET /__capture/last: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var snap captureSnap
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode /__capture/last: %v", err)
	}
	return snap
}

func assertCapturedClaims(t *testing.T, snap captureSnap, wantIss, wantAud, wantKid string) {
	t.Helper()
	if !strings.HasPrefix(snap.AuthorizationSeen, "Bearer ey") {
		t.Fatalf("Authorization not a JWT: %q", snap.AuthorizationSeen)
	}
	if snap.JWTClaims.Iss != wantIss {
		t.Fatalf("iss: got %q want %q", snap.JWTClaims.Iss, wantIss)
	}
	if snap.JWTClaims.Aud != wantAud {
		t.Fatalf("aud: got %q want %q", snap.JWTClaims.Aud, wantAud)
	}
	if wantKid != "" && snap.JWTClaims.Kid != wantKid {
		t.Fatalf("kid: got %q want %q", snap.JWTClaims.Kid, wantKid)
	}
	if snap.JWTClaims.Kid == "" {
		t.Fatalf("kid empty — JWKS lookup did not happen")
	}
	if delta := snap.JWTClaims.Exp - snap.JWTClaims.Iat; delta != 120 {
		t.Fatalf("exp-iat = %d, want 120 (FWD-07)", delta)
	}
	if snap.JWTClaims.Sub == "" {
		t.Fatalf("sub empty — Forwarder/test should set ach-system/<owner>")
	}
	// The demo Environment authorizes exactly one team ("default"), so both
	// the ek_ and pk_ paths mint the same single-entry groups claim. ACH's
	// shell teams (ach-env-*/ach-user-*) must never appear.
	if len(snap.JWTClaims.Groups) != 1 || snap.JWTClaims.Groups[0] != "default" {
		t.Fatalf("groups = %v; want [default]", snap.JWTClaims.Groups)
	}
}

// mcpInitialize POSTs the MCP initialize handshake to mcp-echo (direct,
// no Forwarder hop) and returns the Mcp-Session-Id header. mcp-go's
// Streamable-HTTP server requires this before accepting any tools/*
// call.
func mcpInitialize(t *testing.T, localPort, tok string) string {
	t.Helper()
	url := fmt.Sprintf("http://localhost:%s/", localPort)
	body := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
			`{"protocolVersion":"2024-11-05","capabilities":{},` +
			`"clientInfo":{"name":"e2e-direct","version":"1"}}}`)
	req, _ := http.NewRequest(http.MethodPost, url, body)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialize POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("initialize: status %d body=%s", resp.StatusCode, raw)
	}
	return resp.Header.Get("Mcp-Session-Id")
}

func callEchoTool(t *testing.T, localPort, tok, sessionID, toolName, text string) string {
	t.Helper()
	url := fmt.Sprintf("http://localhost:%s/", localPort)
	payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":{"text":%q}}}`,
		toolName, text)
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Mcp-Session-Id", sessionID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tools/call POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("tools/call: status %d body=%s", resp.StatusCode, raw)
	}
	out, _ := io.ReadAll(resp.Body)
	return string(out)
}

// postMCPViaForwarder POSTs through the local-gateway → forwarder →
// LiteLLM chain and returns the response body. Uses x-ach-key (the
// Forwarder accepts both `x-ach-key: pk_…` and
// `Authorization: Bearer pk_…`; we use x-ach-key to mirror the
// documented client in examples/test-mcp-jwt.sh).
func postMCPViaForwarder(t *testing.T, url, pk, body string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("x-ach-key", pk)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("forwarder POST %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("forwarder POST %s: status %d body=%s", url, resp.StatusCode, raw)
	}
	out, _ := io.ReadAll(resp.Body)
	return string(out)
}
