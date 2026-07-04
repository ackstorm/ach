-- SPDX-License-Identifier: Apache-2.0

-- Reverse 000018: restore has_webhook + its partial index, drop exposed.

ALTER TABLE achagents ADD COLUMN IF NOT EXISTS has_webhook boolean NOT NULL DEFAULT FALSE;

DROP INDEX IF EXISTS achagents_exposed_idx;

CREATE INDEX IF NOT EXISTS achagents_has_webhook_idx
    ON achagents (has_webhook) WHERE has_webhook = TRUE;

ALTER TABLE achagents DROP COLUMN IF EXISTS exposed;
