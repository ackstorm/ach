# OAuth Broker Chain Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ACH's OAuth AS chains the browser through each MCP service's broker with a signed `login_hint`, the access token carries `scope`, and the forwarder gates `/mcp/<svc>` on it — so brokers whose provider names no account (Zoho) can key the grant by the Dex email.

**Architecture:** Spec: `docs/superpowers/specs/2026-09-18-oauth-broker-chain-design.md`. A static service map (`ACH_OAUTH_SERVICES`, JSON) is shared by platform-api (chain, scope on the token) and forwarder (PRM `scopes_supported`, `insufficient_scope` gate). Grants are read from the brokers' Redis projection `oauth:{store}:state:{email}`; the broker's code is never redeemed. Everything mirrors alitellm-auth v0.8.3 `_chain_next` / `broker_callback` / `Grants`.

**Tech Stack:** Go 1.26, chi, go-redis v9, golang-jwt/v5 (EdDSA), Helm 3, kind e2e (`make e2e-full`). Host has NO Go — every `make` target auto-routes into the devtools container; raw go via `./scripts/dev.sh go …`.

## Global Constraints

- Every new `*.go` file starts with `// SPDX-License-Identifier: Apache-2.0` (pre-push gate).
- `ACH_OAUTH_SERVICES` empty/unset ⇒ feature dormant: no chain, no gate, PRMs unchanged, `helm template` byte-identical to today.
- `ACH_OAUTH_SERVICES` non-empty AND `ACH_OAUTH_GRANTS_REDIS_URL` unset ⇒ platform-api refuses to start.
- `login_hint`: EdDSA via the existing signer, `iss` = ACH issuer, `sub` = lowercase email, `aud` = store name, `exp` ≤ 600 s, no `email`/`groups`/`scope` claims.
- `scope` on the ACH token = `ach` (audience) + granted service keys, space-separated; recomputed on refresh.
- Only OAuth bearers are scope-gated; `pk_`/`ek_`/raw `sk-` untouched.
- The v0.9.1 browser-binding cookie must hold across the chain (`/broker-callback` requires it).
- Commit after every task; do not push until the whole plan is green locally (`make test-unit`, `make qa-lint`, `make e2e-full`).
- Docs in the same change (CLAUDE.md rows, jwt-forwarder.md, signing-in-from-tools.md, troubleshooting.md).

---

## File map

| File | Responsibility |
|---|---|
| `internal/forwarder/jwt/signer.go` | `Claims.Scope` claim |
| `internal/forwarder/jwt/verify.go` | `Verify` returns `*Verified{Sub, Scope}` |
| `internal/forwarder/jwt/metadata.go` | `ASMetadata(issuer, services)` scopes_supported |
| `internal/keystore/oauthresolver.go` | `JWTVerifier` new signature; `KeyInfo.OAuth`, `.Scopes` |
| `internal/keystore/keystore.go` | `KeyInfo` fields |
| `internal/platformapi/middleware/keyctx.go` | `KeyContext.OAuth`, `.Scopes` |
| `internal/oauthsvc/services.go` (new) | `Service`, `Parse(json)`, shared by both binaries |
| `internal/platformapi/auth/oauth_grants.go` (new) | `GrantReader`, `RedisGrants` |
| `internal/platformapi/auth/oauth_authorize.go` | scope validation, hand-off to chain, cookie lifetime |
| `internal/platformapi/auth/oauth_chain.go` (new) | broker DCR cache, `chainNext`, `/broker-callback`, `login_hint`, `finish` |
| `internal/platformapi/auth/oauth_token.go` | scope on code/refresh/JWT/response |
| `internal/platformapi/auth/oauth.go` | `OAuthDeps.Services/Grants`, route |
| `internal/forwarder/proxy/wellknown.go` | PRM `scopes_supported` |
| `internal/forwarder/proxy/handlers.go` | `insufficient_scope` gate |
| `internal/forwarder/server.go` | pass services to well-known + handlers |
| `cmd/ach/cmd/platform_api.go`, `cmd/ach/cmd/forwarder.go` | env parsing + wiring |
| `deploy/helm/ach/values.yaml`, `templates/platform-api-deployment.yaml`, `templates/forwarder-deployment.yaml` | `oauth.services`, `oauth.grantsRedis` |
| `test/e2e/mock/main.go`, `test/e2e/mock/broker.go` (new) | `ach-mock broker` |
| `test/e2e/cluster/03-test-backends/ach-mock-broker.yaml` (new), `kustomization.yaml`, `02-ach/ach.values.yaml` | fixture wiring |
| `test/e2e/oauth_frontdoor_test.go` | chain + scope assertions |

---

### Task 1: `scope` rides the JWT trust path (jwt + keystore + middleware)

**Files:**
- Modify: `internal/forwarder/jwt/signer.go:41-65` (Claims), `:185-206` (Sign)
- Modify: `internal/forwarder/jwt/verify.go:20-47`
- Modify: `internal/forwarder/jwt/metadata.go:13-27`
- Modify: `internal/keystore/oauthresolver.go:15-27`, `:60-72`
- Modify: `internal/keystore/keystore.go:55-66`
- Modify: `internal/platformapi/middleware/keyctx.go:20-31`, `:51-68`
- Test: `internal/forwarder/jwt/verify_test.go`, `internal/forwarder/jwt/metadata_test.go` (existing files — extend), `internal/keystore/oauthresolver_test.go`

**Interfaces:**
- Produces: `jwt.Claims.Scope string`; `type jwt.Verified struct{ Sub, Scope string }`; `(*Ed25519Signer).Verify(raw, iss, aud string) (*Verified, error)`; `keystore.JWTVerifier.Verify(raw, iss, aud string) (*jwt.Verified, error)`; `keystore.KeyInfo.OAuth bool`, `.Scopes []string`; `middleware.KeyContext.OAuth bool`, `.Scopes []string`; `jwt.ASMetadata(issuer string, audience string, services []string) map[string]any`.

- [ ] **Step 1: Failing tests — Verify returns scope; ASMetadata lists services**

Append to `internal/forwarder/jwt/verify_test.go` (find the existing signer fixture in that file — a loaded `*Ed25519Signer`; reuse its constructor, e.g. the one used by `TestVerify_RejectsWrongIssuer`):

```go
func TestVerify_ReturnsScope(t *testing.T) {
	s := newLoadedSigner(t) // whatever helper the existing tests use to get a signer with a seed
	raw, err := s.Sign(context.Background(), Claims{Iss: "https://ach.test", Sub: "u@x.com", Aud: "ach", Scope: "ach mcp-a", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.Verify(raw, "https://ach.test", "ach")
	if err != nil || v.Sub != "u@x.com" || v.Scope != "ach mcp-a" {
		t.Fatalf("got %+v err=%v", v, err)
	}
	raw, _ = s.Sign(context.Background(), Claims{Iss: "https://ach.test", Sub: "u@x.com", Aud: "ach", TTL: time.Minute})
	if v, err := s.Verify(raw, "https://ach.test", "ach"); err != nil || v.Scope != "" {
		t.Fatalf("no scope claim: %+v err=%v", v, err)
	}
}
```

Append to `internal/forwarder/jwt/metadata_test.go` (create it if absent, with the SPDX header and `package jwt`):

```go
func TestASMetadata_ScopesSupported(t *testing.T) {
	m := ASMetadata("https://ach.test/", "ach", nil)
	if got := m["scopes_supported"].([]string); len(got) != 2 || got[0] != "offline_access" || got[1] != "ach" {
		t.Fatalf("no services: %v", got)
	}
	m = ASMetadata("https://ach.test", "ach", []string{"mcp-b", "mcp-a"})
	if got := m["scopes_supported"].([]string); len(got) != 4 || got[2] != "mcp-a" || got[3] != "mcp-b" {
		t.Fatalf("services must be sorted after the fixed scopes: %v", got)
	}
}
```

- [ ] **Step 2: Run, expect compile failures**

Run: `make test-unit-pkg PKG=./internal/forwarder/jwt/...`
Expected: FAIL — `Claims has no field Scope`, `v.Sub undefined`, `too many arguments to ASMetadata`.

- [ ] **Step 3: Implement jwt changes**

`signer.go` — add to `Claims` after `Groups`:

```go
	// Scope is the JWT "scope" claim (RFC 8693 §4.2 shape: space-separated).
	// Set by the OAuth AS on user access tokens: the audience plus the MCP
	// services the user holds a broker grant for. Omitted when empty.
	Scope string
```

and in `Sign`, after the `groups` block:

```go
	if c.Scope != "" {
		claims["scope"] = c.Scope
	}
```

`verify.go` — replace the signature and tail:

```go
// Verified is what Verify hands back: the subject and the scope claim
// ("" when absent). Callers that only need identity read Sub.
type Verified struct {
	Sub   string
	Scope string
}

func (s *Ed25519Signer) Verify(raw, iss, aud string) (*Verified, error) {
	if strings.Count(raw, ".") != 2 {
		return nil, ErrNotAJWT
	}
	// keyfunc + jwtv5.Parse unchanged …
	if err != nil {
		return nil, err
	}
	sub, err := tok.Claims.GetSubject()
	if err != nil || sub == "" {
		return nil, errors.New("jwt: no subject")
	}
	scope, _ := tok.Claims.(jwtv5.MapClaims)["scope"].(string)
	return &Verified{Sub: sub, Scope: scope}, nil
}
```

`metadata.go`:

