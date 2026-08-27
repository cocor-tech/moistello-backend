DROP INDEX IF EXISTS idx_notification_deliveries_status;
DROP INDEX IF EXISTS idx_notification_deliveries_notification_id;
DROP TABLE IF EXISTS notification_deliveries;
DROP TYPE IF EXISTS notification_delivery_status;

ALTER TABLE users
    DROP COLUMN IF EXISTS push_token;
