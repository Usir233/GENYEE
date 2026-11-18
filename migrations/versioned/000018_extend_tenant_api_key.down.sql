-- Migration 000018 DOWN: Revert tenant api_key column to varchar(64)
ALTER TABLE tenants ALTER COLUMN api_key TYPE VARCHAR(64) USING LEFT(api_key, 64);