```go
// ASMetadata is the RFC 8414 document. services are the MCP service keys
// (the /mcp/<name> segments) a token may carry as scope; sorted so the
// document is stable.
func ASMetadata(issuer, audience string, services []string) map[string]any {
	issuer = strings.TrimRight(issuer, "/")
	scopes := append([]string{"offline_access", audience}, sortedCopy(services)...)
	return map[string]any{
		// … existing keys …
		"scopes_supported": scopes,
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
```

(add `"sort"` to imports.)

- [ ] **Step 4: Update callers — keystore + middleware + wellknown**

`internal/keystore/oauthresolver.go`:

```go
type JWTVerifier interface {
	Verify(raw, iss, aud string) (*jwt.Verified, error)
}

func (NoJWT) Verify(string, string, string) (*jwt.Verified, error) { return nil, errors.New("oauth disabled") }
```

and in `Resolve`:

```go
	v, err := r.verifier.Verify(plaintext, r.iss, r.aud)
	if err != nil {
		if errors.Is(err, jwt.ErrNotAJWT) {
			return r.inner.Resolve(ctx, plaintext)
		}
		return nil, nil
	}
	row, err := r.lookup(ctx, v.Sub)
	if err != nil || row == nil {
		return nil, err
	}
	info := pkInfoToKeyInfo(row)
	info.OAuth = true
	info.Scopes = strings.Fields(v.Scope)
	return info, nil
```

`internal/keystore/keystore.go` — add to `KeyInfo` after `LiteLLMKeyMaterial`:

```go
	// OAuth is true when the bearer was an OAuth access token (resolved by
	// oauthResolver) rather than a pk_/ek_ plaintext; Scopes is its "scope"
	// claim split on spaces. Both zero for pk_/ek_. Cached with the row
	// under the peppered hash of the JWT, so a token's scopes are stable.
	OAuth  bool     `json:"oauth,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
```

`internal/platformapi/middleware/keyctx.go` — add `OAuth bool` and `Scopes []string` to `KeyContext` (after `LiteLLMUserID`) and copy them in `WithKeyContext`: `OAuth: info.OAuth, Scopes: info.Scopes,`.

`internal/forwarder/proxy/wellknown.go:47` — `doc = jwt.ASMetadata(base, "ach", nil)` for now (Task 5 threads the real values).

Fix any other compile error `./scripts/dev.sh go build ./...` reports (tests in `internal/keystore` that implement `JWTVerifier` with the old signature: change them to return `&jwt.Verified{Sub: …}`).

- [ ] **Step 5: Resolver test — scopes reach KeyInfo**

Append to `internal/keystore/oauthresolver_test.go` (reuse that file's fake verifier/lookup; adapt names):

```go
func TestOAuthResolver_ScopesOnKeyInfo(t *testing.T) {
	r := NewOAuthResolver(&mockResolver{}, verifierReturning(&jwt.Verified{Sub: "u@x.com", Scope: "ach mcp-a"}), "https://ach.test", "ach",
		func(context.Context, string) (*db.PkKeyInfo, error) { return &db.PkKeyInfo{KeyID: "pkid_1", OwnerEmail: "u@x.com"}, nil })
	info, err := r.Resolve(context.Background(), "a.b.c")
	if err != nil || info == nil || !info.OAuth || len(info.Scopes) != 2 || info.Scopes[1] != "mcp-a" {
		t.Fatalf("got %+v err=%v", info, err)
	}
}
```

(`verifierReturning` — a one-line fake type in the test file: `type fakeVerifier struct{ v *jwt.Verified }` with `Verify(...)` returning it.)

- [ ] **Step 6: Run the three packages + full build**

Run: `./scripts/dev.sh go build ./... && make test-unit-pkg PKG=./internal/forwarder/jwt/... && make test-unit-pkg PKG=./internal/keystore/... && make test-unit-pkg PKG=./internal/platformapi/middleware/...`
Expected: all `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/forwarder/jwt internal/keystore internal/platformapi/middleware internal/forwarder/proxy/wellknown.go
git commit -m "feat(jwt): scope claim on user tokens, Verify returns claims, KeyInfo carries OAuth scopes"
```

---

### Task 2: Service map + grants reader (shared config, no behaviour yet)

**Files:**
- Create: `internal/oauthsvc/services.go`, `internal/oauthsvc/services_test.go`
- Create: `internal/platformapi/auth/oauth_grants.go`, `internal/platformapi/auth/oauth_grants_test.go`

**Interfaces:**
- Produces: `oauthsvc.Service{Store, Broker string}`; `oauthsvc.Parse(raw string) (map[string]Service, error)`; `oauthsvc.Keys(m map[string]Service) []string`; `auth.GrantReader` interface `Granted(ctx, email, store string) (bool, error)`; `auth.NewRedisGrants(rdb *redis.Client) GrantReader`; `auth.MapGrants map[string]bool` (test fake keyed `store + "|" + email`).

- [ ] **Step 1: Failing tests**

`internal/oauthsvc/services_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package oauthsvc

import "testing"

func TestParse(t *testing.T) {
	m, err := Parse(`{"mcp-zoho-desk-ro":{"store":"zoho-desk-ro","broker":"https://api.example/zoho-desk-ro-callback/"}}`)
	if err != nil || len(m) != 1 || m["mcp-zoho-desk-ro"].Store != "zoho-desk-ro" || m["mcp-zoho-desk-ro"].Broker != "https://api.example/zoho-desk-ro-callback" {
		t.Fatalf("%+v %v", m, err)
	}
	if m, err := Parse(""); err != nil || len(m) != 0 {
		t.Fatalf("empty must be a nil map: %+v %v", m, err)
	}
	for _, bad := range []string{`{"a":{"store":"","broker":"https://b"}}`, `{"a":{"store":"s","broker":"ftp://b"}}`, `{"a b":{"store":"s","broker":"https://b"}}`, `nope`} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if k := Keys(map[string]Service{"b": {}, "a": {}}); len(k) != 2 || k[0] != "a" {
		t.Fatalf("Keys must be sorted: %v", k)
	}
}
```

`internal/platformapi/auth/oauth_grants_test.go` (uses the miniredis fixture the package already has — see `newOAuthStore` in `oauth_test.go`):

```go
func TestRedisGrants(t *testing.T) {
	store := newOAuthStore(t)
	g := NewRedisGrants(store.RDB)
	ctx := context.Background()
	if ok, err := g.Granted(ctx, "u@x.com", "zoho-desk-ro"); ok || err != nil {
		t.Fatalf("absent: %v %v", ok, err)
	}
	store.RDB.Set(ctx, "oauth:zoho-desk-ro:state:u@x.com", `{"granted":true,"x":1}`, 0)
	if ok, err := g.Granted(ctx, "u@x.com", "zoho-desk-ro"); !ok || err != nil {
		t.Fatalf("granted: %v %v", ok, err)
	}
	store.RDB.Set(ctx, "oauth:zoho-desk-ro:state:u@x.com", `not json`, 0)
	if ok, err := g.Granted(ctx, "u@x.com", "zoho-desk-ro"); ok || err != nil {
		t.Fatalf("garbage reads as not granted, no error: %v %v", ok, err)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `make test-unit-pkg PKG=./internal/oauthsvc/... && make test-unit-pkg PKG=./internal/platformapi/auth/...`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Implement**

`internal/oauthsvc/services.go`:

```go
// SPDX-License-Identifier: Apache-2.0

// Package oauthsvc is the static MCP-service map the OAuth broker chain
// runs on: /mcp/<key> → the broker that fronts it and the store name the
// broker knows the service by. One JSON blob (ACH_OAUTH_SERVICES), the same
// shape as alitellm-auth's authServer.services, read by platform-api (the
// chain, scope on the token) and the forwarder (PRM scopes_supported, the
// insufficient_scope gate).
package oauthsvc

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Service is one /mcp/<key> entry.
type Service struct {
	Store  string `json:"store"`  // the broker's name for it: its scope + the login_hint aud
	Broker string `json:"broker"` // the broker's OAuth AS base (has /register, /authorize); no trailing slash
}

var keyRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`) // the forwarder's serviceRe character class

// Parse decodes ACH_OAUTH_SERVICES. "" → nil map (feature dormant).
func Parse(raw string) (map[string]Service, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var m map[string]Service
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("ACH_OAUTH_SERVICES: %w", err)
	}
	for k, s := range m {
		if !keyRe.MatchString(k) {
			return nil, fmt.Errorf("ACH_OAUTH_SERVICES: key %q is not a /mcp/<name> segment", k)
		}
		if s.Store == "" {
			return nil, fmt.Errorf("ACH_OAUTH_SERVICES[%s]: store required", k)
		}
		u, err := url.Parse(s.Broker)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("ACH_OAUTH_SERVICES[%s]: broker must be an http(s) URL", k)
		}
		s.Broker = strings.TrimRight(s.Broker, "/")
		m[k] = s
	}
	return m, nil
}

// Keys returns the service keys sorted.
func Keys(m map[string]Service) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

`internal/platformapi/auth/oauth_grants.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/redis/go-redis/v9"
)

// GrantReader answers "does email hold a broker grant for store?". The
// production reader is the MCP pods' own cleartext projection
// (mcp-oauth: `oauth:{store}:state:{email}` = {"granted": bool, …}) — one
// GET, no decryption, never written by ACH. email is the lowercased sub;
// the brokers key by the same value (login_hint.sub).
type GrantReader interface {
	Granted(ctx context.Context, email, store string) (bool, error)
}

type redisGrants struct{ rdb *redis.Client }

// NewRedisGrants reads the projection from rdb (ACH_OAUTH_GRANTS_REDIS_URL).
func NewRedisGrants(rdb *redis.Client) GrantReader { return redisGrants{rdb: rdb} }

func (g redisGrants) Granted(ctx context.Context, email, store string) (bool, error) {
	raw, err := g.rdb.Get(ctx, "oauth:"+store+":state:"+email).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var st struct {
		Granted bool `json:"granted"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return false, nil // a malformed projection is "no grant", not an outage
	}
	return st.Granted, nil
}

