CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE communities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    slug VARCHAR(120) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    category VARCHAR(50) NOT NULL DEFAULT 'community',
    tags TEXT[] NOT NULL DEFAULT '{}',
    avatar_url TEXT,
    banner_url TEXT,
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_count INTEGER NOT NULL DEFAULT 0,
    total_saved NUMERIC(20,7) NOT NULL DEFAULT 0,
    is_featured BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE community_members (
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(20) NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'moderator', 'member')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (community_id, user_id)
);

CREATE TABLE community_announcements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    author_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    is_pinned BOOLEAN NOT NULL DEFAULT false,
    like_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE community_activity_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    event_type VARCHAR(50) NOT NULL,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    target_id UUID,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_communities_slug ON communities(slug);
CREATE INDEX idx_communities_category ON communities(category);
CREATE INDEX idx_communities_owner ON communities(owner_id);
CREATE INDEX idx_community_members_user ON community_members(user_id);
CREATE INDEX idx_community_announcements_pinned ON community_announcements(community_id, is_pinned) WHERE is_pinned = true;
CREATE INDEX idx_community_activity_events ON community_activity_events(community_id, created_at DESC);
