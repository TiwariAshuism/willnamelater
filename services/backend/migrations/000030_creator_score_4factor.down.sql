-- Reverse the 4-factor reshape: reactivate the v1 (5-factor influence) weights
-- and drop the new typed columns.
--
-- NOTE: the quarantine of score.audience_quality (step 3 of the up migration) is
-- ONE-WAY. The follower-count reach values that column held were set to NULL and
-- are not recoverable, exactly as migration 000026's quarantine of the fabricated
-- fraud columns could not be undone. This down migration restores the schema and
-- the active weight set, not the withdrawn values.

-- Reactivate v1 weights, deactivate v2 (order respects the one-active index).
UPDATE scoring_weights SET active = false
    WHERE niche = 'default' AND tier = 'default' AND version = 2;
UPDATE scoring_weights SET active = true
    WHERE niche = 'default' AND tier = 'default' AND version = 1;

-- Drop the columns this migration added. audience_quality predates 000030, so it
-- is left in place (its historical values were nulled and cannot be restored).
ALTER TABLE score DROP COLUMN IF EXISTS engagement_authenticity;
ALTER TABLE score DROP COLUMN IF EXISTS consistency;
ALTER TABLE score DROP COLUMN IF EXISTS brand_fit;

COMMENT ON COLUMN score.authenticity IS NULL;
COMMENT ON COLUMN score.engagement IS NULL;
COMMENT ON COLUMN score.content_quality IS NULL;
COMMENT ON COLUMN score.audience_quality IS NULL;
