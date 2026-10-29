-- SPDX-License-Identifier: Apache-2.0

ALTER TABLE environments DROP COLUMN IF EXISTS context_skills;
DROP TABLE IF EXISTS skills;
