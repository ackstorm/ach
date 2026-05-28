-- SPDX-License-Identifier: Apache-2.0

-- 000005: origin + locked columns for source-of-truth coexistence (issue #34).
--
--   origin TEXT  ('cr'|'ui'): provenance — who wrote the row. Operator writes
--                             always set origin='cr'; a future UI will set
--                             origin='ui'. Extends naturally to future writers.
--   locked BOOLEAN          : mutability — is this row read-only to a
--                             non-origin writer? Operator-written rows are
--                             always locked=true; UI inserts default to
--                             locked=false. UI uses this single field to grey
--                             out edit controls.
--
-- Cross-writer DB guard sits on origin via the ON CONFLICT (...) DO UPDATE ...
-- WHERE existing.origin = 'cr' clause added to UpsertX in internal/db/.
-- The locked column is consulted by handler/UI-layer code to refuse edits
-- early. Existing rows are backfilled (origin='cr', locked=true) since the
-- operator is currently the only writer.
--
-- Prereq: scale the operator to 0 before running migrate-up so a CR-origin
-- INSERT cannot land between the backfill UPDATE and the cr_locked_chk ADD.
-- golang-migrate runs each .up.sql in a single transaction by default; the
-- explicit BEGIN/COMMIT below makes the atomicity contract visible to
-- reviewers and is a no-op if the driver already opens one.

BEGIN;

ALTER TABLE environments        ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE plugins             ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE prompts             ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE artifacts           ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE external_refs       ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE marketplace_plugins ADD COLUMN origin TEXT    NOT NULL DEFAULT 'cr',
                                ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;

-- Backfill: every existing row was written by the operator, so it's CR-origin
-- AND locked.
UPDATE environments        SET locked = TRUE WHERE origin = 'cr';
UPDATE plugins             SET locked = TRUE WHERE origin = 'cr';
UPDATE prompts             SET locked = TRUE WHERE origin = 'cr';
UPDATE artifacts           SET locked = TRUE WHERE origin = 'cr';
UPDATE external_refs       SET locked = TRUE WHERE origin = 'cr';
UPDATE marketplace_plugins SET locked = TRUE WHERE origin = 'cr';

ALTER TABLE environments        ADD CONSTRAINT environments_origin_chk        CHECK (origin IN ('cr','ui'));
ALTER TABLE plugins             ADD CONSTRAINT plugins_origin_chk             CHECK (origin IN ('cr','ui'));
ALTER TABLE prompts             ADD CONSTRAINT prompts_origin_chk             CHECK (origin IN ('cr','ui'));
ALTER TABLE artifacts           ADD CONSTRAINT artifacts_origin_chk           CHECK (origin IN ('cr','ui'));
ALTER TABLE external_refs       ADD CONSTRAINT external_refs_origin_chk       CHECK (origin IN ('cr','ui'));
ALTER TABLE marketplace_plugins ADD CONSTRAINT marketplace_plugins_origin_chk CHECK (origin IN ('cr','ui'));

-- Belt-and-suspenders: a CR-origin row must always be locked. Catches
-- accidental hand-edits of the operator's projection.
ALTER TABLE environments        ADD CONSTRAINT environments_cr_locked_chk        CHECK (origin <> 'cr' OR locked = TRUE);
ALTER TABLE plugins             ADD CONSTRAINT plugins_cr_locked_chk             CHECK (origin <> 'cr' OR locked = TRUE);
ALTER TABLE prompts             ADD CONSTRAINT prompts_cr_locked_chk             CHECK (origin <> 'cr' OR locked = TRUE);
ALTER TABLE artifacts           ADD CONSTRAINT artifacts_cr_locked_chk           CHECK (origin <> 'cr' OR locked = TRUE);
ALTER TABLE external_refs       ADD CONSTRAINT external_refs_cr_locked_chk       CHECK (origin <> 'cr' OR locked = TRUE);
ALTER TABLE marketplace_plugins ADD CONSTRAINT marketplace_plugins_cr_locked_chk CHECK (origin <> 'cr' OR locked = TRUE);

COMMIT;
