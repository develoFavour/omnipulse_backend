package handler

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/repository"
	"omnipulse/apps/api-gateway/internal/usecase"
	"omnipulse/apps/api-gateway/internal/utils"
)

type ReviewHandler struct {
	useCase *usecase.ReviewUseCase
}

func NewReviewHandler(useCase *usecase.ReviewUseCase) *ReviewHandler {
	return &ReviewHandler{useCase: useCase}
}

type createReviewLinkRequest struct {
	Label         string `json:"label"`
	ExpiresInDays int    `json:"expires_in_days"`
}

type reviewDecisionRequest struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

type sendReportEmailRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// CreateReviewLink handles POST /api/v1/campaigns/{id}/review-link (owner/admin).
func (h *ReviewHandler) CreateReviewLink(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing campaign ID")
		return
	}
	userID, _ := r.Context().Value(UserIDKey).(string)

	var payload createReviewLinkRequest
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	link, err := h.useCase.CreateLink(r.Context(), tenantID, campaignID, payload.Label, userID, payload.ExpiresInDays)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusCreated, link)
}

// ListReviewLinks handles GET /api/v1/campaigns/{id}/review-links (owner/admin).
func (h *ReviewHandler) ListReviewLinks(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing campaign ID")
		return
	}

	links, err := h.useCase.ListLinks(r.Context(), tenantID, campaignID)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"links": links})
}

// RevokeReviewLink handles DELETE /api/v1/campaigns/{id}/review-links/{linkID} (owner/admin).
func (h *ReviewHandler) RevokeReviewLink(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	campaignID := r.PathValue("id")
	linkID := r.PathValue("linkID")
	if campaignID == "" || linkID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing campaign or link ID")
		return
	}

	if err := h.useCase.RevokeLink(r.Context(), tenantID, campaignID, linkID); err != nil {
		writeReviewError(w, err)
		return
	}
	utils.WriteNoContent(w)
}

// SendReportEmail handles POST /api/v1/campaigns/{id}/report-email (owner/admin).
// It mints a fresh link and emails it to the brand — a review request before the
// campaign is sent, the report afterwards. The link is also returned so the
// sender can share it directly.
func (h *ReviewHandler) SendReportEmail(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	campaignID := r.PathValue("id")
	if campaignID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing campaign ID")
		return
	}
	userID, _ := r.Context().Value(UserIDKey).(string)

	var payload sendReportEmailRequest
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	link, err := h.useCase.EmailBrandLink(r.Context(), tenantID, campaignID, payload.Email, payload.Name, userID)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"sent_to":    strings.TrimSpace(payload.Email),
		"token":      link.Token,
		"label":      link.Label,
		"expires_at": link.ExpiresAt,
	})
}

// GetPublicReview handles GET /api/v1/public/reviews/{token} (public).
func (h *ReviewHandler) GetPublicReview(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	view, err := h.useCase.GetPublicReview(r.Context(), token)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, http.StatusOK, view)
}

// PostReviewDecision handles POST /api/v1/public/reviews/{token}/decision (public).
func (h *ReviewHandler) PostReviewDecision(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing review token")
		return
	}

	var payload reviewDecisionRequest
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	view, err := h.useCase.Decide(r.Context(), token, payload.Decision, payload.Note, clientIP(r))
	if err != nil {
		writeReviewError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, view)
}

// GetReviewOutcomeCSV handles GET /api/v1/public/reviews/{token}/outcome.csv (public).
// It contains aggregate numbers only, never contact rows.
func (h *ReviewHandler) GetReviewOutcomeCSV(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	report, brand, err := h.useCase.GetOutcomeReport(r.Context(), token)
	if err != nil {
		writeReviewError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="campaign-outcome-%s.csv"`, report.CampaignID))

	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{"field", "value"})
	clientName := ""
	if brand != nil {
		clientName = brand.Name
	}
	rows := [][]string{
		{"brand", clientName},
		{"campaign", report.Title},
		{"status", report.Status},
		{"total_targets", strconv.Itoa(report.TotalTargets)},
		{"sent", strconv.Itoa(report.Sent)},
		{"delivered", strconv.Itoa(report.Delivered)},
		{"failed", strconv.Itoa(report.Failed)},
		{"delivery_rate_pct", strconv.FormatFloat(report.DeliveryRate, 'f', 1, 64)},
		{"generated_at", report.GeneratedAt.Format("2006-01-02 15:04:05 MST")},
	}
	for _, row := range rows {
		if err := cw.Write(row); err != nil {
			log.Printf("[REVIEW] failed to write outcome CSV for campaign %s: %v\n", report.CampaignID, err)
			return
		}
	}
}

func writeReviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrReviewLinkNotFound):
		utils.WriteError(w, http.StatusNotFound, "This review link is invalid, expired, or has been revoked")
	case errors.Is(err, domain.ErrReviewLinkExpired):
		utils.WriteError(w, http.StatusGone, "This review link has expired")
	case errors.Is(err, domain.ErrReviewAlreadyDecided):
		utils.WriteError(w, http.StatusConflict, "This review has already been submitted")
	case errors.Is(err, domain.ErrReviewNotReviewable):
		utils.WriteError(w, http.StatusConflict, "This campaign has moved past client review")
	case errors.Is(err, domain.ErrReviewOutcomeUnavailable):
		utils.WriteError(w, http.StatusConflict, "Outcome report is not available until the campaign starts sending")
	case errors.Is(err, domain.ErrReviewNotShareable):
		utils.WriteError(w, http.StatusConflict, "A rejected campaign cannot be shared with a brand")
	case errors.Is(err, domain.ErrReviewInvalidRecipient):
		utils.WriteError(w, http.StatusBadRequest, "Enter a valid email address")
	case errors.Is(err, domain.ErrReviewInvalidDecision):
		utils.WriteError(w, http.StatusBadRequest, "Decision must be 'approve' or 'request_changes'")
	case errors.Is(err, domain.ErrReviewNoteRequired):
		utils.WriteError(w, http.StatusBadRequest, "Tell the agency what should change")
	case errors.Is(err, repository.ErrCampaignNotFound):
		utils.WriteError(w, http.StatusNotFound, "Campaign not found")
	default:
		log.Printf("[REVIEW] request failed: %v\n", err)
		utils.WriteError(w, http.StatusInternalServerError, "Review request failed")
	}
}

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first := strings.TrimSpace(strings.Split(forwarded, ",")[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
