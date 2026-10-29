-- 000014_litellm_key_material_enc.up.sql
-- G3: stop storing the LiteLLM virtual key (sk-…) in cleartext. The column now
-- holds keycrypt's base64std(version||nonce||ciphertext) blob (AES-256-GCM),
-- written by platform-api at mint and decrypted by the forwarder at use.
-- Rename for self-documentation, and NULL the testing-phase plaintext rows:
-- they are unrecoverable as ciphertext and pre-migration keys already break by
-- design (clean cutover, per the G3 decision).
ALTER TABLE personal_keys    RENAME COLUMN litellm_key_material TO litellm_key_material_enc;
ALTER TABLE environment_keys RENAME COLUMN litellm_key_material TO litellm_key_material_enc;
UPDATE personal_keys    SET litellm_key_material_enc = NULL WHERE litellm_key_material_enc IS NOT NULL;
UPDATE environment_keys SET litellm_key_material_enc = NULL WHERE litellm_key_material_enc IS NOT NULL;
