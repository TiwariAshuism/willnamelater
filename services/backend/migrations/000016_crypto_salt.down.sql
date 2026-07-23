-- DROP TABLE removes its own row triggers and does not fire them, so these two
-- statements are redundant for the drop itself. They are kept so the intent of
-- the teardown is explicit and so the order survives a future refactor that
-- replaces DROP TABLE with TRUNCATE, which *would* fire the delete guard.
DROP TRIGGER IF EXISTS trg_crypto_salt_no_delete ON crypto_salt;
DROP TRIGGER IF EXISTS trg_crypto_salt_no_update ON crypto_salt;

DROP TABLE IF EXISTS crypto_salt;

DROP FUNCTION IF EXISTS crypto_salt_is_immutable();
