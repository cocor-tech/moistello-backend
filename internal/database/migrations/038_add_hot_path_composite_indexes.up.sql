-- Issue #206: composite indexes for hot query paths that existing
-- single-column indexes don't efficiently cover.

-- circle/repository_pg.go's FindByID/FindByContractID/List all embed
-- `(SELECT COUNT(*) FROM circle_members WHERE circle_id = circles.id AND
-- status = 'active')` as the member_count correlated subquery — run once per
-- row returned. idx_cm_circle (circle_id alone, migration 003) only lets
-- Postgres narrow to a circle's members; it still has to filter status
-- row-by-row. A composite index lets the same subquery be satisfied by an
-- index-only scan.
CREATE INDEX IF NOT EXISTS idx_circle_members_circle_status ON circle_members(circle_id, status);

-- circle/repository_pg.go's GetContributorsForRound-style lookup
-- (`SELECT user_id FROM contributions WHERE circle_id = $1 AND round_number
-- = $2`) filters on both columns together every round. The existing
-- UNIQUE(circle_id, user_id, round_number) index (migration 004) only helps
-- when user_id is also constrained, since round_number isn't a leftmost
-- prefix without it.
CREATE INDEX IF NOT EXISTS idx_contributions_circle_round ON contributions(circle_id, round_number);

-- swap/repository_pg.go's expiry sweep (`SELECT * FROM swap_offers WHERE
-- status = $1 AND expires_at < $2`) is a hot path for the background job
-- that expires stale swap offers. idx_swap_offers_status and
-- idx_swap_offers_expires_at (migration 035) are separate single-column
-- indexes; a composite matching this exact predicate avoids Postgres having
-- to bitmap-AND two index scans on every sweep.
CREATE INDEX IF NOT EXISTS idx_swap_offers_status_expires ON swap_offers(status, expires_at);

-- notifications(user_id, is_read) (migration 008) and incentives(user_id)
-- (migration 031) — both named in the issue — already exist and already
-- cover the query patterns in notification/repository_pg.go and
-- incentive/repository_pg.go; not duplicated here.
