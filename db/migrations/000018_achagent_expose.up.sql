-- SPDX-License-Identifier: Apache-2.0

-- 000018: gateway routing keys off an explicit opt-in (expose.gateway) instead
-- of the channel-type-derived has_webhook. Replace the column + its partial
-- index. Projection rows are operator-rewritten every reconcile, so no data
-- backfill is needed — the operator repopulates `exposed` on next reconcile.

ALTER TABLE achagents ADD COLUMN IF NOT EXISTS exposed boolean NOT NULL DEFAULT FALSE;

DROP INDEX IF EXISTS achagents_has_webhook_idx;

CREATE INDEX IF NOT EXISTS achagents_exposed_idx
    ON achagents (exposed) WHERE exposed = TRUE;

ALTER TABLE achagents DROP COLUMN IF EXISTS has_webhook;
