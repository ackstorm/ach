ALTER TABLE personal_keys    DROP COLUMN IF EXISTS litellm_key_material;
ALTER TABLE environment_keys DROP COLUMN IF EXISTS litellm_key_material;
