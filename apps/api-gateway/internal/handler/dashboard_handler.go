package handler

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/usecase"
)

type DashboardHandler struct {
	useCase *usecase.DashboardUseCase
}

func NewDashboardHandler(useCase *usecase.DashboardUseCase) *DashboardHandler {
	return &DashboardHandler{useCase: useCase}
}

func (h *DashboardHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		http.Error(w, "Unauthorized: Missing Tenant ID", http.StatusUnauthorized)
		return
	}

	stats, err := h.useCase.GetStats(r.Context(), tenantID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (h *DashboardHandler) ListDeliveries(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		http.Error(w, "Unauthorized: Missing Tenant ID", http.StatusUnauthorized)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 10
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}

	offsetStr := r.URL.Query().Get("offset")
	offset := 0
	if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
		offset = o
	}

	deliveries, err := h.useCase.ListDeliveries(r.Context(), tenantID, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if deliveries == nil {
		deliveries = []domain.DashboardDeliveryActivity{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(deliveries)
}

// ExportDeliveries handles: GET /api/v1/deliveries/export
func (h *DashboardHandler) ExportDeliveries(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		http.Error(w, "Unauthorized: Missing Tenant ID", http.StatusUnauthorized)
		return
	}

	queryParams := r.URL.Query()
	statusFilter := queryParams.Get("status")
	platformFilter := queryParams.Get("platform")
	search := queryParams.Get("search")

	fileName := fmt.Sprintf("messagerail-activity-audit-%s.csv", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Accel-Buffering", "no")

	// Prepend UTF-8 BOM
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	csvWriter := csv.NewWriter(w)

	headers := []string{
		"Delivery ID",
		"Campaign Title",
		"Contact Name",
		"Destination Address",
		"Platform Channel",
		"Status",
		"Destination Type",
		"Error Trace",
		"Timestamp (UTC)",
	}
	if err := csvWriter.Write(headers); err != nil {
		return
	}
	csvWriter.Flush()

	flusher, hasFlusher := w.(http.Flusher)
	if hasFlusher {
		flusher.Flush()
	}

	rowCount := 0
	_ = h.useCase.StreamDeliveries(r.Context(), tenantID, statusFilter, platformFilter, search, func(act *domain.DashboardDeliveryActivity) error {
		row := []string{
			act.ID,
			act.CampaignName,
			act.ContactName,
			act.RoutingValue,
			act.Platform,
			act.Status,
			act.TargetType,
			act.ErrorMessage,
			act.CreatedAt.UTC().Format(time.RFC3339),
		}
		if err := csvWriter.Write(row); err != nil {
			return err
		}

		rowCount++
		if rowCount%100 == 0 {
			csvWriter.Flush()
			if hasFlusher {
				flusher.Flush()
			}
		}
		return nil
	})

	csvWriter.Flush()
	if hasFlusher {
		flusher.Flush()
	}
}
