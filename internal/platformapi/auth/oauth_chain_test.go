// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

type fakeBroker struct {
	srv   *httptest.Server
	mu    sync.Mutex
	authz []url.Values
	hints []string
	deny  bool
}

func newFakeBroker(t *testing.T) *fakeBroker {
	t.Helper()
	b := &fakeBroker{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server/broker", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": b.srv.URL + "/broker/authorize",
			"registration_endpoint":  b.srv.URL + "/broker/register",
		})
	})
	mux.HandleFunc("/broker/register", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "broker-client-1"})
	})
	mux.HandleFunc("/broker/authorize", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.authz = append(b.authz, r.URL.Query())
		b.hints = append(b.hints, r.URL.Query().Get("login_hint"))
		b.mu.Unlock()
		q := url.Values{"state": {r.URL.Query().Get("state")}}
		if b.deny {
			q.Set("error", "access_denied")
		}
		http.Redirect(w, r, r.URL.Query().Get("redirect_uri")+"?"+q.Encode(), http.StatusFound)
	})
	b.srv = httptest.NewServer(mux)
	t.Cleanup(b.srv.Close)
	return b
}

func chainFixture(t *testing.T, probe probeOutcome) (*asFixture, *fakeBroker) {
	t.Helper()
	b := newFakeBroker(t)
	f := withFakeDex(newAS(t), "U@X.com")
	calls := 0
	f.deps.Probe = func(context.Context, string, string, string) probeOutcome {
		calls++
		if calls == 1 {
			return probe
		}
		if b.deny {
			return probeAuthRequired
		}
		return probeOK
	}
	f.deps.ConsentBIP = func(context.Context, string) (*db.BIPRow, error) {
		return &db.BIPRow{ConsentBroker: b.srv.URL + "/broker", ConsentAudience: "broker-store"}, nil
	}
	f.mount()
	return f, b
}

func chainAuthorizeURL(cid string) string {
	return authorizeURL(cid, map[string]string{"resource": "https://ach.test/mcp/demo"})
}

func TestChain_UsesBIPMetadataAndOneHop(t *testing.T) {
	f, b := chainFixture(t, probeAuthRequired)
	cid := registerClient(t, f)
	w := f.do(t, "GET", chainAuthorizeURL(cid), nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	cs := w.Result().Cookies()
	cookie := map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value}
	w = f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie)
	loc := w.Header().Get("Location")
	if w.Code != http.StatusFound || !strings.HasPrefix(loc, b.srv.URL+"/broker/authorize?") {
		t.Fatalf("broker handoff: %d %s", w.Code, loc)
	}
	if q := mustQuery(t, loc, "scope"); q != "" || mustQuery(t, loc, "resource") != "" {
		t.Fatalf("broker request must not carry scope/resource: %s", loc)
	}
	if mustQuery(t, loc, "client_id") != "broker-client-1" || mustQuery(t, loc, "code_challenge_method") != "S256" {
		t.Fatalf("broker params: %s", loc)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(loc)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	callback := strings.TrimPrefix(resp.Header.Get("Location"), "https://ach.test")
	w = f.do(t, "GET", callback, nil, cookie)
	if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "code=") {
		t.Fatalf("finish: %d %s", w.Code, w.Body)
	}
	if len(b.authz) != 1 || b.authz[0].Get("scope") != "" {
		t.Fatalf("authz: %v", b.authz)
	}
	hint := b.hints[0]
	sub, err := f.deps.Signer.Verify(hint, "https://ach.test", "broker-store")
	if err != nil || sub != "u@x.com" {
		t.Fatalf("hint: %q %v", sub, err)
	}
	var claims map[string]any
	parts := strings.Split(hint, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(payload, &claims)
	if claims["scope"] != nil || claims["email"] != nil || claims["groups"] != nil {
		t.Fatalf("hint claims: %v", claims)
	}
}

func TestChain_GrantFailureReturns502(t *testing.T) {
	f, b := chainFixture(t, probeAuthRequired)
	b.deny = true
	cid := registerClient(t, f)
	w := f.do(t, "GET", chainAuthorizeURL(cid), nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	cs := w.Result().Cookies()
	cookie := map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value}
	w = f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	w = f.do(t, "GET", strings.TrimPrefix(resp.Header.Get("Location"), "https://ach.test"), nil, cookie)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("denied consent: %d %s", w.Code, w.Body)
	}
}

func TestBrokerCallback_BurnsChainOnBindingFailure(t *testing.T) {
	f, _ := chainFixture(t, probeAuthRequired)
	cid := registerClient(t, f)
	w := f.do(t, "GET", chainAuthorizeURL(cid), nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	cs := w.Result().Cookies()
	cookie := map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value}
	w = f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie)
	chainID := mustQuery(t, w.Header().Get("Location"), "state")
	if w := f.do(t, "GET", "/platform/oauth/broker-callback?state="+chainID, nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("no cookie: %d", w.Code)
	}
	if w := f.do(t, "GET", "/platform/oauth/broker-callback?state="+chainID, nil, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("burned chain replay: %d", w.Code)
	}
}
