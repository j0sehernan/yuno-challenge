package monitor

// AnomalyDetector checks if duplicate rates exceed thresholds.
type AnomalyDetector struct {
	metrics   *Metrics
	threshold float64 // percentage
}

// NewAnomalyDetector creates a detector with the given threshold.
func NewAnomalyDetector(metrics *Metrics, threshold float64) *AnomalyDetector {
	return &AnomalyDetector{metrics: metrics, threshold: threshold}
}

// IsAnomalous returns true if the current sliding-window duplicate rate exceeds the threshold.
func (d *AnomalyDetector) IsAnomalous() bool {
	snap := d.metrics.Snapshot()
	return snap.WindowDupRate > d.threshold
}

// Report returns the current anomaly state.
func (d *AnomalyDetector) Report() map[string]interface{} {
	snap := d.metrics.Snapshot()
	return map[string]interface{}{
		"anomaly_detected":    snap.WindowDupRate > d.threshold,
		"current_rate":        snap.WindowDupRate,
		"threshold":           d.threshold,
		"window_requests":     snap.WindowRequests,
		"window_duplicates":   snap.WindowDuplicates,
	}
}
