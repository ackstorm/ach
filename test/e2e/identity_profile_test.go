//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestIdentityProfile covers the second release — `profile: identity` as
// http://api.e2e.local:8080 (cluster/02-ach/identity.values.yaml, namespace
// ach-identity): ACH in front of the whole LiteLLM API with no operator, no
// CRDs, no Environments, no BIP. Its own AS, its own database, its own
// signing key (minted by the forwarder), the declared credential headers.
func TestIdentityProfile(t *testing.T) {
	const base = "http://api.e2e.local:8080"
	client := &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(t *testing.T, method, path string, hdr map[string]string, body string) (int, http.Header, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, raw
	}

	t.Run("passthrough_header", func(t *testing.T) {
		code, _, raw := do(t, http.MethodGet, "/v1/models", map[string]string{"x-genai-api-key": sc5MasterKey}, "")
		if code != 200 || !strings.Contains(string(raw), `"data"`) {
			t.Fatalf("x-genai-api-key on /v1/models: %d %s", code, raw)
		}
	})

	t.Run("catch_all_anonymous", func(t *testing.T) {
		if code, _, raw := do(t, http.MethodGet, "/health/liveliness", nil, ""); code != 200 {
			t.Fatalf("anonymous /health/liveliness through gateway + forwarder: %d %s", code, raw)
		}
		// LiteLLM composes this redirect from Host: preserved on both hops,
		// so it names the front door, not the Service.
		code, hdr, _ := do(t, http.MethodGet, "/ui", nil, "")
		if code != 307 || hdr.Get("Location") != base+"/ui/" {
			t.Fatalf("/ui redirect: %d Location=%q (want 307 → %s/ui/)", code, hdr.Get("Location"), base)
		}
	})

	t.Run("v1_challenge_and_document", func(t *testing.T) {
		code, hdr, _ := do(t, http.MethodGet, "/v1/models", nil, "")
		want := `resource_metadata="` + base + `/.well-known/oauth-protected-resource/v1"`
		if code != 401 || !strings.Contains(hdr.Get("WWW-Authenticate"), want) {
			t.Fatalf("anonymous /v1/models: %d %q (want 401 + %s)", code, hdr.Get("WWW-Authenticate"), want)
		}
		code, _, raw := do(t, http.MethodGet, "/.well-known/oauth-protected-resource/v1", nil, "")
		var prm map[string]any
		_ = json.Unmarshal(raw, &prm)
		as, _ := prm["authorization_servers"].([]any)
		if code != 200 || prm["resource"] != base+"/v1" || len(as) != 1 || as[0] != base {
			t.Fatalf("PRM /v1: %d %s", code, raw)
		}
		code, _, raw = do(t, http.MethodGet, "/.well-known/oauth-authorization-server", nil, "")
		var asDoc map[string]any
		_ = json.Unmarshal(raw, &asDoc)
		if code != 200 || asDoc["issuer"] != base {
			t.Fatalf("AS document on the identity host: %d %s", code, raw)
		}
	})

	t.Run("authorization_is_only_our_token", func(t *testing.T) {
		for _, v := range []string{"Bearer " + sc5MasterKey, "Bearer pk-not-a-slot"} {
			if code, _, _ := do(t, http.MethodGet, "/v1/models", map[string]string{"Authorization": v}, ""); code != 401 {
				t.Fatalf("Authorization %q: %d, want 401", v, code)
			}
		}
	})

	t.Run("oauth_ceremony_then_mcp_without_bip", func(t *testing.T) {
		access := identityAccessToken(t, base)
		if code, _, raw := do(t, http.MethodGet, "/v1/models", map[string]string{"Authorization": "Bearer " + access}, ""); code != 200 {
			t.Fatalf("/v1/models with the identity token: %d %s", code, raw)
		}
		// No Environments, no precheck, no BIP JWT: the pod (requireJwt=false)
		// sees a plain platform caller and LiteLLM reports ok.
		if st := serverOutcome(t, base, access, "demo-mcp-jwt"); st != "ok" {
			t.Fatalf("identity /mcp/demo-mcp-jwt: outcome %q", st)
		}
	})
}

// identityAccessToken runs the OAuth ceremony against the identity release's
// own AS (DCR → /authorize → Dex mock → code → /token) and returns the access
// token. The redirect chain is followed with every hop rewritten to base, as
// followToLoopback does for the full release.
func identityAccessToken(t *testing.T, base string) string {
	t.Helper()
	noRedirect := &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	regBody, _ := json.Marshal(map[string]any{"client_name": "e2e-identity", "redirect_uris": []string{"http://127.0.0.1:1/cb"}})
	resp, err := noRedirect.Post(base+"/platform/oauth/register", "application/json", strings.NewReader(string(regBody)))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	_ = resp.Body.Close()
	if resp.StatusCode != 201 || reg.ClientID == "" {
		t.Fatalf("identity register: %d %+v", resp.StatusCode, reg)
	}
	verifier := "e2e-identity-verifier-" + strings.Repeat("y", 40)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {"http://127.0.0.1:1/cb"},
		"state": {"s1"}, "code_challenge_method": {"S256"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"resource":       {base + "/v1"},
	}
	authCode, hops := followToLoopbackWithCount(t, noRedirect, base, base+"/platform/oauth/authorize?"+q.Encode())
	if authCode == "" || hops != 0 {
		t.Fatalf("identity authorize: code=%q broker hops=%d (identity has no BIP → never chains)", authCode, hops)
	}
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {authCode}, "client_id": {reg.ClientID},
		"redirect_uri": {"http://127.0.0.1:1/cb"}, "code_verifier": {verifier},
	}
	resp, err = noRedirect.Post(base+"/platform/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var tok map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	access, _ := tok["access_token"].(string)
	if resp.StatusCode != 200 || access == "" {
		t.Fatalf("identity token: %d %v", resp.StatusCode, tok)
	}
	return access
}
