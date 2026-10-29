// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/keys"
)

func oauthSigner(t *testing.T) *jwt.Ed25519Signer {
	t.Helper()
	s := jwt.NewEd25519Signer()
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	if err := jwt.LoadSeed(s, "k1", seed); err != nil {
		t.Fatal(err)
	}
	return s
}

func fallthroughResolver() *fakeResolver {
	return &fakeResolver{respond: func(string) (*KeyInfo, error) { return nil, nil }}
}

func TestOAuthResolver(t *testing.T) {
	s := oauthSigner(t)
	tok, mat := "lt-1", "sealed"
	lookup := func(_ context.Context, email string) (*db.PkKeyInfo, error) {
		if email != "u@x.com" {
			return nil, nil
		}
		return &db.PkKeyInfo{KeyID: "pkid_1", OwnerEmail: email, ExpiresAt: time.Now().Add(time.Hour),
			LiteLLMToken: &tok, LiteLLMKeyMaterial: &mat, Status: "active"}, nil
	}
	inner := fallthroughResolver()
	r := NewOAuthResolver(inner, s, "https://ach.test/", "ach", lookup)
	ctx := context.Background()
	sign := func(sub string) string {
		raw, _ := s.Sign(ctx, jwt.Claims{Iss: "https://ach.test", Sub: sub, Aud: "ach", TTL: time.Minute})
		return raw
	}

	info, err := r.Resolve(ctx, sign("u@x.com"))
	if err != nil || info == nil {
		t.Fatalf("info=%v err=%v", info, err)
	}
	if info.KeyID != "pkid_1" || info.KeyType != keys.PrefixPk || info.OwnerEmail != "u@x.com" || info.LiteLLMKeyMaterial == nil {
		t.Fatalf("%+v", info)
	}
	if inner.callCount() != 0 {
		t.Fatal("a JWS must not reach the pk_/ek_ resolver")
	}

	if info, err := r.Resolve(ctx, sign("nobody@x.com")); err != nil || info != nil {
		t.Fatalf("no row must read as expired_or_revoked: info=%v err=%v", info, err)
	}
	if info, err := r.Resolve(ctx, "aaa.bbb.ccc"); err != nil || info != nil {
		t.Fatalf("garbage JWS: info=%v err=%v", info, err)
	}

	_, _ = r.Resolve(ctx, "pk-not-a-jws")
	_, _ = r.Resolve(ctx, "sk-a.b.c")
	if inner.callCount() != 2 {
		t.Fatalf("expected fall-through for pk_ and sk-, calls=%d", inner.callCount())
	}

	down := NewOAuthResolver(inner, s, "https://ach.test", "ach",
		func(context.Context, string) (*db.PkKeyInfo, error) { return nil, errors.New("db down") })
	if _, err := down.Resolve(ctx, sign("u")); err == nil {
		t.Fatal("expected error → Authn renders 500, not 401")
	}
}

type fakeVerifier struct{ v string }

func (f fakeVerifier) Verify(string, string, string) (string, error) { return f.v, nil }

func TestOAuthResolver_UsesJWTSubject(t *testing.T) {
	r := NewOAuthResolver(fallthroughResolver(), fakeVerifier{v: "u@x.com"}, "https://ach.test", "ach",
		func(context.Context, string) (*db.PkKeyInfo, error) {
			return &db.PkKeyInfo{KeyID: "pkid_1", OwnerEmail: "u@x.com"}, nil
		})
	info, err := r.Resolve(context.Background(), "a.b.c")
	if err != nil || info == nil || info.OwnerEmail != "u@x.com" {
		t.Fatalf("got %+v err=%v", info, err)
	}
}
