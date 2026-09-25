CREATE TABLE IF NOT EXISTS round_config_snapshots (
    id UUID PRIMARY KEY,
    circle_id UUID NOT NULL REFERENCES circles(id) ON DELETE CASCADE,
    round_number INT NOT NULL,
    config_hash VARCHAR(64) NOT NULL,
    config_json TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_circle_round_config UNIQUE (circle_id, round_number)
);

CREATE INDEX IF NOT EXISTS idx_round_config_snapshots_circle_round ON round_config_snapshots(circle_id, round_number);
