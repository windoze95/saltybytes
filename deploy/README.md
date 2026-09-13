# Single-host deployment (DigitalOcean droplet)

Everything the API needs on one $18/mo box: Caddy (TLS + reverse proxy),
the API container from GHCR, and PostgreSQL 16 + pgvector. Images live in
Cloudflare R2; signup email stays on Amazon SES; the admin dashboard on
Unraid reaches Postgres over Tailscale. Cloudflare proxies all three public
hostnames, so the origin IP is never exposed.

```
Cloudflare proxy ─► droplet:443 Caddy ─► api:8080 ─► db:5432 (pgvector)
                                │
                     tailscale0 └─► db:5432 (dashboard, read-only role)
R2: saltybytes-images (public via img.saltybytes.ai) · saltybytes-backups (pg_dump every 6 h)
```

| File | Purpose |
|---|---|
| `docker-compose.yml` | The stack. Copied to `/opt/saltybytes/` by CI on every deploy. |
| `Caddyfile` | TLS (Cloudflare Origin CA cert) + proxy rules; Cloudflare edge IPs trusted for real client IPs. |
| `.env.example` | Every variable the stack needs; the real `.env` lives only on the box. |
| `scripts/host-setup.sh` | One-time bootstrap of a fresh Ubuntu 24.04 droplet. |
| `scripts/deploy.sh` | Pull + roll a tag, reload Caddy, wait for `/ping`, print the running digest. |
| `scripts/backup.sh` | `pg_dump` → R2, 30-day retention, ntfy on failure (cron, 6-hourly). |

## 1. One-time setup

**Cloudflare**
1. R2 → create `saltybytes-images` (custom domain `img.saltybytes.ai`) and
   `saltybytes-backups` (private). One API token, Object Read & Write, both buckets.
2. SSL/TLS → **Origin Server** → *Create Certificate* (RSA or ECDSA, 15 years,
   hosts `saltybytes.ai`, `*.saltybytes.ai`). Save as `certs/origin.pem` +
   `certs/origin.key`. Zone SSL mode must be **Full (strict)**.

**AWS (SES only)** — IAM user `saltybytes-ses`, inline policy allowing
`ses:SendEmail`/`ses:SendRawEmail`, one access key.

**Droplet** — Ubuntu 24.04, 2 vCPU / 2 GB / 60 GB, NYC3. As root:
```bash
curl -fsSL https://raw.githubusercontent.com/windoze95/saltybytes/main/deploy/scripts/host-setup.sh | bash
tailscale up          # then: tailscale ip -4  → DB_BIND_IP in .env
```
Then, as `deploy`: copy this directory to `/opt/saltybytes/`, write `.env`
from `.env.example` (`chmod 600`), drop the origin cert in `certs/`, and
`rclone config` an `r2` remote (provider Cloudflare, the R2 token, endpoint
`https://<account-id>.r2.cloudflarestorage.com`).

