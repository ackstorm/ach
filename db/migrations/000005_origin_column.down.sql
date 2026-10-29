-- SPDX-License-Identifier: Apache-2.0

-- Reverse 000005: drop CHECK constraints first, then columns.

ALTER TABLE environments        DROP CONSTRAINT IF EXISTS environments_cr_locked_chk;
ALTER TABLE plugins             DROP CONSTRAINT IF EXISTS plugins_cr_locked_chk;
ALTER TABLE prompts             DROP CONSTRAINT IF EXISTS prompts_cr_locked_chk;
ALTER TABLE artifacts           DROP CONSTRAINT IF EXISTS artifacts_cr_locked_chk;
ALTER TABLE external_refs       DROP CONSTRAINT IF EXISTS external_refs_cr_locked_chk;
ALTER TABLE marketplace_plugins DROP CONSTRAINT IF EXISTS marketplace_plugins_cr_locked_chk;

ALTER TABLE environments        DROP CONSTRAINT IF EXISTS environments_origin_chk;
ALTER TABLE plugins             DROP CONSTRAINT IF EXISTS plugins_origin_chk;
ALTER TABLE prompts             DROP CONSTRAINT IF EXISTS prompts_origin_chk;
ALTER TABLE artifacts           DROP CONSTRAINT IF EXISTS artifacts_origin_chk;
ALTER TABLE external_refs       DROP CONSTRAINT IF EXISTS external_refs_origin_chk;
ALTER TABLE marketplace_plugins DROP CONSTRAINT IF EXISTS marketplace_plugins_origin_chk;

ALTER TABLE environments        DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
ALTER TABLE plugins             DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
ALTER TABLE prompts             DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
ALTER TABLE artifacts           DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
ALTER TABLE external_refs       DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
ALTER TABLE marketplace_plugins DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS locked;
