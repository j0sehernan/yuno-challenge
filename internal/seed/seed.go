package seed

import (
	"strconv"
	"strings"
)

// GenerateSQL builds INSERT statements for 130+ realistic payment events.
func GenerateSQL() string {
	var b strings.Builder
	b.WriteString("BEGIN;\n")

	b.WriteString(`
INSERT INTO merchant_policies (merchant_id, retry_policy, expiry_hours) VALUES
('kubo-brazil', 'standard', 24),
('cloudstore-mx', 'standard', 24),
('techhub-co', 'lenient', 48)
ON CONFLICT (merchant_id) DO NOTHING;
`)

	baseTime := "NOW() - INTERVAL '36 hours'"

	writePayment := func(key, merchant, customer string, amount int, currency, status string, attempts int, hoursAgo int, completed bool) {
		ts := baseTime
		if hoursAgo > 0 {
			ts = "NOW() - INTERVAL '" + strconv.Itoa(hoursAgo) + " hours'"
		}
		completedAt := "NULL"
		if completed {
			completedAt = ts + " + INTERVAL '2 seconds'"
		}
		responseBody := "NULL"
		if status == "succeeded" && completed {
			responseBody = "'{\"transaction_id\":\"tx_" + key + "\",\"provider\":\"mock\"}'"
		}

		b.WriteString("INSERT INTO idempotency_keys (idempotency_key, merchant_id, customer_id, amount, currency, status, request_hash, payment_id, attempt_count, first_seen_at, last_seen_at, completed_at, expires_at, response_body) VALUES (")
		b.WriteString("'" + key + "', ")
		b.WriteString("'" + merchant + "', ")
		b.WriteString("'" + customer + "', ")
		b.WriteString(strconv.Itoa(amount) + ", ")
		b.WriteString("'" + currency + "', ")
		b.WriteString("'" + status + "', ")
		b.WriteString("'hash_" + key + "', ")
		b.WriteString("'pay_" + key + "', ")
		b.WriteString(strconv.Itoa(attempts) + ", ")
		b.WriteString(ts + ", ")
		b.WriteString(ts + " + INTERVAL '" + strconv.Itoa(attempts-1) + " seconds', ")
		b.WriteString(completedAt + ", ")
		b.WriteString(ts + " + INTERVAL '24 hours', ")
		b.WriteString(responseBody)
		b.WriteString(") ON CONFLICT (idempotency_key) DO NOTHING;\n")
	}

	currencies := []struct{ merchant, currency string }{
		{"kubo-brazil", "BRL"},
		{"cloudstore-mx", "MXN"},
		{"techhub-co", "COP"},
	}

	// ~85 normal successful payments
	for i := 0; i < 85; i++ {
		c := currencies[i%3]
		amount := 500 + (i*137)%49500
		hoursAgo := 1 + (i % 36)
		customer := "cust_" + strconv.Itoa(i%20+1)
		key := "normal_" + strconv.Itoa(i)
		writePayment(key, c.merchant, customer, amount, c.currency, "succeeded", 1, hoursAgo, true)
	}

	// 7 double-click scenarios (2-3 retries)
	doubleClicks := []struct {
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
	for i, dc := range doubleClicks {
		key := "doubleclick_" + strconv.Itoa(i)
		writePayment(key, dc.merchant, "cust_dblclk_"+strconv.Itoa(i), dc.amount, dc.currency, "succeeded", dc.attempts, 5+i, true)
	}

	// 3 buggy app scenarios (8-12 retries)
	buggyApps := []struct {
		merchant, currency string
		amount, attempts   int
	}{
		{"kubo-brazil", "BRL", 45000, 12},
		{"cloudstore-mx", "MXN", 28700, 10},
		{"techhub-co", "COP", 15000, 8},
	}
	for i, ba := range buggyApps {
		key := "buggy_app_" + strconv.Itoa(i)
		writePayment(key, ba.merchant, "cust_buggy_"+strconv.Itoa(i), ba.amount, ba.currency, "succeeded", ba.attempts, 2+i, true)
	}

	// 3 failed-then-retry cases
	failRetries := []struct {
		merchant, currency string
		amount             int
	}{
		{"kubo-brazil", "BRL", 5000},
		{"cloudstore-mx", "MXN", 3200},
		{"techhub-co", "COP", 8000},
	}
	for i, fr := range failRetries {
		key := "fail_retry_" + strconv.Itoa(i)
		writePayment(key, fr.merchant, "cust_retry_"+strconv.Itoa(i), fr.amount, fr.currency, "succeeded", 2, 10+i, true)
	}

	// 5 processing
	for i := 0; i < 5; i++ {
		c := currencies[i%3]
		key := "processing_" + strconv.Itoa(i)
		writePayment(key, c.merchant, "cust_proc_"+strconv.Itoa(i), 7500+i*1000, c.currency, "processing", 1, 0, false)
	}

	// 5 failed
	for i := 0; i < 5; i++ {
		c := currencies[i%3]
		key := "failed_" + strconv.Itoa(i)
		writePayment(key, c.merchant, "cust_fail_"+strconv.Itoa(i), 2000+i*500, c.currency, "failed", 1, 3+i, true)
	}

	b.WriteString("COMMIT;\n")
	return b.String()
}
