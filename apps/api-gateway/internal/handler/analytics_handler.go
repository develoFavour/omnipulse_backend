package handler

import (
	"net/http"
	"strconv"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type AnalyticsHandler struct {
	useCase domain.AnalyticsUseCase
}

func NewAnalyticsHandler(useCase domain.AnalyticsUseCase) *AnalyticsHandler {
	return &AnalyticsHandler{useCase: useCase}
}

// GetReport handles: GET /api/v1/analytics?days=30
func (h *AnalyticsHandler) GetReport(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}

	report, err := h.useCase.GetReport(r.Context(), tenantID, days)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to compile aggregate analytics: "+err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, report)
}
