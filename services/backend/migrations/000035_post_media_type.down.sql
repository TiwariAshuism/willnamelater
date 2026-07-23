-- Owner: metrics module (internal/metrics).
--
-- Intentionally a no-op. post.media_type is owned by the base content schema
-- (000009); this migration only guarantees the column's presence with an
-- IF NOT EXISTS guard. Rolling it back must NOT drop a column another migration
-- created, so there is nothing to undo here.
SELECT 1;
