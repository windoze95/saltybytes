# SaltyBytes API

An AI-enhanced culinary experience and platform.

[SaltyBytes API](https://api.saltybytes.ai)

## Testing

All tests run offline — no database, network, or external services required.

### Run all tests

```bash
go test ./... -count=1
```

### Run tests with verbose output

```bash
go test ./... -v -count=1
```

### Run a specific package

```bash
go test ./internal/service/ -v
go test ./internal/handlers/ -v
go test ./internal/models/ -v
go test ./internal/middleware/ -v
```

### Run a specific test by name

```bash
go test ./internal/service/ -run TestParseISO8601Duration -v
```

### Lint

```bash
go vet ./...
```

### Test structure

```
internal/
├── testutil/
│   ├── mocks.go          # Mock implementations (repos, AI providers)
│   └── fixtures.go       # Factory functions for test data
├── models/
│   ├── user_test.go      # Auth types, subscription tiers, unit system
│   └── openai_embedded_test.go  # RecipeDef/Ingredients JSONB Scan/Value
├── service/
│   ├── import_test.go         # Pure functions (ISO8601, yield, keywords, JSON-LD)
│   ├── recipe_service_test.go # Response mapping, CRUD with mocked repo
│   ├── import_service_test.go # Import flows with mocked AI + repo
│   └── user_service_test.go   # Auth, validation, user CRUD
├── handlers/
│   ├── helpers_test.go        # parseUintParam
│   ├── recipe_handler_test.go # GET/DELETE /recipes with httptest
│   ├── import_handler_test.go # POST /recipes/import/* with httptest
│   └── user_handler_test.go   # POST /users, /auth/login with httptest
└── middleware/
    └── tokens_test.go    # JWT validation (valid, expired, missing, malformed)
```

### Writing new tests

Mocks and fixtures live in `internal/testutil/`:

- **Mocks**: `MockRecipeRepo`, `MockUserRepo`, `MockTextProvider`, `MockVisionProvider`, `MockImageProvider` — all configurable via function fields
- **Fixtures**: `TestUser()`, `TestRecipe()`, `TestRecipeDef()`, `TestRecipeResult()` — return populated model instances

Services accept repository interfaces (`repository.RecipeRepo`, `repository.UserRepo`) defined in `internal/repository/interfaces.go`, enabling dependency injection for tests.