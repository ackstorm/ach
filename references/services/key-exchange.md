# Key exchange and KeyGrant

A pod trades its ServiceAccount token for an Environment-scoped ACH JWT. Read before changing `POST /platform/keys/exchange`, the exchange resolver or the `KeyGrant` reconciler. User-facing guide: `docs/integrate/exchange-a-serviceaccount-token.md`.

Key exchange (`POST /platform/keys/exchange`, outside the `pk_`/`ek_` Authn
subtree): `Authorization: Bearer <pod-bound ServiceAccount token>` whose
audience is `ACH_BASE_URL` without a trailing slash, body
`{"identity","environment"}`. Guards before any API-server call: a compact
JWS whose unverified `aud` names that audience (else 401 `invalid_token`),
a per-client (first `X-Forwarded-For` hop) rate limit (429 `rate_limited`),
the token's signature, issuer and `exp` verified locally against the
cluster's ServiceAccount JWKS (`/openid/v1/jwks`, issuer from
`/.well-known/openid-configuration`; RS256/ES256 only; cached 10 min, an
unknown `kid` refetches at most once a minute, a failed refetch serves the
cache; 401 `invalid_token`, or 503 `not_ready` with no JWKS at all), and only
then the global rate limit (429) — so forged tokens neither drain the global
budget nor reach TokenReview. Then, first failure answers: TokenReview (authenticated,
audience, `system:serviceaccount:`, pod-name + pod-uid extras; 401
`invalid_token`), ONE `KeyGrant` naming identity + Environment + `<ns>/<sa>`
together (403 `not_granted` for every miss; the audit names which), the
Environment exists and is synced (404/503), the identity's teams ∩
`authorizedTeams` (403 `unauthorized_team`), the identity is not in
`idp_refusals` (403 `idp_refused`: no JWT, nothing minted). Success: the tuple's ONE stable
`ek_` (`issued_via='exchange'`, `requester`, name `exchange:<ns>/<sa>`,
minted on first use by the create path's `mint`; the partial unique index
`environment_keys_exchange_tuple_uniq` makes racing first exchanges converge;
it never counts against `max_keys`; a suspended one is 409 `key_suspended`;
its `key:<id>` budget is the lowest covering `spec.budget`) and a JWT
`iss=ACH_BASE_URL aud=ach-ek sub=<ekid_> jti`, lifetime
`platformApi.keyExchange.tokenTTL` → `ACH_KEY_EXCHANGE_TOKEN_TTL` (15m, 1m
to 1h), answered as `{token, expires_in, key_id}`. The raw `ek_` never leaves
ACH. `keystore.NewExchangeResolver` (forwarder, content-service,
platform-api; wraps the OAuth resolver inside the Redis cache) accepts that JWT
wherever an `ek_` goes (`x-ach-key` or `Authorization`) only while a grant
still allows its tuple. One `platform.ek.exchange` audit event per request
from the body check onwards, never a token; requests the earlier guards
reject, and a global-limit 429 after a valid signature, are counted in
`ach_key_exchange_rejected_total{reason}` and not audited. Only RS256/ES256
ServiceAccount signing keys are supported.
The operator projects `KeyGrant` CRs into `key_grants` (no NOTIFY: readers query it per resolve) and, after every grant change and on the 5-minute resync, revokes (LiteLLM first) each exchange key no grant allows and writes the lowest covering `spec.budget` onto every other one's `key:<id>` tag; dropping `spec.budget` keeps the last ceiling. Each sweep also deletes `key_grants` rows whose `KeyGrant` no longer exists; deleting a grant revokes only the keys it covered. `Ready=False` reasons: `IdentityUnknown` (LiteLLM `/user/info` has no such user: the identity must log in to ACH once), `EnvironmentMissing`. Writing a `KeyGrant` is acting as its identity: the chart ships ClusterRole `<release>-keygrant-editor` bound to nobody and aggregates no ACH role into `admin`/`edit` (`make helm-render-check` check 8; e2e `kubectl auth can-i`).
