---
status: testing
phase: 04-hub-forwarder-jwt-trust-path
source:
  - 04-01-SUMMARY.md
  - 04-02-SUMMARY.md
  - 04-03-SUMMARY.md
  - 04-04-SUMMARY.md
  - 04-05-SUMMARY.md
  - 04-06-SUMMARY.md
  - 04-07-SUMMARY.md
  - 04-08-SUMMARY.md
  - 04-09-SUMMARY.md
started: 2026-05-28T09:15:59Z
updated: 2026-05-28T09:15:59Z
---

## Current Test

number: 1
name: Cold Start Smoke Test
expected: |
  Run `make cluster-down && make cluster-up` (or equivalent fresh-cluster
  bring-up). After hydration, the ach-forwarder Deployment in namespace
  ach-system reaches Ready=True within ~120s. No CrashLoopBackOff. The
  `ach-jwt-signing-keys` Secret was auto-seeded by scripts/cluster.sh
  hydrate_fixtures (kid=dev-<timestamp>, seed=32 random bytes).

  Verify:
  - `kubectl -n ach-system get deploy/ach-forwarder` → READY 1/1
  - `kubectl -n ach-system get secret/ach-jwt-signing-keys` → exists with
    data keys `current.kid` and `current.seed`
  - `kubectl -n ach-system logs deploy/ach-forwarder -c forwarder | grep
    -i 'load.*jwt\|signer.*loaded'` → shows successful load, no fatal
awaiting: user response

## Tests

### 1. Cold Start Smoke Test
expected: |
  Fresh `make cluster-up` brings ach-forwarder Deployment Ready=1/1 within
  120s. ach-jwt-signing-keys Secret pre-seeded. Forwarder logs show signing
  keys loaded without error. No CrashLoopBackOff on any pod.
result: [pending]

### 2. Refuse-to-Start on Missing JWT Signing Keys
expected: |
  Delete the Secret (`kubectl -n ach-system delete secret ach-jwt-signing-keys`),
  then restart the forwarder (`kubectl -n ach-system rollout restart
  deploy/ach-forwarder`). The new Pod enters CrashLoopBackOff. Logs show:
  `fatal: load JWT signing keys: secret "ach-jwt-signing-keys" not found in
  namespace "ach-system"` (or equivalent). After re-seeding the Secret and
  another rollout, the Pod becomes Ready again.
result: [pending]

### 3. Refuse-to-Start on Non-HTTPS ACH_BASE_URL (FWD-10)
expected: |
  Override `forwarder.achBaseURL` in Helm values to `http://...` (plain
  HTTP) and `helm upgrade`. The new forwarder Pod fails to start; logs
  show a refuse-to-start error mentioning ACH_BASE_URL must be HTTPS.
  Reverting to `https://...` recovers Ready=True.
result: [pending]

### 4. JWKS Endpoint Serves Ed25519 OKP Key
expected: |
  `curl -sk https://ach.local.test/.well-known/jwks.json` (or
  port-forwarded equivalent) returns:
  - HTTP 200
  - Content-Type: application/jwk-set+json
  - Cache-Control: public, max-age=3600
  - Body: {"keys":[{"kty":"OKP","crv":"Ed25519","alg":"EdDSA","use":"sig",
    "kid":"<dev-timestamp>","x":"<base64url-32-bytes>"}]}
result: [pending]

### 5. JWKS Returns Non-Null Empty Array When Signer Not Loaded
expected: |
  In a Pod startup window where signer is not yet loaded, JWKS handler
  responds with `{"keys":[]}` — empty ARRAY, never `null`. Verified by
  inspecting jwks_test.go invariants (JWKS1-JWKS4) and a live curl during
  rolling restart.
result: [pending]

### 6. /readyz Gates on Signer Loaded + Cache Sync
expected: |
  `curl -sk https://ach.local.test/readyz` (health port, separate from
  HTTPS request port per D-03 dual-port Runnable). Returns 200 OK only
  after BOTH the signer is loaded AND the controller-runtime manager
  cache has synced. During startup the endpoint returns 503; once Ready,
  always 200.
result: [pending]

