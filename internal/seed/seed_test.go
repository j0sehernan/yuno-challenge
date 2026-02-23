package seed

import (
	"strings"
	"testing"
)

func TestGenerateSQL_ProducesValidSQL(t *testing.T) {
	sql := GenerateSQL()

	if !strings.HasPrefix(sql, "BEGIN;") {
		t.Error("expected SQL to start with BEGIN")
	}
	if !strings.HasSuffix(strings.TrimSpace(sql), "COMMIT;") {
		t.Error("expected SQL to end with COMMIT")
	}
}

func TestGenerateSQL_ContainsMerchantPolicies(t *testing.T) {
	sql := GenerateSQL()

	merchants := []string{"kubo-brazil", "cloudstore-mx", "techhub-co"}
	for _, m := range merchants {
		if !strings.Contains(sql, m) {
			t.Errorf("expected SQL to contain merchant %s", m)
		}
	}
}

func TestGenerateSQL_ContainsExpectedRecordTypes(t *testing.T) {
	sql := GenerateSQL()

	patterns := []string{
		"normal_0",        // normal payments
		"doubleclick_0",   // double-click scenarios
		"buggy_app_0",     // buggy app scenarios
		"fail_retry_0",    // failed-then-retry
		"processing_0",    // still processing
		"failed_0",        // failed payments
	}
	for _, p := range patterns {
		if !strings.Contains(sql, p) {
			t.Errorf("expected SQL to contain %s", p)
		}
	}
}

func TestGenerateSQL_Has130PlusInserts(t *testing.T) {
	sql := GenerateSQL()

	// Count INSERT statements (excluding the merchant_policies one)
	count := strings.Count(sql, "INSERT INTO idempotency_keys")
	// 85 normal + 7 double-click + 3 buggy + 3 fail-retry + 5 processing + 5 failed = 108
	if count < 100 {
		t.Errorf("expected at least 100 idempotency_keys inserts, got %d", count)
	}
}

func TestGenerateSQL_ContainsAllStatuses(t *testing.T) {
	sql := GenerateSQL()

	statuses := []string{"'succeeded'", "'processing'", "'failed'"}
	for _, s := range statuses {
		if !strings.Contains(sql, s) {
			t.Errorf("expected SQL to contain status %s", s)
		}
	}
}

func TestGenerateSQL_ContainsAllCurrencies(t *testing.T) {
	sql := GenerateSQL()

	currencies := []string{"BRL", "MXN", "COP", "USD"}
	for _, c := range currencies {
		if !strings.Contains(sql, c) {
			t.Errorf("expected SQL to contain currency %s", c)
		}
	}
}

func TestGenerateSQL_Deterministic(t *testing.T) {
	sql1 := GenerateSQL()
	sql2 := GenerateSQL()
	if sql1 != sql2 {
		t.Error("GenerateSQL should produce deterministic output")
	}
}
