package handler

import (
	"net/http"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type TelegramDestinationHandler struct {
	repo domain.TelegramDestinationRepository
}

func NewTelegramDestinationHandler(repo domain.TelegramDestinationRepository) *TelegramDestinationHandler {
	return &TelegramDestinationHandler{repo: repo}
}

func (h *TelegramDestinationHandler) ListDestinations(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	destinations, err := h.repo.ListByTenant(r.Context(), tenantID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to list Telegram destinations")
		return
	}

	if destinations == nil {
		destinations = []domain.TelegramDestination{}
	}
	utils.WriteJSON(w, http.StatusOK, destinations)
}
