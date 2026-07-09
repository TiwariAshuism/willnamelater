-- Owner: connector module (internal/connector) -- normalized platform content.

CREATE TABLE post (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    influencer_id    uuid NOT NULL REFERENCES influencer(id) ON DELETE CASCADE,
    platform         platform NOT NULL,
    platform_post_id text NOT NULL,
    -- SET NULL: a post outlives the audit that first captured it.
    audit_job_id     uuid REFERENCES audit_job(id) ON DELETE SET NULL,
    posted_at        timestamptz,
    permalink        text,
    caption          text,
    media_type       text CHECK (media_type IS NULL OR media_type IN ('image', 'video', 'carousel', 'text', 'story', 'reel', 'short')),
    like_count       bigint,
    comment_count    bigint,
    share_count      bigint,
    view_count       bigint,
    engagement_rate  double precision,
    is_sponsored     boolean,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (platform, platform_post_id)
);

CREATE INDEX post_influencer_id_idx ON post (influencer_id);
CREATE INDEX post_audit_job_id_idx ON post (audit_job_id);
CREATE INDEX post_posted_at_idx ON post (posted_at);

CREATE TRIGGER trg_post_set_updated_at BEFORE UPDATE ON post
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE comment_sample (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id             uuid NOT NULL REFERENCES post(id) ON DELETE CASCADE,
    platform_comment_id text,
    author_handle       text,
    body                text,
    like_count          bigint,
    posted_at           timestamptz,
    is_spam             boolean,
    sentiment           double precision,   -- normalized to [-1, 1]
    language            text,               -- BCP 47 tag
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX comment_sample_post_id_idx ON comment_sample (post_id);
CREATE UNIQUE INDEX comment_sample_platform_id_key
    ON comment_sample (post_id, platform_comment_id)
    WHERE platform_comment_id IS NOT NULL;
