package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/kubo-market/idempotency-shield/internal/domain"
)

func getTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		dsn = "postgres://postgres@localhost:5432/idempotency?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("skipping integration test: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("skipping integration test (DB not available): %v", err)
	}
	// Run migration
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS idempotency_keys (
			id              BIGSERIAL PRIMARY KEY,
			idempotency_key TEXT NOT NULL UNIQUE,
			merchant_id     TEXT NOT NULL,
			customer_id     TEXT NOT NULL,
			amount          BIGINT NOT NULL,
			currency        TEXT NOT NULL,
			status          TEXT NOT NULL CHECK(status IN ('processing','succeeded','failed')),
			request_hash    TEXT NOT NULL,
			response_body   JSONB,
			payment_id      TEXT NOT NULL,
			attempt_count   INT NOT NULL DEFAULT 1,
			first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			completed_at    TIMESTAMPTZ,
			expires_at      TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE IF NOT EXISTS merchant_policies (
			merchant_id  TEXT PRIMARY KEY,
			retry_policy TEXT NOT NULL DEFAULT 'standard' CHECK(retry_policy IN ('strict_no_retry','standard','lenient')),
			expiry_hours INT NOT NULL DEFAULT 24 CHECK(expiry_hours IN (24,48,72)),
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	if err != nil {
		t.Fatalf("migration: %v", err)
	}
	return db
}

func cleanupKey(t *testing.T, db *sql.DB, key string) {
	t.Helper()
	db.Exec("DELETE FROM idempotency_keys WHERE idempotency_key = $1", key)
}

func cleanupMerchant(t *testing.T, db *sql.DB, merchantID string) {
	t.Helper()
	db.Exec("DELETE FROM merchant_policies WHERE merchant_id = $1", merchantID)
}

func TestIntegration_InsertOrGet_NewKey(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	key := "inttest_new_" + time.Now().Format("20060102150405.000")
	defer cleanupKey(t, db, key)

	req := domain.PaymentRequest{
		IdempotencyKey: key,
		MerchantID:     "test-merchant",
		CustomerID:     "test-customer",
		Amount:         5000,
		Currency:       "BRL",
	}

	rec, isNew, err := repo.InsertOrGet(context.Background(), req, "pay_test_1", time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("InsertOrGet: %v", err)
	}
	if !isNew {
		t.Error("expected new record")
	}
	if rec.Status != domain.StatusProcessing {
		t.Errorf("expected processing, got %s", rec.Status)
	}
	if rec.AttemptCount != 1 {
		t.Errorf("expected attempt 1, got %d", rec.AttemptCount)
	}
}

func TestIntegration_InsertOrGet_Duplicate(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	key := "inttest_dup_" + time.Now().Format("20060102150405.000")
	defer cleanupKey(t, db, key)

	req := domain.PaymentRequest{
		IdempotencyKey: key,
		MerchantID:     "test-merchant",
		CustomerID:     "test-customer",
		Amount:         5000,
		Currency:       "BRL",
	}

	// First insert
	_, isNew1, _ := repo.InsertOrGet(context.Background(), req, "pay_1", time.Now().Add(24*time.Hour))
	if !isNew1 {
		t.Fatal("first should be new")
	}

	// Duplicate
	rec, isNew2, _ := repo.InsertOrGet(context.Background(), req, "pay_2", time.Now().Add(24*time.Hour))
	if isNew2 {
		t.Error("second should not be new")
	}
	if rec.AttemptCount != 2 {
		t.Errorf("expected attempt 2, got %d", rec.AttemptCount)
	}
}

func TestIntegration_MarkComplete(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	key := "inttest_complete_" + time.Now().Format("20060102150405.000")
	defer cleanupKey(t, db, key)

	req := domain.PaymentRequest{
		IdempotencyKey: key,
		MerchantID:     "test-merchant",
		CustomerID:     "test-customer",
		Amount:         5000,
		Currency:       "BRL",
	}
	repo.InsertOrGet(context.Background(), req, "pay_mc", time.Now().Add(24*time.Hour))

	body := json.RawMessage(`{"tx":"abc"}`)
	err := repo.MarkComplete(context.Background(), key, domain.StatusSucceeded, &body)
	if err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}

	rec, _ := repo.GetByKey(context.Background(), key)
	if rec.Status != domain.StatusSucceeded {
		t.Errorf("expected succeeded, got %s", rec.Status)
	}
}

