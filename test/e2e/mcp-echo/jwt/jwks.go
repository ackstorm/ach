// SPDX-License-Identifier: Apache-2.0

// Package jwt implements the JWKS-fetching + EdDSA-verifying half of
// the ach-mcp-echo backend. Stdlib-only on purpose: the binary is a
// reference for users writing their own MCP backend, and a pure-stdlib
// Ed25519 OKP path is what real users will copy.
package jwt

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// ErrUnknownKid is returned by KeyCache.Lookup when no key in the
// current JWK Set carries the requested kid.
var ErrUnknownKid = errors.New("jwks: kid not found")

// ErrBadKey is returned when a JWK entry has the wrong kty/crv or its
// public-key material does not decode to 32 bytes.
var ErrBadKey = errors.New("jwks: not an Ed25519 OKP key")

// jwk is the minimal RFC 7517 wire shape the Forwarder publishes. We
// intentionally do NOT model "use"/"alg" — verify.go pins alg=EdDSA at
// the JWS-header level.
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	X   string `json:"x"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// KeyCache fetches the JWK Set lazily and caches kid → public key.
// One refresh is in flight at a time (mu). A cache miss triggers a
// blocking refresh; if still missing after the refresh, Lookup returns
// ErrUnknownKid.
//
// The cache does NOT respect Cache-Control max-age. It refreshes on
// miss with a debounce of refreshInterval — sufficient for a single-
// replica e2e fixture, NOT production.
type KeyCache struct {
	url             string
	client          *http.Client
	refreshInterval time.Duration

	mu        sync.Mutex
	keys      map[string]ed25519.PublicKey
	lastFetch time.Time
}

// NewKeyCache returns a cache that fetches from url. Sensible defaults:
// 5s HTTP timeout, 5-minute miss-refresh debounce.
func NewKeyCache(url string) *KeyCache {
	return &KeyCache{
		url:             url,
		client:          &http.Client{Timeout: 5 * time.Second},
		refreshInterval: 5 * time.Minute,
		keys:            map[string]ed25519.PublicKey{},
	}
}

// SetRefreshInterval overrides the miss-refresh debounce. For tests.
func (c *KeyCache) SetRefreshInterval(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshInterval = d
}

// Lookup returns the cached public key for kid, refreshing the JWK Set
// once on miss (subject to refreshInterval debounce).
func (c *KeyCache) Lookup(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	c.mu.Lock()
	if k, ok := c.keys[kid]; ok {
		c.mu.Unlock()
		return k, nil
	}
	staleEnough := time.Since(c.lastFetch) >= c.refreshInterval || c.lastFetch.IsZero()
	c.mu.Unlock()

	if !staleEnough {
		return nil, ErrUnknownKid
	}
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	return nil, ErrUnknownKid
}

func (c *KeyCache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("jwks: build request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("jwks: fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("jwks: fetch status %d", resp.StatusCode)
	}

	var set jwkSet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("jwks: decode: %w", err)
	}

	next := make(map[string]ed25519.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" {
			return fmt.Errorf("%w: kid=%q kty=%q crv=%q", ErrBadKey, k.Kid, k.Kty, k.Crv)
		}
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return fmt.Errorf("jwks: decode x for kid %q: %w", k.Kid, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: kid=%q len=%d", ErrBadKey, k.Kid, len(raw))
		}
		next[k.Kid] = ed25519.PublicKey(raw)
	}

	c.mu.Lock()
	c.keys = next
	c.lastFetch = time.Now()
	c.mu.Unlock()
	return nil
}
