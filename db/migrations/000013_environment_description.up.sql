-- 000013: environments.description — optional catalog metadata surfaced by
-- ach-cli env list / env describe (env spec.description). Plain text; NOT NULL
-- DEFAULT '' so existing rows and the read-side scans never see SQL NULL.
ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
