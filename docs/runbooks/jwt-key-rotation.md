# JWT Signing Key Rotation (Ed25519)

The ACH Forwarder signs ACH JWTs (used on `/mcp/{name}` and `/a2a/{name}`
when a matching `BackendIdentityPolicy` opts in via `forwardIdentityJWT:
true`) with an Ed25519 keypair stored in a single Kubernetes Secret
`ach-jwt-signing-keys` in the deployment namespace. The Secret holds two
slots: `current` (always populated) and `next` (optional — populated only
during a rotation window).

The Forwarder watches the Secret via informer and atomically swaps the
in-memory signer when the Secret updates. The `/.well-known/jwks.json`
endpoint publishes both slots whenever both are populated, allowing
backends to fetch the new public key during the overlap window before
the Forwarder cuts over.

Backends cache JWKS for at most `Cache-Control: public, max-age=3600`
(1 hour). The rotation overlap **MUST exceed** the longest backend
JWKS cache TTL by a comfortable margin: this runbook prescribes
**≥24 hours** of overlap, which gives ≥24× safety over the 1-hour cache.

## Secret data-key layout

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ach-jwt-signing-keys
  namespace: <deployment-namespace>
type: Opaque
data:
  current.kid:  <base64 of "ach-jwt-<26-char-ulid>">
  current.seed: <base64 of 32 raw bytes>
  next.kid:     <base64 of "ach-jwt-<26-char-ulid>">     # OPTIONAL — only during rotation
  next.seed:    <base64 of 32 raw bytes>                  # OPTIONAL — only during rotation
```

## Rotation procedure

### 1. Generate a new keypair

Use any tool that produces a 32-byte Ed25519 seed and an opaque `kid`.
The Go reference snippet:

```go
package main

import (
    "crypto/ed25519"
    "crypto/rand"
    "encoding/base64"
    "fmt"

    "github.com/oklog/ulid/v2"
)

func main() {
    seed := make([]byte, 32)
    if _, err := rand.Read(seed); err != nil {
        panic(err)
    }
    kid := "ach-jwt-" + ulid.Make().String()
    // For kubectl patch, base64-encode both:
    fmt.Println("next.kid (b64):  ", base64.StdEncoding.EncodeToString([]byte(kid)))
    fmt.Println("next.seed (b64): ", base64.StdEncoding.EncodeToString(seed))
    // Sanity:
    pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
    fmt.Println("public (32B b64url-no-pad): ", base64.RawURLEncoding.EncodeToString(pub))
}
```

Run with `go run ./cmd/keygen-stub.go` (or as an ad-hoc playground —
the operator team's choice).

### 2. Patch the Secret to populate `next`

```bash
NEXT_KID_B64="..."   # from step 1
NEXT_SEED_B64="..."  # from step 1
kubectl -n <deployment-namespace> patch secret ach-jwt-signing-keys --type=json \
  -p="[{\"op\":\"replace\",\"path\":\"/data/next.kid\",\"value\":\"$NEXT_KID_B64\"},
       {\"op\":\"replace\",\"path\":\"/data/next.seed\",\"value\":\"$NEXT_SEED_B64\"}]"
```

### 3. Verify the Forwarder reloaded

The Forwarder logs `jwt signing keys reloaded` on every informer event.
Verify JWKS now publishes both slots:

```bash
curl -s "$ACH_BASE_URL/.well-known/jwks.json" | jq '.keys | length'
# expect: 2
curl -s "$ACH_BASE_URL/.well-known/jwks.json" | jq -r '.keys[].kid'
# expect: 2 distinct kids
```

### 4. Wait ≥24 hours

Backends fetch JWKS at most once per hour. Twenty-four hours of overlap
guarantees every backend has cached BOTH public keys before step 5
flips signing material. **Do not skip this wait** — backends still
verifying with the old `current.kid` will reject any JWT signed with
the new `next.kid`.

### 5. Promote `next` → `current`

```bash
NEXT_KID_B64="..."    # same as step 1
NEXT_SEED_B64="..."   # same as step 1
kubectl -n <deployment-namespace> patch secret ach-jwt-signing-keys --type=json \
  -p="[{\"op\":\"replace\",\"path\":\"/data/current.kid\",\"value\":\"$NEXT_KID_B64\"},
       {\"op\":\"replace\",\"path\":\"/data/current.seed\",\"value\":\"$NEXT_SEED_B64\"},
       {\"op\":\"replace\",\"path\":\"/data/next.kid\",\"value\":\"\"},
       {\"op\":\"replace\",\"path\":\"/data/next.seed\",\"value\":\"\"}]"
```

Setting `next.kid` to the empty base64 string `""` clears the slot
(the Forwarder's SecretLoader treats empty `next.kid` as "no next slot").

### 6. Verify the cut-over

```bash
curl -s "$ACH_BASE_URL/.well-known/jwks.json" | jq '.keys | length'
# expect: 1
curl -s "$ACH_BASE_URL/.well-known/jwks.json" | jq -r '.keys[0].kid'
# expect: the NEW kid (was previously next; now current)
```

JWTs signed after this point use the new `current.kid`. Backends that
cached the old key for up to 1 hour (the JWKS `max-age=3600`) continue
verifying with the OLD public key during their cache window; they pick
up the new public key on next JWKS refresh and accept JWTs signed with
the new key immediately.

## Emergency revocation (compromised key)

If `current.seed` is compromised, the publish-overlap-revoke procedure
above takes ≥24 hours and is **inappropriate for compromise response**.
Instead:

1. Generate a fresh keypair (step 1).
2. Replace `current` directly in the Secret (no `next` slot involved).
3. Accept up to 1 hour of backend JWKS cache-stale window during which
   backends still verify with the compromised key. There is no way to
   accelerate JWKS cache eviction in v1alpha1 (per Hub §9.2 `Cache-Control`).
4. File an incident report and consider rotating the LiteLLM shared key
   in the same maintenance window if the same compromise scope applies.

Automated rotation (cron + double-flip) is on the v1beta1 backlog
(Hub §20). In v1alpha1, all rotation is manual `kubectl patch`.

## References

- Hub §9.1 — JWT shape (EdDSA, kid, claims).
- Hub §9.2 — JWKS publication contract (Cache-Control, JWK fields).
- Hub §20 — v1beta1 backlog (automated rotation, dual-key window).
- TODO.md §6 — BackendIdentityPolicy duplicate-target design decision.
- `internal/forwarder/jwt/secret.go` — SecretLoader.LoadOnce + Reload semantics.
- `internal/forwarder/jwt/signer.go` — Sign always uses `current`; JWKS publishes both slots.
