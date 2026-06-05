-- SPDX-License-Identifier: Apache-2.0

-- 000010: SkillMarketplace projection (collections of agent skills).
--
-- skill_marketplace_skills: one row per skill discovered inside a
-- SkillMarketplace. Mirrors marketplace_plugins (000001 + upstream_rev/
-- force_refresh_requested_at from 000002 + origin/locked from 000005) so the
-- cloned db helper has the same column set. PK (marketplace_name, name).
--
-- skill_marketplaces: the marketplace OBJECT + terminal Synced status +
-- skills_count. Mirrors the marketplaces table (000008, PR #111) renaming
-- plugins_count → skills_count; no origin/locked (CR-only — the operator is
-- the sole writer, issue #34).

CREATE TABLE skill_marketplace_skills (
    marketplace_name           TEXT        NOT NULL,
    name                       TEXT        NOT NULL,
    storage_location           TEXT        NOT NULL,
    last_successful_refresh    TIMESTAMPTZ,
    next_refresh_at            TIMESTAMPTZ,
    max_staleness_seconds      BIGINT      NOT NULL,
    upstream_rev               TEXT,
    force_refresh_requested_at TIMESTAMPTZ,
    origin                     TEXT        NOT NULL DEFAULT 'cr',
    locked                     BOOLEAN     NOT NULL DEFAULT FALSE,
    PRIMARY KEY (marketplace_name, name),
    CONSTRAINT skill_mkt_skills_origin_chk     CHECK (origin IN ('cr','ui')),
    CONSTRAINT skill_mkt_skills_cr_locked_chk  CHECK (origin <> 'cr' OR locked = TRUE)
);

CREATE TABLE skill_marketplaces (
    namespace          TEXT        NOT NULL,
    name               TEXT        NOT NULL,
    synced_status      TEXT        NOT NULL DEFAULT '',   -- metav1.Condition.Status: "True" | "False" | "Unknown" | ""
    synced_reason      TEXT        NOT NULL DEFAULT '',   -- condition Reason when not Synced (e.g. UpstreamInvalid)
    skills_count       INTEGER     NOT NULL DEFAULT 0,
    deletion_timestamp TIMESTAMPTZ,
    resource_version   TEXT        NOT NULL DEFAULT '',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);
