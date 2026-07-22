-- Owner: scoring module (internal/scoring).
--
-- Reshape the composite from the retired 5-factor INFLUENCE score to the
-- CreatorTrust 4-factor HIREABILITY score (PRD §6):
--
--   0.30 Engagement Authenticity + 0.30 Audience Quality +
--   0.20 Consistency & Reliability + 0.20 Brand-Fit Clarity
--
-- Follower-count "reach" — the single heaviest term in the old composite — is
-- DROPPED entirely. It was computed from follower count, the exact number
-- purchased followers inflate, so buying followers RAISED an account's score on
-- the dimension the product exists to police. It survives only as the tier that
-- bands metrics; it never adds points.

-- 1. Add the four honest typed factor columns. They are nullable: a factor the
--    audit could not evidence stays SQL NULL, never a fabricated zero. The typed
--    columns are analytics parity only — the API reconstructs factors from the
--    breakdown jsonb — but they must still say the honest thing.
--    audience_quality already exists (see step 3); the other three are new.
ALTER TABLE score ADD COLUMN IF NOT EXISTS engagement_authenticity double precision;
ALTER TABLE score ADD COLUMN IF NOT EXISTS consistency             double precision;
ALTER TABLE score ADD COLUMN IF NOT EXISTS brand_fit               double precision;

-- 2. The retired influence columns (authenticity, engagement, content_quality)
--    are KEPT so historical rows still read, but are no longer written. Mark them.
COMMENT ON COLUMN score.authenticity IS
    'DEPRECATED (v1 influence composite). No longer written as of migration 000030; '
    'the authenticity sub-signal now rides in breakdown.authenticity_signal. '
    'Historical values retained for provenance.';
COMMENT ON COLUMN score.engagement IS
    'DEPRECATED (v1 influence composite). No longer written as of migration 000030. '
    'Engagement is now a sub-signal of the engagement_authenticity factor.';
COMMENT ON COLUMN score.content_quality IS
    'DEPRECATED (v1 influence composite). No longer written as of migration 000030. '
    'Interaction depth is now a sub-signal of the engagement_authenticity factor.';

-- 3. QUARANTINE the poisoned audience_quality column. Following the 000026
--    precedent: every existing value in this column is NOT audience quality — the
--    mapper stored the follower-count "reach" value here (its closest v1 slot), so
--    the number means something different from what the column now names. It is set
--    to NULL, not deleted: the row (a real audit) stays; only the mislabelled
--    measurement is withdrawn. Going forward the column genuinely holds Audience
--    Quality (PRD §6 factor 2, from wired demographics).
UPDATE score SET audience_quality = NULL;
COMMENT ON COLUMN score.audience_quality IS
    'Audience Quality factor (PRD §6). As of migration 000030 this column was '
    'quarantined (set NULL) because it previously stored the follower-count reach '
    'value under this name — a v1 mis-store. Historical NULL = "not recoverable".';

-- 4. Weight-set transition. Deactivate the active v1 (5-factor) weights for the
--    baseline cell and install a v2 (4-factor) active row. The partial unique
--    index scoring_weights_one_active permits one active row per (niche, tier), so
--    the deactivate must precede the activate. Reproducibility of scores stamped
--    weights_version = 1 is preserved: the v1 row is kept, only made inactive.
UPDATE scoring_weights SET active = false
    WHERE niche = 'default' AND tier = 'default' AND active;

INSERT INTO scoring_weights (niche, tier, version, weights, active, notes)
VALUES ('default', 'default', 2,
        '{"engagement_authenticity":0.30,"audience_quality":0.30,"consistency_reliability":0.20,"brand_fit_clarity":0.20}'::jsonb,
        true, 'creator-score 4-factor v2')
ON CONFLICT (niche, tier, version) DO UPDATE
    SET active  = true,
        weights = EXCLUDED.weights,
        notes   = EXCLUDED.notes;
