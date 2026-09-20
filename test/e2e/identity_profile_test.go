//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
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

	t.Run("device_grant", func(t *testing.T) {
		access := deviceGrant(t, base)
		bearer := map[string]string{"Authorization": "Bearer " + access}
		if code, _, raw := do(t, http.MethodGet, "/v1/models", bearer, ""); code != 200 {
			t.Fatalf("/v1/models with the device-grant token: %d %s", code, raw)
		}
	})

	t.Run("oauth_ceremony_then_mcp_without_bip", func(t *testing.T) {
		// Identity has no BIP → the ceremony never chains to a broker.
		access := oauthLogin(t, base, base+"/v1").Access
		bearer := map[string]string{"Authorization": "Bearer " + access}
		if code, _, raw := do(t, http.MethodGet, "/v1/models", bearer, ""); code != 200 {
			t.Fatalf("/v1/models with the identity token: %d %s", code, raw)
		}
		// No Environments, no precheck, no BIP JWT: the pod (requireJwt=false)
		// sees a plain platform caller and LiteLLM reports ok.
		if st := serverOutcome(t, base, access, "demo-mcp-jwt"); st != "ok" {
			t.Fatalf("identity /mcp/demo-mcp-jwt: outcome %q", st)
		}
	})
}
