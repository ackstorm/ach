-- 000004_cs_projection.down.sql
--
-- Reverse of 000004_cs_projection.up.sql — drops the four projection
-- tables (environments, plugins, prompts, artifacts) and their partial
-- deletion_timestamp indexes. Used only by `migrate down` invocations in
-- dev / disaster recovery — production runs only `migrate up` (Plan 01-10
-- init container).
--
-- Indexes are dropped BEFORE tables. Postgres enforces this order
-- natively (DROP TABLE cascades dependent indexes), but explicit
-- ordering documents intent. DROP TABLE order mirrors CREATE order in
-- reverse — there are no FKs between the four tables but the convention
-- matches 000003 and 000002.

DROP INDEX IF EXISTS artifacts_deletion_timestamp_idx;
DROP INDEX IF EXISTS prompts_deletion_timestamp_idx;
DROP INDEX IF EXISTS plugins_deletion_timestamp_idx;
DROP INDEX IF EXISTS environments_deletion_timestamp_idx;

DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS prompts;
DROP TABLE IF EXISTS plugins;
DROP TABLE IF EXISTS environments;
