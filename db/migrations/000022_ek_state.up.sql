-- Unified console §7.2 / §14.1: manual suspension + optional expiry on
-- environment keys. Additive: existing rows stay 'active' with NULL expiry.
ALTER TABLE environment_keys
    DROP CONSTRAINT environment_keys_status_enum,
    ADD CONSTRAINT environment_keys_status_enum CHECK (status IN ('active','suspended','revoked')),
    ADD COLUMN expires_at timestamptz NULL;
