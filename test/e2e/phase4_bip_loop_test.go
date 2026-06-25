//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// TestPhase4BIPClosedLoop is the closed-loop proof that the
// BackendIdentityPolicy.spec.forwardIdentityJWT toggle is what changes the
// wire — not the backend. The demo Environment registers TWO MCP routes
// against the SAME ach-mcp-echo backend (examples/04 + examples/11 + 16):
//
//   - demo-mcp-jwt   (BIP forwardIdentityJWT: true)  → the forwarder mints
//     and attaches the ACH JWT; the backend verifies it and records
//     jwt_present=true with iss/aud=mcp:demo-mcp-jwt.
//   - demo-mcp-nojwt (BIP forwardIdentityJWT: false) → the forwarder
//     forwards with NO Authorization header; the backend (running with
//     ACH_REQUIRE_JWT=false, testMocks.mcpEcho.requireJwt) accepts the
//     tokenless request and records jwt_present=false.
//
// Both subtests drive the full client → ach-local-gateway → ach-forwarder
// → LiteLLM → ach-mcp-echo chain with a single pk_, so the ONLY variable
// between them is the BIP. Reuses the phase4_jwt_validate_test.go helpers.
//
// Activation: same prerequisites as TestPhase4JWTValidate — testMocks.enabled
// and testMocks.mcpEcho.enabled (with requireJwt=false) on the kept cluster.
//
// Run via:
//
//	./scripts/dev.sh make e2e-focus RUN=TestPhase4BIPClosedLoop

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	bipJWTRouteName   = "demo-mcp-jwt"
	bipNoJWTRouteName = "demo-mcp-nojwt"
)

func TestPhase4BIPClosedLoop(t *testing.T) {
	phase4SuiteGuard(t)

	if err := waitDeploymentReady(t, phase4Namespace, mcpEchoDeployment, 15*time.Second); err != nil {
		t.Skipf(
			"ach-mcp-echo Deployment not Ready (%v) — run `make cluster-up` with "+
				"testMocks.mcpEcho.enabled=true (requireJwt=false). Skipping.", err)
	}

	mcpEchoLocal := "8190"
	mcpEchoCleanup := startMcpEchoPortForward(t, mcpEchoLocal)
	defer mcpEchoCleanup()

	gatewayLocal := "8191"
	gwCleanup := phase4StartGatewayPortForward(t, gatewayLocal)
	defer gwCleanup()

	pk := phase4AcquirePkAutomatically(t, gatewayLocal)

	t.Run("jwt_route_attaches_jwt", func(t *testing.T) {
		echoed := "hola-bip-jwt"
		callEchoViaForwarderEventually(t, gatewayLocal, mcpEchoLocal, pk, bipJWTRouteName, echoed)
		snap := readMcpEchoCapture(t, mcpEchoLocal)
		if !snap.JWTPresent {
			t.Fatalf("jwt route: jwt_present=false, want true — forwardIdentityJWT:true "+
				"BIP should have minted a JWT. capture=%+v", snap)
		}
		if !strings.HasPrefix(snap.AuthorizationSeen, "Bearer ey") {
			t.Fatalf("jwt route: Authorization not a JWT: %q", snap.AuthorizationSeen)
		}
		wantAud := "mcp:" + bipJWTRouteName
		if snap.JWTClaims.Aud != wantAud {
			t.Fatalf("jwt route: aud=%q want %q", snap.JWTClaims.Aud, wantAud)
		}
		if snap.JWTClaims.Iss != forwarderBaseURL(t) {
			t.Fatalf("jwt route: iss=%q want %q", snap.JWTClaims.Iss, forwarderBaseURL(t))
		}
	})

	t.Run("nojwt_route_omits_jwt", func(t *testing.T) {
		echoed := "hola-bip-nojwt"
		callEchoViaForwarderEventually(t, gatewayLocal, mcpEchoLocal, pk, bipNoJWTRouteName, echoed)
		snap := readMcpEchoCapture(t, mcpEchoLocal)
		if snap.JWTPresent {
			t.Fatalf("nojwt route: jwt_present=true, want false — forwardIdentityJWT:false "+
				"BIP must NOT mint a JWT. capture=%+v", snap)
		}
		if snap.AuthorizationSeen != "" {
			t.Fatalf("nojwt route: authorization_seen=%q, want empty (no JWT forwarded)",
				snap.AuthorizationSeen)
		}
	})
}

// callEchoViaForwarder POSTs a tools/call for the echo tool of the given
// MCP server (route) through the local-gateway → forwarder → LiteLLM chain
// and returns the response body. The tool wire name is "<server>.echo"
// (LiteLLM's MCP gateway prefixes tool names with the server name).
func callEchoViaForwarder(t *testing.T, gatewayLocal, pk, serverName, text string) string {
	t.Helper()
	url := fmt.Sprintf("http://localhost:%s/mcp/%s/", gatewayLocal, serverName)
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+
			`{"name":"%s.echo","arguments":{"text":%q}}}`,
		serverName, text)
	return postMCPViaForwarder(t, url, pk, body)
}

// callEchoViaForwarderEventually drives the echo tool through the forwarder,
// retrying the whole tools/call on the transient LiteLLM MCP tool-discovery
// warmup window. Right after cluster-up rolls the litellm chart, LiteLLM has
// not yet run tools/list against a freshly-(re)registered MCP server, so the
// proxied tools/call returns a 200 isError result — `Tool '<tool>' not found`
// — instead of the echoed text. That is a warmup race, NOT a JWT/BIP-precedence
// signal, and it clears within a few seconds (the same call succeeds moments
// later, e.g. once SC3 runs). postMCPViaForwarder still t.Fatals on a non-200,
// so a genuine transport/auth failure is not silently retried.
//
// Bounded retry with an explicit failure path (no-naked-loop rule). The
// mcp-echo capture is reset before EVERY attempt, including the winning one, so
// a subsequent readMcpEchoCapture reflects the successful echo and never a
// discarded not-found attempt. Returns the winning response body.
func callEchoViaForwarderEventually(t *testing.T, gatewayLocal, mcpEchoLocal, pk, serverName, text string) string {
	t.Helper()
	const attempts = 20
	var resp string
	for i := 0; i < attempts; i++ {
		resetMcpEchoCapture(t, mcpEchoLocal)
		resp = callEchoViaForwarder(t, gatewayLocal, pk, serverName, text)
		if strings.Contains(resp, text) {
			return resp
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("echo never round-tripped %q on /mcp/%s within %ds "+
		"(LiteLLM MCP tool-discovery warmup window?); last resp=%s",
		text, serverName, attempts, resp)
	return resp
}
