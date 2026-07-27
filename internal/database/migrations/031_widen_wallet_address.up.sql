-- Widen users.wallet_address for databases created before 001 was updated to
-- VARCHAR(128). The column must hold muxed Stellar addresses (69 characters),
-- so 128 matches the definition in 001_create_users.up.sql and this is a no-op
-- on databases already created at that width.
ALTER TABLE users ALTER COLUMN wallet_address TYPE VARCHAR(128);
