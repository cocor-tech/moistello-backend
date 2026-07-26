ALTER TABLE users
    DROP COLUMN IF EXISTS notification_channels,
    DROP COLUMN IF EXISTS notifications_muted;
