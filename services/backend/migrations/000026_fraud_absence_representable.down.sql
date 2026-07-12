-- Reverse of 000026. The quarantined values are NOT restored: they were
-- fabrications, and this migration will not resurrect them. The columns come back
-- with their old NOT NULL DEFAULT 0 shape (which is why they were wrong), holding
-- zeros — so a rollback restores the SCHEMA, not the fiction.
DROP INDEX IF EXISTS training_feature_row_version_idx;
ALTER TABLE training_feature_row DROP COLUMN IF EXISTS feature_order_version;

ALTER TABLE fraud_result DROP COLUMN IF EXISTS risk_score;

UPDATE fraud_result SET
    engagement_anomaly         = 0,
    clique_count               = 0,
    clique_membership_fraction = 0
WHERE engagement_anomaly IS NULL
   OR clique_count IS NULL
   OR clique_membership_fraction IS NULL;

ALTER TABLE fraud_result ALTER COLUMN engagement_anomaly SET DEFAULT 0;
ALTER TABLE fraud_result ALTER COLUMN engagement_anomaly SET NOT NULL;
ALTER TABLE fraud_result ALTER COLUMN clique_count SET DEFAULT 0;
ALTER TABLE fraud_result ALTER COLUMN clique_count SET NOT NULL;
ALTER TABLE fraud_result ALTER COLUMN clique_membership_fraction SET DEFAULT 0;
ALTER TABLE fraud_result ALTER COLUMN clique_membership_fraction SET NOT NULL;

ALTER TABLE fraud_result ADD COLUMN fake_follower_rate double precision NOT NULL DEFAULT 0;
ALTER TABLE fraud_result ADD COLUMN bot_comment_rate double precision NOT NULL DEFAULT 0;
