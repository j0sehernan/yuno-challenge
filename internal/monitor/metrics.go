package monitor

import (
	"sync"
	"time"
)

// Metrics tracks in-memory counters for the idempotency service.
type Metrics struct {
	mu sync.RWMutex

	TotalRequests    int64 `json:"total_requests"`
	NewPayments      int64 `json:"new_payments"`
	DuplicateBlocked int64 `json:"duplicate_blocked"`
	RetryAllowed     int64 `json:"retry_allowed"`
	CachedResponses  int64 `json:"cached_responses"`
	ParamMismatches  int64 `json:"param_mismatches"`

	// Sliding window for duplicate rate
	window []windowEntry
}

type windowEntry struct {
	ts          time.Time
	isDuplicate bool
}

const windowDuration = 5 * time.Minute

// MetricsSnapshot is a point-in-time view of metrics.
type MetricsSnapshot struct {
	TotalRequests     int64   `json:"total_requests"`
	NewPayments       int64   `json:"new_payments"`
	DuplicateBlocked  int64   `json:"duplicate_blocked"`
	RetryAllowed      int64   `json:"retry_allowed"`
	CachedResponses   int64   `json:"cached_responses"`
	ParamMismatches   int64   `json:"param_mismatches"`
	WindowRequests    int     `json:"window_requests_5m"`
	WindowDuplicates  int     `json:"window_duplicates_5m"`
	WindowDupRate     float64 `json:"window_duplicate_rate_5m"`
	AnomalyDetected   bool    `json:"anomaly_detected"`
	AnomalyThreshold  float64 `json:"anomaly_threshold"`
}

// NewMetrics creates a new Metrics instance.
func NewMetrics() *Metrics {
	return &Metrics{}
}

// RecordNew records a new payment request.
func (m *Metrics) RecordNew() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalRequests++
	m.NewPayments++
	m.addWindow(false)
}

// RecordDuplicate records a blocked duplicate.
func (m *Metrics) RecordDuplicate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalRequests++
	m.DuplicateBlocked++
	m.addWindow(true)
}

// RecordRetry records a retry after failure.
func (m *Metrics) RecordRetry() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalRequests++
	m.RetryAllowed++
	m.addWindow(false)
}

// RecordCached records a cached response return.
func (m *Metrics) RecordCached() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalRequests++
	m.CachedResponses++
	m.addWindow(true)
}

// RecordMismatch records a parameter mismatch.
func (m *Metrics) RecordMismatch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalRequests++
	m.ParamMismatches++
	m.addWindow(true)
}

func (m *Metrics) addWindow(isDuplicate bool) {
	now := time.Now()
	m.window = append(m.window, windowEntry{ts: now, isDuplicate: isDuplicate})
	m.pruneWindow(now)
}

func (m *Metrics) pruneWindow(now time.Time) {
	cutoff := now.Add(-windowDuration)
	i := 0
	for i < len(m.window) && m.window[i].ts.Before(cutoff) {
		i++
	}
	m.window = m.window[i:]
}

// Snapshot returns a point-in-time copy of all metrics.
func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	cutoff := now.Add(-windowDuration)
	var windowReqs, windowDups int
	for _, e := range m.window {
		if e.ts.After(cutoff) {
			windowReqs++
			if e.isDuplicate {
				windowDups++
			}
		}
	}

	var dupRate float64
	if windowReqs > 0 {
		dupRate = float64(windowDups) / float64(windowReqs) * 100
	}

	return MetricsSnapshot{
		TotalRequests:    m.TotalRequests,
		NewPayments:      m.NewPayments,
		DuplicateBlocked: m.DuplicateBlocked,
		RetryAllowed:     m.RetryAllowed,
		CachedResponses:  m.CachedResponses,
		ParamMismatches:  m.ParamMismatches,
		WindowRequests:   windowReqs,
		WindowDuplicates: windowDups,
		WindowDupRate:    dupRate,
		AnomalyDetected:  dupRate > 20.0,
		AnomalyThreshold: 20.0,
	}
}
