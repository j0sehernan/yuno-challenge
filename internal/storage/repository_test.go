package storage

import (
	"testing"

	"github.com/kubo-market/idempotency-shield/internal/domain"
)

func TestAdvisoryLockKeyConsistency(t *testing.T) {
	// Same input must produce same lock key
	key1 := advisoryLockKey("payment-abc-123")
	key2 := advisoryLockKey("payment-abc-123")
	if key1 != key2 {
		t.Errorf("same input produced different lock keys: %d vs %d", key1, key2)
	}
}

func TestAdvisoryLockKeyDistribution(t *testing.T) {
	// Different inputs should produce different lock keys
	key1 := advisoryLockKey("payment-abc-123")
	key2 := advisoryLockKey("payment-xyz-456")
	if key1 == key2 {
		t.Error("different inputs produced same lock key (collision)")
	}
}

func TestPaymentRequestHash(t *testing.T) {
	req1 := domain.PaymentRequest{
		IdempotencyKey: "key-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         10000,
		Currency:       "BRL",
	}
	req2 := domain.PaymentRequest{
		IdempotencyKey: "key-2", // different key
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         10000,
		Currency:       "BRL",
	}
	req3 := domain.PaymentRequest{
		IdempotencyKey: "key-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         20000, // different amount
		Currency:       "BRL",
	}

	// Same params (minus idempotency key) should produce same hash
	if req1.Hash() != req2.Hash() {
		t.Error("same params with different idempotency keys should hash the same")
	}

	// Different params should produce different hash
	if req1.Hash() == req3.Hash() {
		t.Error("different params should produce different hashes")
	}
}

func TestRecordIsExpired(t *testing.T) {
	rec := domain.IdempotencyRecord{}

	// Zero time is in the past
	if !rec.IsExpired() {
		t.Error("zero time should be expired")
	}
}
