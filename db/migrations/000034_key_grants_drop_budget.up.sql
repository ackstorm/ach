-- SPDX-License-Identifier: Apache-2.0

-- A KeyGrant sets who may exchange, never how much: the Environment's
-- spec.budget caps the exchange key.
ALTER TABLE key_grants
    DROP COLUMN max_budget,
    DROP COLUMN budget_duration;
