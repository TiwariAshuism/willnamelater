-- Owner: admin module (internal/admin) -- the dispute-resolution label.
--
-- WHAT WAS WRONG
--
-- The "fraud_label" this table feeds into the supervised trainer is NOT ground
-- truth. It is a human ratification of the heuristic:
--
--   * A dispute exists ONLY because the heuristic flagged the account. The label
--     is therefore conditioned on the heuristic's own decision.
--   * 'rejected' (the positive class) means nothing more than "an admin declined
--     to overturn the flag."
--   * ResolveDispute recorded a bare decision + free-text notes. No artifact, no
--     evidence field. Free text cannot gate a training fold.
--   * The adjudicator's screen renders the heuristic's OWN output (risk score,
--     clique figures) as the primary evidence, so the admin is being asked to
--     agree with the model while looking at the model's answer.
--
-- A model fit on that is: heuristic outputs -> predict whether a human agreed
-- with the heuristic. It cannot learn anything the heuristic does not already
-- assert, and the G0-G5 gates cannot see the problem — they check model-vs-labels
-- and assume the labels are real.
--
-- THE FIX: a decision with NO external observation must never become y.

-- 1. A closed set of evidence kinds. CHECK-constrained, never free text: the
--    trainer filters folds on this column, so it must be an enum a query can
--    trust, not prose an admin typed.
--
--    'none_reviewed_heuristic_only' is the honest, first-class answer for the
--    common case — the admin looked at the flag and agreed. It is a REAL admin
--    decision and the dispute outcome still matters to the customer, so the row
--    is kept. It is simply never exported as a training label, because there is
--    no observation in it that the heuristic did not already make.
ALTER TABLE dispute ADD COLUMN label_evidence text
    CHECK (label_evidence IN (
        'platform_enforcement_action',
        'creator_admission',
        'purchase_receipt_or_panel_invoice',
        'brand_campaign_conversion_data',
        'manual_follower_sample_audit',
        'none_reviewed_heuristic_only'
    ));

COMMENT ON COLUMN dispute.label_evidence IS
    'What the adjudicator actually OBSERVED, outside the heuristic''s own output. '
    'Rows with none_reviewed_heuristic_only are excluded from the training-label '
    'export (GET /v1/admin/training/labels): they are heuristic echoes, not labels.';

-- 2. Evidence-blind adjudication. Whether the composite score and its flags were
--    shown to the adjudicator is now a stored fact about the decision. Without it
--    the circularity of any given label is unauditable — and indefensible in a
--    deposition.
--
--    Existing rows are backfilled TRUE, because they were: the dispute review
--    screen has always rendered the heuristic's risk score. The column DEFAULT is
--    then flipped to false, so the review flow now defaults to HIDING the score
--    and a reveal has to be an explicit, recorded act.
ALTER TABLE dispute ADD COLUMN score_shown_to_admin boolean NOT NULL DEFAULT true;
ALTER TABLE dispute ALTER COLUMN score_shown_to_admin SET DEFAULT false;

COMMENT ON COLUMN dispute.score_shown_to_admin IS
    'True when the heuristic composite score/flags were disclosed to the adjudicator '
    'before they decided. Set by the explicit reveal endpoint, never by the client.';

-- 3. QUARANTINE every decision already on the table. Not one of them recorded an
--    observation: the field did not exist, and the score was on screen. They are
--    heuristic echoes by construction, so they are marked as such rather than
--    deleted — the dispute outcomes are real and the customers are owed them; only
--    their standing as training labels is withdrawn.
UPDATE dispute
   SET label_evidence = 'none_reviewed_heuristic_only'
 WHERE status IN ('resolved', 'rejected')
   AND label_evidence IS NULL;

-- 4. A decided dispute must state its evidence. Open/under_review rows have not
--    been adjudicated yet and carry NULL.
ALTER TABLE dispute ADD CONSTRAINT dispute_decided_has_evidence
    CHECK (status IN ('open', 'under_review') OR label_evidence IS NOT NULL);

-- The trainer's fold filter reads this column on every export.
CREATE INDEX dispute_label_evidence_idx ON dispute (label_evidence);
