package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

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

// GetCampaign handles: GET /api/v1/campaigns/{id}
func (h *CampaignHandler) GetCampaign(w http.ResponseWriter, r *http.Request) {
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

	campaign, err := h.useCase.GetCampaign(r.Context(), tenantID, campaignID)
	if err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Campaign not found")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to retrieve campaign details")
		return
	}

	utils.WriteJSON(w, http.StatusOK, campaign)
}

// DispatchCampaign handles: POST /api/v1/campaigns/{id}/dispatch
func (h *CampaignHandler) DispatchCampaign(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		log.Println("[DISPATCH-TRACE] ❌ Missing tenant context in request")
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	campaignID := r.PathValue("id")
	if campaignID == "" {
		log.Println("[DISPATCH-TRACE] ❌ Missing campaign ID in URL path")
		utils.WriteError(w, http.StatusBadRequest, "Missing explicit campaign mapping ID parameter")
		return
	}

	log.Printf("[DISPATCH-TRACE] 📨 Received dispatch request: tenant=%s campaign=%s\n", tenantID, campaignID)

	err := h.useCase.TriggerDispatch(r.Context(), tenantID, campaignID)
	if err != nil {
		log.Printf("[DISPATCH-TRACE] ❌ TriggerDispatch returned error: %v\n", err)
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Target distribution campaign tracking context missing")
			return
		}
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[DISPATCH-TRACE] ✅ Dispatch completed successfully for campaign=%s\n", campaignID)
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

// GetCampaignDeliveries handles: GET /api/v1/campaigns/{id}/deliveries
func (h *CampaignHandler) GetCampaignDeliveries(w http.ResponseWriter, r *http.Request) {
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

	queryParams := r.URL.Query()
	page, _ := strconv.Atoi(queryParams.Get("page"))
	pageSize, _ := strconv.Atoi(queryParams.Get("pageSize"))

	deliveries, err := h.useCase.ListDeliveries(r.Context(), tenantID, campaignID, page, pageSize)
	if err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Campaign not found")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to fetch campaign delivery records")
		return
	}

	utils.WriteJSON(w, http.StatusOK, deliveries)
}

// ScheduleCampaign handles: POST /api/v1/campaigns/{id}/schedule
func (h *CampaignHandler) ScheduleCampaign(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing campaign ID")
		return
	}

	var payload struct {
		ScheduledAt string `json:"scheduled_at"` // RFC3339 / ISO8601
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	scheduledAt, err := time.Parse(time.RFC3339, payload.ScheduledAt)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid scheduled_at format — use ISO8601 / RFC3339")
		return
	}

	if err := h.useCase.ScheduleCampaign(r.Context(), tenantID, campaignID, scheduledAt); err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Campaign not found")
			return
		}
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{
		"message":      "Campaign scheduled successfully",
		"campaign_id":  campaignID,
		"scheduled_at": scheduledAt.UTC().Format(time.RFC3339),
	})
}

// CancelScheduledCampaign handles: DELETE /api/v1/campaigns/{id}/schedule
func (h *CampaignHandler) CancelScheduledCampaign(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing campaign ID")
		return
	}

	if err := h.useCase.CancelScheduledCampaign(r.Context(), tenantID, campaignID); err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Scheduled campaign not found")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to cancel scheduled campaign")
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{
		"message":     "Scheduled campaign cancelled — reverted to draft",
		"campaign_id": campaignID,
	})
}
