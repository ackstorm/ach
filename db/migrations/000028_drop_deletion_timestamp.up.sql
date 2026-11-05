-- Rows a finalizer drain left behind: no CR owns them and nothing removes them.
DELETE FROM plugins WHERE deletion_timestamp IS NOT NULL;
DELETE FROM prompts WHERE deletion_timestamp IS NOT NULL;
DELETE FROM artifacts WHERE deletion_timestamp IS NOT NULL;
DELETE FROM skills WHERE deletion_timestamp IS NOT NULL;
DELETE FROM litellm_connections WHERE deletion_timestamp IS NOT NULL;
DELETE FROM backend_identity_policies WHERE deletion_timestamp IS NOT NULL;
DELETE FROM marketplaces WHERE deletion_timestamp IS NOT NULL;
DELETE FROM skill_marketplaces WHERE deletion_timestamp IS NOT NULL;

ALTER TABLE environments DROP COLUMN deletion_timestamp;
ALTER TABLE plugins DROP COLUMN deletion_timestamp;
ALTER TABLE prompts DROP COLUMN deletion_timestamp;
ALTER TABLE artifacts DROP COLUMN deletion_timestamp;
ALTER TABLE skills DROP COLUMN deletion_timestamp;
ALTER TABLE litellm_connections DROP COLUMN deletion_timestamp;
ALTER TABLE backend_identity_policies DROP COLUMN deletion_timestamp;
ALTER TABLE marketplaces DROP COLUMN deletion_timestamp;
ALTER TABLE skill_marketplaces DROP COLUMN deletion_timestamp;
