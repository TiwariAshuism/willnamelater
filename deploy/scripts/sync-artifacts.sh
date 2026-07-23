#!/usr/bin/env bash
#
# Pulls the promoted champion model from object storage into the ml-artifacts volume.
#
#   sync-artifacts.sh          # sync the current champion and restart the ml service
#
# WHY THIS EXISTS
#
# The ML registry loads a champion from INFLUAUDIT_ML_ARTIFACTS (/var/lib/ml-artifacts).
# Training and promotion happen OUT of band — `make ml-retrain MODEL=fraud PROMOTE=1` —
# and write the artifact to object storage. Nothing was carrying it the last hop onto
# the serving host, so the volume stayed empty and the registry sat permanently on its
# cold-start heuristic while the admin API cheerfully reported a promoted champion.
#
# That is a specific, nasty failure: not a crash, not an error, just a model that was
# trained, validated, gated, promoted — and never served.
#
# THE ARTIFACT IS NOT BAKED INTO THE IMAGE, on purpose. An image carrying a champion
# could not be rolled back independently of the model it shipped with, and the two have
# genuinely independent lifecycles: you promote models far more often than you deploy
# code, and you sometimes need to roll back one without the other.

set -euo pipefail

APP_DIR="${APP_DIR:-/opt/influaudit}"
ENV_FILE="/run/influaudit/.env"
VOLUME="influaudit_ml-artifacts"

log() { printf '\n=== %s\n' "$*"; }

# deploy.sh may already hold the decrypted env on tmpfs. Decrypt only if it does not.
if [[ ! -f "${ENV_FILE}" ]]; then
  install -d -m 0700 /run/influaudit
  trap 'rm -f "${ENV_FILE}"' EXIT
  SOPS_AGE_KEY_FILE="${SOPS_AGE_KEY_FILE:-${APP_DIR}/.age.key}" \
    sops --decrypt "${APP_DIR}/deploy/secrets/prod.enc.env" > "${ENV_FILE}"
  chmod 0600 "${ENV_FILE}"
fi

# shellcheck disable=SC1090
set -a; source "${ENV_FILE}"; set +a

: "${INFLUAUDIT_STORAGE__ENDPOINT:?}"
: "${INFLUAUDIT_STORAGE__ACCESS_KEY:?}"
: "${INFLUAUDIT_STORAGE__SECRET_KEY:?}"
: "${INFLUAUDIT_STORAGE__BUCKET:?}"

PREFIX="${ML_ARTIFACT_PREFIX:-ml/champion}"

log "Syncing ${INFLUAUDIT_STORAGE__BUCKET}/${PREFIX} -> ${VOLUME}"

# --overwrite, not --remove: an artifact that vanishes from object storage must not
# silently delete the model that is currently serving. Removing a champion is a
# deliberate act, and it is not this script's to take.
docker run --rm \
  -v "${VOLUME}:/artifacts" \
  --entrypoint sh \
  minio/mc:latest -c "
    mc alias set store '${INFLUAUDIT_STORAGE__ENDPOINT}' '${INFLUAUDIT_STORAGE__ACCESS_KEY}' '${INFLUAUDIT_STORAGE__SECRET_KEY}' > /dev/null &&
    mc mirror --overwrite 'store/${INFLUAUDIT_STORAGE__BUCKET}/${PREFIX}' /artifacts
  "

# The registry reads the manifest at load, so a running ml service will not notice a
# new champion until it restarts. An empty directory here is a CORRECT state — it means
# cold-start heuristic — so a missing manifest is reported, not treated as a failure.
if ! docker run --rm -v "${VOLUME}:/artifacts:ro" alpine test -f /artifacts/manifest.json; then
  log "No manifest.json in object storage — the registry stays on the cold-start heuristic."
  log "That is a correct state, not an error. Nothing to restart."
  exit 0
fi

log "Champion present. Restarting the ml service to load it."
docker compose -f "${APP_DIR}/deploy/docker-compose.prod.yml" --env-file "${ENV_FILE}" \
  up -d --force-recreate --no-deps ml

log "Synced."
