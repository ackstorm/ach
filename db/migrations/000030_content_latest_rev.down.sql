ALTER TABLE skill_marketplaces DROP COLUMN IF EXISTS latest_upstream_rev;
ALTER TABLE skill_marketplaces DROP COLUMN IF EXISTS upstream_rev;
ALTER TABLE marketplaces DROP COLUMN IF EXISTS latest_upstream_rev;
ALTER TABLE marketplaces DROP COLUMN IF EXISTS upstream_rev;
ALTER TABLE external_refs DROP COLUMN IF EXISTS latest_upstream_rev;
