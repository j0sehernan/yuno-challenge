# Next Steps

## Short Term
- Add rate limiting per merchant (token bucket algorithm)
- Implement key expiration cleanup via background goroutine (periodic `DELETE WHERE expires_at < NOW()`)
- Add structured JSON logging (replace `log.Printf`)

## Medium Term
- Redis caching layer for hot idempotency keys (reduce DB load)
- Webhook notifications for anomaly detection alerts (>20% duplicate rate)
- Prometheus metrics exporter (`/metrics` in OpenMetrics format)
- API authentication via API keys or JWT

## Long Term
- Multi-region PostgreSQL replication for disaster recovery
- Event sourcing for full audit trail of payment state transitions
- Admin dashboard UI for real-time duplicate monitoring
- Load testing suite (k6/vegeta) simulating 45K txns/day with burst patterns
