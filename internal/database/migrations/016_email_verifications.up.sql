CREATE TABLE email_verifications (
    id              UUID PRIMARY KEY,
    email           VARCHAR(255) NOT NULL,
    code_hash       VARCHAR(64) NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    attempts        INT NOT NULL DEFAULT 0,
    max_attempts    INT NOT NULL DEFAULT 5,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ev_email ON email_verifications(email);
CREATE INDEX idx_ev_expires_at ON email_verifications(expires_at);

CREATE TABLE user_emails (
    email           VARCHAR(255) PRIMARY KEY,
    email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION update_ev_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ev_updated_at
    BEFORE UPDATE ON email_verifications
    FOR EACH ROW EXECUTE FUNCTION update_ev_updated_at();
