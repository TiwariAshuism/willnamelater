-- Owner: audit module (internal/audit).
--
-- Persist the per-audit comment-quality summary produced by the display-only
-- comment classifier. This is a DISPLAY signal, never a score input: the ML
-- service's firewall (services/ml/app/features/fraud_vector.py, _QUARANTINED_KEYS)
-- forbids the classifier's output from entering the fraud vector or the 0-100
-- composite until its weight is fitted against real dispute outcomes. The report
-- renders it as a pill with its denominator and caveats; nothing here feeds
-- scoring.
--
-- HONESTY: low_quality_ratio is NULLABLE. The ML service suppresses it below its
-- minimum sample (MIN_RATE_SAMPLE): the classifier is an 18-phrase English rule
-- set with an unmeasured error rate that mislabels non-English comment sections,
-- so a percentage over a handful of comments asserts a precision nobody has. NULL
-- means "not enough sample to state a rate", never 0.
CREATE TABLE comment_quality (
    audit_job_id      uuid PRIMARY KEY REFERENCES audit_job(id) ON DELETE CASCADE,
    -- present is false when no comments were available to classify at all (the
    -- common Instagram case before the manage_comments scope, and every CSV audit).
    present           boolean NOT NULL,
    analyzed_count    integer NOT NULL DEFAULT 0,
    low_quality_count integer NOT NULL DEFAULT 0,
    -- NULL below the ML service's minimum sample; a real fraction otherwise.
    low_quality_ratio double precision,
    sufficient_sample boolean NOT NULL DEFAULT false,
    -- counts is the per-bucket tally (genuine/generic/emoji_only/duplicate).
    counts            jsonb,
    -- rate_key names the quarantined signal ("generic_comment_rate_v1"), recorded
    -- so a future fitted model can find these rows; it is NOT a fraud feature.
    rate_key          text,
    model_version     text,
    created_at        timestamptz NOT NULL DEFAULT now()
);