func TestIntegration_MarkComplete_NotFound(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	err := repo.MarkComplete(context.Background(), "nonexistent_key_xyz", domain.StatusSucceeded, nil)
	if err != domain.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestIntegration_MarkComplete_AlreadyCompleted(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	key := "inttest_already_" + time.Now().Format("20060102150405.000")
	defer cleanupKey(t, db, key)

	req := domain.PaymentRequest{
		IdempotencyKey: key,
		MerchantID:     "test-merchant",
		CustomerID:     "test-customer",
		Amount:         5000,
		Currency:       "BRL",
	}
	repo.InsertOrGet(context.Background(), req, "pay_ac", time.Now().Add(24*time.Hour))
	repo.MarkComplete(context.Background(), key, domain.StatusSucceeded, nil)

	err := repo.MarkComplete(context.Background(), key, domain.StatusSucceeded, nil)
	if err != domain.ErrAlreadyCompleted {
		t.Errorf("expected ErrAlreadyCompleted, got %v", err)
	}
}

func TestIntegration_GetByKey_NotFound(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	_, err := repo.GetByKey(context.Background(), "absolutely_nonexistent")
	if err != domain.ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestIntegration_ResetToProcessing(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	key := "inttest_reset_" + time.Now().Format("20060102150405.000")
	defer cleanupKey(t, db, key)

	req := domain.PaymentRequest{
		IdempotencyKey: key,
		MerchantID:     "test-merchant",
		CustomerID:     "test-customer",
		Amount:         5000,
		Currency:       "BRL",
	}
	repo.InsertOrGet(context.Background(), req, "pay_r1", time.Now().Add(24*time.Hour))
	repo.MarkComplete(context.Background(), key, domain.StatusFailed, nil)

	err := repo.ResetToProcessing(context.Background(), key, "pay_r2", time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("ResetToProcessing: %v", err)
	}

	rec, _ := repo.GetByKey(context.Background(), key)
	if rec.Status != domain.StatusProcessing {
		t.Errorf("expected processing after reset, got %s", rec.Status)
	}
}

func TestIntegration_DeleteExpired(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	key := "inttest_expired_" + time.Now().Format("20060102150405.000")
	// Insert with past expiry
	req := domain.PaymentRequest{
		IdempotencyKey: key,
		MerchantID:     "test-merchant",
		CustomerID:     "test-customer",
		Amount:         1000,
		Currency:       "BRL",
	}
	repo.InsertOrGet(context.Background(), req, "pay_exp", time.Now().Add(-1*time.Hour))

	deleted, err := repo.DeleteExpired(context.Background())
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted < 1 {
		t.Error("expected at least 1 deleted")
	}
}

func TestIntegration_GetDuplicates(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	key := "inttest_getdup_" + time.Now().Format("20060102150405.000")
	defer cleanupKey(t, db, key)

	req := domain.PaymentRequest{
		IdempotencyKey: key,
		MerchantID:     "inttest-merchant-dup",
		CustomerID:     "test-customer",
		Amount:         5000,
		Currency:       "BRL",
	}

	// Insert twice to create a duplicate
	repo.InsertOrGet(context.Background(), req, "pay_d1", time.Now().Add(24*time.Hour))
	repo.InsertOrGet(context.Background(), req, "pay_d2", time.Now().Add(24*time.Hour))

	dups, err := repo.GetDuplicates(context.Background(), "inttest-merchant-dup", time.Now().Add(-1*time.Hour), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("GetDuplicates: %v", err)
	}
	if len(dups) < 1 {
		t.Error("expected at least 1 duplicate")
	}
}

func TestIntegration_GetMerchantStats(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	key := "inttest_stats_" + time.Now().Format("20060102150405.000")
	defer cleanupKey(t, db, key)

	req := domain.PaymentRequest{
		IdempotencyKey: key,
		MerchantID:     "inttest-merchant-stats",
		CustomerID:     "test-customer",
		Amount:         3000,
		Currency:       "MXN",
	}
	repo.InsertOrGet(context.Background(), req, "pay_s1", time.Now().Add(24*time.Hour))

	total, unique, err := repo.GetMerchantStats(context.Background(), "inttest-merchant-stats", time.Now().Add(-1*time.Hour), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("GetMerchantStats: %v", err)
	}
	if total < 1 || unique < 1 {
		t.Errorf("expected at least 1 total and 1 unique, got total=%d unique=%d", total, unique)
	}
}

func TestIntegration_Policy_UpsertAndGet(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	mid := "inttest-policy-" + time.Now().Format("20060102150405.000")
	defer cleanupMerchant(t, db, mid)

	err := repo.UpsertPolicy(context.Background(), domain.MerchantPolicy{
		MerchantID:  mid,
		RetryPolicy: "standard",
		ExpiryHours: 24,
	})
	if err != nil {
		t.Fatalf("UpsertPolicy: %v", err)
	}

	p, err := repo.GetPolicy(context.Background(), mid)
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if p.RetryPolicy != "standard" {
		t.Errorf("expected standard, got %s", p.RetryPolicy)
	}
}

func TestIntegration_GetPolicy_NotFound(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	_, err := repo.GetPolicy(context.Background(), "nonexistent_merchant_xyz")
	if err != domain.ErrMerchantNotFound {
		t.Errorf("expected ErrMerchantNotFound, got %v", err)
	}
}

func TestIntegration_GetAllMerchantStats(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	stats, err := repo.GetAllMerchantStats(context.Background(), time.Now().Add(-48*time.Hour), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("GetAllMerchantStats: %v", err)
	}
	// Just verify it returns without error
	_ = stats
}

func TestIntegration_ConcurrentInserts(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	repo := NewPostgresRepository(db)

	key := "inttest_conc_" + time.Now().Format("20060102150405.000")
	defer cleanupKey(t, db, key)

	req := domain.PaymentRequest{
		IdempotencyKey: key,
		MerchantID:     "test-merchant",
		CustomerID:     "test-customer",
		Amount:         5000,
		Currency:       "BRL",
	}

	const n = 10
	results := make([]bool, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			_, isNew, err := repo.InsertOrGet(context.Background(), req, "pay_conc", time.Now().Add(24*time.Hour))
			if err != nil {
				t.Errorf("goroutine %d: %v", idx, err)
				return
			}
			results[idx] = isNew
		}(i)
	}
	wg.Wait()

	newCount := 0
	for _, isNew := range results {
		if isNew {
			newCount++
		}
	}
	if newCount != 1 {
		t.Errorf("expected exactly 1 new insert, got %d", newCount)
	}
}

func TestIntegration_NewPostgresDB(t *testing.T) {
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		dsn = "postgres://postgres@localhost:5432/idempotency?sslmode=disable"
	}
	db, err := NewPostgresDB(dsn)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Errorf("expected successful ping, got %v", err)
	}
}
