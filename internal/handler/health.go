package handler

import (
	"net/http"

	"github.com/kubo-market/idempotency-shield/internal/monitor"
)

// Pinger checks database connectivity.
type Pinger interface {
	Ping() error
}

// HealthHandler handles health check and metrics endpoints.
type HealthHandler struct {
	db      Pinger
	metrics *monitor.Metrics
}

// NewHealthHandler creates a new HealthHandler.
func NewHealthHandler(db Pinger, metrics *monitor.Metrics) *HealthHandler {
	return &HealthHandler{db: db, metrics: metrics}
}

// Health handles GET /health
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	if err := h.db.Ping(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":   "unhealthy",
			"database": "disconnected",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "healthy",
		"database": "connected",
	})
}

// Metrics handles GET /v1/metrics
func (h *HealthHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.metrics.Snapshot())
}
