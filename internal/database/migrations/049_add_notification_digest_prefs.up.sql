-- Issue #415: digest batching for high-frequency circle events.
--
-- Every circle event fanned out to all members immediately, so in a large
-- circle users were buried and muted everything — then missed the payout alert
-- that actually mattered. Non-urgent events now collapse into a periodic
-- per-user summary; urgent classes (payout, dispute, completed circle, or a
-- deadline under 24h) always bypass batching.
--
-- This migration only adds the *preference* storage. The batching itself is
-- applied in the notification domain, not in the database.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS digest_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- Cadence in minutes. The default of 1440 (24h) is also what the domain
-- substitutes when a user enables batching without naming an interval. The
-- column is left unconstrained at the DB level so the supportable range can
-- widen without a migration; the domain validates on write and treats an
-- out-of-range value as "use the default".
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS digest_interval_minutes INTEGER NOT NULL DEFAULT 1440;
