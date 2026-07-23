-- Owner: platform/crypto -- the stable, application-wide pseudonymization salt.
--
-- WHY THIS TABLE EXISTS AND WHY IT IS IMMUTABLE
--
-- comment_sample.author_hash is HMAC(author_id, salt). The co-commenter graph is
-- built by joining comments on equal author_hash across posts. That join is only
-- correct while the salt is constant: re-key it and the same commenter hashes to
-- two different values, every historical edge silently disappears, clique counts
-- collapse toward zero, and the system reports "no coordination detected" with
-- full confidence. It would look like it was working.
--
-- Rotation is therefore not a routine operation. It requires deleting and
-- rebuilding every comment-derived feature. To make an accidental rotation
-- impossible rather than merely discouraged, UPDATE and DELETE are rejected at
-- the database.
--
-- The salt value itself is never written by a migration. It is generated from a
-- CSPRNG on first boot and sealed with the master key (platform/crypto envelope
-- encryption), so a database dump alone does not yield it.

CREATE TABLE crypto_salt (
    -- Logical name of the salt, e.g. 'comment_author'. One row per purpose.
    name        text PRIMARY KEY,
    -- The salt, sealed under the master key. Never a plaintext salt.
    salt_enc    bytea NOT NULL,
    dek_wrapped bytea NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE crypto_salt IS
    'Immutable, application-wide salts. Rotating a salt invalidates every derived hash; see 000016.';

-- Reject any attempt to change or remove a salt once written. INSERT stays open
-- so first boot can seed it; everything else raises.
CREATE OR REPLACE FUNCTION crypto_salt_is_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION
        'crypto_salt is immutable: % on salt "%" would invalidate every derived hash',
        TG_OP, OLD.name
        USING HINT = 'Rotating a salt requires rebuilding all comment-derived features.';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_crypto_salt_no_update
    BEFORE UPDATE ON crypto_salt
    FOR EACH ROW EXECUTE FUNCTION crypto_salt_is_immutable();

CREATE TRIGGER trg_crypto_salt_no_delete
    BEFORE DELETE ON crypto_salt
    FOR EACH ROW EXECUTE FUNCTION crypto_salt_is_immutable();
