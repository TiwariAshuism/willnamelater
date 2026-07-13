#!/usr/bin/env bash
#
# Prepares a fresh Ubuntu VM to run the stack. Run ONCE per VM, as root.
#
#   curl -fsSL <raw-url>/bootstrap-vm.sh | bash
#   # or: scp it up and run it
#
# This script is the ONLY thing in the repository that assumes an operating system,
# and it assumes the most boring one available. It makes no cloud API calls and
# names no cloud provider: it runs identically on GCP, Azure, AWS, Hetzner, or a
# laptop. That is what makes the VM cattle — Terraform creates it, this configures
# it, deploy.sh fills it, and if it dies you make another one in fifteen minutes
# because it holds no state.

set -euo pipefail

APP_DIR="${APP_DIR:-/opt/influaudit}"
DEPLOY_USER="${DEPLOY_USER:-deploy}"

log() { printf '\n=== %s\n' "$*"; }

[[ "${EUID}" -eq 0 ]] || { echo "run as root" >&2; exit 1; }

# --- Packages -----------------------------------------------------------------
log "Installing Docker, cosign, sops, age"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg age unattended-upgrades

install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
  | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  > /etc/apt/sources.list.d/docker.list

apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# cosign verifies image signatures before deploy.sh will run anything; sops
# decrypts the secrets. Both are single static binaries.
COSIGN_VERSION="${COSIGN_VERSION:-v2.4.1}"
curl -fsSL -o /usr/local/bin/cosign \
  "https://github.com/sigstore/cosign/releases/download/${COSIGN_VERSION}/cosign-linux-amd64"
chmod +x /usr/local/bin/cosign

SOPS_VERSION="${SOPS_VERSION:-v3.9.1}"
curl -fsSL -o /usr/local/bin/sops \
  "https://github.com/getsops/sops/releases/download/${SOPS_VERSION}/sops-${SOPS_VERSION}.linux.amd64"
chmod +x /usr/local/bin/sops

# --- Unattended security updates ---------------------------------------------
# The VM is cattle, but it is cattle with a public IP.
log "Enabling unattended security upgrades"
dpkg-reconfigure -f noninteractive unattended-upgrades

# --- Deploy user --------------------------------------------------------------
log "Creating the ${DEPLOY_USER} user"
id -u "${DEPLOY_USER}" &>/dev/null || useradd --create-home --shell /bin/bash "${DEPLOY_USER}"
usermod -aG docker "${DEPLOY_USER}"

install -d -o "${DEPLOY_USER}" -g "${DEPLOY_USER}" -m 0755 "${APP_DIR}"
install -d -o "${DEPLOY_USER}" -g "${DEPLOY_USER}" -m 0700 "/home/${DEPLOY_USER}/.ssh"

# --- Backup timer -------------------------------------------------------------
# Installed here rather than left as a documented step, because a backup that depends
# on someone remembering to enable it is not a backup.
if [[ -d "${APP_DIR}/deploy/systemd" ]]; then
  log "Installing the backup timer"
  cp "${APP_DIR}"/deploy/systemd/influaudit-backup.* /etc/systemd/system/
  systemctl daemon-reload
  systemctl enable --now influaudit-backup.timer
  systemctl list-timers influaudit-backup.timer --no-pager || true
else
  log "NOTE: ${APP_DIR}/deploy not present yet — after cloning the repo, run:"
  echo "  sudo cp ${APP_DIR}/deploy/systemd/influaudit-backup.* /etc/systemd/system/"
  echo "  sudo systemctl daemon-reload && sudo systemctl enable --now influaudit-backup.timer"
fi

cat <<'EOF'

=== Remaining manual steps ===

1. Install the CI deploy key with a FORCED COMMAND, so a leaked key cannot get a
   shell — it can only run the deploy. In /home/deploy/.ssh/authorized_keys:

     restrict,command="/opt/influaudit/deploy/scripts/ssh-entrypoint.sh" ssh-ed25519 AAAA... ci@influaudit

   `restrict` disables port forwarding, agent forwarding, X11, and PTY allocation.
   The forced command means the key can invoke nothing else, whatever the client asks for.

2. Place the age private key that decrypts deploy/secrets/prod.enc.env at
   /opt/influaudit/.age.key, owned by deploy, mode 0600.

3. Clone the repository to /opt/influaudit (deploy.sh reads the compose file and
   the encrypted secrets from there).

4. Lock down sshd: PasswordAuthentication no, PermitRootLogin no.

5. Point the firewall / security group at 22, 80, 443 only. Nothing else on this
   VM is meant to be reachable from the internet — Postgres and Redis are managed
   and reached outbound; every other container talks over the compose network.

EOF

log "VM bootstrapped"
