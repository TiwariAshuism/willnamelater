-- Owner: influencer module (internal/influencer).

CREATE TABLE influencer (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name text,
    niche        text,   -- content category; keys scoring_weights and benchmark
    tier         text,   -- audience-size band; keys scoring_weights and benchmark
    country      text,   -- ISO 3166-1 alpha-2
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX influencer_niche_tier_idx ON influencer (niche, tier);

CREATE TRIGGER trg_influencer_set_updated_at BEFORE UPDATE ON influencer
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE influencer_handle (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    influencer_id    uuid NOT NULL REFERENCES influencer(id) ON DELETE CASCADE,
    platform         platform NOT NULL,
    handle           text NOT NULL,
    platform_user_id text,
    follower_count   bigint,
    verified         boolean NOT NULL DEFAULT false,
    last_seen_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (platform, handle)
);

-- A stable external id is unique per platform when the connector supplies one.
CREATE UNIQUE INDEX influencer_handle_platform_user_key
    ON influencer_handle (platform, platform_user_id)
    WHERE platform_user_id IS NOT NULL;
CREATE INDEX influencer_handle_influencer_id_idx ON influencer_handle (influencer_id);

CREATE TRIGGER trg_influencer_handle_set_updated_at BEFORE UPDATE ON influencer_handle
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
