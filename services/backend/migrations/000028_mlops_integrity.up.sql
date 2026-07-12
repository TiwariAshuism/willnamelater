-- Owner: mlops module (internal/mlops). Integrity migration: it closes the paths
-- by which a hand-typed number could have entered the one artifact whose job is
-- to detect fabrication (the canary set), the path by which a shadow prediction
-- could never be joined back to an outcome, and the path by which the training
-- export censored its own positive class.
--
-- Nothing here seeds a row. Two statements DELETE / NULL data, deliberately:
-- every row they remove is a row whose provenance cannot be established after the
-- fact, and an unprovenanced ground truth is worse than none.

-- --- A. canary provenance -------------------------------------------------
-- Every pre-existing canary was inserted with a CLIENT-SUPPLIED feature vector
-- and a FREE-TEXT source. Neither can be reconstructed as evidence now: there is
-- no audit to trace the vector back to. They are deleted rather than
-- grandfathered — a canary that may have been hand-typed cannot be allowed to
-- bless a challenger. (In every environment this table is empty: no migration has
-- ever seeded it and the endpoint is admin-only.)
DELETE FROM ml_canary_account;

ALTER TABLE ml_canary_account
    -- The canary is now ANCHORED to a real audit. The server copies the frozen
    -- feature vector from that audit's training_feature_row; the client never
    -- supplies features again.
    ADD COLUMN audit_job_id uuid NOT NULL REFERENCES audit_job(id) ON DELETE RESTRICT,
    -- provenance_kind replaces the free-text 'source' prose with a closed set. Note
    -- what is NOT in it: "an admin looked at it and it seemed fine". No evidence
    -- proves ABSENCE of fraud, so a clean canary is 'presumed_clean' — a stated
    -- basis, never a verified negative.
    ADD COLUMN provenance_kind text NOT NULL,
    DROP COLUMN source;

ALTER TABLE ml_canary_account
    ADD CONSTRAINT ml_canary_account_provenance_kind_check CHECK (provenance_kind IN (
        'oauth_insights_measured',        -- a measured figure from an OAuth Insights pull (reach canaries)
        'platform_enforcement_record',    -- the platform itself acted on the account
        'creator_admission',              -- the creator admitted the purchase
        'vendor_receipt',                 -- a receipt from the follower/engagement vendor
        'operator_constructed_positive',  -- WE bought the followers, so we know
        'presumed_clean'                  -- the stated basis for a negative canary; NOT proof
    )),
    ADD CONSTRAINT ml_canary_account_one_per_audit UNIQUE (model_name, audit_job_id),
    -- A negative (clean) fraud canary may ONLY be presumed_clean. There is no
    -- observation that establishes the absence of a follower purchase.
    ADD CONSTRAINT ml_canary_account_no_verified_negative CHECK (
        expected_label IS DISTINCT FROM false OR provenance_kind = 'presumed_clean'),
    -- A positive fraud canary must rest on evidence someone could actually observe.
    ADD CONSTRAINT ml_canary_account_positive_needs_evidence CHECK (
        expected_label IS DISTINCT FROM true OR provenance_kind IN (
            'platform_enforcement_record', 'creator_admission',
            'vendor_receipt', 'operator_constructed_positive'));

-- --- C. the shadow log must be joinable back to outcomes ------------------
-- audit_job_id was nullable and features_hash is a one-way sha256, so a shadow
-- row logged without the correlation id was PERMANENTLY unresolvable: no join to
-- an outcome, ever. Such rows can never become labelled evidence, so they are
-- removed and the column is made mandatory going forward.
DELETE FROM ml_prediction_log
    WHERE audit_job_id IS NULL
       OR NOT EXISTS (SELECT 1 FROM audit_job aj WHERE aj.id = ml_prediction_log.audit_job_id);

ALTER TABLE ml_prediction_log
    ALTER COLUMN audit_job_id SET NOT NULL,
    ADD CONSTRAINT ml_prediction_log_audit_job_fk
        FOREIGN KEY (audit_job_id) REFERENCES audit_job(id) ON DELETE CASCADE;

CREATE INDEX ml_prediction_log_audit_job ON ml_prediction_log (audit_job_id);

