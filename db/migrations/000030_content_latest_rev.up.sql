-- latest_upstream_rev: the commit a git source's ref points at on the last
-- refresh. It differs from upstream_rev (the served commit) when the CR is
-- pinned to an older spec.<git>.commit.
ALTER TABLE external_refs ADD COLUMN latest_upstream_rev text;
ALTER TABLE marketplaces ADD COLUMN upstream_rev text;
ALTER TABLE marketplaces ADD COLUMN latest_upstream_rev text;
ALTER TABLE skill_marketplaces ADD COLUMN upstream_rev text;
ALTER TABLE skill_marketplaces ADD COLUMN latest_upstream_rev text;
