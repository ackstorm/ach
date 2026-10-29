-- 000012: environments.notice — optional post-hydrate advisory surfaced by
-- ach-cli (env spec.notice). Plain text; NOT NULL DEFAULT '' so existing rows
-- and the read-side scans never see SQL NULL.
ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS notice text NOT NULL DEFAULT '';
