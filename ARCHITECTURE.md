# Architecture: Idempotency Shield

## Overview

The Idempotency Shield is a **validation layer** that sits between API clients and payment processing. It doesn't process payments — it decides whether a payment request should proceed or is a duplicate.

## Design Decisions

### Why PostgreSQL for Concurrency (not app-level mutexes)?

App-level locks (sync.Mutex, sync.Map) only work on a single server instance. In production, Kubo Market runs multiple server replicas behind a load balancer. PostgreSQL advisory locks provide distributed locking that works across all instances.

### 3-Layer Concurrency Defense

```
Layer 1: UNIQUE(idempotency_key)
  └─ Absolute guarantee at the storage level

Layer 2: INSERT ... ON CONFLICT (atomic upsert)
  └─ No gap between "check if exists" and "insert"

Layer 3: pg_advisory_xact_lock(hash)
  └─ Serializes concurrent requests for the SAME key
  └─ Different keys proceed in parallel (no global lock)
```

### Why amounts in cents (BIGINT)?

Floating-point arithmetic causes precision errors in financial calculations. Storing amounts as integer cents (e.g., $150.00 = 15000) eliminates this class of bugs entirely.

### Why SHA-256 for request hashing?

We need to detect when the same idempotency key is reused with different payment parameters. Hashing the canonical parameters (merchant_id, customer_id, amount, currency) gives us a fast equality check without storing and comparing each field individually.

## Data Flow

```
Client POST /v1/payments
  │
  ├─ Validate request fields
  │
  ├─ BEGIN transaction
  │   ├─ pg_advisory_xact_lock(hash(idempotency_key))
  │   ├─ INSERT ... ON CONFLICT DO UPDATE (attempt_count++)
  │   └─ COMMIT
  │
  ├─ New (attempt_count=1)?
  │   └─ Return 201 "proceed with payment"
  │
  ├─ Existing + processing?
  │   └─ Return 409 "already processing"
  │
  ├─ Existing + succeeded?
  │   └─ Return 200 + cached response
  │
  ├─ Existing + failed + same params?
  │   └─ Reset to processing, return 201 "retry"
  │
  └─ Existing + failed + different params?
      └─ Return 422 "parameter mismatch"
```

## Project Structure

```
cmd/server/main.go       → Entry point, DI, routing, graceful shutdown
internal/
  config/                → Environment-based configuration
  domain/                → Models (PaymentRequest, IdempotencyRecord, Status)
                          Errors (ErrDuplicateProcessing, ErrParamsMismatch)
  storage/               → PostgreSQL connection, migrations, repository
  service/               → Business logic (idempotency + reporting)
  handler/               → HTTP handlers + middleware
  monitor/               → In-memory metrics + anomaly detection
```

## Anomaly Detection

The monitoring system uses a 5-minute sliding window to track duplicate rates. When the rate exceeds 20%, an anomaly flag is raised — indicating a possible buggy client or attack.

## Test Data

130+ events across 3 merchants (kubo-brazil, cloudstore-mx, techhub-co):
- 85 normal successful payments
- 7 double-click scenarios (2-3 retries)
- 3 buggy app scenarios (8-12 retries)
- 3 failed-then-retry cases
- 5 still-processing payments
- 5 failed payments
