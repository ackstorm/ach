-- SPDX-License-Identifier: Apache-2.0

-- The dropped budgets are not restored.
ALTER TABLE key_grants
    ADD COLUMN max_budget      double precision NULL,
    ADD COLUMN budget_duration text             NOT NULL DEFAULT '';
