# Environment Variable Setup

This guide walks through obtaining every value needed in `.env`. Copy `.env.example` to `.env` and fill in each variable.

```bash
cp .env.example .env
```

---

## DATABASE_URL

PostgreSQL connection string. The database must have the `pgvector` extension available.

### Local development

```bash
# Install PostgreSQL (macOS)
brew install postgresql@16
brew services start postgresql@16

# Create the database
createdb saltybytes_db

# Your DATABASE_URL
DATABASE_URL=postgres://$(whoami)@localhost:5432/saltybytes_db?sslmode=disable
```

### Hosted (Railway, Render, etc.)

When you provision a PostgreSQL database on your hosting platform, it will provide a `DATABASE_URL` automatically. Make sure the provider supports the `pgvector` extension:

- **Railway**: Supported natively. Enable via `CREATE EXTENSION IF NOT EXISTS vector` (the app does this automatically on startup).
- **Render**: Use their managed PostgreSQL. pgvector is available on paid plans.
- **Supabase**: pgvector is enabled by default.

---

## PORT

The port the API server listens on. Defaults to `8080` if not set.

Most hosting platforms set `PORT` automatically. You only need to set this for local development if `8080` is taken.

```
PORT=8080
```

---

## JWT_SECRET_KEY

A random secret used to sign authentication tokens. Generate one:

```bash
openssl rand -base64 32
```

Paste the output as your `JWT_SECRET_KEY`. Use a different value for production vs development.

---

## ID_HEADER

A secret header value used for internal request validation. Generate one:

```bash
openssl rand -base64 32
```

---

## Image Storage (S3-compatible)

Recipe images go to any bucket that speaks the S3 API. Production uses
**Cloudflare R2** (free tier, no egress fees, served from a custom domain);
AWS S3, DigitalOcean Spaces and MinIO work the same way.

### Cloudflare R2 (production)

1. In the Cloudflare dashboard → **R2** → **Create bucket**: `saltybytes-images`.
2. Bucket → **Settings** → **Custom Domains** → add `img.saltybytes.ai`
   (Cloudflare creates the DNS record and enables public access through it).
3. **R2** → **Manage R2 API Tokens** → create a token with **Object Read & Write**
   scoped to the bucket. Note the Access Key ID, Secret Access Key and the
   S3 endpoint (`https://<account-id>.r2.cloudflarestorage.com`).

```
S3_BUCKET=saltybytes-images
S3_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com
S3_REGION=auto
S3_ACCESS_KEY_ID=...
S3_SECRET_ACCESS_KEY=...
S3_PUBLIC_URL=https://img.saltybytes.ai
```

`S3_PUBLIC_URL` is what stored image URLs are built from, so the database
never references the storage host directly — moving buckets later is a URL
rewrite, not a code change.

### AWS S3 (alternative)

