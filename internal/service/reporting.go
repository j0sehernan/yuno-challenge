package service

import (
	"context"
	"time"

	"github.com/kubo-market/idempotency-shield/internal/domain"
	"github.com/kubo-market/idempotency-shield/internal/storage"
)

const suspiciousThreshold = 3 // attempts > 3 are suspicious

// ReportingService generates duplicate detection reports.
type ReportingService struct {
	repo storage.Repository
}

// NewReportingService creates a new ReportingService.
func NewReportingService(repo storage.Repository) *ReportingService {
	return &ReportingService{repo: repo}
}

// GetDuplicateReport returns a full duplicate analysis for a merchant.
func (s *ReportingService) GetDuplicateReport(ctx context.Context, merchantID string, from, to time.Time) (*domain.DuplicateReport, error) {
	duplicates, err := s.repo.GetDuplicates(ctx, merchantID, from, to)
	if err != nil {
		return nil, err
	}

	totalRequests, uniquePayments, err := s.repo.GetMerchantStats(ctx, merchantID, from, to)
	if err != nil {
		return nil, err
	}

	duplicateCount := totalRequests - uniquePayments
	var duplicateRate float64
	if totalRequests > 0 {
		duplicateRate = float64(duplicateCount) / float64(totalRequests) * 100
	}

	var suspicious []domain.SuspiciousKey
	var amountAtRisk int64
	currencyBreakdown := make(map[string]int64)

	for _, d := range duplicates {
		if d.AttemptCount > suspiciousThreshold {
			suspicious = append(suspicious, domain.SuspiciousKey{
				IdempotencyKey: d.IdempotencyKey,
				AttemptCount:   d.AttemptCount,
				Amount:         d.Amount,
				Currency:       d.Currency,
				Status:         d.Status,
				FirstSeenAt:    d.FirstSeenAt,
				LastSeenAt:     d.LastSeenAt,
			})
		}

		// Amount at risk: duplicates that could have been double-charged
		extraAttempts := int64(d.AttemptCount - 1)
		atRisk := d.Amount * extraAttempts
		amountAtRisk += atRisk
		currencyBreakdown[d.Currency] += atRisk
	}

	return &domain.DuplicateReport{
		MerchantID:        merchantID,
		TotalRequests:     totalRequests,
		UniquePayments:    uniquePayments,
		DuplicateCount:    duplicateCount,
		DuplicateRate:     duplicateRate,
		SuspiciousKeys:    suspicious,
		TimeRange:         domain.TimeRange{From: from, To: to},
		AmountAtRisk:      amountAtRisk,
		CurrencyBreakdown: currencyBreakdown,
	}, nil
}
