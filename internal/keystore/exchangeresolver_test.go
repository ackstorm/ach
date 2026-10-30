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

func liveExchangeRow() *db.EkKeyInfo {
	tok, mat := "lt-ek", "sealed-ek"
	return &db.EkKeyInfo{KeyID: "ekid_1", Environment: "prod", OwnerEmail: "ai.run@ackstorm.com",
		LiteLLMToken: &tok, LiteLLMKeyMaterial: &mat, Status: "active", Requester: "agents/doctor"}
}

func exchangeLookup(rows map[string]*db.EkKeyInfo) ExchangeKeyLookup {
	return func(_ context.Context, keyID string) (*db.EkKeyInfo, error) { return rows[keyID], nil }
}

func TestExchangeResolver(t *testing.T) {
	s := oauthSigner(t)
	ctx := context.Background()
	r := NewExchangeResolver(fallthroughResolver(), s, "https://ach.test/",
		exchangeLookup(map[string]*db.EkKeyInfo{"ekid_1": liveExchangeRow()}))
	sign := func(signer *jwt.Ed25519Signer, c jwt.Claims) string {
		raw, err := signer.Sign(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	claims := func(iss, sub string, ttl time.Duration) jwt.Claims {
		return jwt.Claims{Iss: iss, Sub: sub, Aud: ExchangeAudience, TTL: ttl}
	}

	info, err := r.Resolve(ctx, sign(s, claims("https://ach.test", "ekid_1", time.Minute)))
	if err != nil || info == nil {
		t.Fatalf("info=%v err=%v", info, err)
	}
	if info.KeyType != keys.PrefixEk || info.KeyID != "ekid_1" || info.Environment != "prod" ||
		info.OwnerEmail != "ai.run@ackstorm.com" || info.LiteLLMKeyMaterial == nil {
		t.Fatalf("%+v", info)
	}
	if info.ExpiresAt == nil || info.ExpiresAt.After(time.Now().Add(time.Minute+time.Second)) {
		t.Fatalf("a cache entry must not outlive the JWT: ExpiresAt=%v", info.ExpiresAt)
	}

	other := jwt.NewEd25519Signer()
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(100 + i)
	}
	if err := jwt.LoadSeed(other, "k2", seed); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"expired":           sign(s, claims("https://ach.test", "ekid_1", -time.Minute)),
		"unknown kid":       sign(other, claims("https://ach.test", "ekid_1", time.Minute)),
		"wrong issuer":      sign(s, claims("https://other.test", "ekid_1", time.Minute)),
		"manual or gone id": sign(s, claims("https://ach.test", "ekid_manual", time.Minute)),
	} {
		if info, err := r.Resolve(ctx, raw); err != nil || info != nil {
			t.Errorf("%s: info=%v err=%v; want (nil, nil) = 401", name, info, err)
		}
	}

	down := NewExchangeResolver(fallthroughResolver(), s, "https://ach.test",
		func(context.Context, string) (*db.EkKeyInfo, error) { return nil, errors.New("db down") })
	if _, err := down.Resolve(ctx, sign(s, claims("https://ach.test", "ekid_1", time.Minute))); err == nil {
		t.Fatal("a lookup error must surface (500), not read as 401")
	}
}

// TestExchangeAndOAuthResolversInProductionOrder wires the two resolvers the
// way every service does — cache(exchange(oauth(db))) — and checks each
// refuses the other's token.
func TestExchangeAndOAuthResolversInProductionOrder(t *testing.T) {
	s := oauthSigner(t)
	ctx := context.Background()
	pk := func(_ context.Context, email string) (*db.PkKeyInfo, error) {
		if email != "u@x.com" {
			return nil, nil
		}
		return &db.PkKeyInfo{KeyID: "pkid_1", OwnerEmail: email, ExpiresAt: time.Now().Add(time.Hour), Status: "active"}, nil
	}
	ekLookups := 0
	ek := func(_ context.Context, keyID string) (*db.EkKeyInfo, error) {
		ekLookups++
		if keyID == "ekid_1" {
			return liveExchangeRow(), nil
		}
		return nil, nil
	}
	oauth := NewOAuthResolver(fallthroughResolver(), s, "https://ach.test", "ach", pk)
	chain := NewExchangeResolver(oauth, s, "https://ach.test", ek)

	exTok, _ := s.Sign(ctx, jwt.Claims{Iss: "https://ach.test", Sub: "ekid_1", Aud: ExchangeAudience, TTL: time.Minute})
	oaTok, _ := s.Sign(ctx, jwt.Claims{Iss: "https://ach.test", Sub: "u@x.com", Aud: "ach", TTL: time.Minute})

	if info, err := chain.Resolve(ctx, exTok); err != nil || info == nil || info.KeyType != keys.PrefixEk {
		t.Fatalf("chain, exchange JWT: info=%v err=%v", info, err)
	}
	if info, err := chain.Resolve(ctx, oaTok); err != nil || info == nil || info.KeyType != keys.PrefixPk {
		t.Fatalf("chain, OAuth JWT: info=%v err=%v", info, err)
	}
	if info, _ := oauth.Resolve(ctx, exTok); info != nil {
		t.Fatal("the OAuth resolver accepted an exchange JWT")
	}
	before := ekLookups
	if info, _ := NewExchangeResolver(fallthroughResolver(), s, "https://ach.test", ek).Resolve(ctx, oaTok); info != nil {
		t.Fatal("the exchange resolver accepted an OAuth JWT")
	}
	if ekLookups != before {
		t.Fatal("an OAuth JWT reached the exchange-key lookup")
	}
}

