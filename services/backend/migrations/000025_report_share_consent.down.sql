-- Reverse dependency order: the grant table references report, so it drops first.
DROP INDEX IF EXISTS oauth_token_provider_user_idx;
ALTER TABLE oauth_token DROP COLUMN IF EXISTS provider_user_id;

DROP INDEX IF EXISTS report_share_grant_grantor_idx;
DROP INDEX IF EXISTS report_share_grant_live_idx;
DROP TABLE IF EXISTS report_share_grant;

DROP INDEX IF EXISTS report_live_slug_idx;
ALTER TABLE report DROP COLUMN IF EXISTS revoked_at;
