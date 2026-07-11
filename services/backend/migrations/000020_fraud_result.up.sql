-- Owner: audit module (internal/audit).
--
-- Per-audit fraud / coordination estimate. The orchestrator writes one row per
-- audit job after the ml fraud + clique pass, so the deliverable's coordination
-- headline and the dispute-labelling loop read a stored estimate rather than
-- re-running the models. clique_count (maximal commenter cliques of size >= 5)
-- is the primary coordination signal; the rates are advisory estimates, never
-- measured percentages.
--
-- present = false records that a fraud pass ran but produced no signal (for
-- example the ml service was unavailable), which is distinct from a job that
-- never reached the fraud step and therefore has no row at all.

CREATE TABLE fraud_result (
    audit_job_id               uuid PRIMARY KEY REFERENCES audit_job(id) ON DELETE CASCADE,
    present                    boolean NOT NULL DEFAULT false,
    fake_follower_rate         double precision NOT NULL DEFAULT 0,
    bot_comment_rate           double precision NOT NULL DEFAULT 0,
    engagement_anomaly         double precision NOT NULL DEFAULT 0,
    clique_count               integer NOT NULL DEFAULT 0,
    clique_membership_fraction double precision NOT NULL DEFAULT 0,
    confidence                 double precision NOT NULL DEFAULT 0,
    model_version              text NOT NULL DEFAULT '',
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_fraud_result_set_updated_at BEFORE UPDATE ON fraud_result
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
