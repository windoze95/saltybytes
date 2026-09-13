#!/usr/bin/env bash
# Roll the API to a new image tag on the droplet. Run by CI over SSH after
# it has pushed the image; safe to run by hand: `scripts/deploy.sh <tag>`.
#
#   1. pin API_IMAGE_TAG in .env (so a later plain `docker compose up -d`
#      keeps the same version)
#   2. pull + recreate only what changed (compose is idempotent)
#   3. reload Caddy so Caddyfile edits apply without dropping connections
#   4. wait for /ping and print the running image digest for CI to verify
set -euo pipefail

cd "$(dirname "$0")/.."
TAG="${1:-latest}"

if grep -q '^API_IMAGE_TAG=' .env; then
  sed -i "s|^API_IMAGE_TAG=.*|API_IMAGE_TAG=${TAG}|" .env
else
  printf 'API_IMAGE_TAG=%s\n' "$TAG" >> .env
fi

docker compose pull --quiet api
docker compose up -d --remove-orphans
docker compose exec -T caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1 || true

echo "waiting for api /ping ..."
for _ in $(seq 1 45); do
  if curl -fsS --max-time 2 http://127.0.0.1:8080/ping >/dev/null 2>&1; then
    echo "api healthy"
    docker image prune -f >/dev/null
    printf 'running-digest=%s\n' "$(docker inspect --format '{{index .RepoDigests 0}}' "$(docker compose ps -q api)")"
    exit 0
  fi
  sleep 2
done

echo "ERROR: api did not answer /ping within 90s; recent logs:" >&2
docker compose logs --tail=50 api >&2
exit 1
