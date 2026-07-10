-- Owner: influencer module -- link an influencer profile to the user who owns it.
--
-- The audit orchestrator needs a path from an influencer to the OAuth tokens it
-- should present to a connector. Tokens are keyed by user (oauth_token.user_id),
-- so the influencer must carry the owning user.
--
-- Nullable on purpose: an influencer profile can exist with no connected owner
-- (a brand vetting a creator it has no OAuth grant for). A null owner simply
-- means "no authenticated connections" — the audit then falls back to public
-- data (e.g. YouTube's API-key path), which is the correct cold-start behaviour,
-- not an error.
--
-- ON DELETE SET NULL: deleting the user must not cascade-delete the profile and
-- its audit history; it just severs the ownership link.

ALTER TABLE influencer
    ADD COLUMN user_id uuid REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX influencer_user_id_idx ON influencer (user_id);

COMMENT ON COLUMN influencer.user_id IS
    'Owning user, whose OAuth tokens the audit path uses. NULL = no authenticated connections; public data only.';
