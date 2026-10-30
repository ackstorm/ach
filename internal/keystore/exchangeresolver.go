// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keygrant"
	"github.com/ackstorm/ach/internal/keys"
)

// ExchangeAudience is the aud of a JWT minted by POST /platform/keys/exchange.
// It differs from the OAuth audience "ach", so neither resolver accepts the
// other's token.
const ExchangeAudience = "ach-ek"

// ExchangeVerifier is what *jwt.Ed25519Signer provides for exchange JWTs.
type ExchangeVerifier interface {
	VerifyExp(raw, iss, aud string) (sub string, exp time.Time, err error)
}

// ExchangeKeyLookup returns the live exchange key a JWT subject names while a
// KeyGrant still allows its tuple, or (nil, nil).
type ExchangeKeyLookup func(ctx context.Context, keyID string) (*db.EkKeyInfo, error)

type exchangeResolver struct {
	inner  Resolver
	v      ExchangeVerifier
	iss    string
	lookup ExchangeKeyLookup
}

// NewExchangeResolver wraps the OAuth resolver. Order is load-bearing: the
// OAuth resolver answers 401 for any JWS that fails its audience, so a JWS
// whose unverified aud names ExchangeAudience is decided HERE (verified, then
// mapped to its exchange key — the same KeyInfo an ek_ yields), and every
// other bearer goes to inner unchanged. A JWS with that audience that fails
// verification, or names no live granted key, is (nil, nil) — 401. Sits
// inside the Redis cache: the 60-second window and singleflight apply, and
// the cached entry expires with the JWT.
func NewExchangeResolver(inner Resolver, v ExchangeVerifier, iss string, lookup ExchangeKeyLookup) Resolver {
	return &exchangeResolver{inner: inner, v: v, iss: strings.TrimRight(iss, "/"), lookup: lookup}
}

// NewExchangeResolverDB is the production constructor: the key row must be an
// active exchange key AND a KeyGrant in ns must still allow its tuple, so
// removing a grant entry cuts access within the cache window.
func NewExchangeResolverDB(inner Resolver, v ExchangeVerifier, iss string, pool *pgxpool.Pool, ns string) Resolver {
	return NewExchangeResolver(inner, v, iss, grantedExchangeKey(
		func(ctx context.Context, keyID string) (*db.EkKeyInfo, error) {
			return db.ExchangeKeyResolve(ctx, pool, keyID)
		},
		func(ctx context.Context, identity string) ([]db.KeyGrantRow, error) {
			return db.ListKeyGrantsForIdentity(ctx, pool, ns, identity)
		}))
}

// grantedExchangeKey is the lookup NewExchangeResolverDB serves: the key row
// from read, kept only while a grant from list for its owner covers its tuple.
func grantedExchangeKey(read ExchangeKeyLookup, list func(ctx context.Context, identity string) ([]db.KeyGrantRow, error)) ExchangeKeyLookup {
	return func(ctx context.Context, keyID string) (*db.EkKeyInfo, error) {
		row, err := read(ctx, keyID)
		if err != nil || row == nil {
			return nil, err
		}
		grants, err := list(ctx, row.OwnerEmail)
		if err != nil {
			return nil, err
		}
		if len(keygrant.Covering(grants, row.OwnerEmail, row.Environment, row.Requester)) == 0 {
			return nil, nil
		}
		return row, nil
	}
}

func (r *exchangeResolver) Resolve(ctx context.Context, plaintext string) (*KeyInfo, error) {
	if !keys.LooksLikeJWS(plaintext) || !slices.Contains(keys.PeekAudience(plaintext), ExchangeAudience) {
		return r.inner.Resolve(ctx, plaintext)
	}
	keyID, exp, err := r.v.VerifyExp(plaintext, r.iss, ExchangeAudience)
	if err != nil {
		return nil, nil
	}
	row, err := r.lookup(ctx, keyID)
	if err != nil || row == nil {
		return nil, err
	}
	info := keyInfoFromEK(row)
	if info.ExpiresAt == nil || exp.Before(*info.ExpiresAt) {
		info.ExpiresAt = &exp
	}
	return info, nil
}
