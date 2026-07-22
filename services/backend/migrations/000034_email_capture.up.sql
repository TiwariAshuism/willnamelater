-- Owner: waitlist module (internal/waitlist).
--
-- Email capture for the two "return later" surfaces of the public funnel:
--
--   * B3 connect-wall: a visitor who reaches the OAuth wall but is not ready to
--     connect leaves an email so we can invite them back (source 'connect_wall').
--   * F1 media-kit waitlist: a creator asks to be told when the media-kit surface
--     ships (source 'mediakit').
--
-- Idempotent by construction: UNIQUE (email, source) means a visitor who submits
-- the same address on the same surface twice records one row, not two — the
-- service upserts with ON CONFLICT DO NOTHING and reports success either way.
-- Email is normalized (trimmed + lowercased) in the service before insert, so the
-- uniqueness is case-insensitive without needing the citext extension.
--
-- influencer_id is nullable and carries no FK: a capture may or may not be tied to
-- a creator profile, and an anonymous landing visitor has none. props holds any
-- additional non-sensitive context the surface sent. No IP, no device fingerprint.

CREATE TABLE email_capture (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    email         text NOT NULL CHECK (length(btrim(email)) > 0),

    -- The funnel surface the address was captured on. Constrained so an unknown
    -- source cannot be stored.
    source        text NOT NULL CHECK (source IN ('connect_wall', 'mediakit')),

    created_at    timestamptz NOT NULL DEFAULT now(),

    influencer_id uuid,
    props         jsonb,

    UNIQUE (email, source)
);
