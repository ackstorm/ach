-- 000003_litellm_token.down.sql
--
-- Reverse migration: drops the two partial UNIQUE indexes and the two
-- columns added in the .up companion. Used only by `migrate down`
-- invocations in dev / disaster recovery — production runs only
-- `migrate up` (Plan 01-10 init container).
--
-- Indexes are dropped BEFORE columns. Postgres enforces this order
-- natively (DROP COLUMN cascades dependent indexes), but explicit
-- ordering documents intent.

DROP INDEX IF EXISTS personal_keys_litellm_token_uniq;
DROP INDEX IF EXISTS environment_keys_litellm_token_uniq;

ALTER TABLE personal_keys       DROP COLUMN IF EXISTS litellm_token;
ALTER TABLE environment_keys    DROP COLUMN IF EXISTS litellm_token;
