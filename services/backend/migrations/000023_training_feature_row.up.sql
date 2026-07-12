-- Owner: mlops module (internal/mlops). One clean labeled training row per
-- completed audit, written best-effort by the audit orchestrator through an
-- in-process port. No migration seeds rows.
--
-- quality_ok=false rows ARE stored (auditability / admin review) but excluded
-- from the training export by default. A feature the platform did not report is
-- stored as JSON null inside features_jsonb, never zero-filled — LightGBM
-- consumes it as native missing/NaN. The whole pipeline is gated on real data:
-- below the data floor nothing trains and nothing is promoted, and the serving
-- registry stays on the honest cold-start 'heuristic' state.
--
-- fraud_label is backfilled on a dispute decision (admin.ResolveDispute →
-- TrainingLabelSink port → UPDATE ... WHERE audit_job_id), so it is nullable and
-- absent at capture. reach_label is set at capture only when a real Instagram
-- Insights reach figure was pulled, else left null. A row may carry neither
-- target.

CREATE TABLE training_feature_row (
    audit_job_id             uuid PRIMARY KEY REFERENCES audit_job(id) ON DELETE CASCADE,
    influencer_id            uuid NOT NULL,
    platform                 text NOT NULL,          -- primary platform of the vector
    features_jsonb           jsonb NOT NULL,         -- the frozen feature vector

    -- Multiple independent model targets. Either may be null; a row can lack both.
    fraud_label              boolean,                -- supervised fraud target, backfilled on dispute decision
    fraud_label_source       text,                   -- 'dispute_rejected' | 'dispute_upheld' | NULL
    reach_label              bigint,                 -- real reach from OAuth insights (median reached accounts)
    reach_label_source       text,                   -- 'instagram_insights' | NULL

    -- Anti-gaming / data-quality verdict, computed at capture from the CURRENT
    -- champion's fraud estimate plus the snapshot. The audit still ran; this only
    -- affects whether the row is used for training.
    quality_ok               boolean NOT NULL,
    quality_reasons          text[] NOT NULL DEFAULT '{}',

    model_version_at_capture text NOT NULL,          -- champion version that produced the fraud sub-vector
    verification_tier        text NOT NULL,          -- 'verified' | 'estimated' | 'unverified'
    captured_at              timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX training_feature_row_captured_at ON training_feature_row (captured_at);
CREATE INDEX training_feature_row_clean       ON training_feature_row (captured_at) WHERE quality_ok;
CREATE INDEX training_feature_row_fraud_label ON training_feature_row (captured_at) WHERE fraud_label IS NOT NULL;
CREATE INDEX training_feature_row_reach_label ON training_feature_row (captured_at) WHERE reach_label IS NOT NULL;

CREATE TRIGGER trg_training_feature_row_set_updated_at BEFORE UPDATE ON training_feature_row
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
