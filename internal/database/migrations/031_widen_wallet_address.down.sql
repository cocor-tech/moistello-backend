-- Intentionally a no-op. Narrowing wallet_address back to its former width would
-- fail on any row holding a muxed Stellar address, so this migration is not
-- reversible; leaving the column wide is harmless.
SELECT 1;