// MapGrants is the test reader: key = store + "|" + email.
type MapGrants map[string]bool

func (m MapGrants) Granted(_ context.Context, email, store string) (bool, error) {
	return m[store+"|"+email], nil
}
```

- [ ] **Step 4: Run tests**

Run: `make test-unit-pkg PKG=./internal/oauthsvc/... && make test-unit-pkg PKG=./internal/platformapi/auth/...`
Expected: `ok` both.

- [ ] **Step 5: Commit**

```bash
git add internal/oauthsvc internal/platformapi/auth/oauth_grants.go internal/platformapi/auth/oauth_grants_test.go
git commit -m "feat(oauth): service map (ACH_OAUTH_SERVICES) and broker-grant projection reader"
```

---

### Task 3: `scope` on the AS — validation at /authorize, computed at /token (no chain yet)

**Files:**
- Modify: `internal/platformapi/auth/oauth.go:21-39` (deps), `:44-54` (mount unchanged here)
- Modify: `internal/platformapi/auth/oauth_authorize.go` (`oauthPending`, `authorize`)
- Modify: `internal/platformapi/auth/oauth_token.go` (`oauthRefresh`, `token`, `issue`)
- Test: `internal/platformapi/auth/oauth_test.go`

**Interfaces:**
- Consumes: `oauthsvc.Service`, `GrantReader`, `MapGrants` (Task 2); `jwt.Claims.Scope` (Task 1).
- Produces: `OAuthDeps.Services map[string]oauthsvc.Service`, `OAuthDeps.Grants GrantReader`; `oauthPending.Scopes []string` (requested service keys); `oauthRefresh.Scopes []string`; `(OAuthDeps).grantedScope(ctx, email string, requested []string) (string, error)` returning `"ach mcp-a …"`; `(OAuthDeps).issue(w, r, sub, userID, clientID string, requested []string)`.

- [ ] **Step 1: Failing tests**

Append to `oauth_test.go`. `authorizeURL(cid, over)` already exists; `installFakePKs`, `seedCode`, `tokenForm`, `formHdr`, `tokenBody` exist (see the file).

```go
func withServices(f *asFixture) *asFixture {
	f.deps.Services = map[string]oauthsvc.Service{
		"mcp-a": {Store: "a-store", Broker: "https://broker.test/a"},
		"mcp-b": {Store: "b-store", Broker: "https://broker.test/b"},
	}
	f.deps.Grants = MapGrants{}
	f.mount()
	return f
}

func TestAuthorize_ScopeValidation(t *testing.T) {
	f := withServices(withFakeDex(newAS(t), "u@x.com"))
	cid := registerClient(t, f)
	// unknown scope → invalid_scope back to the client, no pending, no Dex
	w := f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": "ach mcp-nope"}), nil, nil)
	loc := w.Header().Get("Location")
	if w.Code != 302 || !strings.HasPrefix(loc, "http://127.0.0.1:5000/cb?") || !strings.Contains(loc, "error=invalid_scope") || !strings.Contains(loc, "state=xyz") {
		t.Fatalf("unknown scope: %d %s", w.Code, loc)
	}
	// known scopes + the always-accepted ones are stored on the pending
	w = f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": "offline_access ach mcp-b mcp-a"}), nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	var p oauthPending
	if ok, _ := f.store.Get(context.Background(), "pending", state, &p); !ok || strings.Join(p.Scopes, " ") != "mcp-b mcp-a" {
		t.Fatalf("pending scopes: ok=%v %+v", ok, p)
	}
	// no scope at all is fine (today's CLI login)
	if w := f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": ""}), nil, nil); w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "dex.test") {
		t.Fatalf("no scope: %d", w.Code)
	}
}

func TestToken_ScopeFromProjection(t *testing.T) {
	f := withServices(newAS(t))
	installFakePKs(f)
	cid := registerClient(t, f)
	f.deps.Grants = MapGrants{"a-store|u@x.com": true} // mcp-a granted, mcp-b not
	f.mount()
	seedCodeScoped(t, f, cid, []string{"mcp-a", "mcp-b"})
	w := f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr)
	var tb struct {
		tokenBody
		Scope string `json:"scope"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tb)
	if w.Code != 200 || tb.Scope != "ach mcp-a" {
		t.Fatalf("code grant scope: %d %s", w.Code, w.Body)
	}
	v, err := f.deps.Signer.Verify(tb.AccessToken, "https://ach.test", "ach")
	if err != nil || v.Scope != "ach mcp-a" {
		t.Fatalf("jwt scope: %+v %v", v, err)
	}
	// refresh recomputes: grant for mcp-b appears, mcp-a revoked at the broker disappears
	f.deps.Grants = MapGrants{"b-store|u@x.com": true}
	f.mount()
	w = f.do(t, "POST", "/platform/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tb.RefreshToken}, "client_id": {cid}}.Encode(), formHdr)
	_ = json.Unmarshal(w.Body.Bytes(), &tb)
	if w.Code != 200 || tb.Scope != "ach mcp-b" {
		t.Fatalf("refresh scope: %d %s", w.Code, w.Body)
	}
}

func TestToken_GrantsUnavailable(t *testing.T) {
	f := withServices(newAS(t))
	installFakePKs(f)
	cid := registerClient(t, f)
	f.deps.Grants = failingGrants{}
	f.mount()
	seedCodeScoped(t, f, cid, []string{"mcp-a"})
	if w := f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr); w.Code != 503 {
		t.Fatalf("grants down must be 503: %d %s", w.Code, w.Body)
	}
	// …but a code that asked for no services never touches the projection
	seedCodeScoped(t, f, cid, nil)
	if w := f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr); w.Code != 200 {
		t.Fatalf("no services requested: %d %s", w.Code, w.Body)
	}
}

type failingGrants struct{}

func (failingGrants) Granted(context.Context, string, string) (bool, error) {
	return false, errors.New("redis down")
}

func seedCodeScoped(t *testing.T, f *asFixture, cid string, scopes []string) {
	t.Helper()
	err := f.store.Put(context.Background(), "code", "thecode", oauthCode{
		oauthPending: oauthPending{ClientID: cid, RedirectURI: "http://127.0.0.1:5000/cb", State: "s", CodeChallenge: testChallenge, Scopes: scopes},
		Sub:          "u@x.com", UserID: "litellm-user-1",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
}
```

Add `"github.com/ackstorm/ach/internal/oauthsvc"` to the test imports.

- [ ] **Step 2: Run, expect failure**

Run: `make test-unit-pkg PKG=./internal/platformapi/auth/...`
Expected: FAIL — `Services`/`Grants`/`Scopes` undefined.

- [ ] **Step 3: Implement**

`oauth.go` — add to `OAuthDeps` after `Now`:

```go
	// Services is the MCP-service map (ACH_OAUTH_SERVICES); nil → no scopes
	// beyond the audience, no broker chain. Grants reads the brokers' grant
	// projection; required when Services is non-empty.
	Services map[string]oauthsvc.Service
	Grants   GrantReader
```

`oauth_authorize.go` — `oauthPending` gains `Scopes []string \`json:"scopes,omitempty"\`` (the requested service keys, in request order, deduplicated). In `authorize`, after the PKCE check and before `pendingID`:

```go
	scopes, unknown := d.parseScope(q.Get("scope"))
	if len(unknown) > 0 {
		p := url.Values{"error": {"invalid_scope"}, "error_description": {"unknown scope: " + strings.Join(unknown, " ")}}
		if state != "" {
			p.Set("state", state)
		}
		clientRedirect(w, r, redirectURI, p)
		return
	}
```

and set `Scopes: scopes` in the `oauthPending` literal. Add:

```go
// parseScope splits a space-separated scope; the audience and
// offline_access are always accepted and dropped, service keys are kept
// (deduplicated, request order), anything else is returned as unknown.
func (d OAuthDeps) parseScope(raw string) (services, unknown []string) {
	seen := map[string]bool{}
	for _, s := range strings.Fields(raw) {
		switch {
		case s == d.Audience || s == "offline_access":
		case d.Services[s].Store != "":
			if !seen[s] {
				seen[s] = true
				services = append(services, s)
			}
		default:
			unknown = append(unknown, s)
		}
	}
	return services, unknown
}
```

`oauth_token.go`:

```go
type oauthRefresh struct {
	Sub      string   `json:"sub"`
	UserID   string   `json:"user_id"`
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scopes,omitempty"` // requested services; granted is recomputed per refresh
}
```

In `token`: `d.issue(w, r, c.Sub, c.UserID, clientID, c.Scopes)` and `d.issue(w, r, rf.Sub, rf.UserID, clientID, rf.Scopes)`.

`issue` becomes:

```go
func (d OAuthDeps) issue(w http.ResponseWriter, r *http.Request, sub, userID, clientID string, requested []string) {
	scope, err := d.grantedScope(r.Context(), sub, requested)
	if err != nil {
		d.Auth.Logger.Warn("oauth: grant projection unreachable", "err", err)
		oauthError(w, 503, "temporarily_unavailable", "grant projection unreachable")
		return
	}
	// ensureOAuthPK … unchanged …
	access, err := d.Signer.Sign(r.Context(), jwt.Claims{Iss: d.Issuer, Sub: sub, Aud: d.Audience, Email: sub, Scope: scope, TTL: d.AccessTTL})
	// … refresh mint unchanged, but store Scopes: requested …
	if err := d.Store.Put(r.Context(), "refresh", refresh, oauthRefresh{Sub: sub, UserID: userID, ClientID: clientID, Scopes: requested}, d.RefreshTTL); err != nil {
	// … response gains "scope": scope
}

// grantedScope is the token's scope: the audience, then every requested
// service the projection says sub holds a grant for, in request order.
// A projection error is returned as-is — never mint wider than the
// projection allows, never silently narrower.
func (d OAuthDeps) grantedScope(ctx context.Context, sub string, requested []string) (string, error) {
	out := []string{d.Audience}
	for _, s := range requested {
		svc, ok := d.Services[s]
		if !ok {
			continue // map shrank since the code was issued
		}
		granted, err := d.Grants.Granted(ctx, sub, svc.Store)
		if err != nil {
			return "", err
		}
		if granted {
			out = append(out, s)
		}
	}
	return strings.Join(out, " "), nil
}
```

(`d.Grants` is nil only when `Services` is empty; then `requested` is always empty and the loop never runs — Task 5 enforces the pairing at startup.)

- [ ] **Step 4: Run tests**

Run: `make test-unit-pkg PKG=./internal/platformapi/auth/...`
Expected: `ok`; the three new tests PASS; every existing test still PASS (no scope ⇒ `"ach"` scope on the token — check `TestToken_CodeExchangeMintsTheOAuthPKAndAJWT` still passes; it does not assert on scope).

- [ ] **Step 5: Commit**

```bash
git add internal/platformapi/auth
git commit -m "feat(oauth): validate scope at /authorize, mint scope from the grant projection at /token"
```

---

### Task 4: The broker chain — `chainNext`, `/broker-callback`, `login_hint`

**Files:**
- Create: `internal/platformapi/auth/oauth_chain.go`
- Modify: `internal/platformapi/auth/oauth_authorize.go` (`asCallback` tail → hand-off; cookie clearing moves to `finish`)
- Modify: `internal/platformapi/auth/oauth.go:48-53` (route)
- Test: `internal/platformapi/auth/oauth_chain_test.go` (new)

**Interfaces:**
- Consumes: `OAuthDeps.Services/Grants`, `oauthPending.Scopes` (Task 3); `bindingCookieName/bindingCookie/bindingHash` (exist since v0.9.1 in `oauth_authorize.go`); `jwt.Claims` (Task 1).
- Produces: routes `GET /platform/oauth/broker-callback`; store kinds `brokerclient` (id = broker URL, TTL 30 d, `{client_id}`) and `chain` (TTL 10 min); `(OAuthDeps).finish(w, r, p oauthPending, pendingID, email, userID string)`; `(OAuthDeps).chainNext(w, r, c oauthChain)`; `hintTTL = 600 * time.Second`; `oauthChain{oauthPending; PendingID, Sub, UserID string; Todo []string; Verifier string}`.

- [ ] **Step 1: Failing tests**

`internal/platformapi/auth/oauth_chain_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/oauthsvc"
)

