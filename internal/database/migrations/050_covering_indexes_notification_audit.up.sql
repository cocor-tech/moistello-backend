-- Covering indexes for notification and audit queries

-- Notification queries filter by user_id and unread/archived state, and order by created_at DESC
CREATE INDEX IF NOT EXISTS idx_notifications_user_unread_created 
ON notifications (user_id, unread, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_notifications_user_archived_created 
ON notifications (user_id, archived, created_at DESC);

-- Audit log queries filter by resource_type, action, actor_id, and time range created_at
CREATE INDEX IF NOT EXISTS idx_audit_log_resource_action_created 
ON audit_log (resource_type, action, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_log_actor_created 
ON audit_log (actor_id, created_at DESC);
