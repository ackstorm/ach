-- SPDX-License-Identifier: Apache-2.0

-- 000019: Environment.spec.runtime.guardrails — the fourth runtime axis.
--
-- Three changes, one schema unit:
--   1. environments.runtime_guardrails — the Environment's declared guardrail
--      names. Required for GitOps round-trip fidelity: the YAML export renders
--      rowToSpec, so a spec field absent from the row is silently dropped by an
--      export -> commit -> kubectl apply cycle.
--   2. the runtime_catalog_entries kind set gains 'guardrail'.
--   3. runtime_catalog_entries.attributes — per-entry JSON, populated only for
--      guardrails today (mode, defaultOn). A default_on guardrail already runs
--      on every request, so naming it in an Environment is a no-op; surfacing
--      that in the admin catalog is the only way an author can tell.
--
-- Projection rows are operator-rewritten every reconcile, so no backfill is
-- needed — the DEFAULT covers existing rows until the next reconcile.

ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS runtime_guardrails text[] NOT NULL DEFAULT '{}';

ALTER TABLE runtime_catalog_entries
    ADD COLUMN IF NOT EXISTS attributes jsonb;

ALTER TABLE runtime_catalog_entries DROP CONSTRAINT runtime_catalog_entries_kind_check;
ALTER TABLE runtime_catalog_entries ADD CONSTRAINT runtime_catalog_entries_kind_check
    CHECK (kind IN ('model','mcp_server','a2a_agent','team','guardrail'));
