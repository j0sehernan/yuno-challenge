CREATE TABLE IF NOT EXISTS idempotency_keys (
    id              BIGSERIAL PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    merchant_id     TEXT NOT NULL,
    customer_id     TEXT NOT NULL,
    amount          BIGINT NOT NULL,
    currency        TEXT NOT NULL,
    status          TEXT NOT NULL CHECK(status IN ('processing','succeeded','failed')),
    request_hash    TEXT NOT NULL,
    response_body   JSONB,
    payment_id      TEXT NOT NULL,
    attempt_count   INT NOT NULL DEFAULT 1,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_merchant_time ON idempotency_keys(merchant_id, first_seen_at);
CREATE INDEX IF NOT EXISTS idx_expires_at ON idempotency_keys(expires_at);
CREATE INDEX IF NOT EXISTS idx_merchant_attempts ON idempotency_keys(merchant_id, attempt_count DESC);

CREATE TABLE IF NOT EXISTS merchant_policies (
    merchant_id  TEXT PRIMARY KEY,
    retry_policy TEXT NOT NULL DEFAULT 'standard' CHECK(retry_policy IN ('strict_no_retry','standard','lenient')),
    expiry_hours INT NOT NULL DEFAULT 24 CHECK(expiry_hours IN (24,48,72)),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
