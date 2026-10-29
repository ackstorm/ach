-- D-29: no rollback below the console release once any row is suspended or
-- carries expires_at — an older orphan worker would reap suspended keys and
-- dropping the column would silently erase expiry. Refuse loudly instead.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM environment_keys WHERE status = 'suspended' OR expires_at IS NOT NULL) THEN
        RAISE EXCEPTION 'environment_keys has suspended or expiring rows; rollback below the console release is not supported (D-29)';
    END IF;
END $$;
ALTER TABLE environment_keys
    DROP COLUMN expires_at,
    DROP CONSTRAINT environment_keys_status_enum,
    ADD CONSTRAINT environment_keys_status_enum CHECK (status IN ('active','revoked'));
