-- Owner: connector module (internal/connector).

CREATE TABLE connector_config (
    -- Natural key: exactly one configuration row per platform.
    platform           platform PRIMARY KEY,
    enabled            boolean NOT NULL DEFAULT true,
    client_id          text,
    -- The OAuth client secret at rest is envelope-encrypted, like oauth_token:
    -- there is no plaintext secret column. NULL when the platform needs no
    -- client secret.
    client_secret_enc  bytea,
    dek_wrapped        bytea,
    api_base_url       text,
    default_scopes     text[] NOT NULL DEFAULT '{}',
    rate_limit_per_min integer,
    daily_quota        bigint,
    settings           jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_connector_config_set_updated_at BEFORE UPDATE ON connector_config
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE api_quota_ledger (
    -- Natural key: one accounting row per platform per UTC day.
    platform        platform NOT NULL,
    day             date NOT NULL,
    calls_made      bigint NOT NULL DEFAULT 0,
    quota_limit     bigint,
    throttled_count bigint NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (platform, day)
);

CREATE TRIGGER trg_api_quota_ledger_set_updated_at BEFORE UPDATE ON api_quota_ledger
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
