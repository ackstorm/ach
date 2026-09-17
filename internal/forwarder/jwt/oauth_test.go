// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

func expMinusIat(t *testing.T, raw string) int64 {
	t.Helper()
	tok, _, err := jwtv5.NewParser().ParseUnverified(raw, jwtv5.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	mc := tok.Claims.(jwtv5.MapClaims)
	return int64(mc["exp"].(float64)) - int64(mc["iat"].(float64))
}

func signerWith(t *testing.T, kids ...string) *Ed25519Signer {
	t.Helper()
	s := NewEd25519Signer()
	cur, err := newSignerSlot(kids[0], freshSeed(t))
	if err != nil {
		t.Fatal(err)
	}
	s.loadCurrent(cur)
	if len(kids) > 1 {
		nxt, _ := newSignerSlot(kids[1], freshSeed(t))
		s.loadNext(nxt)
	}
	return s
}

func TestSign_TTL(t *testing.T) {
	s := signerWith(t, "k1")
	raw, err := s.Sign(context.Background(), Claims{Iss: "https://ach.test", Sub: "u@x.com", Aud: "ach", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if got := expMinusIat(t, raw); got != 3600 {
		t.Fatalf("exp-iat = %d, want 3600", got)
	}
	raw, _ = s.Sign(context.Background(), Claims{Iss: "i", Sub: "s", Aud: "a"})
	if got := expMinusIat(t, raw); got != 120 {
		t.Fatalf("zero TTL: exp-iat = %d, want 120", got)
	}
}

func TestVerify_AcceptsOwnTokenAndReturnsSub(t *testing.T) {
	s := signerWith(t, "k1")
	raw, _ := s.Sign(context.Background(), Claims{Iss: "https://ach.test", Sub: "U@X.com", Aud: "ach", TTL: time.Minute})
	sub, err := s.Verify(raw, "https://ach.test", "ach")
	if err != nil || sub != "U@X.com" {
		t.Fatalf("sub=%q err=%v", sub, err)
	}
}

func TestVerify_AcceptsNextSlotKid(t *testing.T) {
	s := signerWith(t, "k1", "k2")
	other := NewEd25519Signer()
	other.loadCurrent(s.next.Load())
	raw, _ := other.Sign(context.Background(), Claims{Iss: "i", Sub: "u", Aud: "ach", TTL: time.Minute})
	if _, err := s.Verify(raw, "i", "ach"); err != nil {
		t.Fatalf("next-slot token rejected: %v", err)
	}
}

func TestVerify_Rejects(t *testing.T) {
	s := signerWith(t, "k1")
	good := Claims{Iss: "i", Sub: "u", Aud: "ach", TTL: time.Minute}
	cases := map[string]struct {
		c        Claims
		iss, aud string
		signer   *Ed25519Signer
	}{
		"wrong aud":   {good, "i", "other", s},
		"wrong iss":   {good, "nobody", "ach", s},
		"expired":     {Claims{Iss: "i", Sub: "u", Aud: "ach", TTL: -time.Hour}, "i", "ach", s},
		"unknown kid": {good, "i", "ach", signerWith(t, "zzz")},
		"empty sub":   {Claims{Iss: "i", Sub: "", Aud: "ach", TTL: time.Minute}, "i", "ach", s},
	}
	for name, tc := range cases {
		raw, _ := tc.signer.Sign(context.Background(), tc.c)
		if _, err := s.Verify(raw, tc.iss, tc.aud); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := s.Verify("not.a.jwt", "i", "ach"); err == nil {
		t.Error("garbage: expected error")
	}
	if _, err := s.Verify("", "i", "ach"); !errors.Is(err, ErrNotAJWT) {
		t.Errorf("empty: got %v, want ErrNotAJWT", err)
	}
}

func writeSlot(t *testing.T, dir, prefix, kid string, seed []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, prefix+".kid"), []byte(kid+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, prefix+".seed"), seed, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFromDir(t *testing.T) {
	dir := t.TempDir()
	writeSlot(t, dir, "current", "k1", freshSeed(t))
	writeSlot(t, dir, "next", "k2", freshSeed(t))
	s := NewEd25519Signer()
	if err := LoadFromDir(s, dir); err != nil {
		t.Fatal(err)
	}
	if !s.Loaded() || len(s.JWKS()) != 2 {
		t.Fatalf("loaded=%v jwks=%d", s.Loaded(), len(s.JWKS()))
	}

	dir = t.TempDir()
	writeSlot(t, dir, "current", "k1", freshSeed(t))
	s = NewEd25519Signer()
	if err := LoadFromDir(s, dir); err != nil || len(s.JWKS()) != 1 {
		t.Fatalf("current only: err=%v jwks=%d", err, len(s.JWKS()))
	}

	dir = t.TempDir()
	writeSlot(t, dir, "current", "k1", []byte("short"))
	s = NewEd25519Signer()
	if err := LoadFromDir(s, dir); !errors.Is(err, ErrEmptySeed) {
		t.Fatalf("got %v, want ErrEmptySeed", err)
	}
	if s.Loaded() {
		t.Fatal("must not load on error")
	}
}
