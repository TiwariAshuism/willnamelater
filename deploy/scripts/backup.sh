#!/usr/bin/env bash
#
# Nightly portable backup. Run ON the VM from a systemd timer.
#
# ---------------------------------------------------------------------------
# THIS IS THE SCRIPT THAT MAKES A CLOUD MIGRATION POSSIBLE AT ALL.
#
# There are two backup layers, and only this one is portable:
#
#   Layer 1 — the managed database's automated backups + PITR. Excellent RPO, zero
#   work, and COMPLETELY USELESS for leaving the cloud: a Cloud SQL backup cannot be
#   restored into Azure Database for PostgreSQL. This is the layer that makes you
#   feel safe and keeps you trapped.
#
#   Layer 2 — this. A pg_dump in --format=custom, which restores into ANY Postgres
#   16 on ANY cloud, encrypted with age and written to object storage that is not on
#   the compute cloud. On migration day the backups are ALREADY where the new cloud
#   can reach them. Nothing to move.
#
# A backup you have not restored is a rumour. restore.sh is exercised monthly by
# .github/workflows/dr-drill.yml against a scratch database, and that drill — not
# this script — is the actual guarantee.
# ---------------------------------------------------------------------------

set -euo pipefail

APP_DIR="${APP_DIR:-/opt/influaudit}"
ENV_FILE="/run/influaudit/.env"

install -d -m 0700 /run/influaudit
trap 'rm -f "${ENV_FILE}"' EXIT
SOPS_AGE_KEY_FILE="${SOPS_AGE_KEY_FILE:-${APP_DIR}/.age.key}" \
  sops --decrypt "${APP_DIR}/deploy/secrets/prod.enc.env" > "${ENV_FILE}"
chmod 0600 "${ENV_FILE}"

# shellcheck disable=SC1090
set -a; source "${ENV_FILE}"; set +a

: "${INFLUAUDIT_POSTGRES__DSN:?}"
: "${BACKUP_AGE_RECIPIENT:?the age public key to encrypt the dump to}"
: "${INFLUAUDIT_STORAGE__ENDPOINT:?}"
: "${INFLUAUDIT_STORAGE__ACCESS_KEY:?}"
: "${INFLUAUDIT_STORAGE__SECRET_KEY:?}"

BUCKET="${BACKUP_BUCKET:-influaudit-backups}"
STAMP="$(date -u +%Y/%m/%d/%H%M%S)"
KEY="pg/${STAMP}.dump.age"

# Weekly dumps are kept for a year in their own prefix; daily ones expire at 90d
# under a bucket lifecycle rule.
[[ "$(date -u +%u)" == "7" ]] && KEY="pg/weekly/${STAMP}.dump.age"

echo "=== Backing up to ${BUCKET}/${KEY}"

# --format=custom --no-owner --no-acl is what makes the dump portable: it restores
# into a plain Postgres 16, not into "the same managed service with the same roles".
# Roles and grants are a property of the CLOUD; the schema and data are ours.
#
# The whole thing is a pipe: the plaintext dump never lands on the VM's disk.
docker run --rm -i \
  -e PGCONNECT_TIMEOUT=15 \
  postgres:16-alpine \
  pg_dump --format=custom --no-owner --no-acl --compress=9 "${INFLUAUDIT_POSTGRES__DSN}" \
| age --recipient "${BACKUP_AGE_RECIPIENT}" \
| docker run --rm -i \
    -e AWS_ACCESS_KEY_ID="${INFLUAUDIT_STORAGE__ACCESS_KEY}" \
    -e AWS_SECRET_ACCESS_KEY="${INFLUAUDIT_STORAGE__SECRET_KEY}" \
    --entrypoint sh \
    minio/mc:latest -c "
      mc alias set store '${INFLUAUDIT_STORAGE__ENDPOINT}' '${INFLUAUDIT_STORAGE__ACCESS_KEY}' '${INFLUAUDIT_STORAGE__SECRET_KEY}' > /dev/null &&
      mc pipe 'store/${BUCKET}/${KEY}'
    "

echo "=== Backed up ${KEY}"

# --- The other two things that are not in Postgres ----------------------------
# caddy-data holds the TLS certificates. Restoring it onto the new VM during a
# migration means the new host serves TLS from its first second, with no ACME
# round-trip at the exact moment of cutover.
#
# ml-artifacts holds the promoted champion model. The registry falls back to the
# cold-start heuristic without it, which is correct but is not what was serving.
for vol in caddy-data ml-artifacts; do
  docker run --rm \
    -v "influaudit_${vol}:/data:ro" \
    -e AWS_ACCESS_KEY_ID="${INFLUAUDIT_STORAGE__ACCESS_KEY}" \
    -e AWS_SECRET_ACCESS_KEY="${INFLUAUDIT_STORAGE__SECRET_KEY}" \
    --entrypoint sh \
    minio/mc:latest -c "
      mc alias set store '${INFLUAUDIT_STORAGE__ENDPOINT}' '${INFLUAUDIT_STORAGE__ACCESS_KEY}' '${INFLUAUDIT_STORAGE__SECRET_KEY}' > /dev/null &&
      mc mirror --overwrite --quiet /data 'store/${BUCKET}/volumes/${vol}'
    " || echo "WARN: could not back up ${vol}"
done

# --- Redis is deliberately NOT backed up --------------------------------------
# It holds the asynq queue and the cache. The queue is re-enqueueable (a lost audit
# task is retried; the only scheduled task is an idempotent nightly recompute) and
# the cache is derived from Postgres. Backing it up would be work that protects
# nothing. This paragraph exists so nobody spends a day discovering that.

echo "=== Backup complete"
