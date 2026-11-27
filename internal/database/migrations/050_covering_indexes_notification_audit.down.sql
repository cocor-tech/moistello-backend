-- Rollback covering indexes for notification and audit queries

DROP INDEX IF EXISTS idx_notifications_user_unread_created;
DROP INDEX IF EXISTS idx_notifications_user_archived_created;
DROP INDEX IF EXISTS idx_audit_log_resource_action_created;
DROP INDEX IF EXISTS idx_audit_log_actor_created;
