DELETE FROM runtime_catalog_entries WHERE kind = 'team';
ALTER TABLE runtime_catalog_entries DROP CONSTRAINT runtime_catalog_entries_kind_check;
ALTER TABLE runtime_catalog_entries ADD CONSTRAINT runtime_catalog_entries_kind_check
    CHECK (kind IN ('model','mcp_server','a2a_agent'));
