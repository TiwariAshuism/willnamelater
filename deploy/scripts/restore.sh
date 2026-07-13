#!/usr/bin/env bash
#
# Restores a pg_dump from object storage into a target Postgres.
#
#   restore.sh <target-dsn> [key]
#
# key defaults to the most recent daily dump. The target DSN is explicit and
# required — restoring is destructive (--clean --if-exists) and must never default
# to production because someone forgot an argument.
#
# THIS SCRIPT IS THE BACKUP. A dump that has never been restored is a rumour, so
# .github/workflows/dr-drill.yml runs it monthly against a scratch database and
# asserts the result. That drill, not backup.sh, is the actual guarantee.
#
# It is also step 3 of the cloud migration (deploy/MIGRATION.md): the same command
# that proves the backup works is the command that moves the data, which is why the
# dump is --format=custom and restores into ANY Postgres 16 rather than into one
# cloud's managed service.

set -euo pipefail

TARGET_DSN="${1:?usage: restore.sh <target-dsn> [object-key]}"
KEY="${2:-}"

: "${INFLUAUDIT_STORAGE__ENDPOINT:?}"
: "${INFLUAUDIT_STORAGE__ACCESS_KEY:?}"
: "${INFLUAUDIT_STORAGE__SECRET_KEY:?}"
: "${BACKUP_AGE_KEY_FILE:?path to the age PRIVATE key the dump was encrypted to}"

BUCKET="${BACKUP_BUCKET:-influaudit-backups}"

mc() {
  docker run --rm -i \
    -v "${BACKUP_AGE_KEY_FILE}:/age.key:ro" \
    --entrypoint sh \
    minio/mc:latest -c "
      mc alias set store '${INFLUAUDIT_STORAGE__ENDPOINT}' '${INFLUAUDIT_STORAGE__ACCESS_KEY}' '${INFLUAUDIT_STORAGE__SECRET_KEY}' > /dev/null &&
      $*
    "
}

if [[ -z "${KEY}" ]]; then
  echo "=== Finding the most recent dump"
  KEY="$(mc "mc find 'store/${BUCKET}/pg' --name '*.dump.age' | sort | tail -1" | sed "s|^store/${BUCKET}/||")"
  [[ -n "${KEY}" ]] || { echo "no dumps found in ${BUCKET}/pg" >&2; exit 1; }
fi

echo "=== Restoring ${BUCKET}/${KEY}"
echo "=== Target: ${TARGET_DSN%%:*}://...  (destructive: --clean --if-exists)"

# Streamed end to end: the decrypted dump never lands on disk.
#
# --clean --if-exists drops what it is about to recreate, so a restore into a
# non-empty database is idempotent rather than a pile of "already exists" errors.
# --no-owner --no-acl because roles belong to the cloud we are restoring INTO, not
# the one the dump came from — this is precisely what makes the dump portable.
mc "mc cat 'store/${BUCKET}/${KEY}'" \
| age --decrypt --identity "${BACKUP_AGE_KEY_FILE}" \
| docker run --rm -i postgres:16-alpine \
    pg_restore --clean --if-exists --no-owner --no-acl --exit-on-error \
      --dbname "${TARGET_DSN}"

echo "=== Restored. Verifying."

# A restore that "succeeded" but left an empty schema is the failure this catches.
docker run --rm postgres:16-alpine \
  psql "${TARGET_DSN}" -At -c \
  "SELECT 'schema_migrations=' || count(*) FROM schema_migrations;
   SELECT 'users=' || count(*) FROM users;
   SELECT 'audit_jobs=' || count(*) FROM audit_job;"

echo "=== Restore complete"