func TestGrantedExchangeKey(t *testing.T) {
	ctx := context.Background()
	row := liveExchangeRow()
	grant := db.KeyGrantRow{Namespace: "ach", Name: "g", Identity: row.OwnerEmail,
		Environments: []string{"prod"}, ServiceAccounts: []string{"agents/doctor"}}
	grants := []db.KeyGrantRow{grant}
	read := func(context.Context, string) (*db.EkKeyInfo, error) { return liveExchangeRow(), nil }
	list := func(context.Context, string) ([]db.KeyGrantRow, error) { return grants, nil }
	lookup := grantedExchangeKey(read, list)

	if got, err := lookup(ctx, "ekid_1"); err != nil || got == nil {
		t.Fatalf("granted: got=%v err=%v", got, err)
	}
	for name, g := range map[string]db.KeyGrantRow{
		"identity mismatch":    {Identity: "x@ackstorm.com", Environments: grant.Environments, ServiceAccounts: grant.ServiceAccounts},
		"environment mismatch": {Identity: grant.Identity, Environments: []string{"dev"}, ServiceAccounts: grant.ServiceAccounts},
		"requester mismatch":   {Identity: grant.Identity, Environments: grant.Environments, ServiceAccounts: []string{"agents/other"}},
	} {
		grants = []db.KeyGrantRow{g}
		if got, err := lookup(ctx, "ekid_1"); err != nil || got != nil {
			t.Errorf("%s: got=%v err=%v; want (nil, nil)", name, got, err)
		}
	}
	grants = nil
	if got, err := lookup(ctx, "ekid_1"); err != nil || got != nil {
		t.Errorf("grant removed: got=%v err=%v; want (nil, nil)", got, err)
	}
	if got, err := grantedExchangeKey(func(context.Context, string) (*db.EkKeyInfo, error) { return nil, nil }, list)(ctx, "ekid_1"); err != nil || got != nil {
		t.Errorf("no key row: got=%v err=%v", got, err)
	}
	boom := errors.New("db down")
	if _, err := grantedExchangeKey(read, func(context.Context, string) ([]db.KeyGrantRow, error) { return nil, boom })(ctx, "ekid_1"); !errors.Is(err, boom) {
		t.Errorf("grant listing error must surface, got %v", err)
	}
	if _, err := grantedExchangeKey(func(context.Context, string) (*db.EkKeyInfo, error) { return nil, boom }, list)(ctx, "ekid_1"); !errors.Is(err, boom) {
		t.Errorf("key lookup error must surface, got %v", err)
	}
}

// TestCachedExchangeResolverServesRemovedGrantUntilExpiry runs the production
// chain cache(exchange(oauth(db))): a grant removed after the first resolve is
// still served from the cache until its entry expires, then refused, and an
// entry never outlives the JWT.
func TestCachedExchangeResolverServesRemovedGrantUntilExpiry(t *testing.T) {
	s := oauthSigner(t)
	ctx := context.Background()
	granted := true
	lookup := grantedExchangeKey(
		func(context.Context, string) (*db.EkKeyInfo, error) { return liveExchangeRow(), nil },
		func(context.Context, string) ([]db.KeyGrantRow, error) {
			if !granted {
				return nil, nil
			}
			return []db.KeyGrantRow{{Identity: "ai.run@ackstorm.com", Environments: []string{"prod"},
				ServiceAccounts: []string{"agents/doctor"}}}, nil
		})
	oauth := NewOAuthResolver(fallthroughResolver(), s, "https://ach.test", "ach",
		func(context.Context, string) (*db.PkKeyInfo, error) { return nil, nil })
	now := time.Now()
	chain, mr, _ := setupCachedWith(t, NewExchangeResolver(oauth, s, "https://ach.test", lookup),
		WithClock(func() time.Time { return now }))

	raw := func(ttl time.Duration) string {
		tok, err := s.Sign(ctx, jwt.Claims{Iss: "https://ach.test", Sub: "ekid_1", Aud: ExchangeAudience, TTL: ttl})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	long := raw(10 * time.Minute)
	if info, err := chain.Resolve(ctx, long); err != nil || info == nil {
		t.Fatalf("first resolve: info=%v err=%v", info, err)
	}
	granted = false
	if info, err := chain.Resolve(ctx, long); err != nil || info == nil {
		t.Fatalf("cached entry must still be served inside the window: info=%v err=%v", info, err)
	}
	now = now.Add(defaultTTL + time.Second)
	mr.FastForward(defaultTTL + time.Second)
	if info, err := chain.Resolve(ctx, long); err != nil || info != nil {
		t.Fatalf("after the window the removed grant must refuse: info=%v err=%v", info, err)
	}

	granted, now = true, time.Now()
	short := raw(10 * time.Second)
	if info, err := chain.Resolve(ctx, short); err != nil || info == nil {
		t.Fatalf("short JWT: info=%v err=%v", info, err)
	}
	for _, k := range mr.Keys() {
		if ttl := mr.TTL(k); ttl > 10*time.Second {
			t.Fatalf("cache entry %s TTL %v outlives the 10s JWT", k, ttl)
		}
	}
}
