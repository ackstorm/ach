-- 000002_phase2.up.sql
--
-- Phase 2 schema additions per Plan 02-03.
-- Three concerns, six columns:
--
-- 1. litellm_user_id on personal_keys + environment_keys (OP-15 D-16; nullable in
--    Phase 2; Phase 3 SSO/ek_ write paths set values).
--
-- 2. upstream_rev on external_refs + marketplace_plugins (OP-03; records the
--    fetcher's UpstreamRev for conditional-GET / not-modified detection on the
--    next reconcile).
--
-- 3. force_refresh_requested_at on external_refs + marketplace_plugins (D-07
--    forward-compat for Phase 3 Platform API force-refresh; Phase 2 reconcilers
--    read+clear this column in the same UPDATE as last_successful_refresh).
--
-- No new tables. No removed columns. No CHECK constraint changes. No row writes.
--
-- IF NOT EXISTS provides defense-in-depth against partial-apply re-run drift
-- (Postgres 9.6+ supports the form on ALTER TABLE ADD COLUMN). golang-migrate
-- transaction-per-migration semantics + db.Migrate's ErrNoChange collapse already
-- make re-application a clean no-op in the happy path.

ALTER TABLE personal_keys       ADD COLUMN IF NOT EXISTS litellm_user_id text;
ALTER TABLE environment_keys    ADD COLUMN IF NOT EXISTS litellm_user_id text;

ALTER TABLE external_refs       ADD COLUMN IF NOT EXISTS upstream_rev text;
ALTER TABLE marketplace_plugins ADD COLUMN IF NOT EXISTS upstream_rev text;

ALTER TABLE external_refs       ADD COLUMN IF NOT EXISTS force_refresh_requested_at timestamptz;
ALTER TABLE marketplace_plugins ADD COLUMN IF NOT EXISTS force_refresh_requested_at timestamptz;
