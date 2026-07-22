-- Owner: analytics module (internal/analytics).
--
-- The public acquisition funnel and its PRIMARY success metric — external share
-- opens by people who are NOT the creator — need a durable, append-only event
-- log. This table is that log: one row per first-party funnel event, recorded
-- either by the browser through the public POST /events ingest or server-side by
-- the report module when a public badge/handle page is read.
--
-- HONESTY / PRIVACY invariants baked into the shape:
--
--   * Absence is representable. Every attribution column is nullable: a landing
--     view has no influencer, an anonymous open has no session, an event the
--     client sent without a slug stores NULL rather than a fabricated value. We
--     record only what actually happened.
--
--   * No raw IP is ever stored. The only device signal is user_agent_hash — a
--     one-way hash of the User-Agent header, kept for coarse de-duplication, never
--     the raw string and never an address.
--
--   * Client-claimed identity is NOT trusted as referential truth. influencer_id
--     and audit_job_id carry NO foreign key on purpose: they are untrusted context
--     the client may send, and an append-only analytics signal must not 500 (or be
--     poisoned into referential coupling) because a browser posted a stale id. The
--     one field we DO trust — public_slug on a server-recorded share_open — is set
--     by the report module, not the client.
--
--   * is_owner is server-computed and nullable. The public read is unauthenticated,
--     so an open is attributed non-owner (false) when the reader is external, and
--     left NULL when ownership genuinely could not be determined — never guessed.

CREATE TABLE analytics_event (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The funnel stage this event marks. Constrained to the documented set so an
    -- unknown or client-invented type is rejected at ingest, keeping the log an
    -- analyzable enumeration rather than a free-text dumping ground.
    event_type   text NOT NULL CHECK (event_type IN (
                     'landing_view',
                     'connect_start',
                     'prerequisite_pass',
                     'oauth_grant',
                     'score_shown',
                     'share_open',
                     'mediakit_cta_click'
                 )),

    occurred_at  timestamptz NOT NULL DEFAULT now(),

    -- Untrusted context (see header): no FK, nullable.
    influencer_id uuid,
    audit_job_id  uuid,

    -- The published badge an open refers to. Server-set for share_open.
    public_slug  text,

    -- An anonymous, client-generated session id (not an auth session). Opaque.
    session_id   text,

    -- Server-computed owner-vs-external attribution; NULL when undeterminable.
    is_owner     boolean,

    referrer     text,

    -- One-way hash of the User-Agent header. Never the raw UA, never an IP.
    user_agent_hash text,

    -- Whatever additional non-sensitive context the client sent, as-is.
    props        jsonb
);

-- The funnel query: count events of a type over a time window.
CREATE INDEX analytics_event_type_time_idx ON analytics_event (event_type, occurred_at);

-- The share-open query: opens for a specific published badge.
CREATE INDEX analytics_event_slug_idx ON analytics_event (public_slug)
    WHERE public_slug IS NOT NULL;
