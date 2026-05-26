-- 000003_litellm_token.up.sql
--
-- Phase 02.2 schema addition per Plan 02.2-01 (Gap G1 fix).
-- One concern, two columns:
--
-- 1. litellm_token on personal_keys + environment_keys (D-01; nullable in
--    Phase 02.2; Phase 3 SSO write path populates the value when
--    POST /key/generate returns. The orphan loop's set-difference key
--    changes from litellm_user_id (migration 000002) to litellm_token
--    (this migration) — the LiteLLM GET /key/list response carries
--    `token` (the LiteLLM-internal opaque hex), NOT `key_id`. See
--    .planning/phases/02-external-refs-marketplace-operator-reconciliation/02-HUMAN-UAT.md
--    §Gaps G1 (lines 62-71) for the wire-format diff.
--
-- Partial UNIQUE index per table (WHERE litellm_token IS NOT NULL):
-- LiteLLM tokens are globally unique within a LiteLLM instance, so the
-- constraint is correct. No prefix-validation constraint is applied —
-- Phase 1 chose the `pkid_*` / `ekid_*` prefix validation only on
-- key_id; the LiteLLM-internal opaque hex token has no fixed prefix.
--
-- Phase 3 SSO is the documented write path: this migration ships schema
-- + reader-side change only; the orphan loop returns an empty slice on
-- every tick until Phase 3 populates the column on /key/generate.
--
-- No new tables. No removed columns. No row writes.
--
-- IF NOT EXISTS provides defense-in-depth against partial-apply re-run
-- drift (Postgres 9.6+ supports the form on ALTER TABLE ADD COLUMN).
-- golang-migrate transaction-per-migration semantics + db.Migrate's
-- ErrNoChange collapse already make re-application a clean no-op in the
-- happy path.

ALTER TABLE personal_keys       ADD COLUMN IF NOT EXISTS litellm_token text;
ALTER TABLE environment_keys    ADD COLUMN IF NOT EXISTS litellm_token text;

CREATE UNIQUE INDEX IF NOT EXISTS personal_keys_litellm_token_uniq
    ON personal_keys (litellm_token) WHERE litellm_token IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS environment_keys_litellm_token_uniq
    ON environment_keys (litellm_token) WHERE litellm_token IS NOT NULL;
