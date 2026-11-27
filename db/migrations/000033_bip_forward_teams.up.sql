-- SPDX-License-Identifier: Apache-2.0

ALTER TABLE backend_identity_policies
    ADD COLUMN forward_teams BOOLEAN NOT NULL DEFAULT FALSE;