// fakeBroker is an mcp-oauth stand-in: /register hands out a client id,
// /authorize records the login_hint and bounces to redirect_uri with a code
// (or the error the test armed).
type fakeBroker struct {
	srv       *httptest.Server
	mu        sync.Mutex
	registers int
	hints     []string
	authz     []url.Values
	deny      bool
	down      bool
}

func newFakeBroker(t *testing.T) *fakeBroker {
	t.Helper()
	b := &fakeBroker{}
	mux := http.NewServeMux()
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.down {
			w.WriteHeader(503)
			return
		}
		b.registers++
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "broker-client-1"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		q := r.URL.Query()
		b.authz = append(b.authz, q)
		b.hints = append(b.hints, q.Get("login_hint"))
		p := url.Values{"state": {q.Get("state")}}
		if b.deny {
			p.Set("error", "access_denied")
		} else {
			p.Set("code", "broker-code")
		}
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+p.Encode(), 302)
	})
	b.srv = httptest.NewServer(mux)
	t.Cleanup(b.srv.Close)
	return b
}

// chainFixture: Dex faked, two services on ONE broker, grants map.
func chainFixture(t *testing.T, email string) (*asFixture, *fakeBroker) {
	t.Helper()
	b := newFakeBroker(t)
	f := withFakeDex(newAS(t), email)
	f.deps.Services = map[string]oauthsvc.Service{
		"mcp-a": {Store: "a-store", Broker: b.srv.URL},
		"mcp-b": {Store: "b-store", Broker: b.srv.URL},
	}
	f.deps.Grants = MapGrants{}
	f.mount()
	return f, b
}

// walk follows redirects from /as-callback through the fake broker and back
// into the AS (rewriting the broker's redirect target to the recorder),
// carrying the binding cookie, until a hop lands on the client redirect_uri.
func walk(t *testing.T, f *asFixture, b *fakeBroker, start string, cookie map[string]string) string {
	t.Helper()
	next := start // always an AS path: broker hops are resolved inline below
	for hop := 0; hop < 10; hop++ {
		w := f.do(t, "GET", next, nil, cookie)
		loc := w.Header().Get("Location")
		if w.Code != 302 || loc == "" {
			t.Fatalf("hop %d %s: %d %s", hop, next, w.Code, w.Body)
		}
		if strings.HasPrefix(loc, "http://127.0.0.1:5000/cb") {
			return loc
		}
		if strings.HasPrefix(loc, b.srv.URL) {
			// ask the broker (no redirect following) and continue with its Location
			c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := c.Get(loc)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			loc = resp.Header.Get("Location")
		}
		next = strings.TrimPrefix(loc, "https://ach.test")
	}
	t.Fatal("chain never reached the client")
	return ""
}

func TestChain_OneHopPerUngrantedService(t *testing.T) {
	f, b := chainFixture(t, "U@X.com")
	f.deps.Grants = MapGrants{"a-store|u@x.com": true} // mcp-a already granted
	f.mount()
	cid := registerClient(t, f)
	w := f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": "ach mcp-a mcp-b"}), nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	cs := w.Result().Cookies()
	cookie := map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value}

	loc := walk(t, f, b, "/platform/oauth/as-callback?code=dexcode&state="+state, cookie)
	if !strings.Contains(loc, "code=") || !strings.Contains(loc, "state=xyz") {
		t.Fatalf("client redirect: %s", loc)
	}
	if b.registers != 1 || len(b.authz) != 1 {
		t.Fatalf("expected exactly one broker hop (mcp-b): registers=%d authz=%d", b.registers, len(b.authz))
	}
	q := b.authz[0]
	if q.Get("scope") != "b-store" || q.Get("client_id") != "broker-client-1" || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != "https://ach.test/platform/oauth/broker-callback" || q.Get("response_type") != "code" {
		t.Fatalf("broker authorize params: %v", q)
	}
	// login_hint: our key, iss = issuer, sub = lowercased email, aud = store, ≤ 600 s
	v, err := f.deps.Signer.Verify(b.hints[0], "https://ach.test", "b-store")
	if err != nil || v.Sub != "u@x.com" {
		t.Fatalf("login_hint: %+v %v", v, err)
	}
	if _, err := f.deps.Signer.Verify(b.hints[0], "https://ach.test", "ach"); err == nil {
		t.Fatal("login_hint must not verify as an ACH access token")
	}
	var hdr, body map[string]any
	parts := strings.Split(b.hints[0], ".")
	_ = json.Unmarshal(b64(t, parts[0]), &hdr)
	_ = json.Unmarshal(b64(t, parts[1]), &body)
	if exp, iat := body["exp"].(float64), body["iat"].(float64); exp-iat > 600 || body["scope"] != nil || body["groups"] != nil {
		t.Fatalf("hint claims: %v", body)
	}
	// the code carries the requested scopes; the cookie is cleared at finish
	code := mustQuery(t, loc, "code")
	var rec oauthCode
	if ok, _ := f.store.Get(context.Background(), "code", code, &rec); !ok || strings.Join(rec.Scopes, " ") != "mcp-a mcp-b" {
		t.Fatalf("code scopes: %+v", rec)
	}
}

func TestChain_BrokerDownOrDeclined_SkipsAndFinishes(t *testing.T) {
	for _, mode := range []string{"down", "deny"} {
		t.Run(mode, func(t *testing.T) {
			f, b := chainFixture(t, "u@x.com")
			b.down, b.deny = mode == "down", mode == "deny"
			cid := registerClient(t, f)
			w := f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": "mcp-a"}), nil, nil)
			state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
			cs := w.Result().Cookies()
			loc := walk(t, f, b, "/platform/oauth/as-callback?code=dexcode&state="+state, map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value})
			if !strings.Contains(loc, "code=") {
				t.Fatalf("must still finish: %s", loc)
			}
		})
	}
}

