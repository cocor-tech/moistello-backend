-- Backfill rows where secret_hash contains a plaintext secret instead of a
-- SHA-256 hex digest.  A valid digest is exactly 64 lowercase hex characters;
-- anything that does not match that pattern is treated as plaintext and re-hashed.
UPDATE webhooks
SET secret_hash = encode(sha256(secret_hash::bytea), 'hex')
WHERE secret_hash !~ '^[0-9a-f]{64}$';
