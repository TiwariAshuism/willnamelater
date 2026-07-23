-- Owner: auth module (internal/auth).

CREATE TABLE users (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email          text NOT NULL,
    -- password_hash is NULL for accounts that authenticate only through a
    -- social provider; when set it is a password digest (argon2/bcrypt), never
    -- a plaintext password.
    password_hash  text,
    full_name      text,
    role           text NOT NULL DEFAULT 'user'   CHECK (role   IN ('user', 'admin')),
    status         text NOT NULL DEFAULT 'active'  CHECK (status IN ('active', 'suspended', 'deleted')),
    email_verified boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- Case-insensitive uniqueness: one account per address regardless of casing.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

CREATE TRIGGER trg_users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sessions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Only a hash of the refresh token is stored; the token itself is handed to
    -- the client once and never persisted in the clear.
    refresh_token_hash bytea NOT NULL,
    user_agent         text,
    ip                 inet,
    issued_at          timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    revoked_at         timestamptz
);

CREATE UNIQUE INDEX sessions_refresh_token_hash_key ON sessions (refresh_token_hash);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
