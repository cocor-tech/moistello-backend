CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE swap_offers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    circle_id UUID NOT NULL REFERENCES circles(id) ON DELETE CASCADE,
    offeror_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    offeree_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    offeror_asset VARCHAR(20) NOT NULL,
    offeror_amount BIGINT NOT NULL CHECK (offeror_amount > 0),
    requested_asset VARCHAR(20) NOT NULL,
    requested_amount BIGINT NOT NULL CHECK (requested_amount > 0),
    status VARCHAR(20) NOT NULL DEFAULT 'created',
    transaction_hash VARCHAR(100),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_swap_offers_circle_id ON swap_offers(circle_id);
CREATE INDEX idx_swap_offers_offeror_user_id ON swap_offers(offeror_user_id);
CREATE INDEX idx_swap_offers_offeree_user_id ON swap_offers(offeree_user_id);
CREATE INDEX idx_swap_offers_status ON swap_offers(status);
CREATE INDEX idx_swap_offers_expires_at ON swap_offers(expires_at);