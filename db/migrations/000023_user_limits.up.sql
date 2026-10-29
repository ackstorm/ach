-- Per-user ceiling on how many ek_ keys a person may hold at once.
-- Deny-by-default: absent row means the chart default (ACH_USER_MAX_KEYS,
-- itself 0), so nobody creates an ek_ until an admin grants an allowance
-- via PATCH /platform/admin/users/{email}/limits.
--
-- Why Postgres and not a LiteLLM tag (where spend budgets live): ACH mints
-- the keys and owns environment_keys, so ACH enforces this. LiteLLM would
-- never read it, and a value there could not be consistent with the
-- environment_keys count it gates.
--
-- email is stored already-normalized (lowercased, trimmed) by
-- litellm.NormalizeEmail; every read normalizes the same way.
CREATE TABLE user_limits (
    email      text PRIMARY KEY,
    max_keys   integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_limits_max_keys_non_negative CHECK (max_keys >= 0)
);
