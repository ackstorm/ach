-- SPDX-License-Identifier: Apache-2.0

-- 000026: ServiceAccount token exchange (POST /platform/keys/exchange).
--
-- environment_keys records how a key was issued and, for an exchange key, who
-- asked for it ("<namespace>/<serviceaccount>"). One live exchange key per
-- (owner, environment, requester): the partial unique index makes concurrent
-- first exchanges converge — the insert loser revokes its LiteLLM key and
-- reads the winner's row.
--
-- key_grants is the operator's projection of KeyGrant CRs. The exchange
-- handler and the exchange-JWT resolvers query it per request (resolvers
-- behind the 60 s cache), so it has no NOTIFY channel and no reader cache.
ALTER TABLE environment_keys
    ADD COLUMN issued_via text NOT NULL DEFAULT 'manual',
    ADD COLUMN requester  text NULL,
    ADD CONSTRAINT environment_keys_issued_via_enum CHECK (issued_via IN ('manual','exchange')),
    ADD CONSTRAINT environment_keys_requester_chk CHECK ((issued_via = 'exchange') = (requester IS NOT NULL));

CREATE UNIQUE INDEX environment_keys_exchange_tuple_uniq
    ON environment_keys (owner_email, environment, requester)
    WHERE issued_via = 'exchange' AND status <> 'revoked';

CREATE TABLE key_grants (
    namespace        text             NOT NULL,
    name             text             NOT NULL,
    identity         text             NOT NULL,
    environments     text[]           NOT NULL,
    service_accounts text[]           NOT NULL,
    max_budget       double precision NULL,
    budget_duration  text             NOT NULL DEFAULT '',
    updated_at       timestamptz      NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);

CREATE INDEX key_grants_identity_idx ON key_grants (namespace, identity);