### 7. /v1 pk_ Authorization Pass-Through
expected: |
  `curl -sk -H "Authorization: Bearer pk_<valid_personal_key>"
  https://ach.local.test/v1/models` reaches the LiteLLM backend. Response
  is the LiteLLM model list. Authorization header passed through; no JWT
  swap. Forwarder logs show route=v1, key_kind=pk, status=200.
result: [pending]

### 8. /v1 ek_ Environment-Tag Injection (FWD-06)
expected: |
  POST `/v1/chat/completions` with Authorization: Bearer ek_<env_key> and
  a chat-completion JSON body lacking a `metadata.tags` field. The
  forwarder injects `metadata.tags=["env:<environment-name>"]` BEFORE
  proxying to LiteLLM. LiteLLM receives the augmented body; existing tags
  preserved. Confirm via LiteLLM access log or echo backend.
result: [pending]

### 9. /mcp BIP JWT Mint (FWD-07/FWD-08)
expected: |
  Apply a BackendIdentityPolicy targeting an MCP backend. `curl` to
  `/mcp/<server>/...` results in the forwarder minting an Ed25519 JWT
  (signed by the current `kid` from ach-jwt-signing-keys), JWT carried as
  Authorization: Bearer to the upstream MCP server. Claims: iss=ACH_BASE_URL,
  sub=<user>, aud=<BIP.spec.audience>, exp=iat+120s, no jti.
result: [pending]

### 10. /mcp Precheck Failure → 403
expected: |
  An SSO user not authorized on the target MCP server's AccessGroup makes
  a request to `/mcp/<server>/...`. Forwarder returns HTTP 403 (NOT
  upstream's response — short-circuited at precheck). No JWT minted. Log
  shows precheck.ErrUnauthorized.
result: [pending]

### 11. /a2a Audience Claim
expected: |
  Request to `/a2a/<agent>/...` mints a JWT with `aud` set to the A2A
  agent's BackendIdentityPolicy audience field. Distinct from /mcp's
  audience. Verified by base64url-decoding the JWT payload off the
  outbound Authorization header (sniffed via an echo backend or
  `kubectl logs` on a debug A2A target).
result: [pending]

### 12. BackendIdentityPolicy Duplicate-Target Alpha-LAST Resolution (FWD-05)
expected: |
  Apply TWO BIPs with the same `spec.target` (e.g., names `aaa-bip` and
  `zzz-bip`). On a request to that target, the forwarder uses the BIP
  whose name sorts alphabetically LAST (`zzz-bip`). No operator-side
  DuplicateTarget condition is written (read-side-only contract per
  OP-16). Verified via JWT `aud` or signing identity in the forwarded
  request.
result: [pending]

### 13. Helm Renders Namespace-Scoped Role + Secret resourceNames (D-22)
expected: |
  `helm template deploy/helm/ach/ | grep -A 20 'kind: Role$\|kind:
  ClusterRole'` shows the forwarder's RBAC is a Role (NAMESPACED, not
  ClusterRole) and the Secret rule carries `resourceNames:
  - ach-jwt-signing-keys` — no blanket secret access.
result: [pending]

### 14. JWT Key Rotation Runbook End-to-End
expected: |
  Follow `docs/runbooks/jwt-key-rotation.md` publish-overlap-revoke flow:
  1. `kubectl patch secret ach-jwt-signing-keys` to add a NEW kid+seed
     alongside the existing pair
  2. Wait ≥24h overlap (or simulate by checking JWKS returns BOTH keys)
  3. Promote new key (swap current.kid/current.seed)
  4. Verify forwarder picks up new key via informer reload — JWKS now
     returns only the new kid; outbound JWTs signed with new kid
  No forwarder restart required between steps 1-3. v1alpha1 = manual
  only; no cron yet.
result: [pending]

### 15. BIP Indexer Registered Before First Informer Watch (D-09)
expected: |
  `kubectl -n ach-system logs deploy/ach-forwarder -c forwarder` at
  startup shows `bip.RegisterIndex` invocation completing BEFORE any BIP
  list/watch begins (no ordering violation). Verified by log line
  ordering or by `make e2e-focus FOCUS=PHASE4_INVARIANTS_ALPHA_LAST`
  passing with `ACH_E2E_PHASE4=1`.
result: [pending]

## Summary

total: 15
passed: 0
issues: 0
pending: 15
skipped: 0
blocked: 0

## Gaps

[none yet]
