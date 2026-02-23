// +build ignore

// seed_data.go generates and inserts 130+ realistic idempotency events.
// Run with: go run scripts/seed_data.go
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

func main() {
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		dsn = "postgres://postgres@localhost:5432/idempotency?sslmode=disable"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("ping: %v", err)
	}

	log.Println("Connected, seeding data...")

	// Clean existing data
	db.Exec("TRUNCATE idempotency_keys, merchant_policies CASCADE")

	// Insert merchant policies
	db.Exec(`INSERT INTO merchant_policies (merchant_id, retry_policy, expiry_hours) VALUES
		('kubo-brazil', 'standard', 24),
		('cloudstore-mx', 'standard', 24),
		('techhub-co', 'lenient', 48)`)

	type payment struct {
		key, merchant, customer, currency, status string
		amount, attempts, hoursAgo                int
		completed                                 bool
	}

	var payments []payment

	// ~85 normal successful payments
	merchants := []struct{ id, currency string }{
		{"kubo-brazil", "BRL"},
		{"cloudstore-mx", "MXN"},
		{"techhub-co", "COP"},
	}

	for i := 0; i < 85; i++ {
		m := merchants[i%3]
		payments = append(payments, payment{
			key:      fmt.Sprintf("normal_%03d", i),
			merchant: m.id,
			customer: fmt.Sprintf("cust_%02d", i%20+1),
			amount:   500 + (i*137)%49500,
			currency: m.currency,
			status:   "succeeded",
			attempts: 1,
			hoursAgo: 1 + (i % 36),
			completed: true,
		})
	}

	// 7 double-click scenarios
	dblClicks := []struct {
		merchant, currency string
		amount, attempts   int
	}{
		{"kubo-brazil", "BRL", 15000, 2},
		{"kubo-brazil", "BRL", 8500, 3},
		{"cloudstore-mx", "MXN", 12000, 2},
		{"cloudstore-mx", "MXN", 3500, 2},
		{"techhub-co", "COP", 25000, 3},
		{"techhub-co", "COP", 7500, 2},
		{"kubo-brazil", "USD", 9900, 2},
	}
	for i, d := range dblClicks {
		payments = append(payments, payment{
			key:       fmt.Sprintf("doubleclick_%d", i),
			merchant:  d.merchant,
			customer:  fmt.Sprintf("cust_dblclk_%d", i),
			amount:    d.amount,
			currency:  d.currency,
			status:    "succeeded",
			attempts:  d.attempts,
			hoursAgo:  5 + i,
			completed: true,
		})
	}

	// 3 buggy app scenarios
	buggy := []struct {
		merchant, currency string
		amount, attempts   int
	}{
		{"kubo-brazil", "BRL", 45000, 12},
		{"cloudstore-mx", "MXN", 28700, 10},
		{"techhub-co", "COP", 15000, 8},
	}
	for i, b := range buggy {
		payments = append(payments, payment{
			key:       fmt.Sprintf("buggy_app_%d", i),
			merchant:  b.merchant,
			customer:  fmt.Sprintf("cust_buggy_%d", i),
			amount:    b.amount,
			currency:  b.currency,
			status:    "succeeded",
			attempts:  b.attempts,
			hoursAgo:  2 + i,
			completed: true,
		})
	}

	// 3 failed-then-retry cases
	retries := []struct {
		merchant, currency string
		amount             int
	}{
		{"kubo-brazil", "BRL", 5000},
		{"cloudstore-mx", "MXN", 3200},
		{"techhub-co", "COP", 8000},
	}
	for i, r := range retries {
		payments = append(payments, payment{
			key:       fmt.Sprintf("fail_retry_%d", i),
			merchant:  r.merchant,
			customer:  fmt.Sprintf("cust_retry_%d", i),
			amount:    r.amount,
			currency:  r.currency,
			status:    "succeeded",
			attempts:  2,
			hoursAgo:  10 + i,
			completed: true,
		})
	}

	// 5 still processing
	for i := 0; i < 5; i++ {
		m := merchants[i%3]
		payments = append(payments, payment{
			key:      fmt.Sprintf("processing_%d", i),
			merchant: m.id,
			customer: fmt.Sprintf("cust_proc_%d", i),
			amount:   7500 + i*1000,
			currency: m.currency,
			status:   "processing",
			attempts: 1,
			hoursAgo: 0,
		})
	}

	// 5 failed
	for i := 0; i < 5; i++ {
		m := merchants[i%3]
		payments = append(payments, payment{
			key:       fmt.Sprintf("failed_%d", i),
			merchant:  m.id,
			customer:  fmt.Sprintf("cust_fail_%d", i),
			amount:    2000 + i*500,
			currency:  m.currency,
			status:    "failed",
			attempts:  1,
			hoursAgo:  3 + i,
			completed: true,
		})
	}

	inserted := 0
	for _, p := range payments {
		interval := fmt.Sprintf("%d hours", p.hoursAgo)
		completedAt := "NULL"
		responseBody := sql.NullString{}

		if p.completed {
			completedAt = fmt.Sprintf("NOW() - INTERVAL '%s' + INTERVAL '2 seconds'", interval)
		}
		if p.status == "succeeded" && p.completed {
			responseBody = sql.NullString{
				String: fmt.Sprintf(`{"transaction_id":"tx_%s","provider":"mock"}`, p.key),
				Valid:  true,
			}
		}

		var err error
		if p.completed {
			query := fmt.Sprintf(`INSERT INTO idempotency_keys
				(idempotency_key, merchant_id, customer_id, amount, currency, status, request_hash, payment_id, attempt_count, first_seen_at, last_seen_at, completed_at, expires_at, response_body)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9,
					NOW() - INTERVAL '%s',
					NOW() - INTERVAL '%s' + INTERVAL '%d seconds',
					%s,
					NOW() - INTERVAL '%s' + INTERVAL '24 hours',
					$10)
				ON CONFLICT DO NOTHING`,
				interval, interval, p.attempts-1, completedAt, interval)
			_, err = db.Exec(query,
				p.key, p.merchant, p.customer, p.amount, p.currency, p.status,
				"hash_"+p.key, "pay_"+p.key, p.attempts,
				responseBody)
		} else {
			query := fmt.Sprintf(`INSERT INTO idempotency_keys
				(idempotency_key, merchant_id, customer_id, amount, currency, status, request_hash, payment_id, attempt_count, first_seen_at, last_seen_at, expires_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9,
					NOW() - INTERVAL '%s',
					NOW() - INTERVAL '%s',
					NOW() + INTERVAL '24 hours')
				ON CONFLICT DO NOTHING`, interval, interval)
			_, err = db.Exec(query,
				p.key, p.merchant, p.customer, p.amount, p.currency, p.status,
				"hash_"+p.key, "pay_"+p.key, p.attempts)
		}
		if err != nil {
			log.Printf("insert %s: %v", p.key, err)
		} else {
			inserted++
		}
	}

	log.Printf("Inserted %d/%d records", inserted, len(payments))
}
