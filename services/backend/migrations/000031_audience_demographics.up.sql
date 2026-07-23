-- Owner: metrics module (internal/metrics).
--
-- Persist audience demographics (age, gender, country) pulled from a connected
-- account. The Meta connector already fetches these into Snapshot.Audience; this
-- table is where the audit worker's ingest lands them, for the result-page
-- audience snapshot (PRD §8.2) and future corpus aggregation. The Audience
-- Quality factor itself reads the in-memory distribution during scoring, not this
-- table.
--
-- HONESTY: only OBSERVED buckets get a row. Absence is the lack of a row, never a
-- zero-filled bucket — the same principle migration 000026 enforced for fraud.
-- The connector leaves Audience nil below Meta's 100-follower threshold, so no row
-- is ever written for an account that is too small to have real demographics.

CREATE TABLE audience_demographic (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    influencer_id uuid NOT NULL REFERENCES influencer(id) ON DELETE CASCADE,
    audit_job_id  uuid NOT NULL REFERENCES audit_job(id)  ON DELETE CASCADE,
    platform      platform NOT NULL,
    -- The demographic axis. Constrained so an unexpected axis is a loud failure,
    -- not a silently-stored row nothing knows how to read.
    dimension     text NOT NULL CHECK (dimension IN ('age', 'gender', 'country')),
    -- The bucket label within the dimension: an ISO-3166 alpha-2 code for country,
    -- an age band like '18-24', or a gender label. Stored verbatim from the
    -- connector's normalized distribution.
    bucket        text NOT NULL,
    -- The audience fraction in [0,1]. Buckets within a dimension sum to at most 1
    -- (a platform may report only its top segments).
    fraction      double precision NOT NULL CHECK (fraction >= 0 AND fraction <= 1),
    captured_at   timestamptz NOT NULL,
    -- One row per (influencer, platform, audit, dimension, bucket); re-ingesting an
    -- audit refreshes rather than duplicates.
    UNIQUE (influencer_id, platform, audit_job_id, dimension, bucket)
);

CREATE INDEX audience_demographic_influencer_idx ON audience_demographic (influencer_id);
CREATE INDEX audience_demographic_audit_idx ON audience_demographic (audit_job_id);
