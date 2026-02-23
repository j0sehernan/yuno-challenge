package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kubo-market/idempotency-shield/internal/domain"
	"github.com/kubo-market/idempotency-shield/internal/storage"
)

// IdempotencyService implements the core idempotency validation logic.
type IdempotencyService struct {
	repo      storage.Repository
	expiryTTL time.Duration
}

// NewIdempotencyService creates a new IdempotencyService.
func NewIdempotencyService(repo storage.Repository, expiryTTL time.Duration) *IdempotencyService {
	return &IdempotencyService{repo: repo, expiryTTL: expiryTTL}
}

// ProcessPayment validates an incoming payment request against the idempotency state machine:
//
//	New key → INSERT status='processing' → 201
//	Duplicate + processing → return 409
//	Duplicate + succeeded → return 200 cached result
//	Duplicate + failed + params match → reset to 'processing' → 201
//	Duplicate + failed + params differ → return 422 mismatch
//	Expired key → treat as new → 201
func (s *IdempotencyService) ProcessPayment(ctx context.Context, req domain.PaymentRequest) (*domain.PaymentResponse, int, error) {
	if err := validateRequest(req); err != nil {
		return nil, 422, err
	}

	paymentID := generatePaymentID()
	expiresAt := time.Now().Add(s.expiryTTL)

	rec, isNew, err := s.repo.InsertOrGet(ctx, req, paymentID, expiresAt)
	if err != nil {
		return nil, 500, fmt.Errorf("insert or get: %w", err)
	}

	// New key - first time seeing this idempotency key
	if isNew {
		return &domain.PaymentResponse{
			PaymentID:      rec.PaymentID,
			IdempotencyKey: rec.IdempotencyKey,
			Status:         domain.StatusProcessing,
			Message:        "payment accepted for processing",
			AttemptCount:   1,
		}, 201, nil
	}

	// Existing key - check if expired first
	if rec.IsExpired() {
		// Expired: delete and treat as new
		// The InsertOrGet already bumped attempt_count, but we reset
		if err := s.repo.ResetToProcessing(ctx, rec.IdempotencyKey, paymentID, expiresAt); err != nil {
			return nil, 500, fmt.Errorf("reset expired: %w", err)
		}
		return &domain.PaymentResponse{
			PaymentID:      paymentID,
			IdempotencyKey: rec.IdempotencyKey,
			Status:         domain.StatusProcessing,
			Message:        "expired key reused, payment accepted for processing",
			AttemptCount:   rec.AttemptCount,
		}, 201, nil
	}

	// Check parameter mismatch
	requestHash := req.Hash()

	switch rec.Status {
	case domain.StatusProcessing:
		// Duplicate while still processing
		if rec.RequestHash != requestHash {
			return nil, 422, domain.ErrParamsMismatch
		}
		return &domain.PaymentResponse{
			PaymentID:      rec.PaymentID,
			IdempotencyKey: rec.IdempotencyKey,
			Status:         domain.StatusProcessing,
			Message:        "payment is already being processed",
			AttemptCount:   rec.AttemptCount,
		}, 409, nil

	case domain.StatusSucceeded:
		// Already succeeded - return cached response
		return &domain.PaymentResponse{
			PaymentID:      rec.PaymentID,
			IdempotencyKey: rec.IdempotencyKey,
			Status:         domain.StatusSucceeded,
			Message:        "payment already succeeded",
			AttemptCount:   rec.AttemptCount,
			ResponseBody:   rec.ResponseBody,
		}, 200, nil

	case domain.StatusFailed:
		// Failed - allow retry only if params match
		if rec.RequestHash != requestHash {
			return nil, 422, domain.ErrParamsMismatch
		}
		// Reset to processing for retry
		if err := s.repo.ResetToProcessing(ctx, rec.IdempotencyKey, paymentID, expiresAt); err != nil {
			return nil, 500, fmt.Errorf("reset to processing: %w", err)
		}
		return &domain.PaymentResponse{
			PaymentID:      paymentID,
			IdempotencyKey: rec.IdempotencyKey,
			Status:         domain.StatusProcessing,
			Message:        "previous attempt failed, retrying",
			AttemptCount:   rec.AttemptCount,
		}, 201, nil

	default:
		return nil, 500, fmt.Errorf("unknown status: %s", rec.Status)
	}
}

// MarkComplete finalizes a payment with either succeeded or failed status.
func (s *IdempotencyService) MarkComplete(ctx context.Context, key string, req domain.CompleteRequest) error {
	if req.Status != domain.StatusSucceeded && req.Status != domain.StatusFailed {
		return domain.ErrInvalidStatus
	}
	return s.repo.MarkComplete(ctx, key, req.Status, req.ResponseBody)
}

func validateRequest(req domain.PaymentRequest) error {
	if req.IdempotencyKey == "" {
		return fmt.Errorf("idempotency_key is required")
	}
	if req.MerchantID == "" {
		return fmt.Errorf("merchant_id is required")
	}
	if req.CustomerID == "" {
		return fmt.Errorf("customer_id is required")
	}
	if req.Amount < 0 {
		return fmt.Errorf("amount must be non-negative")
	}
	if req.Currency == "" {
		return fmt.Errorf("currency is required")
	}
	return nil
}

func generatePaymentID() string {
	return fmt.Sprintf("pay_%d", time.Now().UnixNano())
}
