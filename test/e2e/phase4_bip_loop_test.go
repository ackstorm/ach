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
		resetMcpEchoCapture(t, mcpEchoLocal)
		callEchoViaForwarder(t, gatewayLocal, pk, bipJWTRouteName, echoed)
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
		resetMcpEchoCapture(t, mcpEchoLocal)
		callEchoViaForwarder(t, gatewayLocal, pk, bipNoJWTRouteName, echoed)
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
	resp := postMCPViaForwarder(t, url, pk, body)
	if !strings.Contains(resp, text) {
		t.Fatalf("MCP server %q tools/call response did not contain echo %q: %s", serverName, text, resp)
	}
	return resp
}
