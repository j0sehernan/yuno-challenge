package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kubo-market/idempotency-shield/internal/domain"
)

// mockRepo is an in-memory repository for unit tests.
type mockRepo struct {
	mu      sync.Mutex
	records map[string]*domain.IdempotencyRecord
	nextID  int64
}

func newMockRepo() *mockRepo {
	return &mockRepo{records: make(map[string]*domain.IdempotencyRecord), nextID: 1}
}

func (m *mockRepo) InsertOrGet(_ context.Context, req domain.PaymentRequest, paymentID string, expiresAt time.Time) (*domain.IdempotencyRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, ok := m.records[req.IdempotencyKey]; ok {
		rec.AttemptCount++
		rec.LastSeenAt = time.Now()
		// Return a copy to avoid data races on the shared record
		cp := *rec
		return &cp, false, nil
	}

	now := time.Now()
	rec := &domain.IdempotencyRecord{
		ID:             m.nextID,
		IdempotencyKey: req.IdempotencyKey,
		MerchantID:     req.MerchantID,
		CustomerID:     req.CustomerID,
		Amount:         req.Amount,
		Currency:       req.Currency,
		Status:         domain.StatusProcessing,
		RequestHash:    req.Hash(),
		PaymentID:      paymentID,
		AttemptCount:   1,
		FirstSeenAt:    now,
		LastSeenAt:     now,
		ExpiresAt:      expiresAt,
	}
	m.nextID++
	m.records[req.IdempotencyKey] = rec
	// Return a copy
	cp := *rec
	return &cp, true, nil
}

func (m *mockRepo) GetByKey(_ context.Context, key string) (*domain.IdempotencyRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.records[key]; ok {
		return rec, nil
	}
	return nil, domain.ErrKeyNotFound
}

func (m *mockRepo) MarkComplete(_ context.Context, key string, status domain.Status, responseBody *json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[key]
	if !ok {
		return domain.ErrKeyNotFound
	}
	if rec.Status != domain.StatusProcessing {
		return domain.ErrAlreadyCompleted
	}
	rec.Status = status
	rec.ResponseBody = responseBody
	now := time.Now()
	rec.CompletedAt = &now
	return nil
}

func (m *mockRepo) ResetToProcessing(_ context.Context, key string, newPaymentID string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[key]
	if !ok {
		return domain.ErrKeyNotFound
	}
	rec.Status = domain.StatusProcessing
	rec.PaymentID = newPaymentID
	rec.CompletedAt = nil
	rec.ExpiresAt = expiresAt
	return nil
}

func (m *mockRepo) DeleteExpired(_ context.Context) (int64, error)                    { return 0, nil }
func (m *mockRepo) GetDuplicates(_ context.Context, _ string, _, _ time.Time) ([]domain.IdempotencyRecord, error) {
	return nil, nil
}
func (m *mockRepo) GetMerchantStats(_ context.Context, _ string, _, _ time.Time) (int, int, error) {
	return 0, 0, nil
}
func (m *mockRepo) GetPolicy(_ context.Context, _ string) (*domain.MerchantPolicy, error) {
	return nil, domain.ErrMerchantNotFound
}
func (m *mockRepo) UpsertPolicy(_ context.Context, _ domain.MerchantPolicy) error { return nil }
func (m *mockRepo) GetAllMerchantStats(_ context.Context, _, _ time.Time) (map[string][2]int, error) {
	return nil, nil
}