func TestBrokerCallback_RequiresBindingCookieAndBurnsChain(t *testing.T) {
	f, b := chainFixture(t, "u@x.com")
	cid := registerClient(t, f)
	w := f.do(t, "GET", authorizeURL(cid, map[string]string{"scope": "mcp-a"}), nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	cs := w.Result().Cookies()
	cookie := map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value}
	w = f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie)
	chainID := mustQuery(t, w.Header().Get("Location"), "state")
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), b.srv.URL+"/authorize?") || chainID == "" {
		t.Fatalf("hand-off: %d %s", w.Code, w.Header().Get("Location"))
	}
	// victim's browser (no cookie) arrives with the chain state
	if w := f.do(t, "GET", "/platform/oauth/broker-callback?code=x&state="+chainID, nil, nil); w.Code != 400 {
		t.Fatalf("no cookie: %d", w.Code)
	}
	// burned: the right cookie no longer helps
	if w := f.do(t, "GET", "/platform/oauth/broker-callback?code=x&state="+chainID, nil, cookie); w.Code != 400 {
		t.Fatalf("chain replay: %d", w.Code)
	}
	if w := f.do(t, "GET", "/platform/oauth/broker-callback?code=x&state=nope", nil, cookie); w.Code != 400 {
		t.Fatalf("unknown chain: %d", w.Code)
	}
}

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
```

(add `"encoding/base64"` to imports; `mustQuery`, `registerClient`, `authorizeURL`, `withFakeDex`, `newAS` exist in `oauth_test.go`.)

Also update `TestASCallback` in `oauth_test.go`: the "binding cookie not cleared" assertion stays valid — on the no-`todo` path `finish` clears it.

- [ ] **Step 2: Run, expect failure**

Run: `make test-unit-pkg PKG=./internal/platformapi/auth/...`
Expected: FAIL — `/broker-callback` 404, no hop recorded, `rec.Scopes` etc.

- [ ] **Step 3: Implement `oauth_chain.go`**

```go
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/platformapi/auth/cli"
)

const (
	oauthChainTTL        = 10 * time.Minute
	oauthBrokerClientTTL = 30 * 24 * time.Hour
	// hintTTL bounds the login_hint handed to a broker: one chain step, not a
	// session (alitellm-auth HINT_TTL = 600).
	hintTTL = 600 * time.Second
)

// oauthChain is a pending authorization parked while the browser is at a
// service broker. Todo[0] is the service being consented to right now.
type oauthChain struct {
	oauthPending
	PendingID string   `json:"pending_id"` // the binding cookie's name suffix
	Sub       string   `json:"sub"`
	UserID    string   `json:"user_id"`
	Todo      []string `json:"todo"`
	// Verifier is unused today: the broker's code is never redeemed (the
	// projection is the truth). Kept so a revision that redeems it can.
	Verifier string `json:"verifier"`
}

