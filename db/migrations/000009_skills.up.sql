-- SPDX-License-Identifier: Apache-2.0

-- 000009: skills projection (agent-skill content kind) + environments.context_skills.
-- Mirrors plugins (000004 table + 000005 origin/locked). A skill is a directory
-- tree stored as skill/<name>.tar.gz on the artifact PVC. Content-gated like
-- plugins; the operator is the only writer (origin='cr', locked=true).

CREATE TABLE IF NOT EXISTS skills (
    namespace                  text        NOT NULL,
    name                       text        NOT NULL,
    storage_location           text        NOT NULL DEFAULT '',
    last_successful_refresh    timestamptz NULL,
    max_staleness_seconds      bigint      NOT NULL DEFAULT 0,
    deletion_timestamp         timestamptz NULL,
    resource_version           text        NOT NULL DEFAULT '',
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    origin                     text        NOT NULL DEFAULT 'cr',
    locked                     boolean     NOT NULL DEFAULT FALSE,
    PRIMARY KEY (namespace, name),
    CONSTRAINT skills_origin_chk     CHECK (origin IN ('cr','ui')),
    CONSTRAINT skills_cr_locked_chk  CHECK (origin <> 'cr' OR locked = TRUE)
);

CREATE INDEX IF NOT EXISTS skills_deletion_timestamp_idx
    ON skills (deletion_timestamp) WHERE deletion_timestamp IS NOT NULL;

ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS context_skills text[] NOT NULL DEFAULT '{}';
