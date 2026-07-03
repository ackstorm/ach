-- SPDX-License-Identifier: Apache-2.0

-- 000017: achagents projection. Read model for (a) the gateway's /hook route
-- table and (b) the future UI agent list. The operator is the ONLY writer;
-- there is no UI write path, so this table omits the origin/locked/deletion
-- drain machinery the content kinds use. Cleanup is a hard DELETE keyed by
-- (namespace, name) on CR delete. NOTIFY is Go-side (WithTxNotify), no trigger.

CREATE TABLE IF NOT EXISTS achagents (
    namespace         text        NOT NULL,
    name              text        NOT NULL,
    profile_ref       text        NOT NULL DEFAULT '',
    service_name      text        NOT NULL DEFAULT '',   -- '' when no Service (cron/queue-only)
    service_port      integer     NOT NULL DEFAULT 0,    -- 0 when no Service
    has_webhook       boolean     NOT NULL DEFAULT FALSE,
    ready             boolean     NOT NULL DEFAULT FALSE,
    channels          jsonb       NOT NULL DEFAULT '[]'::jsonb,
    resource_version  text        NOT NULL DEFAULT '',
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);

CREATE INDEX IF NOT EXISTS achagents_has_webhook_idx
    ON achagents (has_webhook) WHERE has_webhook = TRUE;
