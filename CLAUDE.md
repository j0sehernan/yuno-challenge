# Idempotency Shield

Go backend service that provides idempotency protection for payment processing.

## Quick Reference

- **Language**: Go 1.21
- **Module**: `github.com/kubo-market/idempotency-shield`
- **Port**: 8080 (configurable via `PORT` env var)
- **Database**: PostgreSQL 16
- **No external frameworks** - uses only `net/http` and `lib/pq`

## Project Structure

```
cmd/server/main.go       # Entrypoint, routing, seed data
internal/
  config/                 # Environment config loading
  domain/                 # Models, errors, value objects
  handler/                # HTTP handlers + middleware (logging, recovery, request ID)
  monitor/                # Metrics collection, anomaly detection
  service/                # Business logic (idempotency, reporting)
  storage/                # PostgreSQL repository layer
migrations/               # SQL schema (001_init.sql)
scripts/                  # Demo and seed scripts
```

## Commands

```bash
make build          # Build binary to bin/idempotency-shield
make run            # Build and run
make test           # Run all tests (verbose, no cache)
make race-test      # Run tests with race detector (5 iterations)
make seed           # Run seed data script
make demo           # Run demo script
make clean          # Remove build artifacts
```

## Running Locally

```bash
docker-compose up -d postgres   # Start PostgreSQL
make run                        # Start the app
```

Or full stack:
```bash
docker-compose up               # Starts postgres + app
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check + metrics summary |
| POST | `/v1/payments` | Process payment with idempotency |
| PATCH | `/v1/payments/{key}/complete` | Mark payment as completed/failed |
| GET | `/v1/merchants/{id}/duplicates` | Duplicate activity report |
| PUT | `/v1/merchants/{id}/policy` | Update merchant idempotency policy |
| GET | `/v1/metrics` | System metrics |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Server port |
| `DATABASE_DSN` | - | PostgreSQL connection string |
| `KEY_EXPIRY_HOURS` | `24` | Idempotency key TTL in hours |

## Key Concepts

- **Idempotency keys** expire after configurable TTL (default 24h)
- **Request hashing** uses SHA-256 over `merchant|customer|amount|currency`
- **Duplicate detection** flags keys with high retry counts as suspicious
- **Statuses**: `processing`, `succeeded`, `failed`

## Architecture Rules

- Handlers only parse HTTP and delegate to services
- Services contain business logic and call the repository
- Repository is the only layer that touches the database
- Domain models have no external dependencies
- Middleware chain: Recovery -> Logging -> RequestID -> routes

## Testing

Tests use the `_test.go` convention alongside source files. Run with:
```bash
make test           # Standard
make race-test      # With race detector
```
