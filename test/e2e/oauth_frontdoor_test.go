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

// TestOAuthFrontDoor drives the whole human ceremony against the kept
// cluster with plain HTTP (no CLI): discovery → DCR → /authorize (through
// the Dex mockCallback connector) → code → /token → a JWT that the
// forwarder accepts on /v1 → refresh rotation → revoke of the OAuth pk_ →
// 401 within the cache window.
func TestOAuthFrontDoor(t *testing.T) {
	base := strings.TrimRight(phase7BaseURL(), "/")
	noRedirect := &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	getJSON := func(t *testing.T, path string, hdr map[string]string) (int, http.Header, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var doc map[string]any
		_ = json.Unmarshal(body, &doc)
		return resp.StatusCode, resp.Header, doc
	}

	// 1. Discovery documents are ACH-composed and anonymous.
	code, _, as := getJSON(t, "/.well-known/oauth-authorization-server", nil)
	if code != 200 || as["registration_endpoint"] != base+"/platform/oauth/register" {
		t.Fatalf("AS metadata: %d %v", code, as)
	}
	code, _, prm := getJSON(t, "/.well-known/oauth-protected-resource/mcp/demo-mcp-echo", nil)
	if code != 200 || prm["resource"] != base+"/mcp/demo-mcp-echo" {
		t.Fatalf("PRM: %d %v", code, prm)
	}
	if srv, _ := prm["authorization_servers"].([]any); len(srv) != 1 || srv[0] != base {
		t.Fatalf("PRM authorization_servers = %v, want [%s]", prm["authorization_servers"], base)
	}

	// 2. Anonymous request → 401 + the RFC 9728 pointer to the service root.
	code, hdr, _ := getJSON(t, "/mcp/demo-mcp-echo/messages", nil)
	wantChallenge := `Bearer resource_metadata="` + base + `/.well-known/oauth-protected-resource/mcp/demo-mcp-echo"`
	if code != 401 || hdr.Get("WWW-Authenticate") != wantChallenge {
		t.Fatalf("anonymous /mcp: %d %q", code, hdr.Get("WWW-Authenticate"))
	}

	// 3. DCR.
	regBody, _ := json.Marshal(map[string]any{
		"client_name": "e2e", "redirect_uris": []string{"http://127.0.0.1:1/cb"},
	})
	resp, err := noRedirect.Post(base+"/platform/oauth/register", "application/json",
		strings.NewReader(string(regBody)))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	if resp.StatusCode != 201 || reg.ClientID == "" {
		t.Fatalf("register: %d %+v", resp.StatusCode, reg)
	}

	// 4. /authorize → Dex (mockCallback, skipApprovalScreen) → as-callback →
	//    a redirect to our (unreachable) loopback URI carrying the code.
	verifier := "e2e-verifier-" + strings.Repeat("x", 40)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {"http://127.0.0.1:1/cb"},
		"state": {"s1"}, "code_challenge_method": {"S256"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"resource":       {base + "/mcp/demo-mcp-echo"},
	}
	authCode := followToLoopback(t, noRedirect, base, base+"/platform/oauth/authorize?"+q.Encode())

	// 5. /token — the JWT + the one purpose='oauth' pk_ behind it.
	tokenPost := func(t *testing.T, form url.Values) (int, map[string]any) {
		t.Helper()
		resp, err := noRedirect.Post(base+"/platform/oauth/token",
			"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}
	mintedAt := time.Now().Add(-5 * time.Second)
	code, tok := tokenPost(t, url.Values{
		"grant_type": {"authorization_code"}, "code": {authCode}, "client_id": {reg.ClientID},
		"redirect_uri": {"http://127.0.0.1:1/cb"}, "code_verifier": {verifier},
	})
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if code != 200 || access == "" || refresh == "" || tok["token_type"] != "Bearer" {
		t.Fatalf("token: %d %v", code, tok)
	}

	// 6. The JWT works on /v1 in both slots; Authorization is consumed.
	for _, h := range []map[string]string{{"Authorization": "Bearer " + access}, {"x-ach-key": access}} {
		if code, _, body := getJSON(t, "/v1/models", h); code != 200 {
			t.Fatalf("/v1/models with %v: %d %v", h, code, body)
		}
	}

	// 7. Refresh rotates; the old refresh token dies.
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {reg.ClientID}}
	code, second := tokenPost(t, refreshForm)
	if code != 200 || second["refresh_token"] == refresh {
		t.Fatalf("refresh: %d %v", code, second)
	}
	if code, _ := tokenPost(t, refreshForm); code != 400 {
		t.Fatalf("old refresh must be dead: %d", code)
	}

	// 8. Revoke the OAuth pk_ → the JWT stops working within the cache window.
	revokeNewestPK(t, base, access, mintedAt)
	deadline := time.Now().Add(90 * time.Second)
	for {
		code, _, _ = getJSON(t, "/v1/models", map[string]string{"Authorization": "Bearer " + access})
		if code == 401 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("revoked OAuth pk_ still answers %d after 90s", code)
		}
		time.Sleep(3 * time.Second)
	}
	// A fresh grant re-mints a row: the AS itself is unaffected by the revoke.
	refreshForm.Set("refresh_token", second["refresh_token"].(string))
	if code, third := tokenPost(t, refreshForm); code != 200 {
		t.Fatalf("post-revoke refresh must re-mint: %d %v", code, third)
	}
}

