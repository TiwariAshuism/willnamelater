#!/usr/bin/env bash
#
# The forced command behind the CI deploy key. It is the ONLY thing that key can
# run, whatever the client asks for.
#
# sshd puts the client's requested command in SSH_ORIGINAL_COMMAND and runs this
# instead. So a key that leaks cannot get a shell, cannot read the age key, and
# cannot run docker — it can ask for a deploy or a rollback of a specific version,
# and nothing else.
#
# In /home/deploy/.ssh/authorized_keys:
#   restrict,command="/opt/influaudit/deploy/scripts/ssh-entrypoint.sh" ssh-ed25519 AAAA... ci

set -euo pipefail

APP_DIR="${APP_DIR:-/opt/influaudit}"

read -r -a argv <<< "${SSH_ORIGINAL_COMMAND:-}"

action="${argv[0]:-}"
version="${argv[1]:-}"

# A version is a git SHA or a semver tag. Anything else is someone trying to smuggle
# a shell metacharacter through, and there is no reason to be polite about it.
if [[ ! "${version}" =~ ^[a-f0-9]{7,40}$ && ! "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "refused: '${version}' is not a valid version" >&2
  exit 1
fi

case "${action}" in
  deploy)
    exec "${APP_DIR}/deploy/scripts/deploy.sh" "${version}"
    ;;
  rollback)
    exec "${APP_DIR}/deploy/scripts/rollback.sh" "${version}"
    ;;
  *)
    echo "refused: '${action}' is not deploy or rollback" >&2
    exit 1
    ;;
esac
