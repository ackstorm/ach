-- 000014_litellm_key_material_enc.down.sql
ALTER TABLE personal_keys    RENAME COLUMN litellm_key_material_enc TO litellm_key_material;
ALTER TABLE environment_keys RENAME COLUMN litellm_key_material_enc TO litellm_key_material;
