#!/usr/bin/env bash
#
# Restores a previous version. Run ON the VM.
#
#   rollback.sh <version>      # deploy.sh calls this automatically on failure
#
# ---------------------------------------------------------------------------
# THIS DOES NOT REVERT MIGRATIONS, AND THAT IS DELIBERATE.
#
# Auto-reverting DDL under load is how an outage becomes data loss: the down
# migration runs against a live database, drops the column the last few in-flight
# writes just used, and now the incident is unrecoverable rather than merely
# embarrassing.
#
# The contract that makes forward-only rollback safe is on the migration, not on
# this script:
#
#     EVERY MIGRATION MUST BE SAFE AGAINST THE PREVIOUS IMAGE.
#
# In practice: add columns, don't rename them; add tables, don't drop them; make
# new columns nullable or defaulted; deploy the code that reads a column one
# release AFTER the migration that adds it. Then the old image can always run
# against the new schema, and rolling back the code is enough.
#
# If a MIGRATION is what broke production, this script cannot help you. That is a
# fix-forward situation: write the corrective migration and deploy it.
# ---------------------------------------------------------------------------

set -euo pipefail

VERSION="${1:?usage: rollback.sh <version>}"

APP_DIR="${APP_DIR:-/opt/influaudit}"
COMPOSE_FILE="${APP_DIR}/deploy/docker-compose.prod.yml"
LAST_GOOD="${APP_DIR}/.last-good"
ENV_FILE="/run/influaudit/.env"

log() { printf '\n=== %s\n' "$*"; }

# deploy.sh already decrypted the env file and holds it on tmpfs. When this script
# is invoked directly (a manual rollback), decrypt it ourselves.
if [[ ! -f "${ENV_FILE}" ]]; then
  log "Decrypting secrets"
  install -d -m 0700 /run/influaudit
  trap 'rm -f "${ENV_FILE}"' EXIT
  SOPS_AGE_KEY_FILE="${SOPS_AGE_KEY_FILE:-${APP_DIR}/.age.key}" \
    sops --decrypt "${APP_DIR}/deploy/secrets/prod.enc.env" > "${ENV_FILE}"
  chmod 0600 "${ENV_FILE}"
fi

export VERSION

log "Rolling back to ${VERSION}"

docker compose -f "${COMPOSE_FILE}" --env-file "${ENV_FILE}" pull
docker compose -f "${COMPOSE_FILE}" --env-file "${ENV_FILE}" up -d --wait --wait-timeout 180

echo "${VERSION}" > "${LAST_GOOD}"

log "Rolled back to ${VERSION}. The schema was NOT reverted — see the header of this script."
