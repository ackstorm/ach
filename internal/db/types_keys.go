// SPDX-License-Identifier: Apache-2.0

// Typed structs returned by the Phase 3 key-resolution SQL helpers.
//
// PkKeyInfo is the row PkCheckAndExtend (Hub §7.1) returns from a successful
// sliding-window check-and-extend; EkKeyInfo is the row EkResolve (Hub §8.1)
// returns from a successful resolve+debounce. Both are also the row shapes
// of GetPersonalKey / GetEnvironmentKey and the slice element types of the
// paginated list helpers.
//
// Field types match the column definitions from db/migrations/000001_init.up.sql
// (Phase 1) plus db/migrations/000002_phase2.up.sql (litellm_user_id) plus
// db/migrations/000003_litellm_token.up.sql (litellm_token): text → string;
// timestamptz → time.Time; nullable text → *string.
//
// The keystore package (Plan 03-05) maps PkKeyInfo / EkKeyInfo INTO the
// generic keystore.KeyInfo carried through the auth middleware; this file
// is the upstream source-of-truth for the SQL-row shape.

package db

import "time"

// PkKeyInfo is the typed row shape returned by PkCheckAndExtend, GetPersonalKey,
// RevokePersonalKey, and the elements of ListPersonalKeysByOwner. The Hub §7.1
// sliding-window contract guarantees that a non-nil PkKeyInfo means the key
// was active and not expired at statement-snapshot time.
//
// LiteLLMUserID and LiteLLMToken are nullable: migration 000002 added
// litellm_user_id as a nullable column and migration 000003 added
// litellm_token nullable; Phase 3 SSO write path populates both on
// /key/generate but rows seeded before that path runs (or written by
// future migration backfills) leave them NULL.
type PkKeyInfo struct {
	KeyID         string     // PRIMARY KEY (always 'pkid_<…>')
	OwnerEmail    string     // §16 DB-05 — verbatim, never normalized
	ExpiresAt     time.Time  // post-check-and-extend wall-clock
	LiteLLMUserID *string    // NULL until Phase 3 SSO write
	LiteLLMToken  *string    // NULL until Phase 3 /key/generate response
	Status        string     // 'active' | 'revoked' | 'expired'
	CreatedAt     time.Time  // row-creation wall-clock (read by ListPersonalKeysByOwner)
	LastUsedAt    *time.Time // NULL on freshly minted rows
	RevokedAt     *time.Time // NULL while status='active'
}

// EkKeyInfo is the typed row shape returned by EkResolve, GetEnvironmentKey,
// RevokeEnvironmentKey, and the elements of ListEnvironmentKeysByOwner. Per
// Hub §8.1, environment_keys has no expires_at column (revocation-only); the
// debounced last_used_at UPDATE in EkResolve does not participate in the auth
// decision (KEY-06 — status='active' is the authoritative predicate).
//
// CredentialHash is populated by GetEnvironmentKey and RevokeEnvironmentKey
// (Plan 03-08 Rule 3 deviation — the §8.5 revoke flow needs it to derive
// the keystore cache key "ach:key:" + credential_hash for the explicit DEL
// invalidation barrier per KEY-08). EkResolve does NOT populate it (the
// resolver path already has the plaintext and can recompute the hash).
type EkKeyInfo struct {
	KeyID          string     // PRIMARY KEY (always 'ekid_<…>')
	CredentialHash string     // HMAC-SHA-256(pepper, plaintext) hex; populated by Get/Revoke only
	Environment    string     // bound to exactly one Environment at creation
	OwnerEmail     string     // §16 DB-05 — verbatim
	Name           string     // human-friendly label (per §8.2)
	LiteLLMUserID  *string    // NULL until Phase 3 SSO write
	LiteLLMToken   *string    // NULL until Phase 3 /key/generate response
	Status         string     // 'active' | 'revoked'
	CreatedAt      time.Time  // row-creation wall-clock
	LastUsedAt     *time.Time // NULL on freshly minted rows
	RevokedAt      *time.Time // NULL while status='active'
}

// PkInsertRow is the value-struct argument to InsertPersonalKey. Fields match
// the INSERT column list verbatim; the helper writes status='active' and
// created_at=DEFAULT (now()) without exposing them on this struct.
type PkInsertRow struct {
	KeyID          string
	CredentialHash string // HMAC-SHA-256(pepper, plaintext) hex; per §16.1 NEVER the plaintext
	OwnerEmail     string
	ExpiresAt      time.Time // sliding-window starts at now()+7 days
	LiteLLMUserID  *string
	LiteLLMToken   *string
}

// EkInsertRow is the value-struct argument to InsertEnvironmentKey. Same
// discipline as PkInsertRow — credential_hash is the HMAC hex, never the
// plaintext bearer.
type EkInsertRow struct {
	KeyID          string
	CredentialHash string
	Environment    string
	OwnerEmail     string
	Name           string
	LiteLLMUserID  *string
	LiteLLMToken   *string
}
