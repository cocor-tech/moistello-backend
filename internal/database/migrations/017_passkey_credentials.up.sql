CREATE TABLE IF NOT EXISTS passkey_credentials (
    credential_id TEXT PRIMARY KEY,
    public_key BYTEA NOT NULL,
    counter INTEGER NOT NULL DEFAULT 0,
    transports TEXT[],
    email_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_passkey_cred_id ON passkey_credentials(credential_id);