**DO Cloud Firewall** (outside the host, so Docker can't bypass it): inbound
80/443 from Cloudflare's ranges only, 22 from home, UDP 41641 any
(Tailscale); everything else denied.

**GitHub** — secrets `DROPLET_HOST`, `DROPLET_SSH_KEY` (private key whose
public half is in `/home/deploy/.ssh/authorized_keys`), `DROPLET_KNOWN_HOSTS`
(`ssh-keyscan <host>`). Repository variable `DEPLOY_TARGET=droplet` switches
CI from ECS to the droplet — leave it unset until cutover.

**Unraid dashboard** — `DATABASE_URL=postgres://dashboard_ro:…@<droplet tailscale ip>:5432/saltybytes`;
unset the `SGSYNC_*` vars (they only existed to whitelist a home IP on RDS).

## 2. Rehearsal (the day before) — same steps as cutover, nothing public changes

```bash
# 1. let the droplet reach RDS for the dump (remove again afterwards)
aws ec2 authorize-security-group-ingress --group-id <rds-sg> --protocol tcp --port 5432 --cidr <droplet-ip>/32

# 2. on the droplet: dump straight from RDS with a matching client
cd /opt/saltybytes
docker run --rm pgvector/pgvector:pg16 pg_dump "$RDS_URL" -Fc > backups/rds.dump

# 3. restore into the stack (db only), then start the API. The app role is
#    POSTGRES_USER (created by the image); only the dashboard's read-only
#    role needs creating by hand — RDS makes pg_dumpall's globals dump messy.
docker compose up -d db
docker compose exec -T db psql -U saltybytes -d saltybytes -c 'CREATE EXTENSION IF NOT EXISTS vector'
docker compose exec -T db pg_restore -U saltybytes -d saltybytes --no-owner --no-privileges -j 2 /backups/rds.dump
docker compose exec -T db psql -U saltybytes -d saltybytes -c "CREATE ROLE dashboard_ro LOGIN PASSWORD '<pw>'; GRANT USAGE ON SCHEMA public TO dashboard_ro; GRANT SELECT ON ALL TABLES IN SCHEMA public TO dashboard_ro; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO dashboard_ro;"

# 4. copy images S3 → R2 (50 objects; rclone keeps Content-Type)
rclone sync s3aws:saltybytesrecipeimages r2:saltybytes-images

# 5. point stored image URLs at the new host — only rows on the S3 host;
#    external recipe-site URLs must be left alone. Check the prefix first:
docker compose exec -T db psql -U saltybytes -d saltybytes -c \
  "SELECT DISTINCT substring(image_url from '^https?://[^/]+') AS host, count(*) FROM recipes GROUP BY 1"
docker compose exec -T db psql -U saltybytes -d saltybytes <<'SQL'
BEGIN;
\set old 'https://saltybytesrecipeimages.s3.us-east-2.amazonaws.com/'
\set new 'https://img.saltybytes.ai/'
UPDATE recipes           SET image_url          = :'new' || substr(image_url,          length(:'old')+1) WHERE image_url          LIKE :'old' || '%';
UPDATE recipes           SET original_image_url = :'new' || substr(original_image_url, length(:'old')+1) WHERE original_image_url LIKE :'old' || '%';
UPDATE canonical_recipes SET image_url          = :'new' || substr(image_url,          length(:'old')+1) WHERE image_url          LIKE :'old' || '%';
UPDATE video_imports     SET thumbnail_url      = :'new' || substr(thumbnail_url,      length(:'old')+1) WHERE thumbnail_url      LIKE :'old' || '%';
UPDATE search_caches     SET image_url          = :'new' || substr(image_url,          length(:'old')+1) WHERE image_url          LIKE :'old' || '%';
COMMIT;
SQL

# 6. bring up the rest and smoke-test without touching DNS
scripts/deploy.sh latest
curl -sk --resolve api.saltybytes.ai:443:<droplet-ip> https://api.saltybytes.ai/ping
```
Then add a temporary proxied A record `beta.saltybytes.ai → <droplet-ip>`
(and `beta.saltybytes.ai` to the Caddyfile host list) and walk the app
against it from a phone: login, search, preview/import, image upload,
cooking WebSocket, finder SSE, `/r/<id>`, `/mcp` initialize,
`/.well-known/apple-app-site-association`. Note how long steps 2–5 took.

## 3. Cutover (~15 min of downtime)

1. `aws ecs update-service --cluster saltybytes --service saltybytes-api --desired-count 0`
   — freezes writes (users see a 502 until step 4).
2. `docker compose down` on the droplet, `docker volume rm saltybytes_pgdata`,
   then repeat rehearsal steps 2–6 for a fresh copy.
3. Cloudflare DNS: `api`, `@`, `www` → **A `<droplet-ip>`**, proxied.
   Immediate behind the proxy.
4. Verify: `curl https://api.saltybytes.ai/ping`, the phone walk-through,
   a signup email arriving (SES via the new key), `docker compose logs -f api`.
5. Set `DEPLOY_TARGET=droplet` so the next merge deploys here.

Rollback within the window: DNS back to the ALB CNAMEs + `--desired-count 1`;
RDS was never modified.

## 4. After a week of soak — AWS teardown

Delete: ECS service (0 → delete), cluster, task-definition family · ALB +
listeners + target group · RDS instance (**take a final snapshot first**, keep
it ~30 days) · ECR repository · CloudWatch `/ecs/saltybytes-api` · SSM
`/saltybytes/prod/*` (after confirming `.env` has every value) · ACM
certificates + their `_acm-validations` CNAMEs on Cloudflare · the
`saltybytes-admin` access key + `AWS_ACCESS_KEY_ID/SECRET` GitHub secrets ·
the stray 2020 snapshot in us-west-2 · the `saltybytesrecipeimages` bucket
after 30 days.

Keep: the SES identity + DKIM records, the `saltybytes-ses` IAM user, and the
`saltybytes-monthly` budget lowered to $5 as a tripwire.

Then remove the ECS steps from `.github/workflows/deploy.yml` and the
legacy `amazonaws.com` URL parsing in `internal/s3` (its tests too).

## Day-to-day

```bash
scripts/deploy.sh <tag>                 # what CI runs; `latest` or a 7-char sha
docker compose logs -f --tail=200 api
docker compose exec db psql -U saltybytes saltybytes
scripts/backup.sh                       # ad-hoc backup; cron does it 6-hourly
# restore drill (into a scratch db):
docker compose exec -T db createdb -U saltybytes scratch && \
docker compose exec -T db pg_restore -U saltybytes -d scratch --no-owner /backups/latest.dump
```

Deploys restart the single API container: expect ~5–10 s of 502s. Resizing
the droplet is a one-minute reboot from the DO console if it ever runs hot.
