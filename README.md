# SaltyBytes API

Backend for [SaltyBytes](https://saltybytes.ai) — an AI-powered recipe platform built with Go, Gin, PostgreSQL, and pgvector.

## Architecture

Handlers → Services → Repositories → PostgreSQL (GORM)

**AI Providers:**
- **Anthropic Claude** (Sonnet for full generation/analysis, Haiku for cheap preview/extraction)
- **OpenAI** (DALL-E 3 for recipe images, Whisper for voice transcription, text-embedding-3-small for vector search)
- **Brave Search** for web recipe discovery

**Key systems:**
- Recipe generation, regeneration, and forking via conversational AI
- Recipe tree (DAG) for branching version history
- Multi-source import pipeline (URL with JSON-LD extraction, photo via vision, text, manual)
- Multi-tier search cache (exact-match → pgvector semantic similarity → Brave API fallback)
- Allergen analysis with family dietary cross-referencing
- Real-time cooking mode via WebSocket (voice commands, Q&A, ephemeral edits)
- Subscription-based usage tracking with per-feature quotas

**Infrastructure:**
- Deployed on AWS ECS (Fargate) with RDS PostgreSQL
- Recipe images stored in S3
- CI via GitHub Actions (vet + test on PR, Docker build + ECR push + ECS deploy on merge)

## Testing

All tests run offline — no database, network, or external services required.

```bash
# Run all tests
go test ./... -count=1

# Verbose
go test ./... -v -count=1

# Specific package
go test ./internal/service/ -v

# Specific test
go test ./internal/service/ -run TestParseISO8601Duration -v

# Lint
go vet ./...
```

Mocks and fixtures live in `internal/testutil/`. Services accept repository interfaces (`repository.RecipeRepo`, `repository.UserRepo`, `repository.SearchCacheRepo`) for dependency injection in tests.

## Environment Variables

| Variable | Required | Purpose |
|----------|----------|---------|
| `DATABASE_URL` | Yes | PostgreSQL connection string |
| `JWT_SECRET_KEY` | Yes | Token signing secret |
| `ANTHROPIC_API_KEY` | Yes | Claude (recipe gen, allergens, voice intent, dietary) |
| `OPENAI_API_KEY` | Yes | DALL-E, Whisper, embeddings |
| `AWS_REGION` | Yes | AWS region for S3 |
| `S3_BUCKET` | Yes | Image storage bucket |
| `ID_HEADER` | Yes | Custom header validation value |
| `BRAVE_SEARCH_KEY` | No | Web recipe search |
| `GOOGLE_SEARCH_KEY` | No | Google CSE (currently disabled) |
| `GOOGLE_SEARCH_CX` | No | Google CSE ID (currently disabled) |
| `AWS_ACCESS_KEY_ID` | No | S3 auth (falls back to IAM role) |
| `AWS_SECRET_ACCESS_KEY` | No | S3 auth (falls back to IAM role) |
| `PORT` | No | Server port (default: 8080) |
| `GIN_MODE` | No | `release` for production logging |

## Deployment

```bash
# Build and push (handled by CI on merge to main)
docker build --platform linux/amd64 -t saltybytes-api .
docker tag saltybytes-api:latest 915841655539.dkr.ecr.us-east-2.amazonaws.com/saltybytes-api:latest
docker push 915841655539.dkr.ecr.us-east-2.amazonaws.com/saltybytes-api:latest
aws ecs update-service --cluster saltybytes --service saltybytes-api --force-new-deployment
```
