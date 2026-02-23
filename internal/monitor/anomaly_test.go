package monitor

import (
	"testing"
)

func TestAnomalyDetector_NotAnomalous(t *testing.T) {
	m := NewMetrics()
	for i := 0; i < 10; i++ {
		m.RecordNew()
	}
	m.RecordDuplicate() // 1/11 ≈ 9%

	d := NewAnomalyDetector(m, 20.0)
	if d.IsAnomalous() {
		t.Error("9% rate should not be anomalous at 20% threshold")
	}
}

func TestAnomalyDetector_IsAnomalous(t *testing.T) {
	m := NewMetrics()
	for i := 0; i < 3; i++ {
		m.RecordNew()
	}
	for i := 0; i < 7; i++ {
		m.RecordDuplicate()
	}

	d := NewAnomalyDetector(m, 20.0)
	if !d.IsAnomalous() {
		t.Error("70% rate should be anomalous at 20% threshold")
	}
}

func TestAnomalyDetector_Report(t *testing.T) {
	m := NewMetrics()
	m.RecordNew()
	m.RecordDuplicate()

	d := NewAnomalyDetector(m, 20.0)
	report := d.Report()

	if report["threshold"] != 20.0 {
		t.Errorf("expected threshold 20, got %v", report["threshold"])
	}
	if _, ok := report["anomaly_detected"]; !ok {
		t.Error("report missing anomaly_detected field")
	}
	if _, ok := report["current_rate"]; !ok {
		t.Error("report missing current_rate field")
	}
	if report["window_requests"] != 2 {
		t.Errorf("expected 2 window requests, got %v", report["window_requests"])
	}
}

func TestAnomalyDetector_EmptyMetrics(t *testing.T) {
	m := NewMetrics()
	d := NewAnomalyDetector(m, 20.0)

	if d.IsAnomalous() {
		t.Error("empty metrics should not be anomalous")
	}

	report := d.Report()
	if report["anomaly_detected"] != false {
		t.Error("empty report should not detect anomaly")
	}
}
