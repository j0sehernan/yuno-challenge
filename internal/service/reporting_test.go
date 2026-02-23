package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kubo-market/idempotency-shield/internal/domain"
)

// reportMockRepo extends mockRepo for reporting tests.
type reportMockRepo struct {
	duplicates []domain.IdempotencyRecord
	total      int
	unique     int
}

func (m *reportMockRepo) InsertOrGet(_ context.Context, _ domain.PaymentRequest, _ string, _ time.Time) (*domain.IdempotencyRecord, bool, error) {
	return nil, false, nil
}
func (m *reportMockRepo) GetByKey(_ context.Context, _ string) (*domain.IdempotencyRecord, error) {
	return nil, domain.ErrKeyNotFound
}
func (m *reportMockRepo) MarkComplete(_ context.Context, _ string, _ domain.Status, _ *json.RawMessage) error {
	return nil
}
func (m *reportMockRepo) ResetToProcessing(_ context.Context, _ string, _ string, _ time.Time) error {
	return nil
}
func (m *reportMockRepo) DeleteExpired(_ context.Context) (int64, error) { return 0, nil }
func (m *reportMockRepo) GetDuplicates(_ context.Context, _ string, _, _ time.Time) ([]domain.IdempotencyRecord, error) {
	return m.duplicates, nil
}
func (m *reportMockRepo) GetMerchantStats(_ context.Context, _ string, _, _ time.Time) (int, int, error) {
	return m.total, m.unique, nil
}
func (m *reportMockRepo) GetPolicy(_ context.Context, _ string) (*domain.MerchantPolicy, error) {
	return nil, domain.ErrMerchantNotFound
}
func (m *reportMockRepo) UpsertPolicy(_ context.Context, _ domain.MerchantPolicy) error { return nil }
func (m *reportMockRepo) GetAllMerchantStats(_ context.Context, _, _ time.Time) (map[string][2]int, error) {
	return nil, nil
}

func TestDuplicateReport_Basic(t *testing.T) {
	now := time.Now()
	repo := &reportMockRepo{
		total:  120,
		unique: 100,
		duplicates: []domain.IdempotencyRecord{
			{IdempotencyKey: "key-1", AttemptCount: 2, Amount: 5000, Currency: "BRL", Status: domain.StatusSucceeded, FirstSeenAt: now, LastSeenAt: now},
			{IdempotencyKey: "key-2", AttemptCount: 8, Amount: 15000, Currency: "BRL", Status: domain.StatusProcessing, FirstSeenAt: now, LastSeenAt: now},
		},
	}

	svc := NewReportingService(repo)
	report, err := svc.GetDuplicateReport(context.Background(), "merchant-1", now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.TotalRequests != 120 {
		t.Errorf("expected 120 total requests, got %d", report.TotalRequests)
	}
	if report.UniquePayments != 100 {
		t.Errorf("expected 100 unique payments, got %d", report.UniquePayments)
	}
	if report.DuplicateCount != 20 {
		t.Errorf("expected 20 duplicate count, got %d", report.DuplicateCount)
	}

	// Duplicate rate: 20/120 * 100 ≈ 16.67%
	if report.DuplicateRate < 16.0 || report.DuplicateRate > 17.0 {
		t.Errorf("expected duplicate rate ~16.67%%, got %.2f%%", report.DuplicateRate)
	}

	// Only key-2 (8 attempts) should be suspicious (> 3 threshold)
	if len(report.SuspiciousKeys) != 1 {
		t.Fatalf("expected 1 suspicious key, got %d", len(report.SuspiciousKeys))
	}
	if report.SuspiciousKeys[0].IdempotencyKey != "key-2" {
		t.Errorf("expected key-2 suspicious, got %s", report.SuspiciousKeys[0].IdempotencyKey)
	}

	// Amount at risk: key-1 = 5000 * 1 = 5000, key-2 = 15000 * 7 = 105000
	expectedRisk := int64(5000 + 105000)
	if report.AmountAtRisk != expectedRisk {
		t.Errorf("expected amount at risk %d, got %d", expectedRisk, report.AmountAtRisk)
	}
}

func TestDuplicateReport_NoDuplicates(t *testing.T) {
	repo := &reportMockRepo{total: 50, unique: 50}
	svc := NewReportingService(repo)
	now := time.Now()

	report, err := svc.GetDuplicateReport(context.Background(), "merchant-clean", now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.DuplicateCount != 0 {
		t.Errorf("expected 0 duplicates, got %d", report.DuplicateCount)
	}
	if report.DuplicateRate != 0 {
		t.Errorf("expected 0%% duplicate rate, got %.2f%%", report.DuplicateRate)
	}
	if len(report.SuspiciousKeys) != 0 {
		t.Errorf("expected no suspicious keys, got %d", len(report.SuspiciousKeys))
	}
}

func TestDuplicateReport_ZeroTotal(t *testing.T) {
	repo := &reportMockRepo{total: 0, unique: 0}
	svc := NewReportingService(repo)
	now := time.Now()

	report, err := svc.GetDuplicateReport(context.Background(), "merchant-empty", now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.DuplicateRate != 0 {
		t.Errorf("expected 0%% rate for zero total, got %.2f%%", report.DuplicateRate)
	}
}
