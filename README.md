# SaltyBytes API

Backend for [SaltyBytes](https://saltybytes.ai) — a recipe app for iOS and Android. Search the web for recipes without the ads and life stories, import from any source, generate with AI, and cook hands-free with voice-guided cooking mode.

Built with Go, Gin, PostgreSQL + pgvector, and Claude AI.

> **See also:** [saltybytes-app](https://github.com/windoze95/saltybytes-app) — the Flutter iOS client | [saltybytes-dashboard](https://github.com/windoze95/saltybytes-dashboard) — the operational metrics dashboard.

## Features

**Recipe Search & Discovery** — Search the web for recipes and get clean results — no ads, no SEO spam, no scrolling past someone's vacation story. Multi-tier pipeline: exact-match cache, pgvector semantic similarity, and Brave web search. Import any result directly into your collection.

**Multi-Source Import** — Import recipes from URLs (with JSON-LD extraction and Firecrawl fallback), photos (vision-based), freeform text, or manual entry. A canonical recipe cache deduplicates URL imports with automatic background refresh.

**AI Recipe Generation** — Create recipes through conversation with Claude when you can't find what you're looking for. Fork existing recipes into new variants, regenerate with feedback, and explore branching version history through a recipe tree.

**Family Allergen Analysis** — AI-powered ingredient analysis detects common allergens (dairy, nuts, shellfish, wheat, soy, sesame, etc.) with confidence scoring. Cross-reference results against family members' dietary profiles.

**Real-Time Cooking Mode** — WebSocket-based hands-free cooking. Voice commands are transcribed (Whisper), classified by intent (Claude), and answered contextually. Supports ephemeral recipe edits during cooking.

**AI Dietary Interviews** — Conversational dietary profiling for family members, covering allergies, intolerances, and preferences.

## Architecture

```
Handlers → Services → Repositories → PostgreSQL (GORM)
```

| Layer | Purpose |
|-------|---------|
| `internal/handlers/` | HTTP request handling and validation |
| `internal/service/` | Business logic, AI orchestration |
| `internal/repository/` | Database access via interfaces (DI-friendly) |
| `internal/models/` | Data models and GORM schema |
| `internal/ai/` | Anthropic, OpenAI, and Brave Search providers |
| `internal/ws/` | WebSocket server for cooking mode |
| `internal/middleware/` | JWT auth, rate limiting, request context |
| `configs/` | AI prompt templates (YAML) |

### External Services

| Service | Used For |
|---------|----------|
| **Anthropic Claude** | Recipe generation, allergen analysis, dietary interviews, voice intent classification, cooking Q&A |
| **OpenAI** | DALL-E 3 (recipe images), Whisper (voice transcription), text-embedding-3-small (vector search) |
| **Brave Search** | Web recipe discovery |
| **AWS S3** | Recipe image storage |
| **PostgreSQL + pgvector** | Data persistence and semantic similarity search |

## Getting Started

### Prerequisites

- Go 1.24+
- PostgreSQL 16+ with the [pgvector](https://github.com/pgvector/pgvector) extension
- API keys for [Anthropic](https://console.anthropic.com/), [OpenAI](https://platform.openai.com/), and an AWS account for S3

### Setup

1. **Clone the repository**

```bash
git clone https://github.com/windoze95/saltybytes-api.git
cd saltybytes-api
```

2. **Configure environment variables**

```bash
cp .env.example .env
```

See [docs/env-setup.md](docs/env-setup.md) for a detailed walkthrough of every variable, including how to create API keys and configure AWS.

3. **Run with Docker Compose** (recommended)

```bash
docker compose up
```

This starts the API and a PostgreSQL instance. The database schema is auto-migrated on startup.

4. **Or run directly**

```bash
go run ./cmd/api
```

### Environment Variables

| Variable | Required | Purpose |
|----------|----------|---------|
| `DATABASE_URL` | Yes | PostgreSQL connection string |
| `JWT_SECRET_KEY` | Yes | Token signing secret |
| `ANTHROPIC_API_KEY` | Yes | Claude — recipe gen, allergens, voice, dietary |
| `OPENAI_API_KEY` | Yes | DALL-E, Whisper, embeddings |
| `AWS_REGION` | Yes | AWS region for S3 |
| `S3_BUCKET` | Yes | Image storage bucket |
| `ID_HEADER` | Yes | Request validation header |
| `BRAVE_SEARCH_KEY` | No | Web recipe search (gracefully disabled if absent) |
| `AWS_ACCESS_KEY_ID` | No | S3 auth (falls back to IAM role) |
| `AWS_SECRET_ACCESS_KEY` | No | S3 auth (falls back to IAM role) |
| `PORT` | No | Server port (default: 8080) |
| `GIN_MODE` | No | Set to `release` for production |

## API Overview

### Authentication
- `POST /v1/users` — Create account
- `POST /v1/auth/login` — Login
- `POST /v1/auth/refresh` — Refresh token

### Recipes
- `POST /v1/recipes/chat` — Generate recipe via conversation
- `PUT /v1/recipes/:id/chat` — Regenerate with feedback
- `POST /v1/recipes/:id/fork` — Fork into a new variant
- `GET /v1/recipes/:id/tree` — Version history tree
- `GET /v1/recipes` — List user's recipes
- `DELETE /v1/recipes/:id` — Delete recipe

### Import
- `POST /v1/recipes/import/url` — Import from URL
- `POST /v1/recipes/import/photo` — Import from photo
- `POST /v1/recipes/import/text` — Import from text
- `POST /v1/recipes/import/manual` — Manual entry
- `POST /v1/recipes/preview/url` — Quick URL preview

### Search
- `GET /v1/recipes/search` — Search recipes (semantic + web)
- `GET /v1/recipes/similar/:id` — Find similar recipes

### Allergens
- `POST /v1/recipes/:id/allergens/analyze` — Run allergen analysis
- `GET /v1/recipes/:id/allergens` — Get analysis results
- `POST /v1/recipes/:id/allergens/check-family` — Cross-reference family dietary profiles

### Family & Dietary
- `POST /v1/family` — Create family
- `POST /v1/family/members` — Add member
- `PUT /v1/family/members/:id/dietary` — Update dietary profile
- `POST /v1/family/members/:id/dietary/interview` — AI dietary interview

### Cooking Mode
- `GET /v1/ws/cook/:id` — WebSocket connection for hands-free cooking

## Testing

All tests run offline — no database, network, or external services required. Services accept repository interfaces for dependency injection.

```bash
# Run all tests
go test ./... -count=1

# Verbose
go test ./... -v -count=1

# Specific package
go test ./internal/service/ -v

# Lint
go vet ./...
```

## Deployment

The CI/CD pipeline runs automatically via GitHub Actions:

- **On PR to main** — vet, test, build validation
- **On merge to main** — vet, test, Docker build, push to ECR, deploy to ECS (Fargate)

### Manual deployment

```bash
docker build --platform linux/amd64 -t saltybytes-api .
```

The Dockerfile uses a multi-stage build (Go 1.24 builder → distroless runtime) for a minimal production image.

## Tech Stack

- **Language**: Go 1.24
- **Framework**: Gin
- **ORM**: GORM
- **Database**: PostgreSQL 17 + pgvector
- **AI**: Anthropic Claude, OpenAI (DALL-E, Whisper, Embeddings)
- **Search**: Brave Search API
- **Storage**: AWS S3
- **Auth**: JWT (golang-jwt)
- **WebSocket**: gorilla/websocket
- **Logging**: zap
- **Container**: Docker (distroless base)
- **CI/CD**: GitHub Actions → ECR → ECS Fargate

## License

This project is dual-licensed:

- **Open Source**: [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE) — you are free to use, modify, and distribute this software, provided that any derivative work or network service built with it is also released under the AGPL-3.0.
- **Commercial**: If you wish to use this software in proprietary/closed-source applications without the AGPL-3.0 obligations, a commercial license is available. Contact the maintainer for details.
