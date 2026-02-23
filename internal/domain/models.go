package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

type Status string

const (
	StatusProcessing Status = "processing"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
)

// PaymentRequest is the incoming request to validate idempotency.
type PaymentRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	MerchantID     string `json:"merchant_id"`
	CustomerID     string `json:"customer_id"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
}

// Hash returns a SHA-256 hex digest of the canonical payment parameters.
func (p PaymentRequest) Hash() string {
	canonical := fmt.Sprintf("%s|%s|%d|%s", p.MerchantID, p.CustomerID, p.Amount, p.Currency)
	h := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%x", h)
}

// IdempotencyRecord is a stored idempotency key row.
type IdempotencyRecord struct {
	ID             int64            `json:"id"`
	IdempotencyKey string           `json:"idempotency_key"`
	MerchantID     string           `json:"merchant_id"`
	CustomerID     string           `json:"customer_id"`
	Amount         int64            `json:"amount"`
	Currency       string           `json:"currency"`
	Status         Status           `json:"status"`
	RequestHash    string           `json:"request_hash"`
	ResponseBody   *json.RawMessage `json:"response_body,omitempty"`
	PaymentID      string           `json:"payment_id"`
	AttemptCount   int              `json:"attempt_count"`
	FirstSeenAt    time.Time        `json:"first_seen_at"`
	LastSeenAt     time.Time        `json:"last_seen_at"`
	CompletedAt    *time.Time       `json:"completed_at,omitempty"`
	ExpiresAt      time.Time        `json:"expires_at"`
}

// IsExpired reports whether the record has passed its expiration time.
func (r IdempotencyRecord) IsExpired() bool {
	return time.Now().After(r.ExpiresAt)
}

// PaymentResponse is returned from the POST /v1/payments endpoint.
type PaymentResponse struct {
	PaymentID      string           `json:"payment_id"`
	IdempotencyKey string           `json:"idempotency_key"`
	Status         Status           `json:"status"`
	Message        string           `json:"message"`
	AttemptCount   int              `json:"attempt_count"`
	ResponseBody   *json.RawMessage `json:"response_body,omitempty"`
}

// CompleteRequest is the body for PATCH /v1/payments/{key}/complete.
type CompleteRequest struct {
	Status       Status           `json:"status"`
	ResponseBody *json.RawMessage `json:"response_body,omitempty"`
}

// MerchantPolicy holds per-merchant idempotency configuration.
type MerchantPolicy struct {
	MerchantID  string    `json:"merchant_id"`
	RetryPolicy string    `json:"retry_policy"`
	ExpiryHours int       `json:"expiry_hours"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DuplicateReport is a summary for a merchant's duplicate activity.
type DuplicateReport struct {
	MerchantID        string              `json:"merchant_id"`
	TotalRequests     int                 `json:"total_requests"`
	UniquePayments    int                 `json:"unique_payments"`
	DuplicateCount    int                 `json:"duplicate_count"`
	DuplicateRate     float64             `json:"duplicate_rate"`
	SuspiciousKeys    []SuspiciousKey     `json:"suspicious_keys"`
	TimeRange         TimeRange           `json:"time_range"`
	AmountAtRisk      int64               `json:"amount_at_risk"`
	CurrencyBreakdown map[string]int64    `json:"currency_breakdown"`
}

// SuspiciousKey is a key with an abnormally high retry count.
type SuspiciousKey struct {
	IdempotencyKey string    `json:"idempotency_key"`
	AttemptCount   int       `json:"attempt_count"`
	Amount         int64     `json:"amount"`
	Currency       string    `json:"currency"`
	Status         Status    `json:"status"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}

// TimeRange specifies the window of a report.
type TimeRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}
