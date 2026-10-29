-- SPDX-License-Identifier: Apache-2.0

ALTER TABLE backend_identity_policies
    DROP COLUMN consent_broker,
    DROP COLUMN consent_audience;
