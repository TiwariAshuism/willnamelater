-- Reverses 000014. The plaintext author_handle is restored as an empty column:
-- the original values are irrecoverable by design, because author_hash is a
-- one-way keyed digest. That asymmetry is the point of the migration.

DROP INDEX IF EXISTS comment_sample_created_at_idx;
DROP INDEX IF EXISTS comment_sample_author_hash_idx;

ALTER TABLE comment_sample DROP COLUMN IF EXISTS author_hash;

ALTER TABLE comment_sample ADD COLUMN author_handle text;
