-- Owner: metrics module -- platform insight columns available only to connected
-- (OAuth) accounts.
--
-- These are the Instagram Graph API insight metrics and their YouTube analogues.
-- They are NULL for a public-only fetch, and NULL is meaningful here: it records
-- "the platform did not expose this", not "the value was zero". Downstream
-- scoring must treat NULL as absent rather than coercing it to 0, which would
-- understate an account that simply has not connected.

ALTER TABLE post
    ADD COLUMN reach_count      bigint,
    ADD COLUMN impression_count bigint,
    ADD COLUMN save_count       bigint;

COMMENT ON COLUMN post.reach_count IS
    'Unique accounts that saw the post. NULL means the platform did not expose it, not zero.';
COMMENT ON COLUMN post.impression_count IS
    'Total views including repeats. NULL means not exposed, not zero.';
COMMENT ON COLUMN post.save_count IS
    'Saves/bookmarks. A high save-to-like ratio is a positive authenticity signal.';

-- Guard the counters that are only ever non-negative. A negative reach is a
-- connector bug, and it should fail at write time rather than silently skew a
-- customer-facing score.
ALTER TABLE post
    ADD CONSTRAINT post_reach_count_nonneg      CHECK (reach_count IS NULL OR reach_count >= 0),
    ADD CONSTRAINT post_impression_count_nonneg CHECK (impression_count IS NULL OR impression_count >= 0),
    ADD CONSTRAINT post_save_count_nonneg       CHECK (save_count IS NULL OR save_count >= 0);
