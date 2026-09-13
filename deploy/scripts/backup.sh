#!/usr/bin/env bash
# Dump the database and ship it to the off-box backup bucket (Cloudflare R2
# via rclone). Cron runs this every 6 hours; a failed run pings ntfy.
#
# Prereqs on the host: rclone with an `r2` remote (see README), and
# NTFY_URL/NTFY_TOPIC/NTFY_TOKEN in .env (already there for the API).
#
# Layout in the bucket:
#   pg/saltybytes-YYYYmmdd-HHMMSS.dump   pg_dump custom format (compressed)
#   pg/globals-YYYYmmdd-HHMMSS.sql       roles/grants (pg_dumpall --globals-only)
# Local: backups/latest.dump is always the newest dump, for a fast restore.
set -euo pipefail

cd "$(dirname "$0")/.."

# .env is compose-format, not shell (some values are raw JSON), so pull out
# just the keys we need instead of sourcing it.
envval() {
  local v
  v="$(grep -E "^$1=" .env 2>/dev/null | head -1 | cut -d= -f2-)"
  v="${v%\"}"; v="${v#\"}"
  printf '%s' "$v"
}
NTFY_URL="$(envval NTFY_URL)"
NTFY_TOPIC="$(envval NTFY_TOPIC)"
NTFY_TOKEN="$(envval NTFY_TOKEN)"
REMOTE="$(envval BACKUP_REMOTE)"; REMOTE="${REMOTE:-r2:saltybytes-backups/pg}"
RETAIN="$(envval BACKUP_RETAIN)"; RETAIN="${RETAIN:-30d}"
DB_USER="$(envval POSTGRES_USER)"; DB_USER="${DB_USER:-saltybytes}"
DB_NAME="$(envval POSTGRES_DB)"; DB_NAME="${DB_NAME:-saltybytes}"
STAMP="$(date -u +%Y%m%d-%H%M%S)"
mkdir -p backups

notify() {
  [ -n "$NTFY_URL" ] && [ -n "$NTFY_TOPIC" ] || return 0
  curl -fsS --max-time 10 \
    ${NTFY_TOKEN:+-H "Authorization: Bearer ${NTFY_TOKEN}"} \
    -H "Title: SaltyBytes backup failed" -H "Priority: high" -H "Tags: rotating_light" \
    -d "$1" "${NTFY_URL%/}/${NTFY_TOPIC}" >/dev/null || true
}
trap 'notify "backup.sh failed at line $LINENO on $(hostname)"' ERR

docker compose exec -T db pg_dump -U "$DB_USER" -d "$DB_NAME" -Fc > "backups/saltybytes-${STAMP}.dump"
docker compose exec -T db pg_dumpall -U "$DB_USER" --globals-only > "backups/globals-${STAMP}.sql"
ln -sf "saltybytes-${STAMP}.dump" backups/latest.dump

rclone copyto "backups/saltybytes-${STAMP}.dump" "${REMOTE}/saltybytes-${STAMP}.dump"
rclone copyto "backups/globals-${STAMP}.sql" "${REMOTE}/globals-${STAMP}.sql"
rclone delete --min-age "$RETAIN" "$REMOTE"

# Keep only the two newest local dumps (each is a few hundred MB at most).
ls -1t backups/saltybytes-*.dump | tail -n +3 | xargs -r rm -f
ls -1t backups/globals-*.sql | tail -n +3 | xargs -r rm -f

echo "backup ${STAMP} ok ($(du -h "backups/saltybytes-${STAMP}.dump" | cut -f1))"
