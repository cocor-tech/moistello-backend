ALTER TABLE circles ADD COLUMN IF NOT EXISTS community_id UUID REFERENCES communities(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_circles_community ON circles(community_id);
