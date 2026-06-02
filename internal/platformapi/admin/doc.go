// SPDX-License-Identifier: Apache-2.0

// Package admin implements the ACH Platform API admin endpoint group
// mounted under /platform/admin/ (Hub §15.5 + §18). Three endpoints:
//
//   - POST /keys/revoke
//     Body: {"key_id":"pkid_..."} or {"key_id":"ekid_..."}
//     Revokes an arbitrary key. The pk_ branch is DB-first (KEY-07 / D-14):
//     the Postgres status flip IS the visible revocation barrier; LiteLLM
//     RevokeKey is a best-effort downstream side effect. (No revoke-time
//     Redis DEL — see "Cache invalidation discipline" below.)
//     The ek_ branch is LiteLLM-first (KEY-08 / D-15): LiteLLM is the load
//     bearing barrier so its ack is required before the DB flip.
//
//   - POST /users/{email}/revoke-keys
//     URL-decodes {email} verbatim (no normalization per Hub §16 DB-05),
//     iterates every active pk_ and ek_ row owned by that email, and
//     runs the appropriate revocation sequence per row.
//
//   - POST /refresh
//     Body: {"kind":"plugin|prompt|artifact|pluginmarketplace","name":"..."}
//     Patches `ach.ackstorm.ai/force-refresh: <RFC3339-now>` onto the
//     target CR. This is Platform API's ONLY write surface to ACH CRDs
//     (MULTI-02 carve-out from Phase 1 plan 01-09 RBAC: only the four
//     external-ref kinds, only the `patch` verb).
//
// # Admin guard contract
//
// AdminOnly middleware runs BEFORE any other validation per Hub §15.5 +
// §18 + API-12. Two rejections:
//
//   - keyCtx.KeyType != keys.PrefixPk → 401 invalid_key_type
//     (audit OutcomeInvalidKeyType, ek_ never admits to admin)
//   - keyCtx.OwnerEmail not in allowlist → 403 not_admin
//     (audit OutcomeNotAdmin)
//
// The allowlist is loaded ONCE at process start via LoadAllowlist; per
// Hub AC18 + AC24 + D-23 ConfigMap edits require Platform API restart.
// A missing or empty allowlist results in zero admins — every caller
// hits 403 not_admin uniformly (no implicit-root fallback).
//
// # Audit emission discipline
//
// Per D-19 audit emission lives in handlers, NOT middleware. AdminOnly
// is the exception — it emits OutcomeInvalidKeyType / OutcomeNotAdmin on
// the rejection paths so the actor + request_id are still recorded when
// the inner handler never runs. ActionAdminKeysRevoke is used as a
// generic admin marker on rejection; the inner handler emits the
// appropriate per-key-type Action* constant (ActionPkRevoke,
// ActionEkRevoke, ActionAdminRefresh, ActionAdminUsersRevokeKeys) on
// success and on per-row failure.
//
// # Cache invalidation discipline
//
// Per the keystore cache key shape ("ach:key:" + hex(HMAC-SHA-256(pepper,
// plaintext))), the canonical revoke-time cache invalidation requires
// the credential_hash. Plan 03-03 deliberately omits credential_hash
// from PkKeyInfo / EkKeyInfo per Hub §16.1 ("plaintext NEVER persisted"
// and credential_hash NEVER flows into audit / logs). Within the
// internal/platformapi/admin/ scope boundary the handlers MUST NOT
// extend internal/db's row shape, so the admin endpoints cannot
// construct the real cache key to DEL it. The keystore-resolver's cache
// entries are therefore reclaimed by the 60s TTL ceiling + the
// orphan-cleanup loop's eventual-consistency mechanism per WARN-04 — the
// Postgres flip is the visible revocation barrier. (A best-effort DEL
// under a synthetic "ach:revoke:keyid:" marker namespace was removed as a
// guaranteed-miss no-op; nothing SET or read that key.)
package admin
