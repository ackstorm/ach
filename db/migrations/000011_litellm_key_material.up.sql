-- TESTING-PHASE (reverts FIX01 §A.6): persist the per-user LiteLLM virtual-key
-- plaintext so the forwarder can authenticate to LiteLLM as the caller's own
-- key (1:1 identity) instead of the shared master key. Plaintext, nullable;
-- populated at mint (/key/generate response `key`). Pre-existing rows stay NULL.
ALTER TABLE personal_keys    ADD COLUMN IF NOT EXISTS litellm_key_material text;
ALTER TABLE environment_keys ADD COLUMN IF NOT EXISTS litellm_key_material text;
