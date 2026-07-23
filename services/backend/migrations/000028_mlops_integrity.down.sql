-- Reverse of 000028_mlops_integrity. It restores the SHAPE of the old columns; it
-- cannot restore the rows the up migration deleted (unprovenanced canaries and
-- unresolvable shadow predictions) or the reach labels it dropped, and it must
-- not invent them.

ALTER TABLE training_feature_row
    DROP CONSTRAINT IF EXISTS training_feature_row_reach_label_provenance,
    DROP CONSTRAINT IF EXISTS training_feature_row_reach_source_check;

DROP INDEX IF EXISTS training_feature_row_trainable;

ALTER TABLE training_feature_row
    DROP COLUMN IF EXISTS reach_is_organic,
    DROP COLUMN IF EXISTS snapshot_sources,
    DROP COLUMN IF EXISTS training_eligible;

DROP INDEX IF EXISTS ml_prediction_log_audit_job;

ALTER TABLE ml_prediction_log
    DROP CONSTRAINT IF EXISTS ml_prediction_log_audit_job_fk,
    ALTER COLUMN audit_job_id DROP NOT NULL;

ALTER TABLE ml_canary_account
    DROP CONSTRAINT IF EXISTS ml_canary_account_positive_needs_evidence,
    DROP CONSTRAINT IF EXISTS ml_canary_account_no_verified_negative,
    DROP CONSTRAINT IF EXISTS ml_canary_account_one_per_audit,
    DROP CONSTRAINT IF EXISTS ml_canary_account_provenance_kind_check;

ALTER TABLE ml_canary_account
    ADD COLUMN source text NOT NULL DEFAULT '',
    DROP COLUMN IF EXISTS provenance_kind,
    DROP COLUMN IF EXISTS audit_job_id;
