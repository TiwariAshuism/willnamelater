-- Owner: report module (internal/report).
--
-- The public badge is a FROZEN, deliberately-limited snapshot of a report,
-- served unauthenticated at /reports/{public_slug}. It is captured at publish
-- time — when the caller is authenticated and the full report is already
-- assembled — so the public read is a single row lookup that never touches
-- private data (the narrative, weakness/fix advice, or another user's audit).
-- Only non-sensitive fields (score, authenticity, niche, tier, benchmark label,
-- handle, generated-at) are stored here; nothing that would leak the private
-- advisory content or the account owner.
ALTER TABLE report ADD COLUMN badge_jsonb jsonb;

-- One current report per (audit, format): publishing is idempotent, so a
-- re-render overwrites the row (and keeps its public slug) rather than
-- accumulating history. The table was previously unused, so no existing rows
-- can violate this.
CREATE UNIQUE INDEX report_audit_format_key ON report (audit_job_id, format);
