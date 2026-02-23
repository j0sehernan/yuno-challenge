package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kubo-market/idempotency-shield/internal/domain"
	"github.com/kubo-market/idempotency-shield/internal/storage"
)

// PolicyHandler handles merchant policy endpoints.
type PolicyHandler struct {
	repo storage.Repository
}

// NewPolicyHandler creates a new PolicyHandler.
func NewPolicyHandler(repo storage.Repository) *PolicyHandler {
	return &PolicyHandler{repo: repo}
}

// UpdatePolicy handles PUT /v1/merchants/{id}/policy
func (h *PolicyHandler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Extract merchant ID from path: /v1/merchants/{id}/policy
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing merchant_id"})
		return
	}
	merchantID := parts[2]

	if r.Method == http.MethodGet {
		policy, err := h.repo.GetPolicy(r.Context(), merchantID)
		if err != nil {
			if errors.Is(err, domain.ErrMerchantNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "merchant policy not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, policy)
		return
	}

	var policy domain.MerchantPolicy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	policy.MerchantID = merchantID

	// Validate
	validPolicies := map[string]bool{"strict_no_retry": true, "standard": true, "lenient": true}
	if !validPolicies[policy.RetryPolicy] {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "retry_policy must be strict_no_retry, standard, or lenient"})
		return
	}
	validHours := map[int]bool{24: true, 48: true, 72: true}
	if !validHours[policy.ExpiryHours] {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "expiry_hours must be 24, 48, or 72"})
		return
	}

	if err := h.repo.UpsertPolicy(r.Context(), policy); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "merchant_id": merchantID})
}
