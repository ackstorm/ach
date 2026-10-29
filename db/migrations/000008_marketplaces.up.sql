-- SPDX-License-Identifier: Apache-2.0

-- 000008: marketplaces projection (admin-inventory redesign).
--
-- One row per PluginMarketplace CR. The operator's reconciler projects the
-- CR's terminal Synced status + pluginsCount here so platform-api's admin
-- inventory can show marketplace OBJECTS (and their status) without reading
-- CRDs — platform-api reads Postgres only (issue #34). The plugins discovered
-- inside each marketplace remain in marketplace_plugins (000001); this table
-- is the marketplace object itself.
--
-- No origin/locked: marketplaces are CR-only (the operator is the sole writer).

CREATE TABLE marketplaces (
    namespace          TEXT        NOT NULL,
    name               TEXT        NOT NULL,
    synced_status      TEXT        NOT NULL DEFAULT '',   -- metav1.Condition.Status: "True" | "False" | "Unknown" | ""
    synced_reason      TEXT        NOT NULL DEFAULT '',   -- condition Reason when not Synced (e.g. UpstreamInvalid)
    plugins_count      INTEGER     NOT NULL DEFAULT 0,
    deletion_timestamp TIMESTAMPTZ,
    resource_version   TEXT        NOT NULL DEFAULT '',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);
