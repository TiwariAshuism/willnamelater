-- Reverse of 000027. Dropping these columns re-opens the defect: every decision
-- becomes an unqualified "the admin agreed with the heuristic" again, and the
-- training export can no longer tell an observed label from an echo.
DROP INDEX IF EXISTS dispute_label_evidence_idx;
ALTER TABLE dispute DROP CONSTRAINT IF EXISTS dispute_decided_has_evidence;
ALTER TABLE dispute DROP COLUMN IF EXISTS score_shown_to_admin;
ALTER TABLE dispute DROP COLUMN IF EXISTS label_evidence;
