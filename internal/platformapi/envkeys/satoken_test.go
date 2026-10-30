// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const satIssuer = "https://kubernetes.default.svc"

type satKey struct {
	kid string
	key *rsa.PrivateKey
}

func newSatKey(t *testing.T, kid string) satKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return satKey{kid, k}
}

func (k satKey) sign(t *testing.T, iss string, exp time.Time) string {
	t.Helper()
	sg, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: k.key, KeyID: k.kid}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.Signed(sg).Claims(jwt.Claims{Issuer: iss, Expiry: jwt.NewNumericDate(exp)}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type fakeJWKS struct {
	keys  []satKey
	err   error
	calls int
}

func (f *fakeJWKS) fetch(context.Context) (string, []byte, error) {
	f.calls++
	if f.err != nil {
		return "", nil, f.err
	}
	var set jose.JSONWebKeySet
	for _, k := range f.keys {
		set.Keys = append(set.Keys, jose.JSONWebKey{Key: &k.key.PublicKey, KeyID: k.kid, Algorithm: "RS256", Use: "sig"})
	}
	raw, _ := json.Marshal(set)
	return satIssuer, raw, nil
}

// unsignedJWT assembles a compact JWS from a literal header, an issuer-only payload and a raw
// signature segment, for the algorithm-confusion cases; no key is involved.
func unsignedJWT(header, iss, sig string) string {
	payload, _ := json.Marshal(map[string]string{"iss": iss})
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(header)) + "." + enc.EncodeToString(payload) + "." + sig
}

func TestSATokenVerifier(t *testing.T) {
	good, other := newSatKey(t, "k1"), newSatKey(t, "k1") // same kid, different key
	src := &fakeJWKS{keys: []satKey{good}}
	now := time.Now()
	v := NewSATokenVerifier(src.fetch)
	v.now = func() time.Time { return now }
	ctx := context.Background()
	future, past := now.Add(time.Hour), now.Add(-time.Hour)

	if err := v.Verify(ctx, good.sign(t, satIssuer, future)); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	for name, tok := range map[string]string{
		"forged signature": other.sign(t, satIssuer, future),
		"wrong issuer":     good.sign(t, "https://evil.example", future),
		"expired":          good.sign(t, satIssuer, past),
		"alg none":         unsignedJWT(`{"alg":"none","kid":"k1"}`, satIssuer, ""),
		"alg HS256":        unsignedJWT(`{"alg":"HS256","kid":"k1"}`, satIssuer, "c2ln"),
	} {
		if err := v.Verify(ctx, tok); !errors.Is(err, ErrSATokenInvalid) {
			t.Errorf("%s: err = %v, want ErrSATokenInvalid", name, err)
		}
	}
	if src.calls != 1 {
		t.Fatalf("fetches = %d, want 1 (known kid served from cache)", src.calls)
	}
}

func TestSATokenVerifier_UnknownKidRefetchesAtMostOncePerMinute(t *testing.T) {
	known, rotated := newSatKey(t, "k1"), newSatKey(t, "k2")
	src := &fakeJWKS{keys: []satKey{known}}
	now := time.Now()
	v := NewSATokenVerifier(src.fetch)
	v.now = func() time.Time { return now }
	ctx := context.Background()
	tok := rotated.sign(t, satIssuer, now.Add(time.Hour))

	_ = v.Verify(ctx, known.sign(t, satIssuer, now.Add(time.Hour)))
	now = now.Add(61 * time.Second)
	for i := 0; i < 5; i++ {
		if err := v.Verify(ctx, tok); !errors.Is(err, ErrSATokenInvalid) {
			t.Fatal(err)
		}
	}
	if src.calls != 2 {
		t.Fatalf("fetches = %d, want 2 (initial + one refetch)", src.calls)
	}
	src.keys = append(src.keys, rotated)
	now = now.Add(61 * time.Second)
	if err := v.Verify(ctx, tok); err != nil {
		t.Fatalf("after rotation and the refetch window: %v", err)
	}
	if src.calls != 3 {
		t.Fatalf("fetches = %d, want 3", src.calls)
	}
}

func TestSATokenVerifier_FetchFailure(t *testing.T) {
	k := newSatKey(t, "k1")
	src := &fakeJWKS{err: errors.New("boom")}
	now := time.Now()
	v := NewSATokenVerifier(src.fetch)
	v.now = func() time.Time { return now }
	tok := k.sign(t, satIssuer, now.Add(24*time.Hour))
	if err := v.Verify(context.Background(), tok); !errors.Is(err, ErrSAKeysUnavailable) {
		t.Fatalf("no cache: err = %v, want ErrSAKeysUnavailable", err)
	}
	src.err, src.keys = nil, []satKey{k}
	now = now.Add(61 * time.Second)
	if err := v.Verify(context.Background(), tok); err != nil {
		t.Fatal(err)
	}
	// Stale cache survives a failed refresh.
	src.err = errors.New("boom")
	now = now.Add(saJWKSTTL + time.Minute)
	if err := v.Verify(context.Background(), tok); err != nil {
		t.Fatalf("stale cache must keep verifying: %v", err)
	}
}

// Forged tokens with an empty cache and a failing API server cost one fetch
// per window, not one per request.
func TestSATokenVerifier_EmptyCacheFailureBacksOff(t *testing.T) {
	k := newSatKey(t, "k1")
	src := &fakeJWKS{err: errors.New("boom")}
	now := time.Now()
	v := NewSATokenVerifier(src.fetch)
	v.now = func() time.Time { return now }
	tok := k.sign(t, satIssuer, now.Add(time.Hour))
	for i := 0; i < 20; i++ {
		if err := v.Verify(context.Background(), tok); !errors.Is(err, ErrSAKeysUnavailable) {
			t.Fatal(err)
		}
	}
	if src.calls != 1 {
		t.Fatalf("fetches = %d, want 1 per window", src.calls)
	}
	now = now.Add(61 * time.Second)
	_ = v.Verify(context.Background(), tok)
	if src.calls != 2 {
		t.Fatalf("fetches = %d, want 2 after the window", src.calls)
	}
}

// A caller that disconnects must not fail the refresh nor burn the window.
func TestSATokenVerifier_CancelledCallerDoesNotPoisonRefresh(t *testing.T) {
	k := newSatKey(t, "k1")
	src := &fakeJWKS{keys: []satKey{k}}
	now := time.Now()
	v := NewSATokenVerifier(func(ctx context.Context) (string, []byte, error) {
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		return src.fetch(ctx)
	})
	v.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := v.Verify(ctx, k.sign(t, satIssuer, now.Add(time.Hour))); err != nil {
		t.Fatalf("cancelled caller: %v", err)
	}
}
