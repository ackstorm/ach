// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"context"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

func TestSignJTIAndVerifyExp(t *testing.T) {
	s := NewEd25519Signer()
	if err := LoadSeed(s, "k1", make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	raw, err := s.Sign(ctx, Claims{Iss: "https://ach.test", Sub: "ekid_1", Aud: "ach-ek", TTL: 15 * time.Minute, JTI: "j-1"})
	if err != nil {
		t.Fatal(err)
	}
	claims := jwtv5.MapClaims{}
	if _, _, err := jwtv5.NewParser().ParseUnverified(raw, claims); err != nil {
		t.Fatal(err)
	}
	if claims["jti"] != "j-1" {
		t.Fatalf("jti = %v", claims["jti"])
	}
	sub, exp, err := s.VerifyExp(raw, "https://ach.test", "ach-ek")
	if err != nil || sub != "ekid_1" {
		t.Fatalf("VerifyExp = %q, %v", sub, err)
	}
	if d := time.Until(exp); d < 14*time.Minute || d > 15*time.Minute+time.Second {
		t.Fatalf("exp in %v, want ~15m", d)
	}
	if _, _, err := s.VerifyExp(raw, "https://ach.test", "ach"); err == nil {
		t.Fatal("an ach-ek token verified for audience ach")
	}

	plain, err := s.Sign(ctx, Claims{Iss: "i", Sub: "s", Aud: "a"})
	if err != nil {
		t.Fatal(err)
	}
	c2 := jwtv5.MapClaims{}
	if _, _, err := jwtv5.NewParser().ParseUnverified(plain, c2); err != nil {
		t.Fatal(err)
	}
	if _, ok := c2["jti"]; ok {
		t.Fatal("jti emitted without Claims.JTI")
	}
}
