package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kubo-market/idempotency-shield/internal/domain"
	"github.com/kubo-market/idempotency-shield/internal/service"
)

// PaymentHandler handles payment idempotency validation endpoints.
type PaymentHandler struct {
	svc *service.IdempotencyService
}

// NewPaymentHandler creates a new PaymentHandler.
func NewPaymentHandler(svc *service.IdempotencyService) *PaymentHandler {
	return &PaymentHandler{svc: svc}
}

// ProcessPayment handles POST /v1/payments
func (h *PaymentHandler) ProcessPayment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req domain.PaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	resp, code, err := h.svc.ProcessPayment(r.Context(), req)
	if err != nil {
		if errors.Is(err, domain.ErrParamsMismatch) {
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, code, resp)
}

// CompletePayment handles PATCH /v1/payments/{key}/complete
func (h *PaymentHandler) CompletePayment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Extract key from path: /v1/payments/{key}/complete
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing idempotency key"})
		return
	}
	key := parts[2]

	var req domain.CompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	if err := h.svc.MarkComplete(r.Context(), key, req); err != nil {
		if errors.Is(err, domain.ErrInvalidStatus) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, domain.ErrKeyNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, domain.ErrAlreadyCompleted) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "completed", "idempotency_key": key})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
