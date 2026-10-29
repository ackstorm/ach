-- runtime_catalog_entries: derived read-model of the LiteLLM runtime
-- registry (models / MCP servers / A2A agents), projected by the operator's
-- Snapshotter on each successful refresh. NOT a <kind>_objects projection:
-- no origin/locked, LiteLLM is the source of truth. Admin-only read surface.
CREATE TABLE runtime_catalog_entries (
    namespace            TEXT        NOT NULL,
    connector_name       TEXT        NOT NULL,
    kind                 TEXT        NOT NULL CHECK (kind IN ('model','mcp_server','a2a_agent')),
    name                 TEXT        NOT NULL,
    status               TEXT        NOT NULL CHECK (status IN ('active','missing')),
    first_seen_at        TIMESTAMPTZ NOT NULL,
    last_seen_at         TIMESTAMPTZ NOT NULL,
    last_successful_sync TIMESTAMPTZ NOT NULL,
    deleted_at           TIMESTAMPTZ,
    PRIMARY KEY (namespace, connector_name, kind, name)
);

CREATE INDEX runtime_catalog_entries_lookup_idx
    ON runtime_catalog_entries (namespace, connector_name, kind);
