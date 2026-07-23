-- Owner: report module (internal/report).
--
-- Creator-directed sharing + a revocable, expiring certificate.
--
-- WHY (Meta Platform Terms, the contract that governs every byte of Instagram
-- Graph data this product holds):
--
--   * §3.a.iv prohibits "Selling, licensing, or purchasing Platform Data" — a
--     flat prohibition with NO user-consent carve-out. And "Platform Data" is
--     defined to include "data anonymized, aggregated, or derived from such
--     data", so a score DERIVED from Insights is still Platform Data. We
--     therefore never sell a creator's data or a score computed from it; we sell
--     the service, and the creator directs where their own report goes.
--
--   * §3.c permits sharing with a third party "when a User expressly directs you
--     to share or expressly consents to your sharing the data with the third
--     party for the purposes as specified in the User's direction or consent".
--     That is the ONLY lawful channel to a brand — so a share must be an
--     explicit, purpose-scoped, revocable act BY THE CREATOR WHO OWNS THE
--     CONNECTED ACCOUNT, recorded as evidence. report_share_grant is that record.
--
--   * §3.d requires us to "update or delete Platform Data promptly after
--     receiving a request from us or the User" and to delete it once retention is
--     no longer necessary. A permanent, immutable public badge cannot satisfy
--     that, so a published report now EXPIRES and can be REVOKED.
--
-- Before this migration, report.expires_at existed but was always written NULL
-- and never read: a published badge granted indefinite public access to
-- Graph-derived data, and any user who merely REQUESTED an audit (not the
-- creator who owns the account) could publish it. Both are closed here and in
-- the service.

-- A published report is now time-bounded and revocable. expires_at already
-- exists on the table (000012) but was never populated; revoked_at is new and is
-- the creator's (or a Meta deletion callback's) kill switch.
ALTER TABLE report ADD COLUMN revoked_at timestamptz;

-- The public badge read filters on both, so a lookup by slug stays a single
-- indexed row hit even once most historical rows are expired/revoked.
CREATE INDEX report_live_slug_idx ON report (public_slug)
    WHERE public_slug IS NOT NULL AND revoked_at IS NULL;

-- The consent record: one row per act of a creator directing us to share a
-- specific report with a specific, NAMED recipient for a stated purpose. This is
-- the §3.c evidence trail — without a live grant, Graph-derived data reaches no
-- third party.
CREATE TABLE report_share_grant (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The report being shared, and the creator directing the share. The creator
    -- MUST be the owner of the connected account the report was built from
    -- (influencer.user_id) — enforced in the service, since that ownership chain
    -- spans modules and cannot be expressed as a single FK here.
    report_id         uuid NOT NULL REFERENCES report(id) ON DELETE CASCADE,
    granted_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- The named third party and the purpose the creator specified. Both are
    -- required: §3.c authorizes sharing "with the third party for the purposes as
    -- specified in the User's direction", so an unnamed recipient or an unstated
    -- purpose is not a valid direction and must not be storable.
    recipient         text NOT NULL CHECK (length(btrim(recipient)) > 0),
    purpose           text NOT NULL CHECK (length(btrim(purpose)) > 0),

    granted_at        timestamptz NOT NULL DEFAULT now(),
    -- Every grant is time-bounded; there is no perpetual share.
    expires_at        timestamptz NOT NULL,
    -- The creator can withdraw consent at any time, as can a Meta deauthorize /
    -- data-deletion callback.
    revoked_at        timestamptz,

    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- The hot path: "is there a live grant on this report for this recipient?"
CREATE INDEX report_share_grant_live_idx
    ON report_share_grant (report_id, recipient)
    WHERE revoked_at IS NULL;

-- "Revoke everything this creator ever shared" — the deauthorize / data-deletion
-- callback path, which must be fast and total.
CREATE INDEX report_share_grant_grantor_idx
    ON report_share_grant (granted_by_user_id)
    WHERE revoked_at IS NULL;

-- Owner: oauth module (internal/oauth).
--
-- Meta's deauthorize and data-deletion callbacks identify the person whose data
-- must be erased by their APP-SCOPED USER ID — not by the Instagram Business
-- account id we store in provider_account_id. Without the app-scoped id recorded
-- at connect time, an incoming deletion callback is unmappable: we would know
-- Meta wants us to delete someone's data and be unable to say whose. That is not
-- a compliance posture, so we capture it up front.
--
-- Nullable: rows connected before this migration never captured it. Those tokens
-- are still revocable by the user in-app; they simply cannot be resolved FROM a
-- Meta callback until the creator reconnects.
ALTER TABLE oauth_token ADD COLUMN provider_user_id text;

-- The callback lookup: (platform, provider_user_id) -> user_id.
CREATE INDEX oauth_token_provider_user_idx
    ON oauth_token (platform, provider_user_id)
    WHERE provider_user_id IS NOT NULL;
