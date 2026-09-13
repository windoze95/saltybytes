#!/usr/bin/env bash
# One-time bootstrap for a fresh Ubuntu 24.04 droplet. Run as root:
#   curl -fsSL https://raw.githubusercontent.com/windoze95/saltybytes/main/deploy/scripts/host-setup.sh | bash
# Idempotent: re-running is harmless. Afterwards, finish the manual steps
# printed at the end (Tailscale login, .env, certs, CI key).
set -euo pipefail

APP_DIR=/opt/saltybytes
DEPLOY_USER=deploy

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends \
  ca-certificates curl gnupg ufw fail2ban unattended-upgrades rclone

# Docker (official repo → current compose plugin).
if ! command -v docker >/dev/null; then
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
fi

# Tailscale (dashboard → Postgres over the tailnet; SSH without a public port).
if ! command -v tailscale >/dev/null; then
  curl -fsSL https://tailscale.com/install.sh | sh
fi

# 2 GB swap: headroom for Postgres + API + a niced ffmpeg on a 2 GB host.
if [ ! -f /swapfile ]; then
  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
  echo '/swapfile none swap sw 0 0' >> /etc/fstab
  sysctl -w vm.swappiness=10 && echo 'vm.swappiness=10' > /etc/sysctl.d/90-swap.conf
fi

# Unprivileged deploy user that owns the stack and can drive Docker.
if ! id "$DEPLOY_USER" >/dev/null 2>&1; then
  useradd -m -s /bin/bash -G docker "$DEPLOY_USER"
fi
mkdir -p "$APP_DIR"/{scripts,certs,backups}
chown -R "$DEPLOY_USER:$DEPLOY_USER" "$APP_DIR"
chmod 700 "$APP_DIR/certs"

# Host firewall. Docker publishes ports past ufw, which is why the compose
# file binds Postgres to loopback/Tailscale explicitly rather than relying
# on this. Prefer also restricting 80/443 to Cloudflare in the DO Cloud
# Firewall (see README).
ufw --force reset >/dev/null
ufw default deny incoming
ufw default allow outgoing
ufw allow in on tailscale0
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 443/udp
ufw allow 22/tcp   # tighten to your home IP / remove once Tailscale SSH works
ufw --force enable

# Security updates apply themselves; reboots (kernel) are left to you.
cat > /etc/apt/apt.conf.d/20auto-upgrades <<'CONF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
CONF

# Backups every 6 hours as the deploy user.
cat > /etc/cron.d/saltybytes-backup <<CONF
0 */6 * * * $DEPLOY_USER $APP_DIR/scripts/backup.sh >> $APP_DIR/backups/backup.log 2>&1
CONF

cat <<MSG

Host bootstrap done. Finish by hand:
  1. tailscale up                       # then note: tailscale ip -4
  2. Put the CI deploy public key in /home/$DEPLOY_USER/.ssh/authorized_keys
  3. Copy deploy/{docker-compose.yml,Caddyfile,scripts/} to $APP_DIR (CI does this on every deploy too)
  4. Write $APP_DIR/.env (see deploy/.env.example) — chmod 600, owned by $DEPLOY_USER
  5. Cloudflare Origin CA cert → $APP_DIR/certs/origin.pem + origin.key (chmod 600)
  6. rclone config → remote "r2" (Cloudflare R2, bucket saltybytes-backups), as $DEPLOY_USER
  7. sudo -u $DEPLOY_USER $APP_DIR/scripts/deploy.sh latest
MSG
