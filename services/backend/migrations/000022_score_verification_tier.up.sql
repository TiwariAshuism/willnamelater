-- Owner: scoring module (internal/scoring).
--
-- The verification tier records how much a score can be trusted based on where
-- its data came from: 'verified' when every contributing platform was a live,
-- authenticated API pull (OAuth / API key), 'estimated' when any contributing
-- platform was an upload or a public-data provider, and 'unverified' when no
-- platform produced data. It is a first-class, queryable trust attribute (e.g.
-- "what share of audits are verified?"), so it is a real column with a closed
-- CHECK set rather than a field buried in the breakdown JSONB — mirroring how
-- contributing_platforms is a real column.
ALTER TABLE score ADD COLUMN verification_tier text NOT NULL DEFAULT 'unverified'
    CHECK (verification_tier IN ('verified', 'estimated', 'unverified'));
