// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
)

// testIdP is a minimal OIDC issuer: discovery + a JWKS whose keys and
// status the test can change between calls.
type testIdP struct {
	srv *httptest.Server

	mu      sync.Mutex
	keys    map[string]*rsa.PrivateKey // served kid → key
	keysErr int                        // non-zero: /keys answers this status
}

func newTestIdP(t *testing.T) *testIdP {
	t.Helper()
	idp := &testIdP{keys: map[string]*rsa.PrivateKey{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.srv.URL,
			"jwks_uri":                              idp.srv.URL + "/keys",
			"authorization_endpoint":                idp.srv.URL + "/auth",
			"token_endpoint":                        idp.srv.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		idp.mu.Lock()
		defer idp.mu.Unlock()
		if idp.keysErr != 0 {
			w.WriteHeader(idp.keysErr)
			return
		}
		var ks []map[string]string
		for kid, k := range idp.keys {
			ks = append(ks, map[string]string{
				"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid,
				"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": ks})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (idp *testIdP) serve(kid string, k *rsa.PrivateKey) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	idp.keys[kid] = k
}

func (idp *testIdP) failKeys(status int) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	idp.keysErr = status
}

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func signRS(t *testing.T, kid string, k *rsa.PrivateKey, c jwtv5.MapClaims) string {
	t.Helper()
	tok := jwtv5.NewWithClaims(jwtv5.SigningMethodRS256, c)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIdPVerifier(t *testing.T) {
	idp := newTestIdP(t)
	k1 := rsaKey(t)
	idp.serve("k1", k1)
	v := NewIdPVerifier(IdPConfig{Issuer: idp.srv.URL, Audiences: []string{"chat", "other"}, Claim: "email"})
	ctx := context.Background()
	exp := time.Now().Add(time.Hour).Truncate(time.Second)

	claims := func(mut func(jwtv5.MapClaims)) jwtv5.MapClaims {
		c := jwtv5.MapClaims{"iss": idp.srv.URL, "aud": "chat", "exp": exp.Unix(),
			"sub": "opaque-protobuf", "email": "  Alice@Corp.IO ", "email_verified": true}
		if mut != nil {
			mut(c)
		}
		return c
	}

	id, gotExp, err := v.Verify(ctx, signRS(t, "k1", k1, claims(nil)))
	if err != nil || id != "alice@corp.io" || !gotExp.Equal(exp) {
		t.Fatalf("valid: id=%q exp=%v err=%v", id, gotExp, err)
	}
	if _, _, err := v.Verify(ctx, signRS(t, "k1", k1, claims(func(c jwtv5.MapClaims) { c["aud"] = []string{"x", "other"} }))); err != nil {
		t.Fatalf("aud list with one accepted value: %v", err)
	}
	if _, _, err := v.Verify(ctx, signRS(t, "k1", k1, claims(func(c jwtv5.MapClaims) { delete(c, "email_verified") }))); err != nil {
		t.Fatalf("email_verified absent must pass: %v", err)
	}

	hsKey, _ := x509.MarshalPKIXPublicKey(&k1.PublicKey)
	hs := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims(nil))
	hs.Header["kid"] = "k1"
	hsTok, _ := hs.SignedString(hsKey)
	none := jwtv5.NewWithClaims(jwtv5.SigningMethodNone, claims(nil))
	noneTok, _ := none.SignedString(jwtv5.UnsafeAllowNoneSignatureType)

	invalid := map[string]string{
		"wrong iss":            signRS(t, "k1", k1, claims(func(c jwtv5.MapClaims) { c["iss"] = "https://evil.example" })),
		"wrong aud":            signRS(t, "k1", k1, claims(func(c jwtv5.MapClaims) { c["aud"] = "ach" })),
		"expired":              signRS(t, "k1", k1, claims(func(c jwtv5.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() })),
		"missing exp":          signRS(t, "k1", k1, claims(func(c jwtv5.MapClaims) { delete(c, "exp") })),
		"missing email":        signRS(t, "k1", k1, claims(func(c jwtv5.MapClaims) { delete(c, "email") })),
		"empty email":          signRS(t, "k1", k1, claims(func(c jwtv5.MapClaims) { c["email"] = "  " })),
		"email_verified=false": signRS(t, "k1", k1, claims(func(c jwtv5.MapClaims) { c["email_verified"] = false })),
		"unknown kid":          signRS(t, "k9", rsaKey(t), claims(nil)),
		"alg HS256":            hsTok,
		"alg none":             noneTok,
	}
	for name, raw := range invalid {
		if _, _, err := v.Verify(ctx, raw); !errors.Is(err, ErrIdPTokenInvalid) {
			t.Errorf("%s: want ErrIdPTokenInvalid, got %v", name, err)
		}
	}

	// Key rotation: a kid the issuer starts serving later is refetched.
	k2 := rsaKey(t)
	idp.serve("k2", k2)
	if _, _, err := v.Verify(ctx, signRS(t, "k2", k2, claims(nil))); err != nil {
		t.Fatalf("rotated kid: %v", err)
	}

	// JWKS 5xx → unreachable, never invalid; recovers on the next call.
	idp.failKeys(http.StatusBadGateway)
	if _, _, err := v.Verify(ctx, signRS(t, "k3", rsaKey(t), claims(nil))); !errors.Is(err, ErrIdPUnreachable) {
		t.Fatalf("keys 502: want ErrIdPUnreachable, got %v", err)
	}
	idp.failKeys(0)
	if _, _, err := v.Verify(ctx, signRS(t, "k1", k1, claims(nil))); err != nil {
		t.Fatalf("recovered: %v", err)
	}
}

func TestIdPVerifier_DiscoveryLazyAndRetried(t *testing.T) {
	idp := newTestIdP(t)
	k1 := rsaKey(t)
	idp.serve("k1", k1)
	url := idp.srv.URL
	tok := signRS(t, "k1", k1, jwtv5.MapClaims{"iss": url, "aud": "chat", "exp": time.Now().Add(time.Hour).Unix(), "email": "a@b.c"})

	idp.srv.Close() // issuer down at "startup": construction must not touch it
	v := NewIdPVerifier(IdPConfig{Issuer: url, Audiences: []string{"chat"}, Claim: "email"})
	if _, _, err := v.Verify(context.Background(), tok); !errors.Is(err, ErrIdPUnreachable) {
		t.Fatalf("down: want ErrIdPUnreachable, got %v", err)
	}
}

func TestPeekIssuer(t *testing.T) {
	k := rsaKey(t)
	if got := peekIssuer(signRS(t, "k", k, jwtv5.MapClaims{"iss": "https://dex.example"})); got != "https://dex.example" {
		t.Fatalf("got %q", got)
	}
	for _, raw := range []string{"aaa.bbb.ccc", "a.b", "", "x.e30.y"} {
		if got := peekIssuer(raw); got != "" {
			t.Errorf("%q → %q", raw, got)
		}
	}
}

func TestTrustedIdPResolver(t *testing.T) {
	idp := newTestIdP(t)
	k1 := rsaKey(t)
	idp.serve("k1", k1)
	ctx := context.Background()
	tokExp := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	dex := func(email string) string {
		return signRS(t, "k1", k1, jwtv5.MapClaims{"iss": idp.srv.URL, "aud": "chat", "exp": tokExp.Unix(), "email": email})
	}

	lt, mat := "lt-1", "sealed"
	var looked []string
	lookup := func(_ context.Context, email string) (*db.PkKeyInfo, error) {
		looked = append(looked, email)
		switch email {
		case "u@x.com":
			return &db.PkKeyInfo{KeyID: "pkid_oa", OwnerEmail: email, ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
				LiteLLMToken: &lt, LiteLLMKeyMaterial: &mat, Extended: true}, nil
		case "dbdown@x.com":
			return nil, errors.New("db down")
		}
		return nil, nil
	}
	var hooked []string
	hook := func(_ context.Context, tok string) { hooked = append(hooked, tok) }
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) { return &KeyInfo{KeyID: "inner"}, nil }}
	r := NewTrustedIdPResolver(inner, NewIdPVerifier(IdPConfig{Issuer: idp.srv.URL, Audiences: []string{"chat"}, Claim: "email"}), lookup, hook)

	info, err := r.Resolve(ctx, dex("U@x.com"))
	if err != nil || info == nil || info.KeyID != "pkid_oa" || info.KeyType != keys.PrefixPk || info.OwnerEmail != "u@x.com" || info.LiteLLMKeyMaterial == nil {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if !info.ExpiresAt.Equal(tokExp) {
		t.Errorf("ExpiresAt=%v, want the token exp %v (cache must not outlive it)", info.ExpiresAt, tokExp)
	}
	if len(hooked) != 1 || hooked[0] != "lt-1" {
		t.Errorf("extend hook: %v", hooked)
	}
	if inner.callCount() != 0 {
		t.Fatal("a trusted-issuer token must not reach inner")
	}

	if _, err := r.Resolve(ctx, dex("nobody@x.com")); !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("no row: want ErrLoginRequired, got %v", err)
	}
	if _, err := r.Resolve(ctx, dex("dbdown@x.com")); err == nil || errors.Is(err, ErrLoginRequired) {
		t.Fatalf("db error must surface as an error (500), got %v", err)
	}
	expired := signRS(t, "k1", k1, jwtv5.MapClaims{"iss": idp.srv.URL, "aud": "chat", "exp": time.Now().Add(-time.Minute).Unix(), "email": "u@x.com"})
	n := len(looked)
	if info, err := r.Resolve(ctx, expired); info != nil || err != nil {
		t.Fatalf("invalid token: want (nil,nil) → 401, got %+v %v", info, err)
	}
	if len(looked) != n {
		t.Fatal("an invalid token must not reach the DB")
	}

	// Other issuers, ACH tokens and keys go to inner untouched.
	for _, raw := range []string{
		signRS(t, "k1", k1, jwtv5.MapClaims{"iss": "https://ach.test", "aud": "ach", "exp": tokExp.Unix(), "sub": "u@x.com"}),
		"pk-abc", "sk-a.b.c", "aaa.bbb.ccc",
	} {
		if info, _ := r.Resolve(ctx, raw); info == nil || info.KeyID != "inner" {
			t.Errorf("%q must fall through to inner", raw)
		}
	}

	idp.srv.Close()
	down := NewTrustedIdPResolver(inner, NewIdPVerifier(IdPConfig{Issuer: idp.srv.URL, Audiences: []string{"chat"}, Claim: "email"}), lookup, hook)
	if _, err := down.Resolve(ctx, dex("u@x.com")); !errors.Is(err, ErrIdPUnreachable) {
		t.Fatalf("issuer down: want ErrIdPUnreachable, got %v", err)
	}
}