func TestProcessPayment_NewKey(t *testing.T) {
	svc := NewIdempotencyService(newMockRepo(), 24*time.Hour)
	req := domain.PaymentRequest{
		IdempotencyKey: "key-new-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         5000,
		Currency:       "BRL",
	}

	resp, code, err := svc.ProcessPayment(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 201 {
		t.Errorf("expected 201, got %d", code)
	}
	if resp.Status != domain.StatusProcessing {
		t.Errorf("expected status processing, got %s", resp.Status)
	}
	if resp.AttemptCount != 1 {
		t.Errorf("expected attempt_count 1, got %d", resp.AttemptCount)
	}
}

func TestProcessPayment_DuplicateWhileProcessing(t *testing.T) {
	svc := NewIdempotencyService(newMockRepo(), 24*time.Hour)
	req := domain.PaymentRequest{
		IdempotencyKey: "key-dup-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         5000,
		Currency:       "BRL",
	}

	// First request
	_, code1, _ := svc.ProcessPayment(context.Background(), req)
	if code1 != 201 {
		t.Fatalf("first request: expected 201, got %d", code1)
	}

	// Duplicate request
	resp, code2, _ := svc.ProcessPayment(context.Background(), req)
	if code2 != 409 {
		t.Errorf("duplicate request: expected 409, got %d", code2)
	}
	if resp.Message != "payment is already being processed" {
		t.Errorf("unexpected message: %s", resp.Message)
	}
}

func TestProcessPayment_DuplicateAfterSuccess(t *testing.T) {
	repo := newMockRepo()
	svc := NewIdempotencyService(repo, 24*time.Hour)
	req := domain.PaymentRequest{
		IdempotencyKey: "key-success-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         5000,
		Currency:       "BRL",
	}

	// First request
	svc.ProcessPayment(context.Background(), req)

	// Mark as succeeded
	body := json.RawMessage(`{"transaction_id":"tx-123"}`)
	svc.MarkComplete(context.Background(), "key-success-1", domain.CompleteRequest{
		Status:       domain.StatusSucceeded,
		ResponseBody: &body,
	})

	// Duplicate after success
	resp, code, _ := svc.ProcessPayment(context.Background(), req)
	if code != 200 {
		t.Errorf("expected 200, got %d", code)
	}
	if resp.Status != domain.StatusSucceeded {
		t.Errorf("expected succeeded, got %s", resp.Status)
	}
	if resp.ResponseBody == nil {
		t.Error("expected cached response body")
	}
}

func TestProcessPayment_RetryAfterFailure(t *testing.T) {
	repo := newMockRepo()
	svc := NewIdempotencyService(repo, 24*time.Hour)
	req := domain.PaymentRequest{
		IdempotencyKey: "key-fail-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         5000,
		Currency:       "BRL",
	}

	// First request
	svc.ProcessPayment(context.Background(), req)

	// Mark as failed
	svc.MarkComplete(context.Background(), "key-fail-1", domain.CompleteRequest{
		Status: domain.StatusFailed,
	})

	// Retry with same params
	resp, code, _ := svc.ProcessPayment(context.Background(), req)
	if code != 201 {
		t.Errorf("expected 201 for retry, got %d", code)
	}
	if resp.Message != "previous attempt failed, retrying" {
		t.Errorf("unexpected message: %s", resp.Message)
	}
}

func TestProcessPayment_ParamsMismatch(t *testing.T) {
	svc := NewIdempotencyService(newMockRepo(), 24*time.Hour)
	req1 := domain.PaymentRequest{
		IdempotencyKey: "key-mismatch-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         5000,
		Currency:       "BRL",
	}

	// First request
	svc.ProcessPayment(context.Background(), req1)

	// Different params, same key
	req2 := domain.PaymentRequest{
		IdempotencyKey: "key-mismatch-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         9999, // different amount
		Currency:       "BRL",
	}

	_, code, err := svc.ProcessPayment(context.Background(), req2)
	if code != 422 {
		t.Errorf("expected 422, got %d", code)
	}
	if !errors.Is(err, domain.ErrParamsMismatch) {
		t.Errorf("expected ErrParamsMismatch, got %v", err)
	}
}

func TestProcessPayment_ValidationErrors(t *testing.T) {
	svc := NewIdempotencyService(newMockRepo(), 24*time.Hour)

	tests := []struct {
		name string
		req  domain.PaymentRequest
	}{
		{"missing idempotency_key", domain.PaymentRequest{MerchantID: "m", CustomerID: "c", Amount: 1, Currency: "BRL"}},
		{"missing merchant_id", domain.PaymentRequest{IdempotencyKey: "k", CustomerID: "c", Amount: 1, Currency: "BRL"}},
		{"missing customer_id", domain.PaymentRequest{IdempotencyKey: "k", MerchantID: "m", Amount: 1, Currency: "BRL"}},
		{"missing currency", domain.PaymentRequest{IdempotencyKey: "k", MerchantID: "m", CustomerID: "c", Amount: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, code, err := svc.ProcessPayment(context.Background(), tt.req)
			if code != 422 {
				t.Errorf("expected 422, got %d", code)
			}
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestMarkComplete_InvalidStatus(t *testing.T) {
	svc := NewIdempotencyService(newMockRepo(), 24*time.Hour)
	err := svc.MarkComplete(context.Background(), "any-key", domain.CompleteRequest{
		Status: "invalid",
	})
	if !errors.Is(err, domain.ErrInvalidStatus) {
		t.Errorf("expected ErrInvalidStatus, got %v", err)
	}
}

func TestProcessPayment_ConcurrentSameKey(t *testing.T) {
	repo := newMockRepo()
	svc := NewIdempotencyService(repo, 24*time.Hour)
	req := domain.PaymentRequest{
		IdempotencyKey: "key-concurrent-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         5000,
		Currency:       "BRL",
	}

	const concurrency = 10
	results := make([]int, concurrency)
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			_, code, _ := svc.ProcessPayment(context.Background(), req)
			results[idx] = code
		}(i)
	}
	wg.Wait()

	// Exactly one should get 201 (new), rest should get 409 (duplicate)
	created := 0
	duplicates := 0
	for _, code := range results {
		switch code {
		case 201:
			created++
		case 409:
			duplicates++
		}
	}
	if created != 1 {
		t.Errorf("expected exactly 1 created (201), got %d", created)
	}
	if duplicates != concurrency-1 {
		t.Errorf("expected %d duplicates (409), got %d", concurrency-1, duplicates)
	}
}

func TestProcessPayment_ExpiredKey(t *testing.T) {
	repo := newMockRepo()
	svc := NewIdempotencyService(repo, 1*time.Nanosecond) // expire immediately

	req := domain.PaymentRequest{
		IdempotencyKey: "key-expired-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         5000,
		Currency:       "BRL",
	}

	// First request
	_, code1, _ := svc.ProcessPayment(context.Background(), req)
	if code1 != 201 {
		t.Fatalf("expected 201, got %d", code1)
	}

	// Wait for expiry
	time.Sleep(10 * time.Millisecond)

	// Second request - key is expired, should be treated as new
	resp, code2, _ := svc.ProcessPayment(context.Background(), req)
	if code2 != 201 {
		t.Errorf("expected 201 for expired key retry, got %d", code2)
	}
	if resp.Message != "expired key reused, payment accepted for processing" {
		t.Errorf("unexpected message: %s", resp.Message)
	}
}