// brokerClientID registers ACH once as a public client of broker (RFC 7591)
// and remembers the id. HTTPClient is a seam for tests (nil → 10s default).
func (d OAuthDeps) brokerClientID(ctx context.Context, broker string) (string, error) {
	var cached struct {
		ClientID string `json:"client_id"`
	}
	if ok, err := d.Store.Get(ctx, "brokerclient", broker, &cached); err == nil && ok && cached.ClientID != "" {
		return cached.ClientID, nil
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                "ACH",
		"redirect_uris":              []string{d.brokerCallbackURL()},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, broker+"/register", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("broker register: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&cached); err != nil || cached.ClientID == "" {
		return "", errors.New("broker register: no client_id")
	}
	_ = d.Store.Put(ctx, "brokerclient", broker, cached, oauthBrokerClientTTL) // best effort: a miss re-registers
	return cached.ClientID, nil
}

func (d OAuthDeps) brokerCallbackURL() string {
	return strings.TrimRight(d.Issuer, "/") + "/platform/oauth/broker-callback"
}

func (d OAuthDeps) httpClient() *http.Client {
	if d.HTTPClient != nil {
		return d.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// chainNext sends the browser to the broker of c.Todo[0]. A broker we
// cannot register with is skipped (logged) — the token simply lacks that
// scope — so one dead broker never blocks the whole login.
func (d OAuthDeps) chainNext(w http.ResponseWriter, r *http.Request, c oauthChain) {
	for len(c.Todo) > 0 {
		key := c.Todo[0]
		svc := d.Services[key]
		clientID, err := d.brokerClientID(r.Context(), svc.Broker)
		if err != nil {
			d.Auth.Logger.Warn("oauth: broker unreachable, skipping scope", "scope", key, "broker", svc.Broker, "err", err)
			c.Todo = c.Todo[1:]
			continue
		}
		chainID, err := cli.NewSessionID()
		if err != nil {
			htmlError(w, 500, "")
			return
		}
		c.Verifier = oauth2.GenerateVerifier()
		if err := d.Store.Put(r.Context(), "chain", chainID, c, oauthChainTTL); err != nil {
			htmlError(w, 500, "store unavailable")
			return
		}
		// Who this ceremony is for: the browser reaches the broker with no
		// header of ours, and the account it then picks at the provider need
		// not carry our email (Zoho names none), so the broker keys the grant
		// by this instead — signed by us, aud = the broker's own store name so
		// it verifies nowhere else, short-lived.
		hint, err := d.Signer.Sign(r.Context(), jwt.Claims{Iss: d.Issuer, Sub: c.Sub, Aud: svc.Store, TTL: hintTTL})
		if err != nil {
			htmlError(w, 500, "signer not loaded")
			return
		}
		q := url.Values{
			"response_type":         {"code"},
			"client_id":             {clientID},
			"redirect_uri":          {d.brokerCallbackURL()},
			"scope":                 {svc.Store},
			"state":                 {chainID},
			"code_challenge":        {oauth2.S256ChallengeFromVerifier(c.Verifier)},
			"code_challenge_method": {pkceS256},
			"login_hint":            {hint},
		}
		http.Redirect(w, r, svc.Broker+"/authorize?"+q.Encode(), http.StatusFound)
		return
	}
	d.finish(w, r, c.oauthPending, c.PendingID, c.Sub, c.UserID)
}

// brokerCallback: back from a service broker. A `code` means the broker ran
// the provider consent and stored the grant before minting it; the
// projection is what we trust, so the code is not redeemed. An `error`
// means the user declined: the token is issued without that scope.
func (d OAuthDeps) brokerCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var c oauthChain
	ok, err := d.Store.Take(r.Context(), "chain", q.Get("state"), &c) // burned whatever follows
	if err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	if !ok {
		htmlError(w, 400, "no authorization is in progress — start again from your client")
		return
	}
	ck, cerr := r.Cookie(bindingCookieName(c.PendingID, d.Auth.InsecureCookie))
	if cerr != nil || subtle.ConstantTimeCompare([]byte(bindingHash(ck.Value)), []byte(c.Binding)) != 1 {
		http.SetCookie(w, bindingCookie(c.PendingID, "", d.Auth.InsecureCookie, -1))
		d.Auth.Logger.Warn("oauth: broker-callback from a browser that did not start the authorization", "client_id", c.ClientID)
		htmlError(w, 400, "this browser did not start the authorization request — start again from your client")
		return
	}
	if e := q.Get("error"); e != "" {
		d.Auth.Logger.Info("oauth: broker declined", "scope", c.Todo[0], "error", e)
	}
	c.Todo = c.Todo[1:]
	d.chainNext(w, r, c)
}

// finish mints the client's authorization code and clears the binding
// cookie — the end of both the plain and the chained ceremony.
func (d OAuthDeps) finish(w http.ResponseWriter, r *http.Request, p oauthPending, pendingID, email, userID string) {
	http.SetCookie(w, bindingCookie(pendingID, "", d.Auth.InsecureCookie, -1))
	code, err := cli.NewSessionID()
	if err != nil {
		htmlError(w, 500, "")
		return
	}
	if err := d.Store.Put(r.Context(), "code", code, oauthCode{oauthPending: p, Sub: email, UserID: userID}, oauthCodeTTL); err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	pv := url.Values{"code": {code}}
	if p.State != "" {
		pv.Set("state", p.State)
	}
	d.Auth.Logger.Info("oauth: authorization code issued", "client_id", p.ClientID, "scopes", strings.Join(p.Scopes, " "))
	clientRedirect(w, r, p.RedirectURI, pv)
}
```

`oauth.go`: add `HTTPClient *http.Client // seam: broker /register; nil → 10s stdlib client` to `OAuthDeps`, and `r.Get("/broker-callback", d.brokerCallback)` in `MountOAuth`.

`oauth_authorize.go` — `asCallback` after `provision` succeeds, replace everything from `code, err := cli.NewSessionID()` to the end with:

```go
	var todo []string
	for _, key := range p.Scopes {
		granted, gerr := d.Grants.Granted(r.Context(), email, d.Services[key].Store)
		if gerr != nil {
			// Projection down: re-consent is harmless, a login blocked is not.
			d.Auth.Logger.Warn("oauth: grant projection unreachable at authorize; chaining every requested service", "err", gerr)
		}
		if !granted {
			todo = append(todo, key)
		}
	}
	if len(todo) > 0 {
		d.chainNext(w, r, oauthChain{oauthPending: p, PendingID: pendingID, Sub: email, UserID: userID, Todo: todo})
		return
	}
	d.finish(w, r, p, pendingID, email, userID)
```

and remove the `http.SetCookie(w, bindingCookie(pendingID, "", …, -1))` line that ran right after `Take` (the cookie must survive the chain; the mismatch branch still clears it — keep a `SetCookie(… -1)` inside the `cerr != nil || …` branch).

- [ ] **Step 4: Run tests**

Run: `make test-unit-pkg PKG=./internal/platformapi/auth/...`
Expected: `ok`, including `TestASCallback` (cookie now cleared by `finish`) and `TestASCallback_RequiresTheBrowserThatStartedIt`.

- [ ] **Step 5: Lint**

Run: `./scripts/dev.sh ./bin/golangci-lint run ./internal/platformapi/auth/... ./internal/oauthsvc/...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/platformapi/auth
git commit -m "feat(oauth): chain /authorize through service brokers with a signed login_hint"
```

---

### Task 5: Forwarder — PRM `scopes_supported` and the `insufficient_scope` gate; wiring + Helm

**Files:**
- Modify: `internal/forwarder/proxy/wellknown.go`, `internal/forwarder/proxy/handlers.go:30-46` (`HandlerDeps`), `:108-118` (gate), `internal/forwarder/server.go:59-85`
- Modify: `cmd/ach/cmd/forwarder.go`, `cmd/ach/cmd/platform_api.go`
- Modify: `deploy/helm/ach/values.yaml:129-132`, `templates/platform-api-deployment.yaml:105-110`, `templates/forwarder-deployment.yaml:72-74`
- Test: `internal/forwarder/proxy/wellknown_test.go`, `internal/forwarder/proxy/handlers_test.go` (extend existing), `cmd/ach/cmd/platform_api_test.go` (extend)

**Interfaces:**
- Consumes: `oauthsvc.Parse/Keys`, `KeyContext.OAuth/Scopes` (Tasks 1–2).
- Produces: `proxy.WellKnownHandler(base, audience string, services map[string]oauthsvc.Service)`; `proxy.HandlerDeps.Services map[string]oauthsvc.Service`; `forwarder.Deps.Services`, `.OAuthAudience`; env `ACH_OAUTH_SERVICES` (both), `ACH_OAUTH_GRANTS_REDIS_URL` (platform-api); Helm `oauth.services`, `oauth.grantsRedis.secretRef{name,key}`.

- [ ] **Step 1: Failing tests**

`wellknown_test.go` — add:

```go
func TestWellKnown_ScopesSupportedPerService(t *testing.T) {
	svcs := map[string]oauthsvc.Service{"demo-mcp-jwt": {Store: "echo", Broker: "http://b"}}
	h := WellKnownHandler("https://ach.example.com", "ach", svcs)
	get := func(p string) map[string]any {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		var doc map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &doc)
		return doc
	}
	if got := get("/.well-known/oauth-protected-resource/mcp/demo-mcp-jwt")["scopes_supported"]; fmt.Sprint(got) != "[ach demo-mcp-jwt]" {
		t.Fatalf("mapped service: %v", got)
	}
	if _, has := get("/.well-known/oauth-protected-resource/mcp/other")["scopes_supported"]; has {
		t.Fatal("unmapped service must not advertise scopes")
	}
	if _, has := get("/.well-known/oauth-protected-resource")["scopes_supported"]; has {
		t.Fatal("API root must not advertise scopes")
	}
	if got := get("/.well-known/oauth-authorization-server")["scopes_supported"]; fmt.Sprint(got) != "[offline_access ach demo-mcp-jwt]" {
		t.Fatalf("AS metadata: %v", got)
	}
}
```

`handlers_test.go` — find how the existing tests build `HandlerDeps` + a request with a `KeyContext` for `/mcp/{name}` (there is a helper that installs `middleware.WithKeyContext`; reuse it) and add:

```go
func TestHandlerMCP_InsufficientScope(t *testing.T) {
	deps := testHandlerDeps(t) // the existing constructor used by the precheck tests
	deps.Services = map[string]oauthsvc.Service{"svc-a": {Store: "a", Broker: "http://b"}}
	deps.BaseURL = "https://ach.example.com"
	h := HandlerMCP(deps)
	call := func(name string, kc middleware.KeyContext) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/mcp/"+name, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("name", name)
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
		ctx = middleware.WithKeyContext(ctx, &keystore.KeyInfo{KeyID: kc.KeyID, KeyType: kc.KeyType, OwnerEmail: kc.OwnerEmail, OAuth: kc.OAuth, Scopes: kc.Scopes}, false)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r.WithContext(ctx))
		return rec
	}
	oauthNoScope := middleware.KeyContext{KeyID: "pkid_1", KeyType: keys.PrefixPk, OwnerEmail: "u@x.com", OAuth: true, Scopes: []string{"ach"}}
	rec := call("svc-a", oauthNoScope)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "insufficient_scope") ||
		rec.Header().Get("WWW-Authenticate") != `Bearer error="insufficient_scope", resource_metadata="https://ach.example.com/.well-known/oauth-protected-resource/mcp/svc-a"` {
		t.Fatalf("oauth without scope: %d %q %s", rec.Code, rec.Header().Get("WWW-Authenticate"), rec.Body)
	}
	// with the scope, an unmapped service, or a pk_ bearer: the gate is silent
	// (the request proceeds to precheck — whatever the fixture answers there is not 403 insufficient_scope)
	for _, tc := range []struct {
		name string
		kc   middleware.KeyContext
	}{
		{"svc-a", middleware.KeyContext{KeyID: "pkid_1", KeyType: keys.PrefixPk, OwnerEmail: "u@x.com", OAuth: true, Scopes: []string{"ach", "svc-a"}}},
		{"svc-other", oauthNoScope},
		{"svc-a", middleware.KeyContext{KeyID: "pkid_2", KeyType: keys.PrefixPk, OwnerEmail: "u@x.com"}},
	} {
		if rec := call(tc.name, tc.kc); strings.Contains(rec.Body.String(), "insufficient_scope") {
			t.Fatalf("%s/%v must pass the scope gate: %d %s", tc.name, tc.kc.Scopes, rec.Code, rec.Body)
		}
	}
}
```

`cmd/ach/cmd/platform_api_test.go` — add:

```go
func TestPlatformAPIConfig_ServicesRequireGrantsRedis(t *testing.T) {
	t.Setenv("ACH_OAUTH_SERVICES", `{"mcp-a":{"store":"a","broker":"https://b"}}`)
	t.Setenv("ACH_OAUTH_GRANTS_REDIS_URL", "")
	// … set every other required env the existing config tests set (copy from TestValidatePlatformAPIConfig_RedisDB) …
	if _, err := parsePlatformAPIConfig(); err == nil || !strings.Contains(err.Error(), "ACH_OAUTH_GRANTS_REDIS_URL") {
		t.Fatalf("expected refuse-to-start, got %v", err)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `make test-unit-pkg PKG=./internal/forwarder/proxy/... && make test-unit-pkg PKG=./cmd/ach/...`
Expected: FAIL (signatures/fields undefined).

- [ ] **Step 3: Implement forwarder**

`wellknown.go`:

```go
func WellKnownHandler(base, audience string, services map[string]oauthsvc.Service) http.Handler {
	base = strings.TrimRight(base, "/")
	keys := oauthsvc.Keys(services)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var doc map[string]any
		switch {
		case r.URL.Path == "/.well-known/oauth-authorization-server":
			doc = jwt.ASMetadata(base, audience, keys)
		case strings.HasPrefix(r.URL.Path, wellKnownPRMSegment):
			rest := strings.TrimPrefix(r.URL.Path, wellKnownPRMSegment)
			if rest != "" && resourceRoot(rest) != rest {
				http.NotFound(w, r)
				return
			}
			doc = map[string]any{
				"resource":                 base + rest,
				"authorization_servers":    []string{base},
				"bearer_methods_supported": []string{"header"},
			}
			// A brokered MCP service names the scope a client must ask for
			// (RFC 9728 §2): the audience plus its own key.
			if m := serviceRe.FindStringSubmatch(rest); m != nil && m[1] == "mcp" {
				if _, ok := services[m[2]]; ok {
					doc["scopes_supported"] = []string{audience, m[2]}
				}
			}
		default:
			http.NotFound(w, r)
			return
		}
		// … encode unchanged …
	})
}
```

`handlers.go` — `HandlerDeps` gains:

```go
	// Services + Audience drive the OAuth scope gate on /mcp/<name>: an
	// OAuth bearer must carry scope <name> for a mapped service. Empty map →
	// no gate. pk_/ek_/raw sk- are never gated.
	Services map[string]oauthsvc.Service
```

In `handlerNamed`, after `kc, _ := middleware.KeyContextFromCtx(r.Context())` and before precheck:

```go
		// 0b. OAuth scope gate (RFC 6750 §3.1). Only OAuth bearers, only
		//     mapped MCP services: the client re-runs /authorize asking for
		//     the scope the PRM advertises.
		if kc.OAuth && audPrefix == "mcp:" && deps.Services[name].Store != "" && !hasScope(kc.Scopes, name) {
			metrics.IncRequests(routeLabel, keyTypeLabel, "insufficient_scope")
			w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", resource_metadata="`+
				strings.TrimRight(deps.BaseURL, "/")+wellKnownPRMSegment+"/mcp/"+name+`"`)
			render.Error(w, http.StatusForbidden, "insufficient_scope", "token lacks scope "+name, reqID)
			return
		}
