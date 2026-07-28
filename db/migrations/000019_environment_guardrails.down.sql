-- SPDX-License-Identifier: Apache-2.0

-- Reverse 000019. Rows of the new kind must go BEFORE the CHECK is narrowed,
-- or the constraint re-add fails on existing data (the 000016 precedent).

DELETE FROM runtime_catalog_entries WHERE kind = 'guardrail';

ALTER TABLE runtime_catalog_entries DROP CONSTRAINT runtime_catalog_entries_kind_check;
ALTER TABLE runtime_catalog_entries ADD CONSTRAINT runtime_catalog_entries_kind_check
    CHECK (kind IN ('model','mcp_server','a2a_agent','team'));

ALTER TABLE runtime_catalog_entries DROP COLUMN IF EXISTS attributes;
ALTER TABLE environments DROP COLUMN IF EXISTS runtime_guardrails;
