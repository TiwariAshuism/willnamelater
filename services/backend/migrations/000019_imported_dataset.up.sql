-- Owner: dataimport module (internal/dataimport) -- a user-uploaded snapshot of
-- their own platform data.
--
-- This is the real-data path for a platform the product has no live API grant
-- for yet: Instagram, pending Meta app review. The creator uploads their own
-- Insights export (their real numbers, not fabricated), it is normalized to the
-- connector's snapshot vocabulary, and the csvimport connector serves the latest
-- upload for a handle at audit time.
--
-- posts/metrics/comments are stored as JSON in the connector's own shapes
-- (connector.Post / connector.MetricPoint / connector.Comment) so the connector
-- reads them straight back without a per-row schema to migrate. comments is
-- empty for a CSV upload (an Insights CSV carries no per-comment data), which is
-- why an Instagram audit from this path is partial for the coordination signal
-- rather than fabricating one.

CREATE TABLE imported_dataset (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The uploader. CASCADE: deleting the user removes their uploads.
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The influencer profile the upload is for. CASCADE with the profile.
    influencer_id  uuid REFERENCES influencer(id) ON DELETE CASCADE,
    platform       platform NOT NULL,
    handle         text NOT NULL,
    followers      bigint NOT NULL DEFAULT 0,
    -- Provenance of the upload, e.g. 'instagram_csv' or 'instagram_json'.
    source         text NOT NULL,
    posts_jsonb    jsonb NOT NULL DEFAULT '[]'::jsonb,
    metrics_jsonb  jsonb NOT NULL DEFAULT '[]'::jsonb,
    comments_jsonb jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- When the underlying data was captured (the export's as-of time); defaults
    -- to upload time when the export does not state one.
    captured_at    timestamptz NOT NULL DEFAULT now(),
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- The connector resolves the latest dataset for a (platform, handle): this index
-- serves that lookup directly.
CREATE INDEX imported_dataset_lookup_idx ON imported_dataset (platform, handle, created_at DESC);

-- A user's own uploads, newest first, for the management surface.
CREATE INDEX imported_dataset_user_idx ON imported_dataset (user_id, created_at DESC);
