-- 000001_init.up.sql
--
-- ACH Hub Postgres schema — Phase 1.
--
-- Schema source: Hub §16 (ACH DB State) — four tables for Hub-owned operational
-- state: personal_keys, environment_keys, external_refs, marketplace_plugins.
--
-- Credential storage contract: Hub §16.1 — raw bearer values (pk_… / ek_…) MUST
-- NOT be persisted anywhere. This schema stores only one-way HMAC-SHA-256
-- credential hashes (computed application-side with a server-side pepper held
-- outside Postgres per D-09/D-10). There are NO columns capable of holding the
-- raw bearer value (no such column exists by design — DB-04).
--
-- Key ID prefix invariant: Hub §16 — personal_keys.key_id MUST begin with the
-- literal prefix 'pkid_' and environment_keys.key_id MUST begin with the literal
-- prefix 'ekid_' (deliberately distinct from the 'pk_'/'ek_' bearer prefixes;
-- key_id is an opaque ID, not the bearer value). CHECK constraints below enforce
-- this at the SQL layer (DB-02).
--
-- This migration is the SOLE schema writer; Plan 08 ships the migration init
-- container that runs `migrate up` against this file (D-07/D-08). Phase 1 does
-- NOT write rows into these tables — Phase 3 begins issuing pk_/ek_ values and
-- becomes the first row-writer.

CREATE TABLE personal_keys (
    key_id           text PRIMARY KEY,
    credential_hash  text NOT NULL UNIQUE,
    owner_email      text NOT NULL,
    status           text NOT NULL DEFAULT 'active',
    created_at       timestamptz NOT NULL DEFAULT now(),
    last_used_at     timestamptz,
    expires_at       timestamptz NOT NULL,
    revoked_at       timestamptz,
    CONSTRAINT personal_keys_key_id_prefix CHECK (key_id LIKE 'pkid_%'),
    CONSTRAINT personal_keys_status_enum CHECK (status IN ('active','revoked','expired'))
);

CREATE TABLE environment_keys (
    key_id           text PRIMARY KEY,
    credential_hash  text NOT NULL UNIQUE,
    environment      text NOT NULL,
    owner_email      text NOT NULL,
    name             text NOT NULL,
    status           text NOT NULL DEFAULT 'active',
    created_at       timestamptz NOT NULL DEFAULT now(),
    last_used_at     timestamptz,
    revoked_at       timestamptz,
    CONSTRAINT environment_keys_key_id_prefix CHECK (key_id LIKE 'ekid_%'),
    CONSTRAINT environment_keys_status_enum CHECK (status IN ('active','revoked'))
);

CREATE TABLE external_refs (
    kind                      text NOT NULL,
    name                      text NOT NULL,
    storage_location          text NOT NULL,
    last_successful_refresh   timestamptz,
    next_refresh_at           timestamptz,
    max_staleness_seconds     bigint NOT NULL,
    PRIMARY KEY (kind, name)
);

CREATE TABLE marketplace_plugins (
    marketplace_name          text NOT NULL,
    name                      text NOT NULL,
    storage_location          text NOT NULL,
    last_successful_refresh   timestamptz,
    next_refresh_at           timestamptz,
    max_staleness_seconds     bigint NOT NULL,
    PRIMARY KEY (marketplace_name, name)
);
