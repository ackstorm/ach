-- SPDX-License-Identifier: Apache-2.0

-- Refused while a live exchange key exists: dropping issued_via would turn it
-- into a manual key that counts against max_keys and that no grant governs.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM environment_keys WHERE issued_via = 'exchange' AND status <> 'revoked') THEN
        RAISE EXCEPTION 'environment_keys has live exchange keys; revoke them before rolling back 000026';
    END IF;
END $$;
DROP TABLE key_grants;
DROP INDEX environment_keys_exchange_tuple_uniq;
ALTER TABLE environment_keys
    DROP CONSTRAINT environment_keys_requester_chk,
    DROP CONSTRAINT environment_keys_issued_via_enum,
    DROP COLUMN requester,
    DROP COLUMN issued_via;
