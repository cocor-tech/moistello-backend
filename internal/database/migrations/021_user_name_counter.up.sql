CREATE TABLE IF NOT EXISTS user_name_counter (
    id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    value BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT single_row CHECK (id = 1)
);

INSERT INTO user_name_counter (id, value) VALUES (1, 0) ON CONFLICT DO NOTHING;
