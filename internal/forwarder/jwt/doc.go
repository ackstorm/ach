// SPDX-License-Identifier: Apache-2.0

// Package jwt implements the Forwarder's JWT trust-path primitives per
// FWD-07 (Ed25519 JWT signing), FWD-08 (JWKS publication at
// /.well-known/jwks.json), and FWD-09 (manual rotation via current+next
// slots in a single K8s Secret). The shape of the JWT, the JWK, and the
// rotation overlap derive directly from Hub spec §9.1 (JWT claims +
// header) and §9.2 (JWKS endpoint cache-control + slot publication).
//
// Three concerns, one package:
//
//   - Signer / Ed25519Signer (signer.go) — the signing primitive. Constructed
//     empty via NewEd25519Signer; populated by the SecretLoader. Uses
//     atomic.Pointer[signerSlot] so the Sign hot path is lock-free
//     (sub-ns Load), and rotation events (informer-driven Secret updates)
//     publish a fresh slot via atomic.Pointer.Store. Sign synthesizes
//     iat=now, exp=iat+120 per FWD-07 and emits NO jti claim — accepted
//     v1alpha1 threat-model decision per Hub §9.1 and §20.
//
//   - SecretLoader (secret.go) — the loader/reloader bridge between the
//     ach-jwt-signing-keys Secret (D-10 data-key layout) and the in-memory
//     Ed25519Signer slots. LoadOnce is the startup path: surfaces error to
//     the cobra RunE so the forwarder REFUSES TO START on missing or
//     malformed current.kid / current.seed. Reload is the informer event
//     path: on a malformed update it LOGS and KEEPS the prior valid slot
//     (refuse-to-update, not refuse-to-die) so traffic continues to flow
//     while operators rectify the Secret.
//
//   - JWKSHandler (jwks.go) — the public read endpoint. Renders the
//     signer's current + optional next slot as a JWK Set with
//     Content-Type: application/jwk-set+json and
//     Cache-Control: public, max-age=3600 (Hub §9.2). The 1-hour cache
//     TTL is what forces the ≥24h rotation overlap of FWD-09 (backends
//     can hold a stale JWKS view for up to an hour after a rotation
//     step, so ≥24h overlap is ≥24× the cache TTL).
//
// Exported surface:
//
//   - Signer (interface) — the contract every consumer (proxy per-route
//     handler, JWKSHandler) types its dependency as.
//   - Ed25519Signer (struct) + NewEd25519Signer — the production
//     implementation.
//   - SecretLoader (struct) + NewSecretLoader — the K8s Secret bridge.
//   - JWKSHandler (func) — http.HandlerFunc factory for the JWK Set route.
//   - Claims (struct), JWK (struct) — the wire-shape types.
//   - ErrEmptyKid, ErrEmptySeed, ErrNoCurrentSlot — refuse-to-construct /
//     refuse-to-sign sentinels.
//   - SecretName + DataKey* constants — the K8s Secret coordinates.
package jwt
