CREATE TABLE IF NOT EXISTS treasury_fees (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    circle_id VARCHAR(255) NOT NULL,
    amount NUMERIC(18,7) NOT NULL,
    tx_hash VARCHAR(64) NOT NULL,
    ledger BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_treasury_fees_tx_circle UNIQUE (tx_hash, circle_id)
);

CREATE INDEX IF NOT EXISTS idx_treasury_fees_circle_id ON treasury_fees(circle_id);
CREATE INDEX IF NOT EXISTS idx_treasury_fees_created_at ON treasury_fees(created_at);