```

plus:

```go
func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}
```

Add `insufficient_scope` to the outcome list comment in `internal/forwarder/metrics/counters.go:49` and `internal/metrics/forwarder.go`.

`server.go`: `forwarder.Deps` gains `Services map[string]oauthsvc.Service` and `OAuthAudience string`; pass `Services: deps.Services` into `HandlerDeps` and `proxy.WellKnownHandler(deps.BaseURL, deps.OAuthAudience, deps.Services)`.

- [ ] **Step 4: Implement wiring**

`cmd/ach/cmd/forwarder.go`: config field `OAuthServices map[string]oauthsvc.Service`; in parse: `if cfg.OAuthServices, err = oauthsvc.Parse(os.Getenv("ACH_OAUTH_SERVICES")); err != nil { return nil, err }`; in `forwarder.Deps{…}` add `Services: cfg.OAuthServices, OAuthAudience: cfg.OAuthAudience`.

`cmd/ach/cmd/platform_api.go`: config fields `OAuthServices map[string]oauthsvc.Service`, `OAuthGrantsRedisURL string`; in parse:

```go
	if cfg.OAuthServices, err = oauthsvc.Parse(os.Getenv("ACH_OAUTH_SERVICES")); err != nil {
		return nil, err
	}
	cfg.OAuthGrantsRedisURL = os.Getenv("ACH_OAUTH_GRANTS_REDIS_URL")
	if len(cfg.OAuthServices) > 0 && cfg.OAuthGrantsRedisURL == "" {
		return nil, fmt.Errorf("ACH_OAUTH_GRANTS_REDIS_URL required when ACH_OAUTH_SERVICES is set (the broker chain reads the grant projection)")
	}
```

In the `oauthDeps = &auth.OAuthDeps{…}` literal add `Services: cfg.OAuthServices`, and after it:

```go
		if cfg.OAuthGrantsRedisURL != "" {
			gopts, err := redis.ParseURL(cfg.OAuthGrantsRedisURL)
			if err != nil {
				return out, fmt.Errorf("ACH_OAUTH_GRANTS_REDIS_URL: %w", err)
			}
			out.grantsRedis = redis.NewClient(gopts) // closed with the other clients in out.Close()
			oauthDeps.Grants = auth.NewRedisGrants(out.grantsRedis)
		}
```

(add `grantsRedis *redis.Client` to the deps struct and close it in the existing `Close`.)

- [ ] **Step 5: Helm**

`values.yaml` under `oauth:`:

```yaml
oauth:
  audience: ach
  # services: MCP services a user token may carry as scopes. Key = the
  # /mcp/<key> path segment; store = the broker's name for it; broker = the
  # mcp-oauth AS base. Same shape as alitellm-auth authServer.services.
  # Empty → no broker chain, no scope gate (today's behaviour).
  services: {}
  #  mcp-zoho-desk-ro: { store: zoho-desk-ro, broker: https://api.ackstorm.ai/zoho-desk-ro-callback }
  # grantsRedis: the brokers' grant projection (oauth:{store}:state:{email}),
  # read-only, platform-api only. Required when services is non-empty.
  grantsRedis:
    secretRef:
      name: ""
      key: url
```

`platform-api-deployment.yaml` after `ACH_OAUTH_AUDIENCE`:

```yaml
            {{- if .Values.oauth.services }}
            - name: ACH_OAUTH_SERVICES
              value: {{ .Values.oauth.services | toJson | quote }}
            {{- end }}
            {{- if .Values.oauth.grantsRedis.secretRef.name }}
            - name: ACH_OAUTH_GRANTS_REDIS_URL
              valueFrom:
                secretKeyRef:
                  name: {{ .Values.oauth.grantsRedis.secretRef.name | quote }}
                  key: {{ .Values.oauth.grantsRedis.secretRef.key | default "url" | quote }}
            {{- end }}
```

`forwarder-deployment.yaml` after `ACH_OAUTH_AUDIENCE`: the same `ACH_OAUTH_SERVICES` block only.

- [ ] **Step 6: Verify**

Run: `./scripts/dev.sh go build ./... && make test-unit-pkg PKG=./internal/forwarder/... && make test-unit-pkg PKG=./cmd/ach/... && helm template ach deploy/helm/ach > /tmp/a.yaml && git stash -q && helm template ach deploy/helm/ach > /tmp/b.yaml; git stash pop -q; diff /tmp/a.yaml /tmp/b.yaml && echo IDENTICAL-WHEN-EMPTY; helm template ach deploy/helm/ach --set 'oauth.services.mcp-a.store=a' --set 'oauth.services.mcp-a.broker=https://b' --set oauth.grantsRedis.secretRef.name=g | grep -A1 "ACH_OAUTH_SERVICES\|ACH_OAUTH_GRANTS" `
Expected: tests `ok`; `IDENTICAL-WHEN-EMPTY`; the set render shows `ACH_OAUTH_SERVICES` on both platform-api and forwarder and `ACH_OAUTH_GRANTS_REDIS_URL` only on platform-api.

(⚠ `git stash` is banned in shared worktrees per the environment note — instead render `b.yaml` from `git show origin/main:deploy/helm/ach/…` into a temp dir: `git archive origin/main deploy/helm/ach | tar -x -C /tmp/base && helm template ach /tmp/base/deploy/helm/ach > /tmp/b.yaml`.)

- [ ] **Step 7: Commit**

```bash
git add internal/forwarder cmd/ach deploy/helm/ach internal/metrics
git commit -m "feat(forwarder): scopes_supported on brokered PRMs and the insufficient_scope gate; wire ACH_OAUTH_SERVICES"
```

---

### Task 6: e2e — `ach-mock broker` fixture + `TestOAuthFrontDoor` chain assertions

**Files:**
- Create: `test/e2e/mock/broker.go`
- Modify: `test/e2e/mock/main.go` (subcommand dispatch — find the `switch os.Args[1]` and add `"broker"`)
- Create: `test/e2e/cluster/03-test-backends/ach-mock-broker.yaml`
- Modify: `test/e2e/cluster/03-test-backends/kustomization.yaml`, `test/e2e/cluster/02-ach/ach.values.yaml`
- Modify: `test/e2e/oauth_frontdoor_test.go`
- Modify: `scripts/cluster.sh` only if the mock Deployment set is enumerated for readiness (grep `ach-mock-a2a` and mirror for `ach-mock-broker`); `Makefile` `wait-mocks` likewise.

**Interfaces:**
- Consumes: everything above deployed via `make cluster-sync`.
- Produces: Service `ach-mock-broker.ach-system.svc` (port 80 → 9090) with `/register`, `/authorize`, `/healthz`; env `BROKER_ISSUER` (default `http://ach.e2e.local:8080`), `BROKER_REDIS_ADDR` (`valkey-primary.ach-system.svc.cluster.local:6379`).

- [ ] **Step 1: The mock broker**

`test/e2e/mock/broker.go` (module deps only: go-redis and golang-jwt are already in go.mod; the Dockerfile copies `go.mod`/`go.sum` so they resolve):

```go
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

// runBroker is `ach-mock broker`: an mcp-oauth stand-in for the e2e chain.
// /register hands out a client id; /authorize verifies login_hint against
// the issuer's JWKS (found through its RFC 8414 document, like mcp-oauth),
// writes the grant projection oauth:<scope>:state:<sub> = {"granted":true}
// and bounces to redirect_uri with a code. No provider, no consent screen.
func runBroker() {
	issuer := strings.TrimRight(envOr("BROKER_ISSUER", "http://ach.e2e.local:8080"), "/")
	rdb := redis.NewClient(&redis.Options{Addr: envOr("BROKER_REDIS_ADDR", "valkey-primary.ach-system.svc.cluster.local:6379")})
	keys := &jwksCache{issuer: issuer}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.RedirectURIs) == 0 {
			http.Error(w, `{"error":"invalid_redirect_uri"}`, 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "mock-broker-client", "redirect_uris": body.RedirectURIs})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		store := q.Get("scope")
		sub, err := keys.verifyHint(r.Context(), q.Get("login_hint"), store)
		if err != nil {
			log.Printf("broker: login_hint rejected: %v", err)
			http.Error(w, "an account the provider did not name: "+err.Error(), 400)
			return
		}
		if err := rdb.Set(r.Context(), "oauth:"+store+":state:"+sub, `{"granted":true,"via":"ach-mock-broker"}`, 0).Err(); err != nil {
			http.Error(w, "projection write failed", 503)
			return
		}
		p := url.Values{"code": {"mock-broker-code"}, "state": {q.Get("state")}}
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+p.Encode(), 302)
	})
	addr := envOr("MOCK_BIND_ADDRESS", ":9090")
	log.Printf("ach-mock broker listening on %s (issuer %s)", addr, issuer)
	log.Fatal(http.ListenAndServe(addr, mux))
}

type jwksCache struct {
	issuer string
	mu     sync.Mutex
	keys   map[string]ed25519.PublicKey
	at     time.Time
}

func (c *jwksCache) verifyHint(ctx context.Context, raw, aud string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("no login_hint")
	}
	tok, err := jwtv5.Parse(raw, func(t *jwtv5.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		k, err := c.key(ctx, kid)
		if err != nil {
			return nil, err
		}
		return k, nil
	}, jwtv5.WithValidMethods([]string{"EdDSA"}), jwtv5.WithIssuer(c.issuer), jwtv5.WithAudience(aud), jwtv5.WithExpirationRequired())
	if err != nil {
		return "", err
	}
	sub, _ := tok.Claims.GetSubject()
	if sub == "" || sub != strings.ToLower(sub) {
		return "", fmt.Errorf("sub %q missing or not lowercase", sub)
	}
	return sub, nil
}

func (c *jwksCache) key(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[kid]; ok && time.Since(c.at) < time.Minute {
		return k, nil
	}
	var meta struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := getJSON(ctx, c.issuer+"/.well-known/oauth-authorization-server", &meta); err != nil || meta.JWKSURI == "" {
		return nil, fmt.Errorf("as metadata: %v", err)
	}
	var jwks struct {
		Keys []struct {
			Kid, Kty, Crv, X string
		} `json:"keys"`
	}
	if err := getJSON(ctx, meta.JWKSURI, &jwks); err != nil {
		return nil, err
	}
	c.keys = map[string]ed25519.PublicKey{}
	for _, k := range jwks.Keys {
		if k.Kty == "OKP" && k.Crv == "Ed25519" {
			if x, err := base64.RawURLEncoding.DecodeString(k.X); err == nil && len(x) == ed25519.PublicKeySize {
				c.keys[k.Kid] = ed25519.PublicKey(x)
			}
		}
	}
	c.at = time.Now()
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown kid %q", kid)
}

func getJSON(ctx context.Context, u string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", u, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
```

