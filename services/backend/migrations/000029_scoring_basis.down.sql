-- Owner: scoring module (internal/scoring).
--
-- Deliberately a no-op. The up migration removed a fabricated sample count from
-- bootstrap benchmark rows; there is nothing to restore, because the number it
-- removed was never a measurement of anything. Writing it back would be
-- re-fabricating it.
SELECT 1;
