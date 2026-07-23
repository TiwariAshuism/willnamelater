-- Owner: oauth module (internal/oauth).

CREATE TABLE oauth_token (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform            platform NOT NULL,
    provider_account_id text NOT NULL,
    -- Envelope-encrypted secrets ONLY. There is deliberately NO plaintext token
    -- column here: the *_enc columns hold ciphertext sealed under a per-row DEK,
    -- and dek_wrapped holds that DEK sealed under the master key (see
    -- internal/platform/crypto). Adding a plaintext token column is a security
    -- defect, not a convenience.
    access_token_enc    bytea NOT NULL,
    refresh_token_enc   bytea,
    dek_wrapped         bytea NOT NULL,
    scopes              text[] NOT NULL DEFAULT '{}',
    access_expires_at   timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, platform, provider_account_id)
);

CREATE INDEX oauth_token_user_id_idx ON oauth_token (user_id);
CREATE INDEX oauth_token_access_expires_at_idx ON oauth_token (access_expires_at);

CREATE TRIGGER trg_oauth_token_set_updated_at BEFORE UPDATE ON oauth_token
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