Leave `S3_ENDPOINT`/`S3_PUBLIC_URL` empty; the SDK's own object URL is stored
(`https://<bucket>.s3.<region>.amazonaws.com/<key>`), so the bucket must allow
public reads:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "PublicReadGetObject",
      "Effect": "Allow",
      "Principal": "*",
      "Action": "s3:GetObject",
      "Resource": "arn:aws:s3:::<bucket>/*"
    }
  ]
}
```

Give the API an IAM key pair (or task role) with `s3:PutObject`, `s3:GetObject`
and `s3:DeleteObject` on `arn:aws:s3:::<bucket>/*`, and set `AWS_REGION`,
`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `S3_BUCKET`.

---

## AWS Credentials (SES email)

Signup verification emails go through Amazon SES (`EMAIL_VERIFICATION_ENABLED=true`
+ `EMAIL_FROM`, a verified identity). The `AWS_*` variables serve SES — and S3
only when no `S3_*` override is set. In production the key pair belongs to a
dedicated IAM user allowed nothing but `ses:SendEmail` / `ses:SendRawEmail`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["ses:SendEmail", "ses:SendRawEmail"],
      "Resource": "*"
    }
  ]
}
```

```
AWS_REGION=us-east-2
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...
```

---

## ANTHROPIC_API_KEY

Used for all text generation and reasoning (recipe creation, forking, allergen analysis, dietary interviews, cooking Q&A, voice intent classification).

1. Go to [console.anthropic.com](https://console.anthropic.com/)
2. Sign up or log in
3. Go to **API Keys** in the left sidebar
4. Click **Create Key**
5. Copy the key (starts with `sk-ant-`)

```
ANTHROPIC_API_KEY=sk-ant-...
```

**Pricing**: Pay-per-use. The app uses Claude 3.5 Sonnet. Typical recipe generation costs ~$0.01-0.03 per request.

---

## OPENAI_API_KEY

Used for three specific services that Anthropic doesn't offer:

- **Whisper** — speech-to-text for voice commands in cooking mode
- **DALL-E** — recipe image generation
- **text-embedding-3-small** — vector embeddings for recipe similarity search

1. Go to [platform.openai.com](https://platform.openai.com/)
2. Sign up or log in
3. Go to **API Keys** in the left sidebar
4. Click **Create new secret key**
5. Copy the key (starts with `sk-`)

```
OPENAI_API_KEY=sk-...
```

**Pricing**: Pay-per-use. DALL-E image generation is the most expensive at ~$0.04 per image. Whisper and embeddings are very cheap.

---

## Web Search (Brave)

Used for web recipe search — finding recipes across the internet. Brave Search is the active provider. Google CSE support exists in the codebase but is disabled because Google no longer allows Custom Search Engines to search the entire web (a curated site list is required).

If `BRAVE_SEARCH_KEY` is not configured, web search returns an error gracefully; all other features work.

### BRAVE_SEARCH_KEY

**Pricing**: $5 per 1,000 requests, with $5 in free monthly credits (~1,000 queries/month at no cost). Credit card required for signup.

1. Go to [brave.com/search/api](https://brave.com/search/api/)
2. Sign up for the **Search** plan
3. Go to your dashboard → **API Keys**
4. Copy the key

```
BRAVE_SEARCH_KEY=BSA...
```

### GOOGLE_SEARCH_KEY + GOOGLE_SEARCH_CX (currently unused)

Google CSE is disabled in the code. These variables are accepted but ignored at runtime. If Google re-enables full-web search for Custom Search Engines in the future, the provider can be re-enabled in `internal/ai/web_search.go`.

```
GOOGLE_SEARCH_KEY=AIza...
GOOGLE_SEARCH_CX=a1b2c3d4e...
```

---

## Quick Start Checklist

```
[ ] DATABASE_URL       — PostgreSQL running locally or hosted
[ ] JWT_SECRET_KEY     — openssl rand -base64 32
[ ] ID_HEADER          — openssl rand -base64 32
[ ] S3_BUCKET          — your bucket name
[ ] S3_ENDPOINT / S3_PUBLIC_URL / S3_ACCESS_KEY_ID / S3_SECRET_ACCESS_KEY — for R2 (empty = AWS S3)
[ ] AWS_REGION         — e.g., us-east-2 (SES; also S3 when S3_ENDPOINT is empty)
[ ] AWS_ACCESS_KEY_ID  — (optional if using IAM role)
[ ] AWS_SECRET_ACCESS_KEY
[ ] ANTHROPIC_API_KEY  — from console.anthropic.com
[ ] OPENAI_API_KEY     — from platform.openai.com
[ ] BRAVE_SEARCH_KEY   — from brave.com/search/api (optional)
[ ] GOOGLE_SEARCH_KEY  — from Google Cloud Console (disabled, optional)
[ ] GOOGLE_SEARCH_CX   — from Programmable Search Engine (disabled, optional)
```

Once all variables are set:

```bash
cd saltybytes-api
go run ./cmd/api
```
