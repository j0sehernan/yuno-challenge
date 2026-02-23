package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kubo-market/idempotency-shield/internal/domain"
	"github.com/kubo-market/idempotency-shield/internal/monitor"
	"github.com/kubo-market/idempotency-shield/internal/service"
	"github.com/kubo-market/idempotency-shield/internal/storage"
)

// --- mock repo (same logic as service tests, implements storage.Repository) ---

type mockRepo struct {
	mu      sync.Mutex
	records map[string]*domain.IdempotencyRecord
	nextID  int64

	policies map[string]*domain.MerchantPolicy
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		records:  make(map[string]*domain.IdempotencyRecord),
		nextID:   1,
		policies: make(map[string]*domain.MerchantPolicy),
	}
}

func (m *mockRepo) InsertOrGet(_ context.Context, req domain.PaymentRequest, paymentID string, expiresAt time.Time) (*domain.IdempotencyRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, ok := m.records[req.IdempotencyKey]; ok {
		rec.AttemptCount++
		rec.LastSeenAt = time.Now()
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
	cp := *rec
	return &cp, true, nil
}

func (m *mockRepo) GetByKey(_ context.Context, key string) (*domain.IdempotencyRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.records[key]; ok {
		cp := *rec
		return &cp, nil
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

func (m *mockRepo) DeleteExpired(_ context.Context) (int64, error) { return 0, nil }
func (m *mockRepo) GetDuplicates(_ context.Context, merchantID string, _, _ time.Time) ([]domain.IdempotencyRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []domain.IdempotencyRecord
	for _, rec := range m.records {
		if rec.MerchantID == merchantID && rec.AttemptCount > 1 {
			result = append(result, *rec)
		}
	}
	return result, nil
}
func (m *mockRepo) GetMerchantStats(_ context.Context, merchantID string, _, _ time.Time) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	total, unique := 0, 0
	for _, rec := range m.records {
		if rec.MerchantID == merchantID {
			total += rec.AttemptCount
			unique++
		}
	}
	return total, unique, nil
}
func (m *mockRepo) GetPolicy(_ context.Context, merchantID string) (*domain.MerchantPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.policies[merchantID]; ok {
		return p, nil
	}
	return nil, domain.ErrMerchantNotFound
}
func (m *mockRepo) UpsertPolicy(_ context.Context, policy domain.MerchantPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	policy.CreatedAt = now
	policy.UpdatedAt = now
	m.policies[policy.MerchantID] = &policy
	return nil
}
func (m *mockRepo) GetAllMerchantStats(_ context.Context, _, _ time.Time) (map[string][2]int, error) {
	return nil, nil
}

// ensure mockRepo implements storage.Repository
var _ storage.Repository = (*mockRepo)(nil)

// --- helpers ---

func postJSON(handler http.HandlerFunc, path string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func patchJSON(handler http.HandlerFunc, path string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func getRequest(handler http.HandlerFunc, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// --- Payment handler tests ---

func TestProcessPayment_New_201(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	w := postJSON(h.ProcessPayment, "/v1/payments", domain.PaymentRequest{
		IdempotencyKey: "test-key-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         10000,
		Currency:       "BRL",
	})

	if w.Code != 201 {
		t.Errorf("expected 201, got %d", w.Code)
	}

	var resp domain.PaymentResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Status != domain.StatusProcessing {
		t.Errorf("expected processing, got %s", resp.Status)
	}
}

func TestProcessPayment_Duplicate_409(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	payload := domain.PaymentRequest{
		IdempotencyKey: "dup-key-1",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         10000,
		Currency:       "BRL",
	}

	postJSON(h.ProcessPayment, "/v1/payments", payload) // first
	w := postJSON(h.ProcessPayment, "/v1/payments", payload) // duplicate

	if w.Code != 409 {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestProcessPayment_InvalidJSON_400(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/v1/payments", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ProcessPayment(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestProcessPayment_MissingFields_422(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	w := postJSON(h.ProcessPayment, "/v1/payments", domain.PaymentRequest{
		// missing all fields
	})

	if w.Code != 422 {
		t.Errorf("expected 422, got %d", w.Code)
	}
}

func TestProcessPayment_MethodNotAllowed(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	w := getRequest(h.ProcessPayment, "/v1/payments")
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestProcessPayment_ParamsMismatch_422(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	postJSON(h.ProcessPayment, "/v1/payments", domain.PaymentRequest{
		IdempotencyKey: "mismatch-key",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         10000,
		Currency:       "BRL",
	})

	w := postJSON(h.ProcessPayment, "/v1/payments", domain.PaymentRequest{
		IdempotencyKey: "mismatch-key",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         99999, // different
		Currency:       "BRL",
	})

	if w.Code != 422 {
		t.Errorf("expected 422 for mismatch, got %d", w.Code)
	}
}

func TestProcessPayment_SucceededCached_200(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	payload := domain.PaymentRequest{
		IdempotencyKey: "cached-key",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         10000,
		Currency:       "BRL",
	}
	postJSON(h.ProcessPayment, "/v1/payments", payload)

	// Mark succeeded
	body := json.RawMessage(`{"tx":"123"}`)
	svc.MarkComplete(context.Background(), "cached-key", domain.CompleteRequest{
		Status:       domain.StatusSucceeded,
		ResponseBody: &body,
	})

	w := postJSON(h.ProcessPayment, "/v1/payments", payload)
	if w.Code != 200 {
		t.Errorf("expected 200 cached, got %d", w.Code)
	}
}

// --- CompletePayment tests ---

func TestCompletePayment_200(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	postJSON(h.ProcessPayment, "/v1/payments", domain.PaymentRequest{
		IdempotencyKey: "complete-key",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         10000,
		Currency:       "BRL",
	})

	w := patchJSON(h.CompletePayment, "/v1/payments/complete-key/complete", domain.CompleteRequest{
		Status: domain.StatusSucceeded,
	})

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestCompletePayment_NotFound_404(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	w := patchJSON(h.CompletePayment, "/v1/payments/nonexistent/complete", domain.CompleteRequest{
		Status: domain.StatusSucceeded,
	})

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestCompletePayment_InvalidStatus_422(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	postJSON(h.ProcessPayment, "/v1/payments", domain.PaymentRequest{
		IdempotencyKey: "invalid-status-key",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         10000,
		Currency:       "BRL",
	})

	w := patchJSON(h.CompletePayment, "/v1/payments/invalid-status-key/complete", domain.CompleteRequest{
		Status: "invalid",
	})

	if w.Code != 422 {
		t.Errorf("expected 422, got %d", w.Code)
	}
}

func TestCompletePayment_AlreadyCompleted_409(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	postJSON(h.ProcessPayment, "/v1/payments", domain.PaymentRequest{
		IdempotencyKey: "already-done",
		MerchantID:     "merchant-1",
		CustomerID:     "customer-1",
		Amount:         10000,
		Currency:       "BRL",
	})

	// First complete
	patchJSON(h.CompletePayment, "/v1/payments/already-done/complete", domain.CompleteRequest{
		Status: domain.StatusSucceeded,
	})

	// Second complete → conflict
	w := patchJSON(h.CompletePayment, "/v1/payments/already-done/complete", domain.CompleteRequest{
		Status: domain.StatusSucceeded,
	})

	if w.Code != 409 {
		t.Errorf("expected 409 for already completed, got %d", w.Code)
	}
}

func TestCompletePayment_MethodNotAllowed(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	w := getRequest(h.CompletePayment, "/v1/payments/key/complete")
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestCompletePayment_InvalidJSON_400(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	req := httptest.NewRequest(http.MethodPatch, "/v1/payments/key/complete", bytes.NewReader([]byte("bad")))
	w := httptest.NewRecorder()
	h.CompletePayment(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCompletePayment_ShortPath_400(t *testing.T) {
	repo := newMockRepo()
	svc := service.NewIdempotencyService(repo, 24*time.Hour)
	h := NewPaymentHandler(svc)

	req := httptest.NewRequest(http.MethodPatch, "/v1/payments", bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()
	h.CompletePayment(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for short path, got %d", w.Code)
	}
}

// --- Reporting handler tests ---

func TestGetDuplicates_200(t *testing.T) {
	repo := newMockRepo()
	reportingSvc := service.NewReportingService(repo)
	h := NewReportingHandler(reportingSvc)

	w := getRequest(h.GetDuplicates, "/v1/merchants/merchant-1/duplicates")

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var report domain.DuplicateReport
	json.Unmarshal(w.Body.Bytes(), &report)
	if report.MerchantID != "merchant-1" {
		t.Errorf("expected merchant-1, got %s", report.MerchantID)
	}
}

func TestGetDuplicates_WithTimeRange(t *testing.T) {
	repo := newMockRepo()
	reportingSvc := service.NewReportingService(repo)
	h := NewReportingHandler(reportingSvc)

	from := time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	to := time.Now().Format(time.RFC3339)
	w := getRequest(h.GetDuplicates, "/v1/merchants/merchant-1/duplicates?from="+from+"&to="+to)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGetDuplicates_MethodNotAllowed(t *testing.T) {
	repo := newMockRepo()
	reportingSvc := service.NewReportingService(repo)
	h := NewReportingHandler(reportingSvc)

	w := postJSON(h.GetDuplicates, "/v1/merchants/merchant-1/duplicates", nil)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestGetDuplicates_ShortPath_400(t *testing.T) {
	repo := newMockRepo()
	reportingSvc := service.NewReportingService(repo)
	h := NewReportingHandler(reportingSvc)

	w := getRequest(h.GetDuplicates, "/v1/merchants")
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Policy handler tests ---

func TestUpdatePolicy_PUT_200(t *testing.T) {
	repo := newMockRepo()
	h := NewPolicyHandler(repo)

	body, _ := json.Marshal(map[string]interface{}{
		"retry_policy": "standard",
		"expiry_hours": 24,
	})
	req := httptest.NewRequest(http.MethodPut, "/v1/merchants/merchant-1/policy", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.UpdatePolicy(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUpdatePolicy_GET_200(t *testing.T) {
	repo := newMockRepo()
	h := NewPolicyHandler(repo)

	// First create
	body, _ := json.Marshal(map[string]interface{}{
		"retry_policy": "lenient",
		"expiry_hours": 48,
	})
	req := httptest.NewRequest(http.MethodPut, "/v1/merchants/merchant-1/policy", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.UpdatePolicy(w, req)

	// Then get
	req2 := httptest.NewRequest(http.MethodGet, "/v1/merchants/merchant-1/policy", nil)
	w2 := httptest.NewRecorder()
	h.UpdatePolicy(w2, req2)

	if w2.Code != 200 {
		t.Errorf("expected 200, got %d", w2.Code)
	}
}

func TestUpdatePolicy_GET_NotFound_404(t *testing.T) {
	repo := newMockRepo()
	h := NewPolicyHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/v1/merchants/nonexistent/policy", nil)
	w := httptest.NewRecorder()
	h.UpdatePolicy(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestUpdatePolicy_InvalidRetryPolicy_422(t *testing.T) {
	repo := newMockRepo()
	h := NewPolicyHandler(repo)

	body, _ := json.Marshal(map[string]interface{}{
		"retry_policy": "invalid",
		"expiry_hours": 24,
	})
	req := httptest.NewRequest(http.MethodPut, "/v1/merchants/merchant-1/policy", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.UpdatePolicy(w, req)

	if w.Code != 422 {
		t.Errorf("expected 422, got %d", w.Code)
	}
}

func TestUpdatePolicy_InvalidExpiryHours_422(t *testing.T) {
	repo := newMockRepo()
	h := NewPolicyHandler(repo)

	body, _ := json.Marshal(map[string]interface{}{
		"retry_policy": "standard",
		"expiry_hours": 99,
	})
	req := httptest.NewRequest(http.MethodPut, "/v1/merchants/merchant-1/policy", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.UpdatePolicy(w, req)

	if w.Code != 422 {
		t.Errorf("expected 422, got %d", w.Code)
	}
}

func TestUpdatePolicy_InvalidJSON_400(t *testing.T) {
	repo := newMockRepo()
	h := NewPolicyHandler(repo)

	req := httptest.NewRequest(http.MethodPut, "/v1/merchants/merchant-1/policy", bytes.NewReader([]byte("bad")))
	w := httptest.NewRecorder()
	h.UpdatePolicy(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUpdatePolicy_MethodNotAllowed(t *testing.T) {
	repo := newMockRepo()
	h := NewPolicyHandler(repo)

	req := httptest.NewRequest(http.MethodDelete, "/v1/merchants/merchant-1/policy", nil)
	w := httptest.NewRecorder()
	h.UpdatePolicy(w, req)

	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestUpdatePolicy_ShortPath_400(t *testing.T) {
	repo := newMockRepo()
	h := NewPolicyHandler(repo)

	req := httptest.NewRequest(http.MethodPut, "/v1/merchants", bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()
	h.UpdatePolicy(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for short path, got %d", w.Code)
	}
}

// --- mock pinger for health tests ---

type mockPinger struct{ err error }

func (p *mockPinger) Ping() error { return p.err }

// --- Health handler tests ---

func TestHealth_Healthy(t *testing.T) {
	m := monitor.NewMetrics()
	h := NewHealthHandler(&mockPinger{err: nil}, m)

	w := getRequest(h.Health, "/health")
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHealth_Unhealthy(t *testing.T) {
	m := monitor.NewMetrics()
	h := NewHealthHandler(&mockPinger{err: fmt.Errorf("connection refused")}, m)

	w := getRequest(h.Health, "/health")
	if w.Code != 503 {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestMetrics_200(t *testing.T) {
	m := monitor.NewMetrics()
	h := NewHealthHandler(&mockPinger{}, m)

	w := getRequest(h.Metrics, "/v1/metrics")
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var snap monitor.MetricsSnapshot
	json.Unmarshal(w.Body.Bytes(), &snap)
	if snap.TotalRequests != 0 {
		t.Errorf("expected 0, got %d", snap.TotalRequests)
	}
}

func TestMetrics_MethodNotAllowed(t *testing.T) {
	m := monitor.NewMetrics()
	h := NewHealthHandler(&mockPinger{}, m)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", nil)
	w := httptest.NewRecorder()
	h.Metrics(w, req)

	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHealth_MethodNotAllowed(t *testing.T) {
	m := monitor.NewMetrics()
	h := NewHealthHandler(&mockPinger{}, m)

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()
	h.Health(w, req)

	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// --- Middleware tests ---

func TestLoggingMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	handler := Logging(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})
	handler := Recovery(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestRequestIDMiddleware_Generated(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	handler := RequestID(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID header to be set")
	}
}

func TestRequestIDMiddleware_Passthrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	handler := RequestID(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "custom-id-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("X-Request-ID") != "custom-id-123" {
		t.Errorf("expected custom-id-123, got %s", w.Header().Get("X-Request-ID"))
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 201, map[string]string{"key": "value"})

	if w.Code != 201 {
		t.Errorf("expected 201, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json, got %s", w.Header().Get("Content-Type"))
	}
}
