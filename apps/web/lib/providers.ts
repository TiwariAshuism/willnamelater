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

/**
 * An Instagram connection is authorized through Meta OAuth (instagram_basic +
 * instagram_manage_insights, per packages/config/connectors.yaml), so the
 * provider segment is "meta" while the resulting connection's `platform` is
 * "instagram". The backend keeps this provider unavailable until the instagram
 * connector is enabled (which is gated on Meta App Review), so the button
 * surfaces a "provider unavailable" error until then.
 */
export const INSTAGRAM_PROVIDER = "meta";
