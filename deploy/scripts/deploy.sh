#!/usr/bin/env bash
#
# Deploys one version to this VM. Run ON the VM; CI invokes it over SSH.
#
#   deploy.sh <version>        # version is a git SHA — the tag release.yml pushed
#
# The order below is the whole point of the script:
#
#   verify signatures -> pull -> record last-good -> MIGRATE -> up -> health -> rollback on failure
#
# Migrations run as an explicit, foreground, blocking step BEFORE the new
# containers start, and a non-zero exit aborts the deploy with the OLD version
# still serving. They are deliberately NOT a compose `depends_on:
# service_completed_successfully` the way they are in dev, because that re-runs
# them on every `up` — and on a rollback it would apply the NEW schema under the
# OLD image.
#
# THE RULE THAT MAKES THIS SAFE: every migration must be safe against the previous
# image. Rollback restores the previous containers; it does NOT revert migrations,
# because auto-reverting DDL under load is how an outage becomes data loss. If a
# migration is the problem, that is a fix-forward situation.

set -euo pipefail

VERSION="${1:?usage: deploy.sh <version>}"

APP_DIR="${APP_DIR:-/opt/influaudit}"
COMPOSE_FILE="${APP_DIR}/deploy/docker-compose.prod.yml"
LAST_GOOD="${APP_DIR}/.last-good"
ENV_FILE="/run/influaudit/.env"

REGISTRY="ghcr.io/getnyx"
IMAGES=(influaudit-backend influaudit-ml influaudit-web)

# The identity release.yml signs with. cosign verifies the image was built by THAT
# workflow in THIS repository — which is what makes "pull an image and run it as
# root on my server" something other than a standing invitation.
CERT_IDENTITY_REGEXP="^https://github.com/getnyx/influaudit/.github/workflows/release.yml@refs/"
CERT_OIDC_ISSUER="https://token.actions.githubusercontent.com"

log() { printf '\n=== %s\n' "$*"; }
die() { printf '\nFATAL: %s\n' "$*" >&2; exit 1; }

compose() {
  docker compose -f "${COMPOSE_FILE}" --env-file "${ENV_FILE}" "$@"
}

# --- 0. Decrypt secrets to tmpfs --------------------------------------------
# /run is a tmpfs on any systemd host, so the plaintext never touches a disk. It is
# removed on exit regardless of how we leave.
log "Decrypting secrets"
install -d -m 0700 /run/influaudit
trap 'rm -f "${ENV_FILE}"' EXIT
SOPS_AGE_KEY_FILE="${SOPS_AGE_KEY_FILE:-${APP_DIR}/.age.key}" \
  sops --decrypt "${APP_DIR}/deploy/secrets/prod.enc.env" > "${ENV_FILE}"
chmod 0600 "${ENV_FILE}"

export VERSION

# --- 1. Verify signatures BEFORE pulling ------------------------------------
log "Verifying image signatures for ${VERSION}"
for image in "${IMAGES[@]}"; do
  cosign verify \
    --certificate-identity-regexp "${CERT_IDENTITY_REGEXP}" \
    --certificate-oidc-issuer "${CERT_OIDC_ISSUER}" \
    "${REGISTRY}/${image}:${VERSION}" > /dev/null \
    || die "signature verification failed for ${image}:${VERSION} — refusing to deploy"
done

# --- 2. Pull ------------------------------------------------------------------
log "Pulling images"
compose pull

# --- 3. Record the version we are replacing, for rollback ---------------------
PREVIOUS="$(cat "${LAST_GOOD}" 2>/dev/null || true)"
log "Currently deployed: ${PREVIOUS:-<none>}"

# --- 4. Migrate. Blocking, and fatal. ----------------------------------------
# cmd/migrate exits non-zero on a dirty schema, which is exactly the gate we want.
log "Applying migrations"
compose --profile tools run --rm migrate \
  || die "migrations failed — ${PREVIOUS:-the current version} is still serving, nothing was changed"

# --- 5. Start the new version -------------------------------------------------
log "Starting ${VERSION}"
compose up -d --wait --wait-timeout 180 || {
  printf '\ncompose up did not become healthy\n' >&2
  [[ -n "${PREVIOUS}" ]] && "${APP_DIR}/deploy/scripts/rollback.sh" "${PREVIOUS}"
  die "deploy failed"
}

# --- 6. Health check from OUTSIDE the container network ------------------------
# The compose healthcheck proves the process is up. This proves the thing the
# public actually reaches — through Caddy, over TLS — is serving.
log "Smoke-testing the public endpoints"
ok=true
for i in 1 2 3; do
  ok=true
  curl -fsS --max-time 10 "https://${API_DOMAIN}/readyz" > /dev/null || ok=false
  curl -fsS --max-time 10 -o /dev/null "https://${APP_DOMAIN}/" || ok=false
  [[ "${ok}" == true ]] && break
  printf 'attempt %s failed, retrying\n' "${i}"
  sleep 5
done

if [[ "${ok}" != true ]]; then
  printf '\nsmoke test failed\n' >&2
  if [[ -n "${PREVIOUS}" ]]; then
    "${APP_DIR}/deploy/scripts/rollback.sh" "${PREVIOUS}"
    die "deploy failed; rolled back to ${PREVIOUS}"
  fi
  die "deploy failed and there is no previous version to roll back to"
fi

# --- 7. Commit ----------------------------------------------------------------
echo "${VERSION}" > "${LAST_GOOD}"
log "Deployed ${VERSION}"

# Old images accumulate and the VM's disk is not large. Keep the last few.
docker image prune -af --filter "until=168h" > /dev/null 2>&1 || true
