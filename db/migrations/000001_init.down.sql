-- 000001_init.down.sql
--
-- Reverse of 000001_init.up.sql.
--
-- None of the four tables carry FOREIGN KEYs cross-referencing each other (Hub
-- §16 keeps them denormalized), so drop order is not technically constrained;
-- we still drop in reverse declaration order for readability.

DROP TABLE IF EXISTS marketplace_plugins;
DROP TABLE IF EXISTS external_refs;
DROP TABLE IF EXISTS environment_keys;
DROP TABLE IF EXISTS personal_keys;
