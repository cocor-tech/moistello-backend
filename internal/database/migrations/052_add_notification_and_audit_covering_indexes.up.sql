-- Issue #460: covering indexes for notification and audit queries.
--
-- notification/repository_pg.go (List) and notification/search.go (Search)
-- filter by user_id (plus optional is_read / is_archived / type predicates)
-- and always sort by created_at DESC. Existing indexes (idx_notifs_user on
-- (user_id, is_read) and idx_notifications_user_archived on (user_id,
-- is_archived)) omit created_at DESC and payload columns, forcing heap
-- fetches and Top-N sorts on every paginated notification request.

CREATE INDEX IF NOT EXISTS idx_notifications_user_created_covering
    ON notifications (user_id, created_at DESC)
    INCLUDE (id, type, title, is_read, is_archived, channel);

CREATE INDEX IF NOT EXISTS idx_notifications_user_unread_created_covering
    ON notifications (user_id, is_read, created_at DESC)
    INCLUDE (id, type, title, is_archived, channel);

CREATE INDEX IF NOT EXISTS idx_notifications_user_search_covering
    ON notifications (user_id, is_archived, is_read, type, created_at DESC)
    INCLUDE (id, title, channel);

-- audit/repository_pg.go (List) filters on optional resource_type, action,
-- actor_id, and created_at range bounds, and always orders by
-- (created_at DESC, id DESC). Covering indexes avoid heap lookups for the
-- index-only count and ordered key/metadata projection paths.

CREATE INDEX IF NOT EXISTS idx_audit_log_created_id_covering
    ON audit_log (created_at DESC, id DESC)
    INCLUDE (actor_id, action, resource_type, resource_id);

CREATE INDEX IF NOT EXISTS idx_audit_log_actor_created_covering
    ON audit_log (actor_id, created_at DESC, id DESC)
    INCLUDE (action, resource_type, resource_id);

CREATE INDEX IF NOT EXISTS idx_audit_log_resource_type_created_covering
    ON audit_log (resource_type, created_at DESC, id DESC)
    INCLUDE (actor_id, action, resource_id);

CREATE INDEX IF NOT EXISTS idx_audit_log_action_created_covering
    ON audit_log (action, created_at DESC, id DESC)
    INCLUDE (actor_id, resource_type, resource_id);
