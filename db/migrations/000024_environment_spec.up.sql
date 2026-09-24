-- SPDX-License-Identifier: Apache-2.0

-- 000024: environments.spec — the Environment spec, verbatim, as JSON.
--
-- The UI Objects API (GET /platform/objects/Environment/{name}[/yaml])
-- rebuilt the spec from the per-field projection columns, so every spec
-- field without a column was silently dropped by an export -> commit ->
-- kubectl apply round-trip (spec.budget, spec.runtime.*Groups), and the
-- operator-expanded runtime_mcp_servers / runtime_a2a_agents came back as
-- explicit names. Storing the spec itself makes the export exact for every
-- current and future field; the per-field columns stay the read path for
-- the forwarder, hydrate and the console.
--
-- Nullable, no backfill: CR rows are rewritten on the next reconcile (<= 5
-- min); a NULL spec falls back to the column rebuild.

ALTER TABLE environments ADD COLUMN IF NOT EXISTS spec jsonb;
