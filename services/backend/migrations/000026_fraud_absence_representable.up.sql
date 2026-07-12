-- Owner: audit module (internal/audit) + mlops module (internal/mlops).
--
-- Make ABSENCE representable, and quarantine the rows captured while it was not.
--
-- WHAT WAS WRONG
--
-- fraud_result declared every signal `NOT NULL DEFAULT 0`. The schema therefore
-- could not express "we did not measure this" — only "we measured this and it was
-- zero". Three columns were, in production, never measurements at all:
--
--   * fake_follower_rate  = the composite risk score, divided by 100, RENAMED.
--     Nothing in this pipeline has ever fetched a follower list, so no
--     fake-follower rate has ever been computed. It was rendered to brands as
--     "Estimated fake-follower rate".
--
--   * bot_comment_rate    = a bit-for-bit DUPLICATE of clique_membership_fraction.
--     The comment classifier was never called from the audit path, so no comment's
--     text was ever examined. The report printed both numbers as adjacent rows,
--     manufacturing the appearance of two corroborating signals out of one.
--
--   * engagement_anomaly  = a structural constant 0. The scoring layer never passed
--     a sourced engagement benchmark down, and the ml service returns 0.0 without
--     one — so "Engagement anomaly: 0%" was printed as a clean bill of health for a
--     check that never ran.
--
-- The coordination columns were equally misleading: an audit that pulled no
-- comments (every Instagram and CSV audit) stored clique_count = 0 and
-- clique_membership_fraction = 0, which reads as "we analyzed the commenters and
-- found no coordination" rather than "we never looked at a single commenter".
--
-- These rows are also the intake of the training feature store, so the fabricated
-- zeros were being frozen into training data as if they were real observations.

-- 1. Drop the two columns that were never measurements.
ALTER TABLE fraud_result DROP COLUMN IF EXISTS fake_follower_rate;
ALTER TABLE fraud_result DROP COLUMN IF EXISTS bot_comment_rate;

-- 2. The honest composite: the ml service's per-account risk estimate (0-100).
--    Nullable, because an audit where no signal was observable has no risk score.
ALTER TABLE fraud_result ADD COLUMN risk_score double precision;

-- 3. Absence becomes representable: NULL = not measured, 0 = measured as zero.
ALTER TABLE fraud_result ALTER COLUMN engagement_anomaly DROP NOT NULL;
ALTER TABLE fraud_result ALTER COLUMN engagement_anomaly DROP DEFAULT;
ALTER TABLE fraud_result ALTER COLUMN clique_count DROP NOT NULL;
ALTER TABLE fraud_result ALTER COLUMN clique_count DROP DEFAULT;
ALTER TABLE fraud_result ALTER COLUMN clique_membership_fraction DROP NOT NULL;
ALTER TABLE fraud_result ALTER COLUMN clique_membership_fraction DROP DEFAULT;

-- 4. QUARANTINE the historical rows. Every existing value in these columns was
--    written by the code described above, so not one of them can be trusted to
--    mean what its name says. They are set to NULL rather than deleted: the row
--    still records that a fraud pass RAN for that audit (present), which is true
--    and worth keeping — only the fabricated measurements are withdrawn.
--
--    engagement_anomaly is nulled unconditionally: it was a structural constant.
--    The clique columns are nulled unconditionally too, because the schema cannot
--    distinguish a genuine measured 0 from an unobserved one — that is precisely
--    the defect — so keeping any of them would keep a value we cannot vouch for.
UPDATE fraud_result SET
    engagement_anomaly         = NULL,
    clique_count               = NULL,
    clique_membership_fraction = NULL;

-- 5. Quarantine the poisoned TRAINING rows. Every feature vector captured before
--    this migration was built from the columns above: it carries the renamed
--    risk score in a slot labelled "fake_follower_rate", a duplicated column
--    presented as independent evidence, and a constant 0 anomaly. A model trained
--    on it would learn a fabrication and, worse, would clear the G1-G5 gates —
--    the gates check the model against the labels, and cannot detect that the
--    FEATURES are fiction.
--
--    The rows are marked, not deleted: they are evidence of what happened, and the
--    audits they point at are real. The trainer must exclude them.
ALTER TABLE training_feature_row ADD COLUMN feature_order_version integer NOT NULL DEFAULT 1;
COMMENT ON COLUMN training_feature_row.feature_order_version IS
    'Version of training.features.FEATURE_ORDER this row was captured under. '
    'v1 rows contain fabricated columns (fake_follower_rate = renamed risk score; '
    'bot_comment_rate = duplicate of clique_membership_fraction; engagement_anomaly '
    'structurally 0) and MUST NOT be trained on. v2 is the honest vector.';

-- Existing rows are v1 by definition (the default above). New captures write v2.
-- The trainer filters on this, so the poisoned rows are inert rather than deleted.
CREATE INDEX training_feature_row_version_idx ON training_feature_row (feature_order_version);
