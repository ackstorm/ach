-- SPDX-License-Identifier: Apache-2.0

-- 000007: backend_identity_policies projection (issue #34).
--
-- One row per BackendIdentityPolicy CR. The operator's BIP reconciler writes
-- this projection so the forwarder can resolve per-target JWT mint policy
-- from Postgres alone (no informer). The forwarder's bipcache reads via
-- ListAllBIPs + NOTIFY ach_backend_identity_policies_changed.
--
-- Target indexing: (namespace, target_kind, target_name) index supports the
-- forwarder's per-request target-lookup path.
--
-- origin / locked / cr_locked_chk mirror 000005 for UI coexistence.

CREATE TABLE backend_identity_policies (
    namespace             TEXT        NOT NULL,
    name                  TEXT        NOT NULL,
    target_kind           TEXT        NOT NULL CHECK (target_kind IN ('MCPServer','A2AAgent')),
    target_name           TEXT        NOT NULL,
    forward_identity_jwt  BOOLEAN     NOT NULL,
    observed_generation   BIGINT      NOT NULL DEFAULT 0,
    deletion_timestamp    TIMESTAMPTZ,
    resource_version      TEXT        NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin                TEXT        NOT NULL DEFAULT 'cr'
        CHECK (origin IN ('cr','ui')),
    locked                BOOLEAN     NOT NULL DEFAULT FALSE,
    PRIMARY KEY (namespace, name),
    CONSTRAINT backend_identity_policies_cr_locked_chk
        CHECK (origin <> 'cr' OR locked = TRUE)
);

CREATE INDEX backend_identity_policies_target_idx
    ON backend_identity_policies (namespace, target_kind, target_name);
