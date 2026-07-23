#!/usr/bin/env bash
#
# Asserts that every deploy/secrets/*.enc.env is ACTUALLY ENCRYPTED.
#
# THE FAILURE THIS EXISTS TO PREVENT
#
# deploy/secrets/prod.enc.env is committed to git on purpose — SOPS encrypts the values
# and leaves the keys in plaintext, so a diff shows which secret rotated without
# revealing it. That is the design.
#
# But it means the repository contains a file whose *name* says "encrypted" and whose
# *contents* are whatever you last saved. Copy the .example, fill in the real database
# password, forget `sops --encrypt`, `git add .` — and you have just published your
# production credentials, in a file that looks exactly like the one that is supposed to
# be there.
#
# .gitignore cannot catch this: the encrypted and unencrypted files are the same path.
# Only reading the contents can. That is this script.
#
# Run it: `make secrets-check`, in CI, and from .githooks/pre-commit.

set -euo pipefail

SECRETS_DIR="${SECRETS_DIR:-deploy/secrets}"
failed=0

shopt -s nullglob
files=("${SECRETS_DIR}"/*.enc.env)
shopt -u nullglob

if [[ ${#files[@]} -eq 0 ]]; then
  echo "No ${SECRETS_DIR}/*.enc.env yet — nothing to check."
  echo "Create one with:  cp ${SECRETS_DIR}/prod.enc.env.example ${SECRETS_DIR}/prod.enc.env"
  exit 0
fi

for f in "${files[@]}"; do
  # SOPS stamps its own metadata into every file it encrypts. Its presence is the only
  # reliable signal that the values are ciphertext and not the real thing.
  if ! grep -q "^sops_version=\|^sops_mac=\|\"sops\":" "$f" 2>/dev/null; then
    echo "::error file=${f}::${f} is NOT SOPS-encrypted."
    echo ""
    echo "  This file is committed to git. If it holds real credentials in plaintext,"
    echo "  committing it publishes them."
    echo ""
    echo "  Encrypt it:   sops --encrypt --in-place ${f}"
    echo "  Edit it:      make secrets-edit     (decrypts to a temp file, re-encrypts on save)"
    echo ""
    failed=1
    continue
  fi

  # A file can carry SOPS metadata and still have an unencrypted value if it was
  # hand-edited after decryption. Every value should be an ENC[...] blob.
  if grep -E '^[A-Z_]+=' "$f" | grep -qv 'ENC\['; then
    echo "::error file=${f}::${f} has SOPS metadata but contains at least one PLAINTEXT value."
    grep -nE '^[A-Z_]+=' "$f" | grep -v 'ENC\[' | sed 's/=.*/=<REDACTED — not printing it>/' | sed 's/^/    /'
    echo ""
    echo "  Someone edited a decrypted file and re-saved it without re-encrypting."
    echo "  Fix:  sops --encrypt --in-place ${f}"
    echo ""
    failed=1
    continue
  fi

  echo "ok  ${f} — encrypted"
done

if [[ "${failed}" -ne 0 ]]; then
  echo ""
  echo "REFUSING TO PASS. Plaintext secrets must not reach git."
  exit 1
fi
