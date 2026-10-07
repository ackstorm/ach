//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	trustedIdPClient = "e2e-chat"
	trustedIdPSecret = "e2e-chat-not-for-prod"
	mockDexEmail     = "kilgore@kilgore.trout"
)

// TestTrustedIdP — forwarder.trustedIdP: a Dex access token minted for a
// non-ACH client (LibreChat's shape) is accepted at the forwarder as
// `Authorization: Bearer` and resolves to the user's EXISTING oauth pk_:
// /v1 works, the BIP JWT names the user, spend lands on user:<email>, and a
// revoked oauth pk_ is a 403 ach_login_required, never a mint.
func TestTrustedIdP(t *testing.T) {
	phase4SuiteGuard(t)
	base := "http://" + phase4GatewayAuthority(t)
	// The ACH login guarantees the oauth pk_ the Dex token maps onto.
	access := oauthLogin(t, base, "").Access

	t.Run("v1_models", func(t *testing.T) {
		code, body := bearerDo(t, http.MethodGet, base+"/v1/models", dexAccessToken(t, base), "")
		if code != http.StatusOK || !strings.Contains(body, `"data"`) {
			t.Fatalf("/v1/models with a Dex token: %d %s", code, truncate([]byte(body), 400))
		}
	})

	t.Run("mcp_bip_identity", func(t *testing.T) {
		if err := waitDeploymentReady(t, phase4Namespace, mcpEchoDeployment, 15*time.Second); err != nil {
			t.Skipf("ach-mcp-echo not Ready: %v", err)
		}
		mcpEchoLocal := "8192"
		defer startMcpEchoPortForward(t, mcpEchoLocal)()
		resetMcpEchoCapture(t, mcpEchoLocal)
		code, body := bearerDo(t, http.MethodPost, base+"/mcp/"+bipJWTRouteName+"/", dexAccessToken(t, base),
			fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s.echo","arguments":{"text":"hola-dex"}}}`, bipJWTRouteName))
		if code != http.StatusOK || !strings.Contains(body, "hola-dex") {
			t.Fatalf("MCP tools/call with a Dex token: %d %s", code, truncate([]byte(body), 400))
		}
		snap := readMcpEchoCapture(t, mcpEchoLocal)
		if !snap.JWTPresent || snap.JWTClaims.Sub != mockDexEmail || snap.JWTClaims.Iss != forwarderBaseURL(t) {
			t.Fatalf("backend must see ACH's BIP JWT for %s (never the Dex token): %+v", mockDexEmail, snap)
		}
	})

	t.Run("user_budget_tag", func(t *testing.T) {
		llPort := startPortForward(t, sc5LiteLLMNS, sc5LiteLLMSvc, 4000)
		llURL := fmt.Sprintf("http://127.0.0.1:%d", llPort)
		withMcpQueryCost(t, llURL, costedMcpServer, mcpQueryCost)
		tag := "user:" + mockDexEmail
		before := 0.0
		if e, ok := tagInfo(t, llURL, tag); ok && e != nil {
			before = e.Spend
		}
		dex := dexAccessToken(t, base)
		call := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s.echo","arguments":{"text":"tag"}}}`, costedMcpServer)
		if code, body := bearerDo(t, http.MethodPost, base+"/mcp/"+costedMcpServer+"/", dex, call); code != http.StatusOK {
			t.Fatalf("costed MCP call: %d %s", code, truncate([]byte(body), 400))
		}
		within(t, tagSpendWindow, "spend to land on "+tag, func() bool {
			e, ok := tagInfo(t, llURL, tag)
			return ok && e != nil && e.Spend > before
		})
	})

	t.Run("foreign_issuer_401", func(t *testing.T) {
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://evil.example","aud":"e2e-chat","email":"` + mockDexEmail + `"}`))
		code, _, hdr := bearerDoHdr(t, http.MethodGet, base+"/v1/models", "eyJhbGciOiJSUzI1NiJ9."+payload+".c2ln", "")
		if code != http.StatusUnauthorized || hdr.Get("WWW-Authenticate") == "" {
			t.Fatalf("foreign-issuer JWS: %d challenge=%q, want 401 + challenge", code, hdr.Get("WWW-Authenticate"))
		}
	})

	t.Run("revoked_oauth_pk_403", func(t *testing.T) {
		revokeOAuthPK(t, base, access)
		// Restore the row for the rest of the kept-cluster suite.
		t.Cleanup(func() { oauthLogin(t, base, "") })
		// A fresh token is a fresh cache key: no 60 s positive entry to wait out.
		code, body := bearerDo(t, http.MethodGet, base+"/v1/models", dexAccessToken(t, base), "")
		if code != http.StatusForbidden || !strings.Contains(body, "ach_login_required") {
			t.Fatalf("revoked oauth pk_: %d %s, want 403 ach_login_required", code, truncate([]byte(body), 400))
		}
	})
}

// dexAccessToken runs the code flow for the e2e-chat static client against
// the mock connector (auto-approve) and returns Dex's access token.
func dexAccessToken(t *testing.T, base string) string {
	t.Helper()
	noRedirect := &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	q := url.Values{"response_type": {"code"}, "client_id": {trustedIdPClient},
		"redirect_uri": {"http://127.0.0.1:1/cb"}, "scope": {"openid email"}, "state": {"s1"}}
	code := followToLoopback(t, noRedirect, base, base+"/dex/auth?"+q.Encode())
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"http://127.0.0.1:1/cb"}}
	req, _ := http.NewRequest(http.MethodPost, base+"/dex/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(trustedIdPClient, trustedIdPSecret)
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var tok struct {
		Access string `json:"access_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	if resp.StatusCode != http.StatusOK || strings.Count(tok.Access, ".") != 2 {
		t.Fatalf("dex token: %d (access token JWS-shaped=%v)", resp.StatusCode, strings.Count(tok.Access, ".") == 2)
	}
	return tok.Access
}

func bearerDo(t *testing.T, method, u, bearer, body string) (int, string) {
	t.Helper()
	code, b, _ := bearerDoHdr(t, method, u, bearer, body)
	return code, b
}

func bearerDoHdr(t *testing.T, method, u, bearer, body string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), resp.Header
}
