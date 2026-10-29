-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE IF NOT EXISTS achagents (
    namespace         text        NOT NULL,
    name              text        NOT NULL,
    profile_ref       text        NOT NULL DEFAULT '',
    service_name      text        NOT NULL DEFAULT '',
    service_port      integer     NOT NULL DEFAULT 0,
    ready             boolean     NOT NULL DEFAULT FALSE,
    channels          jsonb       NOT NULL DEFAULT '[]'::jsonb,
    resource_version  text        NOT NULL DEFAULT '',
    updated_at        timestamptz NOT NULL DEFAULT now(),
    exposed           boolean     NOT NULL DEFAULT FALSE,
    PRIMARY KEY (namespace, name)
);

CREATE INDEX IF NOT EXISTS achagents_exposed_idx
    ON achagents (exposed) WHERE exposed = TRUE;
