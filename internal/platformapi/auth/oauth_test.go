// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/oauthsvc"
)

func newOAuthStore(t *testing.T) *OAuthStore {
	t.Helper()
	mr := miniredis.RunT(t)
	return &OAuthStore{RDB: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
}

type sample struct{ Sub string }

func TestOAuthStore_PutGetTakeAndKinds(t *testing.T) {
	s := newOAuthStore(t)
	ctx := context.Background()
	if err := s.Put(ctx, "code", "abc", sample{"u@x"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	var got sample
	ok, err := s.Get(ctx, "code", "abc", &got)
	if err != nil || !ok || got.Sub != "u@x" {
		t.Fatalf("get: ok=%v err=%v got=%+v", ok, err, got)
	}
	if ok, _ := s.Get(ctx, "client", "abc", &got); ok {
		t.Fatal("kinds must not share a namespace")
	}
	if ok, _ = s.Take(ctx, "code", "abc", &got); !ok {
		t.Fatal("take: expected hit")
	}
	if ok, _ = s.Get(ctx, "code", "abc", &got); ok {
		t.Fatal("take must be single-use")
	}
}

// asFixture is a bare chi router with only the AS mounted.
type asFixture struct {
	r     chi.Router
	deps  OAuthDeps
	store *OAuthStore
}

func newAS(t *testing.T) *asFixture {
	t.Helper()
	store := newOAuthStore(t)
	signer := jwt.NewEd25519Signer()
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	if err := jwt.LoadSeed(signer, "k1", seed); err != nil {
		t.Fatal(err)
	}
	f := &asFixture{store: store, deps: OAuthDeps{
		Auth:  Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		Store: store, Signer: signer,
		Issuer: "https://ach.test", Audience: "ach",
		AccessTTL: time.Hour, RefreshTTL: 30 * 24 * time.Hour,
	}}
	f.mount()
	return f
}

func (f *asFixture) mount() {
	f.r = chi.NewRouter()
	f.r.Route("/platform/oauth", MountOAuth(f.deps))
}

func (f *asFixture) do(t *testing.T, method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req := httptest.NewRequest(method, path, rd)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func TestASMetadata_NamesEveryEndpointUnderTheIssuer(t *testing.T) {
	m := jwt.ASMetadata("https://ach.test/", "ach", nil)
	want := map[string]string{
		"issuer":                 "https://ach.test",
		"authorization_endpoint": "https://ach.test/platform/oauth/authorize",
		"token_endpoint":         "https://ach.test/platform/oauth/token",
		"registration_endpoint":  "https://ach.test/platform/oauth/register",
		"jwks_uri":               "https://ach.test/.well-known/jwks.json",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %s", k, m[k], v)
		}
	}
	if cc, _ := m["code_challenge_methods_supported"].([]string); len(cc) != 1 || cc[0] != "S256" {
		t.Errorf("S256 must be advertised: %v", m["code_challenge_methods_supported"])
	}
	if ss, _ := m["scopes_supported"].([]string); len(ss) != 2 || ss[0] != "offline_access" || ss[1] != "ach" {
		t.Errorf("offline_access + audience must be advertised: %v", m["scopes_supported"])
	}
}

func TestRegister(t *testing.T) {
	f := newAS(t)
	w := f.do(t, "POST", "/platform/oauth/register", map[string]any{
		"client_name":   "OpenCode",
		"redirect_uris": []string{"http://127.0.0.1:19876/mcp/oauth/callback", "http://localhost:53421/callback", "https://app.example/cb"},
	}, map[string]string{"Content-Type": "application/json"})
	if w.Code != 201 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["client_id"] == "" || body["token_endpoint_auth_method"] != "none" {
		t.Fatalf("body: %v", body)
	}
	if w := f.do(t, "POST", "/platform/oauth/register", map[string]any{"redirect_uris": []string{"http://evil.example/cb"}}, nil); w.Code != 400 {
		t.Fatalf("http off loopback: %d", w.Code)
	}
	if w := f.do(t, "POST", "/platform/oauth/register", map[string]any{"client_name": "x"}, nil); w.Code != 400 {
		t.Fatalf("no redirects: %d", w.Code)
	}
}

func registerClient(t *testing.T, f *asFixture, uris ...string) string {
	t.Helper()
	if len(uris) == 0 {
		uris = []string{"http://127.0.0.1:5000/cb"}
	}
	w := f.do(t, "POST", "/platform/oauth/register", map[string]any{"redirect_uris": uris}, nil)
	var body struct {
		ClientID string `json:"client_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.ClientID
}

const (
	testChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" // S256 of testVerifier (RFC 7636 App. B)
	testVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
)

func authorizeURL(clientID string, over map[string]string) string {
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"http://127.0.0.1:5000/cb"},
		"state": {"xyz"}, "code_challenge": {testChallenge}, "code_challenge_method": {"S256"},
		"resource": {"https://ach.test/mcp/whatever"}, // RFC 8707: Claude Code and Codex send it; must be tolerated
	}
	for k, v := range over {
		if v == "" {
			q.Del(k)
		} else {
			q.Set(k, v)
		}
	}
	return "/platform/oauth/authorize?" + q.Encode()
}

func withFakeDex(f *asFixture, email string) *asFixture {
	f.deps.DexLogin = func(state, _ string) string { return "http://dex.test/auth?state=" + state }
	f.deps.DexExchange = func(_ context.Context, code, _ string) (string, error) {
		if code != "dexcode" {
			return "", errors.New("bad code")
		}
		return email, nil
	}
	f.deps.Provision = func(_ context.Context, _ string) (string, error) { return "litellm-user-1", nil }
	f.mount()
	return f
}

func mustQuery(t *testing.T, raw, key string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}

func TestAuthorize(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	cid := registerClient(t, f)

	w := f.do(t, "GET", authorizeURL(cid, nil), nil, nil)
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=") {
		t.Fatalf("happy: %d %s", w.Code, w.Header().Get("Location"))
	}

	w = f.do(t, "GET", authorizeURL(cid, map[string]string{"redirect_uri": "https://evil.example/cb"}), nil, nil)
	if w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatalf("unregistered uri: %d %s", w.Code, w.Header().Get("Location"))
	}

	w = f.do(t, "GET", authorizeURL(cid, map[string]string{"code_challenge": ""}), nil, nil)
	loc := w.Header().Get("Location")
	if w.Code != 302 || !strings.HasPrefix(loc, "http://127.0.0.1:5000/cb?") || !strings.Contains(loc, "error=invalid_request") || !strings.Contains(loc, "state=xyz") {
		t.Fatalf("no pkce: %d %s", w.Code, loc)
	}

	cid2 := registerClient(t, f, "http://localhost:1/callback")
	w = f.do(t, "GET", authorizeURL(cid2, map[string]string{"redirect_uri": "http://localhost:53421/callback"}), nil, nil)
	if w.Code != 302 {
		t.Fatalf("RFC 8252 §7.3 port change refused: %d %s", w.Code, w.Body)
	}
}

// startAuthorize runs /authorize and returns Dex's state (= the pending id)
// plus the Cookie header a real browser would send back on /as-callback.
func startAuthorize(t *testing.T, f *asFixture, cid string) (state string, cookie map[string]string) {
	t.Helper()
	w := f.do(t, "GET", authorizeURL(cid, nil), nil, nil)
	if w.Code != 302 {
		t.Fatalf("authorize: %d %s", w.Code, w.Body)
	}
	state = strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "__Host-ach_oauth_"+state || !cs[0].Secure || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("binding cookie: %+v", cs)
	}
	return state, map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value}
}

func TestASCallback(t *testing.T) {
	f := withFakeDex(newAS(t), "U@X.com")
	cid := registerClient(t, f)
	state, cookie := startAuthorize(t, f, cid)
	w := f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie)
	loc := w.Header().Get("Location")
	if w.Code != 302 || !strings.HasPrefix(loc, "http://127.0.0.1:5000/cb?") || !strings.Contains(loc, "code=") || !strings.Contains(loc, "state=xyz") {
		t.Fatalf("%d %s", w.Code, loc)
	}
	if cs := w.Result().Cookies(); len(cs) != 1 || cs[0].Name != "__Host-ach_oauth_"+state || cs[0].MaxAge >= 0 {
		t.Fatalf("binding cookie not cleared: %+v", cs)
	}
	code := mustQuery(t, loc, "code")
	var rec oauthCode
	ok, _ := f.store.Get(context.Background(), "code", code, &rec)
	if !ok || rec.Sub != "u@x.com" || rec.UserID != "litellm-user-1" || rec.ClientID != cid {
		t.Fatalf("code record: ok=%v %+v", ok, rec)
	}
	if w := f.do(t, "GET", "/platform/oauth/as-callback?code=x&state=nope", nil, nil); w.Code != 400 {
		t.Fatalf("no pending: %d", w.Code)
	}
	// pending is single-use
	if w := f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie); w.Code != 400 {
		t.Fatalf("pending replay: %d", w.Code)
	}
}

// TestASCallback_RequiresTheBrowserThatStartedIt is the login-CSRF /
// code-injection regression: an attacker starts /authorize for THEIR
// client and hands the Dex URL to a victim. The victim's browser reaches
// /as-callback with the attacker's `state` but without the binding cookie
// (or with a stale one from its own flow) — no code may be minted, and the
// pending must be burned so the link cannot be retried.
func TestASCallback_RequiresTheBrowserThatStartedIt(t *testing.T) {
	f := withFakeDex(newAS(t), "victim@x.com")
	cid := registerClient(t, f)

	// No cookie at all.
	state, _ := startAuthorize(t, f, cid)
	w := f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, nil)
	if w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatalf("no cookie: %d %s", w.Code, w.Header().Get("Location"))
	}
	if n := f.store.RDB.Keys(context.Background(), "ach:oauth:code:*").Val(); len(n) != 0 {
		t.Fatalf("code minted without binding: %v", n)
	}
	// Burned: the same link cannot be replayed even with the right cookie now.
	if w := f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, nil); w.Code != 400 {
		t.Fatalf("pending survived a binding failure: %d", w.Code)
	}

	// A cookie from a DIFFERENT pending (the victim's own concurrent flow)
	// does not satisfy the attacker's pending.
	attackerState, _ := startAuthorize(t, f, cid)
	_, victimCookie := startAuthorize(t, f, cid)
	w = f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+attackerState, nil, victimCookie)
	if w.Code != 400 {
		t.Fatalf("foreign cookie: %d %s", w.Code, w.Header().Get("Location"))
	}

	// Wrong value under the right name.
	state, _ = startAuthorize(t, f, cid)
	w = f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil,
		map[string]string{"Cookie": "__Host-ach_oauth_" + state + "=not-the-secret"})
	if w.Code != 400 {
		t.Fatalf("wrong cookie value: %d", w.Code)
	}
}

func TestAuthorize_InsecureBaseUsesPlainCookieName(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	f.deps.Auth.InsecureCookie = true
	f.mount()
	cid := registerClient(t, f)
	w := f.do(t, "GET", authorizeURL(cid, nil), nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "ach_oauth_"+state || cs[0].Secure {
		t.Fatalf("insecure cookie: %+v", cs)
	}
	w = f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil,
		map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value})
	if w.Code != 302 {
		t.Fatalf("insecure happy path: %d %s", w.Code, w.Body)
	}
}

// fakeOAuthPKs is the DB seam: the active oauth row per email + a revoke log.
type fakeOAuthPKs struct {
	rows    map[string]*db.PkKeyInfo
	revoked []string
	minted  int
}

func installFakePKs(f *asFixture) *fakeOAuthPKs {
	p := &fakeOAuthPKs{rows: map[string]*db.PkKeyInfo{}}
	f.deps.OAuthPKLookup = func(_ context.Context, email string) (*db.PkKeyInfo, error) { return p.rows[email], nil }
	f.deps.OAuthPKRevoke = func(_ context.Context, id string) error {
		p.revoked = append(p.revoked, id)
		for e, r := range p.rows {
			if r.KeyID == id {
				delete(p.rows, e)
			}
		}
		return nil
	}
	f.deps.Mint = func(_ context.Context, email, userID, purpose string) (string, db.PkInsertRow, error) {
		if p.rows[email] != nil {
			return "", db.PkInsertRow{}, errors.New("unique index: one active oauth row per owner")
		}
		p.minted++
		tok, mat := "lt-"+email, "sealed"
		row := db.PkInsertRow{KeyID: fmt.Sprintf("pkid_%d", p.minted), OwnerEmail: email, ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
			LiteLLMUserID: &userID, LiteLLMToken: &tok, LiteLLMKeyMaterial: &mat, Purpose: purpose}
		p.rows[email] = &db.PkKeyInfo{KeyID: row.KeyID, OwnerEmail: email, ExpiresAt: row.ExpiresAt, LiteLLMToken: &tok, LiteLLMKeyMaterial: &mat, Status: "active"}
		return "pk-discarded", row, nil
	}
	f.mount()
	return p
}

func seedCode(t *testing.T, f *asFixture, cid string) {
	t.Helper()
	err := f.store.Put(context.Background(), "code", "thecode", oauthCode{
		oauthPending: oauthPending{ClientID: cid, RedirectURI: "http://127.0.0.1:5000/cb", State: "s", CodeChallenge: testChallenge},
		Sub:          "u@x.com", UserID: "litellm-user-1",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
}

func tokenForm(cid string, over map[string]string) string {
	v := url.Values{"grant_type": {"authorization_code"}, "code": {"thecode"}, "client_id": {cid},
		"redirect_uri": {"http://127.0.0.1:5000/cb"}, "code_verifier": {testVerifier},
		"resource": {"https://ach.test"}} // RFC 8707 on /token too — tolerated
	for k, val := range over {
		v.Set(k, val)
	}
	return v.Encode()
}

var formHdr = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

type tokenBody struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

func TestToken_CodeExchangeMintsTheOAuthPKAndAJWT(t *testing.T) {
	f := newAS(t)
	pks := installFakePKs(f)
	cid := registerClient(t, f)
	seedCode(t, f, cid)

	w := f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var body tokenBody
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.TokenType != "Bearer" || body.ExpiresIn != 3600 || body.RefreshToken == "" {
		t.Fatalf("body: %+v", body) // token_type + expires_in are hard client requirements
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token responses must be no-store")
	}
	v, err := f.deps.Signer.Verify(body.AccessToken, "https://ach.test", "ach")
	if err != nil || v.Sub != "u@x.com" {
		t.Fatalf("sub=%+v err=%v", v, err)
	}
	if pks.minted != 1 || pks.rows["u@x.com"] == nil {
		t.Fatalf("oauth pk_ not minted: %+v", pks)
	}

	// second login reuses the row
	seedCode(t, f, cid)
	f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr)
	if pks.minted != 1 || len(pks.revoked) != 0 {
		t.Fatalf("second login must reuse the row: minted=%d revoked=%v", pks.minted, pks.revoked)
	}
}

func TestToken_RotatesAnOAuthPKThatIsAboutToExpire(t *testing.T) {
	f := newAS(t)
	pks := installFakePKs(f)
	tok := "lt-old"
	pks.rows["u@x.com"] = &db.PkKeyInfo{KeyID: "pkid_old", OwnerEmail: "u@x.com", ExpiresAt: time.Now().Add(30 * time.Minute), LiteLLMToken: &tok, Status: "active"}
	cid := registerClient(t, f)
	seedCode(t, f, cid)
	if w := f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if pks.minted != 1 || len(pks.revoked) != 1 || pks.revoked[0] != "pkid_old" {
		t.Fatalf("expected revoke then mint: minted=%d revoked=%v", pks.minted, pks.revoked)
	}
}

func TestToken_Rejects(t *testing.T) {
	f := newAS(t)
	installFakePKs(f)
	a, b := registerClient(t, f), registerClient(t, f)

	seedCode(t, f, a)
	w := f.do(t, "POST", "/platform/oauth/token", tokenForm(a, map[string]string{"code_verifier": "wrong-wrong-wrong-wrong-wrong-wrong-wrong-wrong"}), formHdr)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_grant") {
		t.Fatalf("wrong verifier: %d %s", w.Code, w.Body)
	}
	if w = f.do(t, "POST", "/platform/oauth/token", tokenForm(a, nil), formHdr); w.Code != 400 {
		t.Fatalf("code must be single-use: %d", w.Code)
	}

	seedCode(t, f, a)
	if w := f.do(t, "POST", "/platform/oauth/token", tokenForm(b, nil), formHdr); w.Code != 400 {
		t.Fatalf("other client: %d", w.Code)
	}

	w = f.do(t, "POST", "/platform/oauth/token", "grant_type=password", formHdr)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_grant_type") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestToken_RefreshRotatesAndTheOldOneDies(t *testing.T) {
	f := newAS(t)
	installFakePKs(f)
	cid := registerClient(t, f)
	seedCode(t, f, cid)
	var first tokenBody
	_ = json.Unmarshal(f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr).Body.Bytes(), &first)

	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {cid}}.Encode()
	w := f.do(t, "POST", "/platform/oauth/token", refresh, formHdr)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var second tokenBody
	_ = json.Unmarshal(w.Body.Bytes(), &second)
	if second.RefreshToken == "" || second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh must rotate")
	}
	if w := f.do(t, "POST", "/platform/oauth/token", refresh, formHdr); w.Code != 400 {
		t.Fatalf("old refresh must be dead: %d", w.Code)
	}
}

func withServices(f *asFixture) *asFixture {
	f.deps.Services = map[string]oauthsvc.Service{
		"mcp-a": {Store: "a-store", Broker: "https://broker.test/a"},
		"mcp-b": {Store: "b-store", Broker: "https://broker.test/b"},
	}
	f.deps.Grants = MapGrants{}
	f.mount()
	return f
}

func TestAuthorize_ScopeValidation(t *testing.T) {
	f := withServices(withFakeDex(newAS(t), "u@x.com"))
	cid := registerClient(t, f)
	// unknown scope → invalid_scope back to the client, no pending, no Dex
	w := f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": "ach mcp-nope"}), nil, nil)
	loc := w.Header().Get("Location")
	if w.Code != 302 || !strings.HasPrefix(loc, "http://127.0.0.1:5000/cb?") || !strings.Contains(loc, "error=invalid_scope") || !strings.Contains(loc, "state=xyz") {
		t.Fatalf("unknown scope: %d %s", w.Code, loc)
	}
	// known scopes + the always-accepted ones are stored on the pending
	w = f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": "offline_access ach mcp-b mcp-a"}), nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	var p oauthPending
	if ok, _ := f.store.Get(context.Background(), "pending", state, &p); !ok || strings.Join(p.Scopes, " ") != "mcp-b mcp-a" {
		t.Fatalf("pending scopes: ok=%v %+v", ok, p)
	}
	// no scope at all is fine (today's CLI login)
	if w := f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": ""}), nil, nil); w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "dex.test") {
		t.Fatalf("no scope: %d", w.Code)
	}
}

func TestToken_ScopeFromProjection(t *testing.T) {
	f := withServices(newAS(t))
	installFakePKs(f)
	cid := registerClient(t, f)
	f.deps.Grants = MapGrants{"a-store|u@x.com": true} // mcp-a granted, mcp-b not
	f.mount()
	seedCodeScoped(t, f, cid, []string{"mcp-a", "mcp-b"})
	w := f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr)
	var tb struct {
		tokenBody
		Scope string `json:"scope"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tb)
	if w.Code != 200 || tb.Scope != "ach mcp-a" {
		t.Fatalf("code grant scope: %d %s", w.Code, w.Body)
	}
	v, err := f.deps.Signer.Verify(tb.AccessToken, "https://ach.test", "ach")
	if err != nil || v.Scope != "ach mcp-a" {
		t.Fatalf("jwt scope: %+v %v", v, err)
	}
	// refresh recomputes: grant for mcp-b appears, mcp-a revoked at the broker disappears
	f.deps.Grants = MapGrants{"b-store|u@x.com": true}
	f.mount()
	w = f.do(t, "POST", "/platform/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tb.RefreshToken}, "client_id": {cid}}.Encode(), formHdr)
	_ = json.Unmarshal(w.Body.Bytes(), &tb)
	if w.Code != 200 || tb.Scope != "ach mcp-b" {
		t.Fatalf("refresh scope: %d %s", w.Code, w.Body)
	}
}

func TestToken_GrantsUnavailable(t *testing.T) {
	f := withServices(newAS(t))
	installFakePKs(f)
	cid := registerClient(t, f)
	f.deps.Grants = failingGrants{}
	f.mount()
	seedCodeScoped(t, f, cid, []string{"mcp-a"})
	if w := f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr); w.Code != 503 {
		t.Fatalf("grants down must be 503: %d %s", w.Code, w.Body)
	}
	// …but a code that asked for no services never touches the projection
	seedCodeScoped(t, f, cid, nil)
	if w := f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr); w.Code != 200 {
		t.Fatalf("no services requested: %d %s", w.Code, w.Body)
	}
}

type failingGrants struct{}

func (failingGrants) Granted(context.Context, string, string) (bool, error) {
	return false, errors.New("redis down")
}

func seedCodeScoped(t *testing.T, f *asFixture, cid string, scopes []string) {
	t.Helper()
	err := f.store.Put(context.Background(), "code", "thecode", oauthCode{
		oauthPending: oauthPending{ClientID: cid, RedirectURI: "http://127.0.0.1:5000/cb", State: "s", CodeChallenge: testChallenge, Scopes: scopes},
		Sub:          "u@x.com", UserID: "litellm-user-1",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
}
