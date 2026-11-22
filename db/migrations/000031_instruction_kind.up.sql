-- SPDX-License-Identifier: Apache-2.0
-- Instruction projection: the prompts table shape without content_type.
CREATE TABLE instructions (
    namespace                text        NOT NULL,
    name                     text        NOT NULL,
    storage_location         text        NOT NULL DEFAULT '',
    last_successful_refresh  timestamptz NULL,
    max_staleness_seconds    bigint      NOT NULL DEFAULT 0,
    resource_version         text        NOT NULL DEFAULT '',
    updated_at               timestamptz NOT NULL DEFAULT now(),
    origin                   text        NOT NULL DEFAULT 'cr',
    locked                   boolean     NOT NULL DEFAULT FALSE,
    CONSTRAINT instructions_pkey PRIMARY KEY (namespace, name),
    CONSTRAINT instructions_origin_chk CHECK (origin IN ('cr','ui')),
    CONSTRAINT instructions_cr_locked_chk CHECK (origin <> 'cr' OR locked = TRUE)
);

ALTER TABLE environments ADD COLUMN context_instructions text[] NOT NULL DEFAULT '{}';
