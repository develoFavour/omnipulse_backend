package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"omnipulse/apps/api-gateway/internal/domain"
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

// CreateCampaign handles: POST /api/v1/campaigns
func (h *CampaignHandler) CreateCampaign(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var payload domain.Campaign
	r.Body = http.MaxBytesReader(w, r.Body, 1048576) // 1MB cap

	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&payload); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid JSON structure")
		return
	}

	payload.TenantID = tenantID // Securely bind to the authorized tenant context

	err := h.useCase.CreateCampaign(r.Context(), &payload)
	if err != nil {
		utils.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, payload)
}

// ListCampaigns handles: GET /api/v1/campaigns
func (h *CampaignHandler) ListCampaigns(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	queryParams := r.URL.Query()
	page, _ := strconv.Atoi(queryParams.Get("page"))
	pageSize, _ := strconv.Atoi(queryParams.Get("pageSize"))

	campaigns, err := h.useCase.ListCampaigns(r.Context(), tenantID, page, pageSize)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Error streaming campaign collection results")
		return
	}

	utils.WriteJSON(w, http.StatusOK, campaigns)
}

// DispatchCampaign handles: POST /api/v1/campaigns/{id}/dispatch
func (h *CampaignHandler) DispatchCampaign(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing explicit campaign mapping ID parameter")
		return
	}

	err := h.useCase.TriggerDispatch(r.Context(), tenantID, campaignID)
	if err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Target distribution campaign tracking context missing")
			return
		}
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusAccepted, map[string]string{
		"message":     "Campaign processing cycle successfully initialized",
		"campaign_id": campaignID,
	})
}

func (h *CampaignHandler) GetCampaignStats(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing explicit campaign mapping ID parameter")
		return
	}

	stats, err := h.useCase.GetStats(r.Context(), tenantID, campaignID)
	if err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Target analytical campaign context missing")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to compile live dashboard telemetry data")
		return
	}

	utils.WriteJSON(w, http.StatusOK, stats)
}
