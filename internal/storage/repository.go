package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/kubo-market/idempotency-shield/internal/domain"
)

// Repository defines the interface for idempotency key storage.
type Repository interface {
	// InsertOrGet atomically inserts a new idempotency key or returns the existing record.
	// Returns the record, a bool indicating if it was newly created, and any error.
	InsertOrGet(ctx context.Context, req domain.PaymentRequest, paymentID string, expiresAt time.Time) (*domain.IdempotencyRecord, bool, error)

	// GetByKey retrieves a record by its idempotency key.
	GetByKey(ctx context.Context, key string) (*domain.IdempotencyRecord, error)

	// MarkComplete updates a record's status and stores the response body.
	MarkComplete(ctx context.Context, key string, status domain.Status, responseBody *json.RawMessage) error

	// ResetToProcessing resets a failed record back to processing for retry.
	ResetToProcessing(ctx context.Context, key string, newPaymentID string, expiresAt time.Time) error

	// DeleteExpired removes records past their expiration.
	DeleteExpired(ctx context.Context) (int64, error)

	// GetDuplicates returns records with attempt_count > 1 for a merchant within a time range.
	GetDuplicates(ctx context.Context, merchantID string, from, to time.Time) ([]domain.IdempotencyRecord, error)

	// GetMerchantStats returns aggregate stats for a merchant within a time range.
	GetMerchantStats(ctx context.Context, merchantID string, from, to time.Time) (total int, unique int, err error)

	// GetPolicy retrieves a merchant's idempotency policy.
	GetPolicy(ctx context.Context, merchantID string) (*domain.MerchantPolicy, error)

	// UpsertPolicy creates or updates a merchant policy.
	UpsertPolicy(ctx context.Context, policy domain.MerchantPolicy) error

	// GetAllMerchantStats returns stats for all merchants within a time range.
	GetAllMerchantStats(ctx context.Context, from, to time.Time) (map[string][2]int, error)
}

// PostgresRepository implements Repository using PostgreSQL.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository creates a new PostgresRepository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// advisoryLockKey generates a consistent int64 hash for pg_advisory_xact_lock.
func advisoryLockKey(idempotencyKey string) int64 {
	h := fnv.New64a()
	h.Write([]byte(idempotencyKey))
	return int64(h.Sum64())
}

// InsertOrGet uses the 3-layer concurrency defense:
// Layer 1: UNIQUE constraint on idempotency_key
// Layer 2: INSERT ... ON CONFLICT in a single atomic statement
// Layer 3: pg_advisory_xact_lock to serialize same-key concurrent requests
func (r *PostgresRepository) InsertOrGet(ctx context.Context, req domain.PaymentRequest, paymentID string, expiresAt time.Time) (*domain.IdempotencyRecord, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Layer 3: Advisory lock serializes concurrent requests for the same key
	lockKey := advisoryLockKey(req.IdempotencyKey)
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		return nil, false, fmt.Errorf("advisory lock: %w", err)
	}

	hash := req.Hash()
	now := time.Now()

	// Layer 2: Atomic upsert - INSERT or return existing (Layer 1: UNIQUE constraint backs this up)
	var rec domain.IdempotencyRecord
	var responseBody sql.NullString
	var completedAt sql.NullTime

	err = tx.QueryRowContext(ctx, `
		INSERT INTO idempotency_keys (idempotency_key, merchant_id, customer_id, amount, currency, status, request_hash, payment_id, first_seen_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'processing', $6, $7, $8, $8, $9)
		ON CONFLICT (idempotency_key) DO UPDATE SET
			last_seen_at = $8,
			attempt_count = idempotency_keys.attempt_count + 1
		RETURNING id, idempotency_key, merchant_id, customer_id, amount, currency, status, request_hash, response_body, payment_id, attempt_count, first_seen_at, last_seen_at, completed_at, expires_at
	`, req.IdempotencyKey, req.MerchantID, req.CustomerID, req.Amount, req.Currency,
		hash, paymentID, now, expiresAt,
	).Scan(
		&rec.ID, &rec.IdempotencyKey, &rec.MerchantID, &rec.CustomerID,
		&rec.Amount, &rec.Currency, &rec.Status, &rec.RequestHash,
		&responseBody, &rec.PaymentID, &rec.AttemptCount,
		&rec.FirstSeenAt, &rec.LastSeenAt, &completedAt, &rec.ExpiresAt,
	)
	if err != nil {
		return nil, false, fmt.Errorf("upsert: %w", err)
	}

	if responseBody.Valid {
		raw := json.RawMessage(responseBody.String)
		rec.ResponseBody = &raw
	}
	if completedAt.Valid {
		rec.CompletedAt = &completedAt.Time
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit: %w", err)
	}

	// attempt_count == 1 means this was a new insert
	isNew := rec.AttemptCount == 1
	return &rec, isNew, nil
}

