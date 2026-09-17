DROP INDEX IF EXISTS personal_keys_one_active_oauth_per_owner;
ALTER TABLE personal_keys DROP COLUMN IF EXISTS purpose;
