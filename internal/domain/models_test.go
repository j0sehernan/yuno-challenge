package domain

import (
	"testing"
	"time"
)

func TestPaymentRequest_Hash_Deterministic(t *testing.T) {
	req := PaymentRequest{
		IdempotencyKey: "key-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         10000,
		Currency:       "BRL",
	}
	h1 := req.Hash()
	h2 := req.Hash()
	if h1 != h2 {
		t.Errorf("hash not deterministic: %s vs %s", h1, h2)
	}
}

func TestPaymentRequest_Hash_IgnoresIdempotencyKey(t *testing.T) {
	req1 := PaymentRequest{
		IdempotencyKey: "key-A",
		MerchantID:     "m1",
		CustomerID:     "c1",
		Amount:         5000,
		Currency:       "USD",
	}
	req2 := PaymentRequest{
		IdempotencyKey: "key-B",
		MerchantID:     "m1",
		CustomerID:     "c1",
		Amount:         5000,
		Currency:       "USD",
	}
	if req1.Hash() != req2.Hash() {
		t.Error("different idempotency keys with same params should hash equally")
	}
}

func TestPaymentRequest_Hash_DifferentParams(t *testing.T) {
	base := PaymentRequest{
		IdempotencyKey: "k",
		MerchantID:     "m1",
		CustomerID:     "c1",
		Amount:         5000,
		Currency:       "USD",
	}

	variations := []PaymentRequest{
		{IdempotencyKey: "k", MerchantID: "m2", CustomerID: "c1", Amount: 5000, Currency: "USD"},
		{IdempotencyKey: "k", MerchantID: "m1", CustomerID: "c2", Amount: 5000, Currency: "USD"},
		{IdempotencyKey: "k", MerchantID: "m1", CustomerID: "c1", Amount: 9999, Currency: "USD"},
		{IdempotencyKey: "k", MerchantID: "m1", CustomerID: "c1", Amount: 5000, Currency: "BRL"},
	}

	for i, v := range variations {
		if base.Hash() == v.Hash() {
			t.Errorf("variation %d should produce different hash", i)
		}
	}
}

func TestIdempotencyRecord_IsExpired(t *testing.T) {
	// Expired
	expired := IdempotencyRecord{ExpiresAt: time.Now().Add(-1 * time.Hour)}
	if !expired.IsExpired() {
		t.Error("record 1h ago should be expired")
	}

	// Not expired
	future := IdempotencyRecord{ExpiresAt: time.Now().Add(1 * time.Hour)}
	if future.IsExpired() {
		t.Error("record 1h in future should not be expired")
	}

	// Zero time
	zero := IdempotencyRecord{}
	if !zero.IsExpired() {
		t.Error("zero time should be expired")
	}
}
