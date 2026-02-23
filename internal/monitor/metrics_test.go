package monitor

import (
	"sync"
	"testing"
)

func TestMetrics_RecordNew(t *testing.T) {
	m := NewMetrics()
	m.RecordNew()
	m.RecordNew()
	m.RecordNew()

	snap := m.Snapshot()
	if snap.TotalRequests != 3 {
		t.Errorf("expected 3 total, got %d", snap.TotalRequests)
	}
	if snap.NewPayments != 3 {
		t.Errorf("expected 3 new, got %d", snap.NewPayments)
	}
}

func TestMetrics_RecordDuplicate(t *testing.T) {
	m := NewMetrics()
	m.RecordDuplicate()

	snap := m.Snapshot()
	if snap.DuplicateBlocked != 1 {
		t.Errorf("expected 1 duplicate, got %d", snap.DuplicateBlocked)
	}
	if snap.TotalRequests != 1 {
		t.Errorf("expected 1 total, got %d", snap.TotalRequests)
	}
}

func TestMetrics_RecordRetry(t *testing.T) {
	m := NewMetrics()
	m.RecordRetry()

	snap := m.Snapshot()
	if snap.RetryAllowed != 1 {
		t.Errorf("expected 1 retry, got %d", snap.RetryAllowed)
	}
}

func TestMetrics_RecordCached(t *testing.T) {
	m := NewMetrics()
	m.RecordCached()

	snap := m.Snapshot()
	if snap.CachedResponses != 1 {
		t.Errorf("expected 1 cached, got %d", snap.CachedResponses)
	}
}

func TestMetrics_RecordMismatch(t *testing.T) {
	m := NewMetrics()
	m.RecordMismatch()

	snap := m.Snapshot()
	if snap.ParamMismatches != 1 {
		t.Errorf("expected 1 mismatch, got %d", snap.ParamMismatches)
	}
}

func TestMetrics_SlidingWindowDuplicateRate(t *testing.T) {
	m := NewMetrics()

	// 8 new + 2 duplicates = 20% rate
	for i := 0; i < 8; i++ {
		m.RecordNew()
	}
	m.RecordDuplicate()
	m.RecordDuplicate()

	snap := m.Snapshot()
	if snap.WindowRequests != 10 {
		t.Errorf("expected 10 window requests, got %d", snap.WindowRequests)
	}
	if snap.WindowDuplicates != 2 {
		t.Errorf("expected 2 window duplicates, got %d", snap.WindowDuplicates)
	}
	// 2/10 = 20%
	if snap.WindowDupRate < 19.9 || snap.WindowDupRate > 20.1 {
		t.Errorf("expected ~20%% rate, got %.2f%%", snap.WindowDupRate)
	}
	if snap.AnomalyDetected {
		t.Error("20% should not trigger anomaly (threshold is >20%)")
	}
}

func TestMetrics_AnomalyDetection(t *testing.T) {
	m := NewMetrics()

	// 5 new + 5 duplicates = 50% rate → anomaly
	for i := 0; i < 5; i++ {
		m.RecordNew()
	}
	for i := 0; i < 5; i++ {
		m.RecordDuplicate()
	}

	snap := m.Snapshot()
	if !snap.AnomalyDetected {
		t.Error("50% rate should trigger anomaly")
	}
	if snap.AnomalyThreshold != 20.0 {
		t.Errorf("expected threshold 20, got %.1f", snap.AnomalyThreshold)
	}
}

func TestMetrics_SnapshotEmpty(t *testing.T) {
	m := NewMetrics()
	snap := m.Snapshot()

	if snap.TotalRequests != 0 {
		t.Errorf("expected 0, got %d", snap.TotalRequests)
	}
	if snap.WindowDupRate != 0 {
		t.Errorf("expected 0 rate, got %.2f", snap.WindowDupRate)
	}
	if snap.AnomalyDetected {
		t.Error("empty metrics should not trigger anomaly")
	}
}

func TestMetrics_ConcurrentAccess(t *testing.T) {
	m := NewMetrics()
	var wg sync.WaitGroup
	wg.Add(50)

	for i := 0; i < 25; i++ {
		go func() {
			defer wg.Done()
			m.RecordNew()
		}()
		go func() {
			defer wg.Done()
			m.RecordDuplicate()
		}()
	}
	wg.Wait()

	snap := m.Snapshot()
	if snap.TotalRequests != 50 {
		t.Errorf("expected 50 total, got %d", snap.TotalRequests)
	}
	if snap.NewPayments != 25 {
		t.Errorf("expected 25 new, got %d", snap.NewPayments)
	}
	if snap.DuplicateBlocked != 25 {
		t.Errorf("expected 25 duplicate, got %d", snap.DuplicateBlocked)
	}
}
