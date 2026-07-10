ALTER TABLE post
    DROP CONSTRAINT IF EXISTS post_save_count_nonneg,
    DROP CONSTRAINT IF EXISTS post_impression_count_nonneg,
    DROP CONSTRAINT IF EXISTS post_reach_count_nonneg;

ALTER TABLE post
    DROP COLUMN IF EXISTS save_count,
    DROP COLUMN IF EXISTS impression_count,
    DROP COLUMN IF EXISTS reach_count;
