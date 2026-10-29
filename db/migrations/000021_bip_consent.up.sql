-- SPDX-License-Identifier: Apache-2.0

ALTER TABLE backend_identity_policies
    ADD COLUMN consent_broker   TEXT NOT NULL DEFAULT '',
    ADD COLUMN consent_audience TEXT NOT NULL DEFAULT '';
