-- 000002_phase2.down.sql
--
-- Reverse migration: drops the six columns added in the .up companion.
-- Used only by `migrate down` invocations in dev / disaster recovery —
-- production runs only `migrate up` (Plan 01-10 init container).

ALTER TABLE personal_keys       DROP COLUMN IF EXISTS litellm_user_id;
ALTER TABLE environment_keys    DROP COLUMN IF EXISTS litellm_user_id;

ALTER TABLE external_refs       DROP COLUMN IF EXISTS upstream_rev;
ALTER TABLE marketplace_plugins DROP COLUMN IF EXISTS upstream_rev;

ALTER TABLE external_refs       DROP COLUMN IF EXISTS force_refresh_requested_at;
ALTER TABLE marketplace_plugins DROP COLUMN IF EXISTS force_refresh_requested_at;