In `main.go`'s subcommand switch add `case "broker": runBroker()` and extend the doc comment with the fourth mode. If `main.go` already defines an `envOr`, delete the copy above.

`test/e2e/cluster/03-test-backends/ach-mock-broker.yaml` — copy `ach-mock-a2a.yaml`, replace every `mock-a2a` with `mock-broker`, `args: ["broker"]`, add env `BROKER_ISSUER=http://ach.e2e.local:8080` and `BROKER_REDIS_ADDR=valkey-primary.ach-system.svc.cluster.local:6379`, label `test.ach.ackstorm.ai/phase: "oauth"`. Add it to `kustomization.yaml` resources.

`test/e2e/cluster/03-test-backends/files/nginx.conf` — the host test runner must
walk the broker hop through the single gateway origin (`followToLoopback`
rewrites every hop to it), so expose the broker under a path:

```nginx
    location /mock-broker/ {
        proxy_pass http://ach-mock-broker.ach-system.svc/;
    }
```

`02-ach/ach.values.yaml` — add:

```yaml
oauth:
  services:
    demo-mcp-jwt: { store: echo, broker: http://ach.e2e.local:8080/mock-broker }
extraEnv:
  # …existing entries…
  - name: ACH_OAUTH_GRANTS_REDIS_URL
    value: "redis://valkey-primary.ach-system.svc.cluster.local:6379/0"   # e2e: the brokers' projection = ACH's own valkey
```

(the e2e valkey has no password; the mock broker writes `oauth:echo:state:<sub>` there.)

- [ ] **Step 2: Build the mock, verify readiness wiring**

Run: `make build-image-mock && grep -n "ach-mock-a2a" scripts/cluster.sh Makefile`
Expected: image builds; wherever `ach-mock-a2a` is waited on, add `ach-mock-broker` alongside (same `kubectl rollout status` form).

- [ ] **Step 3: Extend `TestOAuthFrontDoor`**

In `test/e2e/oauth_frontdoor_test.go`:

Step 1 (discovery) — after the existing PRM asserts add:

```go
	code, _, jwtPRM := getJSON(t, "/.well-known/oauth-protected-resource/mcp/demo-mcp-jwt", nil)
	if code != 200 || fmt.Sprint(jwtPRM["scopes_supported"]) != "[ach demo-mcp-jwt]" {
		t.Fatalf("brokered PRM must advertise its scope: %d %v", code, jwtPRM)
	}
```

Step 4 (authorize) — add `"scope": {"ach demo-mcp-jwt"}` to `q`. `followToLoopback` already carries cookies and rewrites every hop's authority to the gateway origin, so the broker hop (`/mock-broker/authorize` via nginx) needs no test change. Raise its hop limit from 12 to 16 (one broker adds two hops).

Step 5 (token) — after the existing checks:

```go
	if tok["scope"] != "ach demo-mcp-jwt" {
		t.Fatalf("token scope: %v", tok["scope"])
	}
```

Step 6b — add:

```go
	// 6c. The scope gate: the mapped MCP service passes with the scope…
	callBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"demo-mcp-jwt.echo","arguments":{"text":"via-oauth-chain"}}}`
	if resp := postMCPViaForwarder(t, base+"/mcp/demo-mcp-jwt/", access, callBody); !strings.Contains(resp, "via-oauth-chain") {
		t.Fatalf("mcp via oauth: %s", resp)
	}
	// …and a token minted WITHOUT the scope is refused with the RFC 6750 pointer.
	plainCode := followToLoopback(t, noRedirect, base, base+"/platform/oauth/authorize?"+withoutScope(q).Encode())
	_, plain := tokenPost(t, url.Values{"grant_type": {"authorization_code"}, "code": {plainCode}, "client_id": {reg.ClientID},
		"redirect_uri": {"http://127.0.0.1:1/cb"}, "code_verifier": {verifier}})
	req, _ := http.NewRequest(http.MethodPost, base+"/mcp/demo-mcp-jwt/", strings.NewReader(callBody))
	req.Header.Set("x-ach-key", plain["access_token"].(string))
	req.Header.Set("Content-Type", "application/json")
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="insufficient_scope"`) {
		t.Fatalf("no-scope token on brokered mcp: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
```

with helper:

```go
func withoutScope(q url.Values) url.Values {
	c := url.Values{}
	for k, v := range q {
		if k != "scope" {
			c[k] = v
		}
	}
	return c
}
```

(`postMCPViaForwarder` accepts any `x-ach-key` value — the JWT works there since Authn reads `x-ach-key` for JWS too. `q` must be built with a fresh `state` for the second ceremony — `state=s1` again is fine, the pending is per request.)

Also: the second ceremony reuses `verifier`/`code_challenge` — allowed (PKCE is per request, the AS doesn't dedupe challenges).

- [ ] **Step 4: Run on the kept cluster**

Run: `make cluster-sync && make e2e-focus RUN=TestOAuthFrontDoor`
Expected: `--- PASS: TestOAuthFrontDoor`. If the broker hop 400s, `./scripts/dev.sh kubectl -n ach-system logs deploy/ach-mock-broker` shows the hint rejection reason.

- [ ] **Step 5: Full suite**

Run: `make e2e-run`
Expected: `>>> E2E RESULT: PASS`.

- [ ] **Step 6: Commit**

```bash
git add test/e2e scripts/cluster.sh Makefile
git commit -m "test(e2e): mock broker fixture; OAuth front door walks the chain, token carries scope, forwarder gates on it"
```

---

### Task 7: Docs (same change) + final gates

**Files:**
- Modify: `AGENTS.md` (CLAUDE.md symlink) platform-api + forwarder rows in the service table
- Modify: `docs/developer-guide/jwt-forwarder.md` (new §: scope gate + PRM `scopes_supported`)
- Modify: `docs/user-guide/signing-in-from-tools.md` (the extra browser hop; `insufficient_scope`)
- Modify: `references/troubleshooting.md` (two entries)
- Modify: `deploy/helm/ach/values.yaml` comments already done in Task 5

- [ ] **Step 1: Service table**

platform-api row, append: `+ **broker chain** (`ACH_OAUTH_SERVICES` map → after the Dex leg, one `<broker>/authorize` hop per requested-but-ungranted MCP service carrying an EdDSA `login_hint` (`aud` = store, 600 s), `/platform/oauth/broker-callback` never redeems the broker code; `scope` on the token = audience + services granted in the brokers' Redis projection `oauth:{store}:state:{email}` (`ACH_OAUTH_GRANTS_REDIS_URL`, read-only, required with the map))`.

forwarder row, append: `; brokered `/mcp/<svc>` PRMs advertise `scopes_supported: [ach, <svc>]` and an OAuth bearer without that scope gets `403 insufficient_scope` + the PRM pointer (pk_/ek_ never gated)`.

- [ ] **Step 2: jwt-forwarder.md**

Add a section "§1.5 OAuth scope gate" stating: which bearers are gated (OAuth only), the map, the 403 shape (`WWW-Authenticate: Bearer error="insufficient_scope", resource_metadata=…`), that the client's remedy is re-running `/authorize` with the scope from the PRM, and that `/a2a` and unmapped services are never gated.

- [ ] **Step 3: signing-in-from-tools.md**

Under "MCP servers: the tool signs in itself" add one paragraph: for a brokered service the browser makes one extra stop per service at that service's broker (and the provider behind it) before returning to the tool; that is where the provider consent lives. Under "When it goes wrong" add: **`403 insufficient_scope` on an MCP** — the token was minted before you consented to that service (or the consent was revoked at the provider); the tool re-runs its login for that server (`claude mcp login <name>` etc.).

- [ ] **Step 4: troubleshooting.md**

Two `### ❌ … ✅ …` entries: (a) `insufficient_scope` on `/mcp/<svc>` — check `ACH_OAUTH_SERVICES` on the forwarder contains `<svc>`, the token's `scope` claim (`ach-cli token | cut -d. -f2 | base64 -d`), and the projection key `oauth:<store>:state:<email>` in the grants Redis; (b) chain silently skips a service ("oauth: broker unreachable, skipping scope" in platform-api logs) — the broker's `/register` failed; the token lacks that scope; fix the broker URL / network policy, the user re-logs in.

- [ ] **Step 5: Gates**

Run: `make test-unit && make qa-lint && make e2e-full`
Expected: unit 0 FAIL, lint clean, `>>> E2E RESULT: PASS`.

- [ ] **Step 6: Commit + push + PR**

```bash
git add AGENTS.md docs references
git commit -m "docs: OAuth broker chain — service table, scope gate, user guide, troubleshooting"
git push -u origin feat/oauth-broker-chain
gh pr create --base main --title "feat(oauth): broker chain with login_hint; scope on tokens; forwarder scope gate" --body "<summary + test plan>"
```

Then: CI green → merge → `make release-cut VERSION=0.10.0` → tell the mcp team "ready" (they add `https://ach.<domain>` to `AUTH_BROKER_HINT_ISSUER`, delete the 23 HTTPRoutes) → gitops sets `oauth.services` + `oauth.grantsRedis.secretRef` on ach.
