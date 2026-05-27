-- 000004_cs_projection.up.sql
--
-- Phase 5 spec v4 §5.2 reversal — DB projection layer for ACH CRDs
-- consumed by Content Service (D-13). Adds four tables: environments,
-- plugins, prompts, artifacts. PK = (namespace, name) per CRD.
--
-- Spec v4 §5.2 (line 13): "Platform API, Forwarder, and Content Service
-- no longer hold informers over ACH CRDs; they read CRD spec/status from
-- Postgres. Only the ACH Operator watches Kubernetes." These four tables
-- are the Operator's projection of ACH CRD state into Postgres, written
-- in the same transaction as the K8s Status subresource update. The
-- Operator becomes the K8s↔DB projector; Content Service reads ONLY
-- Postgres for content authorization (no informer, no in-memory CRD
-- cache outside the explicit Redis env-row cache).
--
-- Source plan: Phase 5 Plan 05-02 (D-13 column set, D-18 helpers).
--
-- IF NOT EXISTS provides defense-in-depth against partial-apply re-run
-- drift. golang-migrate transaction-per-migration semantics +
-- db.Migrate's ErrNoChange collapse already make re-application a
-- clean no-op in the happy path.
--
-- No CHECK constraints between projection tables (no FKs across them by
-- design — Operator owns referential integrity; Content Service reads
-- each projection independently per request).

CREATE TABLE IF NOT EXISTS environments (
    namespace                                text        NOT NULL,
    name                                     text        NOT NULL,
    authorized_teams                         text[]      NOT NULL DEFAULT '{}',
    context_prompts                          text[]      NOT NULL DEFAULT '{}',
    context_plugins                          text[]      NOT NULL DEFAULT '{}',
    context_artifacts                        text[]      NOT NULL DEFAULT '{}',
    runtime_models                           text[]      NOT NULL DEFAULT '{}',
    runtime_mcp_servers                      text[]      NOT NULL DEFAULT '{}',
    runtime_a2a_agents                       text[]      NOT NULL DEFAULT '{}',
    available_condition                      jsonb       NULL,
    access_group_synced_condition            jsonb       NULL,
    execution_resources_resolved_condition   jsonb       NULL,
    deletion_timestamp                       timestamptz NULL,
    resource_version                         text        NOT NULL,
    updated_at                               timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);

CREATE TABLE IF NOT EXISTS plugins (
    namespace                  text        NOT NULL,
    name                       text        NOT NULL,
    storage_location           text        NOT NULL DEFAULT '',
    last_successful_refresh    timestamptz NULL,
    max_staleness_seconds      bigint      NOT NULL DEFAULT 0,
    deletion_timestamp         timestamptz NULL,
    resource_version           text        NOT NULL DEFAULT '',
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);

CREATE TABLE IF NOT EXISTS prompts (
    namespace                  text        NOT NULL,
    name                       text        NOT NULL,
    storage_location           text        NOT NULL DEFAULT '',
    content_type               text        NULL,
    last_successful_refresh    timestamptz NULL,
    max_staleness_seconds      bigint      NOT NULL DEFAULT 0,
    deletion_timestamp         timestamptz NULL,
    resource_version           text        NOT NULL DEFAULT '',
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);

CREATE TABLE IF NOT EXISTS artifacts (
    namespace                  text        NOT NULL,
    name                       text        NOT NULL,
    storage_location           text        NOT NULL DEFAULT '',
    scope                      text        NOT NULL CHECK (scope IN ('object','directory')),
    last_successful_refresh    timestamptz NULL,
    max_staleness_seconds      bigint      NOT NULL DEFAULT 0,
    deletion_timestamp         timestamptz NULL,
    resource_version           text        NOT NULL DEFAULT '',
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, name)
);

-- Partial indexes on deletion_timestamp support cleanup sweeps over
-- soft-deleted rows (Operator finalizer drain reads these to find rows
-- eligible for hard DELETE). Mirrors the 000002 marketplace_plugins
-- index hygiene; defensive only — Phase 5 does not yet emit drain queries.
CREATE INDEX IF NOT EXISTS environments_deletion_timestamp_idx
    ON environments (deletion_timestamp) WHERE deletion_timestamp IS NOT NULL;
CREATE INDEX IF NOT EXISTS plugins_deletion_timestamp_idx
    ON plugins (deletion_timestamp) WHERE deletion_timestamp IS NOT NULL;
CREATE INDEX IF NOT EXISTS prompts_deletion_timestamp_idx
    ON prompts (deletion_timestamp) WHERE deletion_timestamp IS NOT NULL;
CREATE INDEX IF NOT EXISTS artifacts_deletion_timestamp_idx
    ON artifacts (deletion_timestamp) WHERE deletion_timestamp IS NOT NULL;