-- --- D. the training export must not censor its own positive class --------
-- The fraud-risk quality reason is derived from our OWN heuristic's output. It
-- excluded exactly the accounts that later get disputed and labelled POSITIVE, so
-- y=1 could accrue at ~0/week forever. A HUMAN-LABELED row is ground truth: no
-- fraud-score-derived filter may overrule it. Every other reason (too new, too
-- few posts, follower spike, no estimate) is an OBSERVED data-quality fact and
-- still applies.
--
-- The rule lives in the schema as a generated column so it holds for every reader
-- and updates itself the moment a dispute backfills fraud_label. It mirrors
-- model.TrainingEligible in Go (both name the reason code once).
-- The evidence kind the dispute adjudicator actually OBSERVED, carried onto the
-- feature row so the trainer can filter folds on it without joining back to the
-- admin module's table.
--
-- CRITICAL: the waiver below keys on THIS, not on fraud_label. Waiving the
-- fraud-risk exclusion for any row that merely HAS a label would hand-pick the
-- rows most likely to be heuristic echoes and feed them to the trainer as y —
-- the exact circularity the label evidence exists to prevent. A label with no
-- observation behind it is not a label; it is the heuristic agreeing with itself
-- through a human.
ALTER TABLE training_feature_row ADD COLUMN fraud_label_evidence text
    CHECK (fraud_label_evidence IN (
        'platform_enforcement_action',
        'creator_admission',
        'purchase_receipt_or_panel_invoice',
        'brand_campaign_conversion_data',
        'manual_follower_sample_audit',
        'none_reviewed_heuristic_only'
    ));

COMMENT ON COLUMN training_feature_row.fraud_label_evidence IS
    'What the adjudicator OBSERVED outside the heuristic''s own output. '
    'none_reviewed_heuristic_only (and NULL) are heuristic echoes: the row keeps its '
    'fraud_label for the customer-facing dispute outcome, but is NEVER training-eligible.';

ALTER TABLE training_feature_row
    ADD COLUMN training_eligible boolean GENERATED ALWAYS AS (
        cardinality(
            -- The fraud-risk exclusion is waived ONLY for a label backed by an
            -- OBSERVATION. NULL evidence and 'none_reviewed_heuristic_only' are not
            -- observations, so those rows stay excluded — an empty positive class is
            -- a shippable answer; a laundered one is not.
            CASE WHEN fraud_label IS NOT NULL
                  AND fraud_label_evidence IS NOT NULL
                  AND fraud_label_evidence <> 'none_reviewed_heuristic_only'
                 THEN array_remove(quality_reasons, 'fraud_risk_estimate_high')
                 ELSE quality_reasons
            END
        ) = 0
    ) STORED;

CREATE INDEX training_feature_row_trainable ON training_feature_row (captured_at) WHERE training_eligible;

-- --- A/E. what the audit actually SAW ------------------------------------
-- snapshot_sources records the concrete data paths behind the row ('instagram-graph',
-- 'csv', 'youtube-api', 'provider'). It exists so the canary endpoint can REFUSE an
-- audit that contains a creator-uploaded CSV: with Instagram gated on app review,
-- CSV is the only Instagram path, so a hand-typed export could otherwise be
-- laundered through the audit pipeline into a canary. An empty array means the row
-- predates this column — unknown provenance, which the canary endpoint also refuses
-- (absence is not evidence).
ALTER TABLE training_feature_row
    ADD COLUMN snapshot_sources text[] NOT NULL DEFAULT '{}',
    -- reach_is_organic states whether reach_label EXCLUDES ad-delivered reach.
    -- Insights `reach` on a boosted post includes reach the account PAID for;
    -- training on it teaches the model "ad spend = organic virality". NULL means
    -- the split is unknown — and unknown is not "organic".
    ADD COLUMN reach_is_organic boolean;

-- E. Pre-existing reach labels carry FORGED provenance: the service stamped
-- reach_label_source = 'instagram_insights' for any caller that set a reach
-- integer, without ever inspecting where it came from, and nothing recorded
-- whether the figure was organic. That is not recoverable after the fact, and
-- "estimate the organic portion" is not an option, so the labels are dropped.
UPDATE training_feature_row
    SET reach_label = NULL, reach_label_source = NULL
    WHERE reach_label IS NOT NULL OR reach_label_source IS NOT NULL;

ALTER TABLE training_feature_row
    -- The column is now a closed set, not a constant. Only a live Meta/Instagram
    -- Graph pull can claim measured reach provenance.
    ADD CONSTRAINT training_feature_row_reach_source_check CHECK (
        reach_label_source IS NULL OR reach_label_source = 'instagram_graph_insights'),
    -- A reach label may exist ONLY with a real source AND an explicit organic
    -- statement. No source or unknown organic split => no label.
    ADD CONSTRAINT training_feature_row_reach_label_provenance CHECK (
        reach_label IS NULL OR (reach_label_source IS NOT NULL AND reach_is_organic IS TRUE));