func (r *PostgresRepository) GetByKey(ctx context.Context, key string) (*domain.IdempotencyRecord, error) {
	var rec domain.IdempotencyRecord
	var responseBody sql.NullString
	var completedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, `
		SELECT id, idempotency_key, merchant_id, customer_id, amount, currency, status, request_hash, response_body, payment_id, attempt_count, first_seen_at, last_seen_at, completed_at, expires_at
		FROM idempotency_keys WHERE idempotency_key = $1
	`, key).Scan(
		&rec.ID, &rec.IdempotencyKey, &rec.MerchantID, &rec.CustomerID,
		&rec.Amount, &rec.Currency, &rec.Status, &rec.RequestHash,
		&responseBody, &rec.PaymentID, &rec.AttemptCount,
		&rec.FirstSeenAt, &rec.LastSeenAt, &completedAt, &rec.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		return nil, domain.ErrKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get by key: %w", err)
	}
	if responseBody.Valid {
		raw := json.RawMessage(responseBody.String)
		rec.ResponseBody = &raw
	}
	if completedAt.Valid {
		rec.CompletedAt = &completedAt.Time
	}
	return &rec, nil
}

func (r *PostgresRepository) MarkComplete(ctx context.Context, key string, status domain.Status, responseBody *json.RawMessage) error {
	var bodyVal interface{}
	if responseBody != nil {
		bodyVal = string(*responseBody)
	}

	res, err := r.db.ExecContext(ctx, `
		UPDATE idempotency_keys SET status = $1, response_body = $2, completed_at = NOW()
		WHERE idempotency_key = $3 AND status = 'processing'
	`, string(status), bodyVal, key)
	if err != nil {
		return fmt.Errorf("mark complete: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		// Check if the key exists at all
		var exists bool
		r.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM idempotency_keys WHERE idempotency_key = $1)", key).Scan(&exists)
		if !exists {
			return domain.ErrKeyNotFound
		}
		return domain.ErrAlreadyCompleted
	}
	return nil
}

func (r *PostgresRepository) ResetToProcessing(ctx context.Context, key string, newPaymentID string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE idempotency_keys SET status = 'processing', payment_id = $1, completed_at = NULL, expires_at = $2, last_seen_at = NOW()
		WHERE idempotency_key = $3 AND status = 'failed'
	`, newPaymentID, expiresAt, key)
	return err
}

func (r *PostgresRepository) DeleteExpired(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE expires_at < NOW()")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *PostgresRepository) GetDuplicates(ctx context.Context, merchantID string, from, to time.Time) ([]domain.IdempotencyRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, idempotency_key, merchant_id, customer_id, amount, currency, status, request_hash, response_body, payment_id, attempt_count, first_seen_at, last_seen_at, completed_at, expires_at
		FROM idempotency_keys
		WHERE merchant_id = $1 AND first_seen_at >= $2 AND first_seen_at <= $3 AND attempt_count > 1
		ORDER BY attempt_count DESC
	`, merchantID, from, to)
	if err != nil {
		return nil, fmt.Errorf("get duplicates: %w", err)
	}
	defer rows.Close()

	var records []domain.IdempotencyRecord
	for rows.Next() {
		var rec domain.IdempotencyRecord
		var responseBody sql.NullString
		var completedAt sql.NullTime
		if err := rows.Scan(
			&rec.ID, &rec.IdempotencyKey, &rec.MerchantID, &rec.CustomerID,
			&rec.Amount, &rec.Currency, &rec.Status, &rec.RequestHash,
			&responseBody, &rec.PaymentID, &rec.AttemptCount,
			&rec.FirstSeenAt, &rec.LastSeenAt, &completedAt, &rec.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan duplicate: %w", err)
		}
		if responseBody.Valid {
			raw := json.RawMessage(responseBody.String)
			rec.ResponseBody = &raw
		}
		if completedAt.Valid {
			rec.CompletedAt = &completedAt.Time
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (r *PostgresRepository) GetMerchantStats(ctx context.Context, merchantID string, from, to time.Time) (int, int, error) {
	var total, unique int
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(attempt_count), 0), COUNT(*)
		FROM idempotency_keys
		WHERE merchant_id = $1 AND first_seen_at >= $2 AND first_seen_at <= $3
	`, merchantID, from, to).Scan(&total, &unique)
	return total, unique, err
}

func (r *PostgresRepository) GetPolicy(ctx context.Context, merchantID string) (*domain.MerchantPolicy, error) {
	var p domain.MerchantPolicy
	err := r.db.QueryRowContext(ctx, `
		SELECT merchant_id, retry_policy, expiry_hours, created_at, updated_at
		FROM merchant_policies WHERE merchant_id = $1
	`, merchantID).Scan(&p.MerchantID, &p.RetryPolicy, &p.ExpiryHours, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, domain.ErrMerchantNotFound
	}
	return &p, err
}

func (r *PostgresRepository) UpsertPolicy(ctx context.Context, policy domain.MerchantPolicy) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_policies (merchant_id, retry_policy, expiry_hours, created_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (merchant_id) DO UPDATE SET
			retry_policy = $2, expiry_hours = $3, updated_at = NOW()
	`, policy.MerchantID, policy.RetryPolicy, policy.ExpiryHours)
	return err
}

func (r *PostgresRepository) GetAllMerchantStats(ctx context.Context, from, to time.Time) (map[string][2]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT merchant_id, COALESCE(SUM(attempt_count), 0), COUNT(*)
		FROM idempotency_keys
		WHERE first_seen_at >= $1 AND first_seen_at <= $2
		GROUP BY merchant_id
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string][2]int)
	for rows.Next() {
		var mid string
		var total, unique int
		if err := rows.Scan(&mid, &total, &unique); err != nil {
			return nil, err
		}
		stats[mid] = [2]int{total, unique}
	}
	return stats, rows.Err()
}
