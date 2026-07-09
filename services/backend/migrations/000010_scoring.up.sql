-- Owner: scoring module (internal/scoring).

CREATE TABLE scoring_weights (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    niche      text NOT NULL,
    tier       text NOT NULL,
    version    integer NOT NULL,
    weights    jsonb NOT NULL,   -- component -> weight
    active     boolean NOT NULL DEFAULT false,
    notes      text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (niche, tier, version)
);

-- At most one active weight set per (niche, tier).
CREATE UNIQUE INDEX scoring_weights_one_active
    ON scoring_weights (niche, tier)
    WHERE active;

CREATE TABLE benchmark (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    niche       text NOT NULL,
    tier        text NOT NULL,
    metric      text NOT NULL,
    version     integer NOT NULL,
    source      text NOT NULL CHECK (source IN ('bootstrap', 'corpus')),
    p10         double precision,
    p25         double precision,
    p50         double precision,
    p75         double precision,
    p90         double precision,
    mean        double precision,
    stddev      double precision,
    sample_size integer,
    active      boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (niche, tier, metric, version)
);

-- At most one active benchmark per (niche, tier, metric).
CREATE UNIQUE INDEX benchmark_one_active
    ON benchmark (niche, tier, metric)
    WHERE active;

CREATE TABLE score (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    audit_job_id           uuid NOT NULL REFERENCES audit_job(id) ON DELETE CASCADE,
    influencer_id          uuid REFERENCES influencer(id) ON DELETE SET NULL,
    overall                double precision NOT NULL,
    authenticity           double precision,
    engagement             double precision,
    audience_quality       double precision,
    content_quality        double precision,
    -- Version stamps make a score reproducible: they pin the exact weight set
    -- and benchmark generation used, independent of whichever rows are active
    -- now.
    weights_version        integer NOT NULL,
    benchmark_version      integer NOT NULL,
    -- The platforms that actually contributed data. A partial audit records the
    -- reduced set here so a consumer never reads the score as if it covered
    -- every requested platform.
    contributing_platforms platform[] NOT NULL DEFAULT '{}',
    breakdown              jsonb,
    created_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (audit_job_id)
);

CREATE INDEX score_influencer_id_idx ON score (influencer_id);

CREATE TABLE fraud_result (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    audit_job_id             uuid NOT NULL REFERENCES audit_job(id) ON DELETE CASCADE,
    fake_follower_pct        double precision,
    bot_comment_pct          double precision,
    engagement_anomaly_score double precision,
    verdict                  text CHECK (verdict IS NULL OR verdict IN ('clean', 'suspicious', 'fraudulent')),
    signals                  jsonb,
    model_version            text,
    created_at               timestamptz NOT NULL DEFAULT now(),
    UNIQUE (audit_job_id)
);
