# Kubo Market Idempotency Shield

Payment idempotency service that prevents duplicate processing even under concurrent requests. Built for Kubo Market (e-commerce, BR/CO/MX, ~45K txns/day) after a mobile app bug caused 1,200+ double charges ($287K).

## Quick Start

### Prerequisites

- Go 1.21+
- PostgreSQL running on `localhost:5432` (user `postgres`, no password)

### Setup

```bash
# Create the database
createdb -U postgres idempotency

# Build and run (auto-migrates schema and seeds data)
make run
```

The server starts on port 8080 with 130+ pre-seeded events.

### Run Tests

```bash
make test          # Unit tests
make race-test     # Concurrency safety (go test -race -count=5)
```

### Run Demo

```bash
make demo          # Runs all scenarios via curl
```

## API Endpoints

| Method | Path | Description | Codes |
|--------|------|-------------|-------|
| POST | `/v1/payments` | Validate payment idempotency | 201, 200, 409, 422 |
| PATCH | `/v1/payments/{key}/complete` | Mark payment result | 200 |
| GET | `/v1/merchants/{id}/duplicates` | Duplicate detection report | 200 |
| GET | `/health` | Health check | 200 |
| GET | `/v1/metrics` | Monitoring metrics | 200 |
| PUT | `/v1/merchants/{id}/policy` | Configure retry policy | 200 |

## Payment State Machine

```
New key           → 201 (processing)
Duplicate + processing → 409 (already processing)
Duplicate + succeeded  → 200 (cached response)
Failed + same params   → 201 (retry allowed)
Failed + diff params   → 422 (mismatch)
Expired key           → 201 (treated as new)
```

## Concurrency Strategy (3-Layer Defense)

1. **UNIQUE constraint** - PostgreSQL rejects duplicates at the DB level
2. **INSERT ... ON CONFLICT** - Atomic upsert, no gap between check and insert
3. **pg_advisory_xact_lock** - Serializes same-key concurrent requests without blocking different keys

## Configuration

| Env Variable | Default | Description |
|-------------|---------|-------------|
| `PORT` | `8080` | Server port |
| `DATABASE_DSN` | `postgres://postgres@localhost:5432/idempotency?sslmode=disable` | PostgreSQL connection |
| `KEY_EXPIRY_HOURS` | `24` | Idempotency key TTL in hours |

## Example Usage

```bash
# New payment
curl -X POST http://localhost:8080/v1/payments \
  -H "Content-Type: application/json" \
  -d '{
    "idempotency_key": "order-12345",
    "merchant_id": "kubo-brazil",
    "customer_id": "cust_001",
    "amount": 15000,
    "currency": "BRL"
  }'

# Mark as succeeded
curl -X PATCH http://localhost:8080/v1/payments/order-12345/complete \
  -H "Content-Type: application/json" \
  -d '{"status": "succeeded", "response_body": {"transaction_id": "tx_abc"}}'

# Check duplicates
curl http://localhost:8080/v1/merchants/kubo-brazil/duplicates
```
