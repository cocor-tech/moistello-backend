-- Backfill: hash any remaining plaintext emails (those containing '@') using
-- the canonical SHA-256 hex transform that matches user.HashEmail() in Go.
-- After this migration every email stored in users.email is a 64-char hex
-- digest and FindByEmail / UpdateProfile / Register are all consistent.
UPDATE users
SET email = encode(sha256(email::bytea), 'hex')
WHERE email LIKE '%@%';
