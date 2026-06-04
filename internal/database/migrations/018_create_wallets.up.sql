CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    public_key VARCHAR(56) NOT NULL UNIQUE,
    encrypted_secret_key BYTEA NOT NULL,
    encryption_nonce BYTEA NOT NULL,
    wallet_type VARCHAR(20) NOT NULL DEFAULT 'auto',
    is_primary BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, wallet_type)
);

CREATE INDEX idx_wallets_user_id ON wallets(user_id);
CREATE INDEX idx_wallets_public_key ON wallets(public_key);
