package handler

import (
	"errors"
	"net/http"

	"omnipulse/apps/api-gateway/internal/repository"
	"omnipulse/apps/api-gateway/internal/usecase"
	"omnipulse/apps/api-gateway/internal/utils"
)

type CampaignHandler struct {
	useCase *usecase.CampaignUseCase
}

func NewCampaignHandler(useCase *usecase.CampaignUseCase) *CampaignHandler {
	return &CampaignHandler{useCase: useCase}
}

// DispatchCampaign handles: POST /api/v1/campaigns/{id}/dispatch
func (h *CampaignHandler) DispatchCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing explicit campaign mapping ID parameter")
		return
	}

	// Execute processing loop pipeline asynchronously or contextually
	err := h.useCase.TriggerDispatch(r.Context(), campaignID)
	if err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Target distribution campaign tracking context missing")
			return
		}
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Return a 202 Accepted status signifying the system accepted the request and is processing it
	utils.WriteJSON(w, http.StatusAccepted, map[string]string{
		"message":     "Campaign processing cycle successfully initialized",
		"campaign_id": campaignID,
	})
}
