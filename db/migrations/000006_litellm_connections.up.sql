-- SPDX-License-Identifier: Apache-2.0

-- 000006: litellm_connections projection (issue #34).
--
-- One row per LiteLLMConnection CR (steady-state: a single row named
-- 'default' in the operator's namespace). The operator's reconciler
-- projects the spec here so the forwarder can boot from Postgres alone —
-- the Secret pointed at by master_key_secret_namespace/name/key stays in
-- Kubernetes (Secrets are not CRDs, so they remain on the k8s control plane).
--
-- origin / locked / cr_locked_chk mirror 000005 so UI-managed connection
-- rows (future) coexist with operator-managed ones without one writer
-- clobbering the other.

CREATE TABLE litellm_connections (
    namespace                    TEXT        NOT NULL,
    name                         TEXT        NOT NULL DEFAULT 'default',
    endpoint                     TEXT        NOT NULL,
    master_key_secret_namespace  TEXT        NOT NULL,
    master_key_secret_name       TEXT        NOT NULL,
    master_key_secret_key        TEXT        NOT NULL DEFAULT 'master_key',
    deletion_timestamp           TIMESTAMPTZ,
    resource_version             TEXT        NOT NULL,
    updated_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin                       TEXT        NOT NULL DEFAULT 'cr'
        CHECK (origin IN ('cr','ui')),
    locked                       BOOLEAN     NOT NULL DEFAULT FALSE,
    PRIMARY KEY (namespace, name),
    CONSTRAINT litellm_connections_cr_locked_chk
        CHECK (origin <> 'cr' OR locked = TRUE)
);
