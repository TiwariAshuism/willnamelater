/**
 * OAuth provider identifiers used in the `/oauth/{provider}/…` backend routes.
 *
 * A YouTube connection is authorized through Google OAuth (youtube.readonly +
 * yt-analytics.readonly scopes, per packages/config/connectors.yaml), so the
 * provider segment is "google" while the resulting connection's `platform` is
 * "youtube". If the backend keys its OAuth routes by platform instead, change
 * this single constant to "youtube".
 */
export const YOUTUBE_PROVIDER = "google";