// revokeNewestPK deletes the caller's newest active pk_ (the OAuth row the
// token endpoint just minted — ListKeys is created_at DESC) with ?force=true,
// since it is also the key authenticating the call.
func revokeNewestPK(t *testing.T, base, access string, mintedAfter time.Time) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/platform/keys?type=pk&status=active", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []struct {
			KeyID     string `json:"key_id"`
			CreatedAt string `json:"created_at"`
		} `json:"items"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(list.Items) == 0 {
		t.Fatalf("list keys: %d %+v", resp.StatusCode, list)
	}
	created, _ := time.Parse(time.RFC3339, list.Items[0].CreatedAt)
	if created.Before(mintedAfter) {
		t.Fatalf("newest pk_ is not the OAuth row minted at /token: %+v", list.Items[0])
	}
	req, _ = http.NewRequest(http.MethodDelete, base+"/platform/keys/"+list.Items[0].KeyID+"?force=true", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode/100 != 2 {
		t.Fatalf("revoke %s: err=%v status=%v", list.Items[0].KeyID, err, resp)
	}
	resp.Body.Close()
}

// followToLoopback walks the authorize → Dex → as-callback redirect chain
// (rewriting every hop's authority to the single gateway origin, like
// ssoMintPK) until a hop points at the client's loopback redirect_uri, and
// returns the code it carries.
func followToLoopback(t *testing.T, client *http.Client, base, start string) string {
	t.Helper()
	baseURL, _ := url.Parse(base)
	cookies := map[string]string{}
	next := start
	for hop := 0; hop < 12; hop++ {
		req, _ := http.NewRequest(http.MethodGet, next, nil)
		if len(cookies) > 0 {
			req.Header.Set("Cookie", encodeCookieMap(cookies))
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("hop %d GET %s: %v", hop, next, err)
		}
		for _, c := range resp.Cookies() {
			cookies[c.Name] = c.Value
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode/100 != 3 || loc == "" {
			t.Fatalf("hop %d %s: %d (no redirect)\n%s", hop, next, resp.StatusCode, truncate(body, 600))
		}
		if strings.HasPrefix(loc, "http://127.0.0.1:1/cb") {
			u, _ := url.Parse(loc)
			if e := u.Query().Get("error"); e != "" || u.Query().Get("state") != "s1" {
				t.Fatalf("client redirect: %s", loc)
			}
			return u.Query().Get("code")
		}
		cur, _ := url.Parse(next)
		ref, err := url.Parse(loc)
		if err != nil {
			t.Fatal(err)
		}
		abs := cur.ResolveReference(ref)
		abs.Scheme, abs.Host = baseURL.Scheme, baseURL.Host
		next = abs.String()
	}
	t.Fatal("authorize chain never reached the loopback redirect")
	return ""
}
