# Writing an MCP backend that validates ACH JWTs

ACH's Forwarder mints a short-lived Ed25519 JWT on `/mcp/<name>` when
a `BackendIdentityPolicy` (BIP) with `spec.forwardIdentityJWT=true`
targets the named MCP server. This page describes how a backend
operator implements the verifying side.

## The contract

| Aspect | Value | Reference |
|---|---|---|
| Signing algorithm | EdDSA (Ed25519) | FWD-07 |
| Header `kid` | Stable id of the signing slot | FWD-08 |
| `iss` | Forwarder `ACH_BASE_URL` (HTTPS-only) | FWD-10 |
| `sub` | `<namespace>/<owner-email>` | Hub §9.1 |
| `aud` | `mcp:<bare-name>` on `/mcp/<bare-name>` | Hub §9.1 |
| `exp - iat` | 120 seconds | FWD-07 |
| `jti` | Not emitted | Hub §9.1 + §20 |
| JWKS endpoint | `<iss>/.well-known/jwks.json` | Hub §9.2 |
| JWKS `Content-Type` | `application/jwk-set+json` | RFC 7517 §8.5.1 |
| JWKS `Cache-Control` | `public, max-age=3600` | Hub §9.2 |
| JWK shape | `kty=OKP, crv=Ed25519, alg=EdDSA, use=sig` | RFC 8037 |

## Recommended verification posture

1. **Pin the algorithm.** Reject anything other than `alg=EdDSA` on
   the JWS header. Never accept `alg=none`. Never trust the token's
   own `alg` value to pick a verifier — pin it before parsing.
2. **Resolve `kid` against a fresh-enough JWKS.** Cache the JWK Set
   but refresh on `kid` miss; the Forwarder rotates with a publish-
   overlap-revoke flow (≥24h overlap), so a backend that holds a stale
   view of the JWKS for up to 1h is correct by design.
3. **Validate `iss`, `aud`, `exp`.** `iss` is fixed per Forwarder
   install. `aud` is fixed per route (a backend that serves multiple
   `<name>`s accepts a small allowlist).
4. **Do NOT trust `sub` as identity** unless your security model
   matches ACH's. `sub` carries `<namespace>/<owner-email>` — useful
   for audit, NOT authorization.

## Reference implementation

A runnable, stdlib-only Go reference lives at
[`test/e2e/mcp-echo/`](../../test/e2e/mcp-echo/). The verifier is
in `test/e2e/mcp-echo/jwt/verify.go`; the JWKS cache is in
`test/e2e/mcp-echo/jwt/jwks.go`. Both files are ~150 lines and ship
with their unit tests next to them — copy-and-adapt rather than
import-as-library.

## Common pitfalls

- **`kid` missing from header** — the Forwarder always emits one;
  a missing `kid` means you parsed a JWT minted by someone else.
- **Backend hot-loop on JWKS endpoint** — refresh on miss, not on
  every request. The Forwarder's `Cache-Control: public, max-age=3600`
  is your hint.
- **Accepting `alg=none`** — `jwt.Parse(...)` defaults vary by
  library; pin the algorithm explicitly.
- **LiteLLM strips `Authorization` by default** — when ACH proxies
  through LiteLLM's MCP gateway (the default `/mcp/<name>` route),
  the gateway drops the caller's `Authorization` header before
  calling the backend. Register the backend with
  `extra_headers: ["authorization"]` so LiteLLM propagates the
  forwarder-minted JWT through to your MCP server. Without this the
  backend sees no token and 401s every request, and LiteLLM
  surfaces `tools=[]` even on healthy servers. ACH's
  `scripts/cluster.sh hydrate_fixtures` registers the e2e
  `demo-mcp-echo` server with this opt-in already in place.
