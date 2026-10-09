// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/ackstorm/ach/internal/keys"
)

var (
	// ErrIdPUnreachable: the trusted issuer's discovery or JWKS could not be
	// fetched. Authn renders 503 idp_unreachable — never a silent pass.
	ErrIdPUnreachable = errors.New("identity provider unreachable")
	// ErrIdPTokenInvalid: the token failed verification (401).
	ErrIdPTokenInvalid = errors.New("identity provider token invalid")
)

// IdPConfig is the forwarder's ONE trusted external issuer
// (forwarder.trustedIdP → ACH_TRUSTED_IDP_{ISSUER,AUDIENCES,CLAIM}).
type IdPConfig struct {
	Issuer    string
	Audiences []string
	Claim     string
}

// IdPVerifier verifies access tokens of one external OIDC issuer (Dex for
// LibreChat). Discovery is lazy and retried until it succeeds, so an
// unreachable issuer never stops the forwarder from starting — only this
// credential path fails. JWKS caching and the refetch on an unknown kid
// are go-oidc's RemoteKeySet.
type IdPVerifier struct {
	cfg    IdPConfig
	client *http.Client
	// down is set by the transport on a transport error or 5xx: go-oidc
	// flattens fetch errors with %v, so errors.Is cannot tell "issuer
	// down" from "bad token".
	down atomic.Bool

	mu       sync.Mutex
	ver      *oidc.IDTokenVerifier
	failedAt time.Time // last discovery failure; retried after discoveryBackoff
}

// discoveryBackoff: during an issuer outage, requests fail fast instead of
// queueing behind one 10 s discovery attempt each.
const discoveryBackoff = 5 * time.Second

type flagTransport struct{ down *atomic.Bool }

func (t flagTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	t.down.Store(err != nil || resp.StatusCode >= 500)
	return resp, err
}

func NewIdPVerifier(cfg IdPConfig) *IdPVerifier {
	v := &IdPVerifier{cfg: cfg}
	v.client = &http.Client{Timeout: 10 * time.Second, Transport: flagTransport{&v.down}}
	return v
}

func (v *IdPVerifier) Issuer() string { return v.cfg.Issuer }

func (v *IdPVerifier) verifier() (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ver != nil {
		return v.ver, nil
	}
	if time.Since(v.failedAt) < discoveryBackoff {
		return nil, ErrIdPUnreachable
	}
	// Background, not the request ctx: RemoteKeySet keeps this ctx for
	// every later JWKS fetch.
	p, err := oidc.NewProvider(oidc.ClientContext(context.Background(), v.client), v.cfg.Issuer)
	if err != nil {
		v.failedAt = time.Now()
		// Once per backoff window; the cause (unreachable, 404, issuer
		// mismatch) is otherwise invisible behind a 503. No token here.
		slog.Warn("trusted idp: discovery failed", "issuer", v.cfg.Issuer, "err", err)
		return nil, fmt.Errorf("%w: discovery: %v", ErrIdPUnreachable, err)
	}
	// The audience is checked below against a list; go-oidc takes one ClientID.
	v.ver = p.Verifier(&oidc.Config{SkipClientIDCheck: true, SupportedSigningAlgs: []string{oidc.RS256}})
	return v.ver, nil
}

// Verify checks signature (RS256 only), exact iss, exp, aud ∈ Audiences,
// email_verified not false, and returns the identity claim trimmed and
// lower-cased plus the token's expiry.
func (v *IdPVerifier) Verify(ctx context.Context, raw string) (string, time.Time, error) {
	ver, err := v.verifier()
	if err != nil {
		return "", time.Time{}, err
	}
	// Reset first: only a fetch made during THIS verify may mark the issuer
	// down (a key already cached means no fetch, and a stale flag would turn
	// an expired token into a 503 the client never refreshes on).
	v.down.Store(false)
	tok, err := ver.Verify(ctx, raw)
	if err != nil {
		// ponytail: process-wide flag — during an outage a concurrent bad
		// token may read 503 instead of 401; both fail closed.
		if v.down.Load() {
			return "", time.Time{}, ErrIdPUnreachable
		}
		return "", time.Time{}, ErrIdPTokenInvalid
	}
	if !slices.ContainsFunc(tok.Audience, func(a string) bool { return slices.Contains(v.cfg.Audiences, a) }) {
		return "", time.Time{}, ErrIdPTokenInvalid
	}
	var c map[string]any
	if err := tok.Claims(&c); err != nil {
		return "", time.Time{}, ErrIdPTokenInvalid
	}
	// false = the upstream never proved the address, and the address IS
	// the ACH identity. Absent is accepted (some connectors omit it).
	if ev, ok := c["email_verified"].(bool); ok && !ev {
		return "", time.Time{}, ErrIdPTokenInvalid
	}
	id, _ := c[v.cfg.Claim].(string)
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return "", time.Time{}, ErrIdPTokenInvalid
	}
	return id, tok.Expiry, nil
}

// ErrLoginRequired: a valid trusted-IdP token whose user has no live oauth
// pk_ — never signed in to ACH, row revoked, or past the 90-day cap. Authn
// renders 403 ach_login_required (no challenge: the caller already holds a
// valid IdP token, an ACH OAuth dance is not what it needs).
var ErrLoginRequired = errors.New("no live ACH oauth identity for this user")

type trustedIdPResolver struct {
	inner  Resolver
	v      *IdPVerifier
	lookup OAuthPKLookup
	hook   PkExtendHook
}

// NewTrustedIdPResolver wraps the OAuth resolver: a JWS whose unverified
// iss equals the trusted issuer is verified here and mapped to the user's
// EXISTING oauth pk_ row (same KeyInfo an ACH OAuth token yields — budget
// tag, shell team, revocation identical); everything else goes to inner
// unchanged. Never mints: no row is ErrLoginRequired. lookup slides the row
// (db.OAuthPKCheckAndExtend); hook mirrors a slide onto the LiteLLM key.
// Sits OUTSIDE the Redis cache: that cache is shared with platform-api and
// content-service, which must never accept an IdP token.
func NewTrustedIdPResolver(inner Resolver, v *IdPVerifier, lookup OAuthPKLookup, hook PkExtendHook) Resolver {
	return &trustedIdPResolver{inner: inner, v: v, lookup: lookup, hook: hook}
}

func (r *trustedIdPResolver) Resolve(ctx context.Context, plaintext string) (*KeyInfo, error) {
	if !keys.LooksLikeJWS(plaintext) || peekIssuer(plaintext) != r.v.Issuer() {
		return r.inner.Resolve(ctx, plaintext)
	}
	email, exp, err := r.v.Verify(ctx, plaintext)
	if errors.Is(err, ErrIdPUnreachable) {
		return nil, err
	}
	if err != nil {
		return nil, nil // 401 + challenge, like any bad bearer
	}
	row, err := r.lookup(ctx, email)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrLoginRequired
	}
	if row.Extended && r.hook != nil && row.LiteLLMToken != nil {
		r.hook(context.WithoutCancel(ctx), *row.LiteLLMToken) // outlives the singleflight leader ctx
	}
	info := KeyInfoFromPK(row)
	if exp.Before(*info.ExpiresAt) {
		info.ExpiresAt = &exp // never outlive the IdP token
	}
	return info, nil
}

// peekIssuer reads iss WITHOUT verifying it — routing only, never trust.
func peekIssuer(raw string) string {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var c struct {
		Iss string `json:"iss"`
	}
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return c.Iss
}
