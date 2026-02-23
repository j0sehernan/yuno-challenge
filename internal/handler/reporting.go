package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/kubo-market/idempotency-shield/internal/service"
)

// ReportingHandler handles duplicate detection report endpoints.
type ReportingHandler struct {
	svc *service.ReportingService
}

// NewReportingHandler creates a new ReportingHandler.
func NewReportingHandler(svc *service.ReportingService) *ReportingHandler {
	return &ReportingHandler{svc: svc}
}

// GetDuplicates handles GET /v1/merchants/{id}/duplicates
func (h *ReportingHandler) GetDuplicates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Extract merchant ID from path: /v1/merchants/{id}/duplicates
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing merchant_id"})
		return
	}
	merchantID := parts[2]

	// Parse time range from query params, default to last 24h
	now := time.Now()
	from := now.Add(-24 * time.Hour)
	to := now

	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}

	report, err := h.svc.GetDuplicateReport(r.Context(), merchantID, from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, report)
}
