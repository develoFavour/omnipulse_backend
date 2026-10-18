package handler

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
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
	subRepo domain.SubscriptionRepository
}

func NewCampaignHandler(useCase *usecase.CampaignUseCase, subRepo domain.SubscriptionRepository) *CampaignHandler {
	return &CampaignHandler{useCase: useCase, subRepo: subRepo}
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
	status := queryParams.Get("status")

	campaigns, err := h.useCase.ListCampaigns(r.Context(), tenantID, status, page, pageSize)
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
		if errors.Is(err, domain.ErrQuotaExceeded) {
			utils.WriteError(w, http.StatusPaymentRequired, err.Error())
			return
		}
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

// SubmitForApproval handles: POST /api/v1/campaigns/{id}/submit
func (h *CampaignHandler) SubmitForApproval(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	userID, _ := r.Context().Value(UserIDKey).(string)

	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing campaign ID")
		return
	}

	if err := h.useCase.SubmitForApproval(r.Context(), tenantID, campaignID, userID); err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Campaign not found")
			return
		}
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{
		"message":     "Campaign successfully submitted for approval",
		"campaign_id": campaignID,
		"status":      "pending_approval",
	})
}

// ApproveCampaign handles: POST /api/v1/campaigns/{id}/approve
func (h *CampaignHandler) ApproveCampaign(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	userID, _ := r.Context().Value(UserIDKey).(string)

	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing campaign ID")
		return
	}

	if err := h.useCase.ApproveCampaign(r.Context(), tenantID, campaignID, userID); err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Campaign not found")
			return
		}
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{
		"message":     "Campaign approved successfully",
		"campaign_id": campaignID,
		"status":      "approved",
	})
}

// RejectCampaign handles: POST /api/v1/campaigns/{id}/reject
func (h *CampaignHandler) RejectCampaign(w http.ResponseWriter, r *http.Request) {
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
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)

	if err := h.useCase.RejectCampaign(r.Context(), tenantID, campaignID, payload.Reason); err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Campaign not found")
			return
		}
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{
		"message":     "Campaign has been rejected",
		"campaign_id": campaignID,
		"status":      "rejected",
	})
}

// GetCampaignReport handles: GET /api/v1/campaigns/{id}/report
func (h *CampaignHandler) GetCampaignReport(w http.ResponseWriter, r *http.Request) {
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

	report, err := h.useCase.GetCampaignReport(r.Context(), tenantID, campaignID)
	if err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Campaign not found")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to compile campaign report")
		return
	}

	utils.WriteJSON(w, http.StatusOK, report)
}

// ExportCampaignReport handles: GET /api/v1/campaigns/{id}/export
func (h *CampaignHandler) ExportCampaignReport(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	// Gate CSV streaming exports for Growth and Agency tiers
	if h.subRepo != nil {
		sub, err := h.subRepo.GetByTenantID(r.Context(), tenantID)
		if err == nil && sub != nil && sub.PlanTier == domain.PlanStarter {
			utils.WriteError(w, http.StatusPaymentRequired, "Campaign audit export requires a Growth or Agency subscription plan.")
			return
		}
	}

	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing campaign ID")
		return
	}

	report, err := h.useCase.GetCampaignReport(r.Context(), tenantID, campaignID)
	if err != nil {
		if errors.Is(err, repository.ErrCampaignNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Campaign not found")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to compile campaign report")
		return
	}

	fileName := fmt.Sprintf("campaign-report-%s-%s.csv", campaignID[:8], time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// UTF-8 BOM
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(w)

	// Section 1: Executive Campaign Summary
	_ = writer.Write([]string{"EXECUTIVE CAMPAIGN SUMMARY"})
	_ = writer.Write([]string{"Campaign ID", report.Campaign.ID})
	_ = writer.Write([]string{"Title", report.Campaign.Title})
	_ = writer.Write([]string{"Status", report.Campaign.Status})
	_ = writer.Write([]string{"Created At", report.Campaign.CreatedAt.UTC().Format(time.RFC3339)})
	_ = writer.Write([]string{"Total Targets", strconv.Itoa(report.Stats.TotalTargets)})
	_ = writer.Write([]string{"Delivered", strconv.Itoa(report.Stats.Delivered)})
	_ = writer.Write([]string{"Failed", strconv.Itoa(report.Stats.Failed)})
	_ = writer.Write([]string{"Delivery Rate %", fmt.Sprintf("%.1f%%", report.DeliveryRate)})
	_ = writer.Write([]string{"Read Rate %", fmt.Sprintf("%.1f%%", report.ReadRate)})
	_ = writer.Write([]string{}) // blank separator

	// Section 2: Detailed Delivery Log
	_ = writer.Write([]string{"DETAILED DELIVERY LOG"})
	_ = writer.Write([]string{"Delivery ID", "Destination", "Platform", "Status", "Error Trace", "Timestamp (UTC)"})

	for _, d := range report.Deliveries {
		errMsg := ""
		if d.ErrorMessage != nil {
			errMsg = *d.ErrorMessage
		}
		_ = writer.Write([]string{
			d.ID,
			d.RoutingValue,
			d.Platform,
			d.Status,
			errMsg,
			d.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	writer.Flush()
}
