-- SPDX-License-Identifier: Apache-2.0
ALTER TABLE plugins
    ADD COLUMN delivery text NOT NULL DEFAULT 'ach'
    CHECK (delivery IN ('ach', 'native'));
