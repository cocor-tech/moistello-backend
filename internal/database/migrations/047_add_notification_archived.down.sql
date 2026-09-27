DROP INDEX IF EXISTS idx_notifications_user_archived;
ALTER TABLE notifications DROP COLUMN IF EXISTS is_archived;
