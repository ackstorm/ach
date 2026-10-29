-- Add 'team' to the runtime_catalog_entries kind set (authorizedTeams axis).
ALTER TABLE runtime_catalog_entries DROP CONSTRAINT runtime_catalog_entries_kind_check;
ALTER TABLE runtime_catalog_entries ADD CONSTRAINT runtime_catalog_entries_kind_check
    CHECK (kind IN ('model','mcp_server','a2a_agent','team'));
