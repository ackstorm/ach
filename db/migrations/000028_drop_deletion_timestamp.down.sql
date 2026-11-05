-- The purged rows are not restored.
ALTER TABLE environments ADD COLUMN deletion_timestamp timestamptz NULL;
ALTER TABLE plugins ADD COLUMN deletion_timestamp timestamptz NULL;
ALTER TABLE prompts ADD COLUMN deletion_timestamp timestamptz NULL;
ALTER TABLE artifacts ADD COLUMN deletion_timestamp timestamptz NULL;
ALTER TABLE skills ADD COLUMN deletion_timestamp timestamptz NULL;
ALTER TABLE litellm_connections ADD COLUMN deletion_timestamp timestamptz NULL;
ALTER TABLE backend_identity_policies ADD COLUMN deletion_timestamp timestamptz NULL;
ALTER TABLE marketplaces ADD COLUMN deletion_timestamp timestamptz NULL;
ALTER TABLE skill_marketplaces ADD COLUMN deletion_timestamp timestamptz NULL;

CREATE INDEX IF NOT EXISTS environments_deletion_timestamp_idx
    ON environments (deletion_timestamp) WHERE deletion_timestamp IS NOT NULL;
CREATE INDEX IF NOT EXISTS plugins_deletion_timestamp_idx
    ON plugins (deletion_timestamp) WHERE deletion_timestamp IS NOT NULL;
CREATE INDEX IF NOT EXISTS prompts_deletion_timestamp_idx
    ON prompts (deletion_timestamp) WHERE deletion_timestamp IS NOT NULL;
CREATE INDEX IF NOT EXISTS artifacts_deletion_timestamp_idx
    ON artifacts (deletion_timestamp) WHERE deletion_timestamp IS NOT NULL;
CREATE INDEX IF NOT EXISTS skills_deletion_timestamp_idx
    ON skills (deletion_timestamp) WHERE deletion_timestamp IS NOT NULL;
