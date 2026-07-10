-- Owner: metrics module -- pseudonymize commenter identities.
--
-- comment_sample previously stored author_handle in plaintext. A commenter is a
-- third party who is not our user and never consented to being profiled, and a
-- co-commenter graph is precisely a behavioural profile of them. Storing the raw
-- handle is unnecessary: every downstream feature (co-occurrence edges, clique
-- counts, repeat-commenter concentration) needs only a stable equality key.
--
-- author_hash is HMAC-SHA256(author_id, application_salt). It is:
--   * stable  -- the same commenter on two posts yields the same hash, which is
--              what the co-occurrence graph is built on;
--   * keyed   -- an attacker holding the table cannot brute-force the small
--              space of platform user IDs without also stealing the salt;
--   * one-way -- we never need to recover the handle, and cannot.
--
-- The salt is a SINGLE, STABLE, application-wide value (see 000016). Rotating it
-- re-keys every hash and silently destroys every historical co-occurrence edge
-- while appearing to work. Do not rotate without a full graph rebuild.

ALTER TABLE comment_sample DROP COLUMN author_handle;

ALTER TABLE comment_sample ADD COLUMN author_hash bytea;

COMMENT ON COLUMN comment_sample.author_hash IS
    'HMAC-SHA256 of the platform author id under the application salt. Never store a raw handle here.';

-- The co-commenter graph is built by grouping comments by author across posts,
-- so this index carries the dominant read pattern.
CREATE INDEX comment_sample_author_hash_idx ON comment_sample (author_hash);

-- Retention sweeps delete comment rows older than the configured window. Without
-- this index the sweep degrades into a sequential scan of the largest table.
CREATE INDEX comment_sample_created_at_idx ON comment_sample (created_at);

-- body holds user-generated comment text, needed for near-duplicate detection.
-- It is already present as `body`; make the retention contract explicit.
COMMENT ON COLUMN comment_sample.body IS
    'Comment text. Personal data of a third party: subject to the retention sweep.';
