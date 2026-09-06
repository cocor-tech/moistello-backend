-- No automatic down-migration: once a plaintext secret has been hashed it
-- cannot be reversed.  Mark the migration as a no-op rollback.
SELECT 1;
