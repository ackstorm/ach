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

	"github.com/ackstorm/ach/internal/oauthsvc"
)

// fakeBroker is an mcp-oauth stand-in: /register hands out a client id,
// /authorize records the login_hint and bounces to redirect_uri with a code
// (or the error the test armed).
type fakeBroker struct {
	srv       *httptest.Server
	mu        sync.Mutex
	registers int
	hints     []string
	authz     []url.Values
	deny      bool
	down      bool
}

func newFakeBroker(t *testing.T) *fakeBroker {
	t.Helper()
	b := &fakeBroker{}
	mux := http.NewServeMux()
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.down {
			w.WriteHeader(503)
			return
		}
		b.registers++
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "broker-client-1"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		q := r.URL.Query()
		b.authz = append(b.authz, q)
		b.hints = append(b.hints, q.Get("login_hint"))
		p := url.Values{"state": {q.Get("state")}}
		if b.deny {
			p.Set("error", "access_denied")
		} else {
			p.Set("code", "broker-code")
		}
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+p.Encode(), 302)
	})
	b.srv = httptest.NewServer(mux)
	t.Cleanup(b.srv.Close)
	return b
}

// chainFixture: Dex faked, two services on ONE broker, grants map.
func chainFixture(t *testing.T, email string) (*asFixture, *fakeBroker) {
	t.Helper()
	b := newFakeBroker(t)
	f := withFakeDex(newAS(t), email)
	f.deps.Services = map[string]oauthsvc.Service{
		"mcp-a": {Store: "a-store", Broker: b.srv.URL},
		"mcp-b": {Store: "b-store", Broker: b.srv.URL},
	}
	f.deps.Grants = MapGrants{}
	f.mount()
	return f, b
}

// walk follows redirects from /as-callback through the fake broker and back
// into the AS (rewriting the broker's redirect target to the recorder),
// carrying the binding cookie, until a hop lands on the client redirect_uri.
func walk(t *testing.T, f *asFixture, b *fakeBroker, start string, cookie map[string]string) string {
	t.Helper()
	next := start // always an AS path: broker hops are resolved inline below
	for hop := 0; hop < 10; hop++ {
		w := f.do(t, "GET", next, nil, cookie)
		loc := w.Header().Get("Location")
		if w.Code != 302 || loc == "" {
			t.Fatalf("hop %d %s: %d %s", hop, next, w.Code, w.Body)
		}
		if strings.HasPrefix(loc, "http://127.0.0.1:5000/cb") {
			return loc
		}
		if strings.HasPrefix(loc, b.srv.URL) {
			// ask the broker (no redirect following) and continue with its Location
			c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := c.Get(loc)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			loc = resp.Header.Get("Location")
		}
		next = strings.TrimPrefix(loc, "https://ach.test")
	}
	t.Fatal("chain never reached the client")
	return ""
}

func TestChain_OneHopPerUngrantedService(t *testing.T) {
	f, b := chainFixture(t, "U@X.com")
	f.deps.Grants = MapGrants{"a-store|u@x.com": true} // mcp-a already granted
	f.mount()
	cid := registerClient(t, f)
	w := f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": "ach mcp-a mcp-b"}), nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	cs := w.Result().Cookies()
	cookie := map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value}

	loc := walk(t, f, b, "/platform/oauth/as-callback?code=dexcode&state="+state, cookie)
	if !strings.Contains(loc, "code=") || !strings.Contains(loc, "state=xyz") {
		t.Fatalf("client redirect: %s", loc)
	}
	if b.registers != 1 || len(b.authz) != 1 {
		t.Fatalf("expected exactly one broker hop (mcp-b): registers=%d authz=%d", b.registers, len(b.authz))
	}
	q := b.authz[0]
	if q.Get("scope") != "b-store" || q.Get("client_id") != "broker-client-1" || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != "https://ach.test/platform/oauth/broker-callback" || q.Get("response_type") != "code" {
		t.Fatalf("broker authorize params: %v", q)
	}
	// login_hint: our key, iss = issuer, sub = lowercased email, aud = store, ≤ 600 s
	v, err := f.deps.Signer.Verify(b.hints[0], "https://ach.test", "b-store")
	if err != nil || v.Sub != "u@x.com" {
		t.Fatalf("login_hint: %+v %v", v, err)
	}
	if _, err := f.deps.Signer.Verify(b.hints[0], "https://ach.test", "ach"); err == nil {
		t.Fatal("login_hint must not verify as an ACH access token")
	}
	var hdr, body map[string]any
	parts := strings.Split(b.hints[0], ".")
	_ = json.Unmarshal(b64(t, parts[0]), &hdr)
	_ = json.Unmarshal(b64(t, parts[1]), &body)
	if exp, iat := body["exp"].(float64), body["iat"].(float64); exp-iat > 600 || body["scope"] != nil || body["groups"] != nil {
		t.Fatalf("hint claims: %v", body)
	}
	// the code carries the requested scopes; the cookie is cleared at finish
	code := mustQuery(t, loc, "code")
	var rec oauthCode
	if ok, _ := f.store.Get(context.Background(), "code", code, &rec); !ok || strings.Join(rec.Scopes, " ") != "mcp-a mcp-b" {
		t.Fatalf("code scopes: %+v", rec)
	}
}

func TestChain_BrokerDownOrDeclined_SkipsAndFinishes(t *testing.T) {
	for _, mode := range []string{"down", "deny"} {
		t.Run(mode, func(t *testing.T) {
			f, b := chainFixture(t, "u@x.com")
			b.down, b.deny = mode == "down", mode == "deny"
			cid := registerClient(t, f)
			w := f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": "mcp-a"}), nil, nil)
			state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
			cs := w.Result().Cookies()
			loc := walk(t, f, b, "/platform/oauth/as-callback?code=dexcode&state="+state, map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value})
			if !strings.Contains(loc, "code=") {
				t.Fatalf("must still finish: %s", loc)
			}
		})
	}
}

func TestBrokerCallback_RequiresBindingCookieAndBurnsChain(t *testing.T) {
	f, b := chainFixture(t, "u@x.com")
	cid := registerClient(t, f)
	w := f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": "mcp-a"}), nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	cs := w.Result().Cookies()
	cookie := map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value}
	w = f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie)
	chainID := mustQuery(t, w.Header().Get("Location"), "state")
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), b.srv.URL+"/authorize?") || chainID == "" {
		t.Fatalf("hand-off: %d %s", w.Code, w.Header().Get("Location"))
	}
	// victim's browser (no cookie) arrives with the chain state
	if w := f.do(t, "GET", "/platform/oauth/broker-callback?code=x&state="+chainID, nil, nil); w.Code != 400 {
		t.Fatalf("no cookie: %d", w.Code)
	}
	// burned: the right cookie no longer helps
	if w := f.do(t, "GET", "/platform/oauth/broker-callback?code=x&state="+chainID, nil, cookie); w.Code != 400 {
		t.Fatalf("chain replay: %d", w.Code)
	}
	if w := f.do(t, "GET", "/platform/oauth/broker-callback?code=x&state=nope", nil, cookie); w.Code != 400 {
		t.Fatalf("unknown chain: %d", w.Code)
	}
}

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
