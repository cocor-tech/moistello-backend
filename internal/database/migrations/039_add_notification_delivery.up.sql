-- Issue #191: email/SMS/push delivery channels, user preference gating, and
-- a delivery audit trail. In-app notifications already worked; this adds
-- what the other three channels need.

-- users.notification_channels/notifications_muted (migration 028) already
-- gate *whether* to attempt email/SMS/push. The push channel additionally
-- needs to know *where* to push to — there was no device token storage at
-- all, so push could never have worked regardless of channel plumbing.
-- Registering this (client SDK integration) is a natural follow-up; this
-- column just gives the push channel somewhere to read from once that
-- exists.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS push_token TEXT;

-- Delivery audit trail (#191's "Delivery + retry + audit" criterion). One
-- row per (notification, channel) attempt — SMS/push/email are dispatched
-- outside the request/response cycle that creates the notification, so
-- without this there is no record of whether a channel actually delivered,
-- was skipped by preference gating, or failed.
CREATE TYPE notification_delivery_status AS ENUM ('sent', 'failed', 'skipped');

CREATE TABLE notification_deliveries (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    notification_id UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    channel         VARCHAR(20) NOT NULL,
    status          notification_delivery_status NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 1,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notification_deliveries_notification_id ON notification_deliveries(notification_id);
CREATE INDEX idx_notification_deliveries_status ON notification_deliveries(status);
