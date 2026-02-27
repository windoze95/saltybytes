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

## AWS Credentials (S3 Image Storage)

Recipe images are stored in AWS S3. You need an S3 bucket and IAM credentials.

### 1. Create an S3 bucket

1. Go to [AWS S3 Console](https://s3.console.aws.amazon.com/)
2. Click **Create bucket**
3. Name: `saltybytesrecipeimages` (or your preferred name)
4. Region: `us-east-2` (or your preferred region)
5. Uncheck "Block all public access" (images need to be publicly readable)
6. Create the bucket
7. Add a bucket policy for public read:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "PublicReadGetObject",
      "Effect": "Allow",
      "Principal": "*",
      "Action": "s3:GetObject",
      "Resource": "arn:aws:s3:::saltybytesrecipeimages/*"
    }
  ]
}
```

### 2. Create IAM credentials

1. Go to [AWS IAM Console](https://console.aws.amazon.com/iam/)
2. Create a new user (e.g., `saltybytes-api`)
3. Attach a policy with S3 access to your bucket:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:PutObject", "s3:GetObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::saltybytesrecipeimages/*"
    }
  ]
}
```

4. Create an access key for the user
5. Copy the values:

```
AWS_REGION=us-east-2
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...
S3_BUCKET=saltybytesrecipeimages
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
[ ] AWS_REGION         — e.g., us-east-2
[ ] AWS_ACCESS_KEY_ID  — (optional if using IAM role)
[ ] AWS_SECRET_ACCESS_KEY
[ ] S3_BUCKET          — your bucket name
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
