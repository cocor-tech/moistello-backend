CREATE TABLE IF NOT EXISTS cookie_consents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    session_id  VARCHAR(128),               -- anonymous identifier for non-authenticated users
    analytics   BOOLEAN NOT NULL DEFAULT false,
    marketing   BOOLEAN NOT NULL DEFAULT false,
    ip_address  INET,
    user_agent  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A user can only have one consent record; on revisit we update it
CREATE UNIQUE INDEX idx_cookie_consents_user_id  ON cookie_consents(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX         idx_cookie_consents_session ON cookie_consents(session_id) WHERE session_id IS NOT NULL;
