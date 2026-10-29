-- One OAuth-backed pk_ per user: the front door maps a user JWT to this row.
-- 'cli' is every row that exists today (console + device-code mints).
ALTER TABLE personal_keys
    ADD COLUMN purpose text NOT NULL DEFAULT 'cli'
    CHECK (purpose IN ('cli', 'oauth'));

CREATE UNIQUE INDEX personal_keys_one_active_oauth_per_owner
    ON personal_keys (owner_email)
    WHERE purpose = 'oauth' AND status = 'active';
