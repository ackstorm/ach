// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// OIDCFetchFunc reads the cluster's ServiceAccount issuer and JWKS
// (/.well-known/openid-configuration, /openid/v1/jwks).
type OIDCFetchFunc func(ctx context.Context) (issuer string, jwks []byte, err error)

const (
	saJWKSTTL         = 10 * time.Minute
	saJWKSMinRefetch  = time.Minute
	saJWKSFetchTimout = 5 * time.Second
)

var (
	// ErrSATokenInvalid: the token is not signed by the cluster's
	// ServiceAccount keys, names another issuer, or is expired.
	ErrSATokenInvalid = errors.New("invalid ServiceAccount token")
	// ErrSAKeysUnavailable: no cluster JWKS is cached and none could be read.
	ErrSAKeysUnavailable = errors.New("cluster ServiceAccount keys unavailable")
)

// SATokenVerifier checks a ServiceAccount token's signature, issuer and
// expiry locally, so a forged token costs neither a TokenReview nor a slot
// in the global exchange limit. The JWKS is cached for saJWKSTTL; an unknown
// kid refetches at most once per saJWKSMinRefetch; a failed refetch keeps
// serving the cached set.
type SATokenVerifier struct {
	fetch OIDCFetchFunc
	now   func() time.Time

	mu        sync.Mutex
	issuer    string
	keys      jose.JSONWebKeySet
	loaded    bool
	fetchedAt time.Time // last successful fetch
	attempted time.Time // last fetch attempt
}

// NewSATokenVerifier returns a verifier reading keys through fetch.
func NewSATokenVerifier(fetch OIDCFetchFunc) *SATokenVerifier {
	return &SATokenVerifier{fetch: fetch, now: time.Now}
}

// Verify returns nil for a validly signed, unexpired token of the cluster's
// issuer; ErrSATokenInvalid, or ErrSAKeysUnavailable when the keys cannot be
// had.
func (v *SATokenVerifier) Verify(ctx context.Context, token string) error {
	// Only asymmetric algorithms: none and HS* never parse.
	jws, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256, jose.ES256})
	if err != nil || len(jws.Signatures) != 1 {
		return ErrSATokenInvalid
	}
	kid := jws.Signatures[0].Header.KeyID
	issuer, keys, err := v.keysFor(ctx, kid)
	if err != nil {
		return err
	}
	cands := keys.Key(kid)
	if kid == "" || len(cands) == 0 {
		return ErrSATokenInvalid
	}
	payload, err := jws.Verify(cands[0].Key)
	if err != nil {
		return ErrSATokenInvalid
	}
	var c struct {
		Iss string  `json:"iss"`
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(payload, &c) != nil || c.Iss == "" || c.Iss != issuer || c.Exp <= float64(v.now().Unix()) {
		return ErrSATokenInvalid
	}
	return nil
}

// keysFor returns the cached issuer and key set, refreshing when stale or
// when kid is unknown (rate-limited).
func (v *SATokenVerifier) keysFor(ctx context.Context, kid string) (string, jose.JSONWebKeySet, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	stale := !v.loaded || now.Sub(v.fetchedAt) >= saJWKSTTL
	unknown := v.loaded && len(v.keys.Key(kid)) == 0
	if (stale || unknown) && (!v.loaded || now.Sub(v.attempted) >= saJWKSMinRefetch) {
		v.attempted = now
		fctx, cancel := context.WithTimeout(ctx, saJWKSFetchTimout)
		issuer, raw, err := v.fetch(fctx)
		cancel()
		var set jose.JSONWebKeySet
		if err == nil {
			err = json.Unmarshal(raw, &set)
		}
		switch {
		case err == nil:
			v.issuer, v.keys, v.loaded, v.fetchedAt = issuer, set, true, now
		case !v.loaded:
			return "", jose.JSONWebKeySet{}, ErrSAKeysUnavailable
		}
	}
	return v.issuer, v.keys, nil
}
