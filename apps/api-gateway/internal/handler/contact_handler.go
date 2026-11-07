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
	"omnipulse/apps/api-gateway/internal/utils"
)

// ContactHandler wraps our business domain port to handle REST network routing
type ContactHandler struct {
	useCase domain.ContactUseCase
	subRepo domain.SubscriptionRepository
}

// NewContactHandler is our explicit dependency injection constructor for transport control
func NewContactHandler(useCase domain.ContactUseCase, subRepo domain.SubscriptionRepository) *ContactHandler {
	return &ContactHandler{useCase: useCase, subRepo: subRepo}
}

// GetContact handles: GET /api/v1/contacts/{id}
func (h *ContactHandler) GetContact(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	id := r.PathValue("id")
	contact, err := h.useCase.FetchContact(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, domain.ErrContactNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Target audience member not found")
			return
		}
		if errors.Is(err, domain.ErrInvalidContact) {
			utils.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "An unexpected data execution issue occurred")
		return
	}

	utils.WriteJSON(w, http.StatusOK, contact)
}

// DeleteContact handles: DELETE /api/v1/contacts/{id}
//
// Erasure is anonymize-now, hard-delete-later: the recipient's PII is blanked
// and they are opted out immediately, the row survives (delivery history
// intact) until the retention sweep permanently deletes it after
// domain.AnonymizedRetention, and the response carries delete_after_at so the
// UI can show the operator a live countdown.
func (h *ContactHandler) DeleteContact(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	id := r.PathValue("id")
	contact, err := h.useCase.DeleteRecipient(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, domain.ErrContactNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Target audience member not found")
			return
		}
		if errors.Is(err, domain.ErrInvalidContact) {
			utils.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("[ContactHandler] erase failed for tenant=%s contact=%s: %v\n", tenantID, id, err)
		utils.WriteError(w, http.StatusInternalServerError, "Failed to erase audience member")
		return
	}

	utils.WriteJSON(w, http.StatusOK, contact)
}

// CreateContact handles: POST /api/v1/contacts
func (h *ContactHandler) CreateContact(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var payload domain.Contact
	r.Body = http.MaxBytesReader(w, r.Body, 1048576) // 1MB cap

	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&payload); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid JSON structure")
		return
	}

	payload.TenantID = tenantID // Securely bind to the authorized tenant context

	err := h.useCase.RegisterContact(r.Context(), &payload)
	if err != nil {
		if errors.Is(err, domain.ErrQuotaExceeded) {
			utils.WriteError(w, http.StatusPaymentRequired, err.Error())
			return
		}
		if errors.Is(err, domain.ErrInvalidContact) {
			utils.WriteError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to provision new audience model")
		return
	}

	utils.WriteJSON(w, http.StatusCreated, payload)
}

// ImportContacts handles: POST /api/v1/contacts/import
//
// Accepts a parsed list (paste or CSV) with an optional consent decision per
// row. Rows are validated independently, so one bad line does not reject the
// whole file; the response reports what was created, skipped, or rejected.
func (h *ContactHandler) ImportContacts(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 5<<20) // 5MB cap for a 1,000-row import

	var req domain.ContactImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid import payload")
		return
	}

	summary, err := h.useCase.ImportContacts(r.Context(), tenantID, req)
	if err != nil {
		if errors.Is(err, domain.ErrQuotaExceeded) {
			utils.WriteError(w, http.StatusPaymentRequired, err.Error())
			return
		}
		if errors.Is(err, domain.ErrInvalidContact) {
			utils.WriteError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to import contacts")
		return
	}

	utils.WriteJSON(w, http.StatusOK, summary)
}

// ListContacts handles: GET /api/v1/contacts
func (h *ContactHandler) ListContacts(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	queryParams := r.URL.Query()
	page, _ := strconv.Atoi(queryParams.Get("page"))
	pageSize, _ := strconv.Atoi(queryParams.Get("pageSize"))
	channelFilter := queryParams.Get("channel")
	tagID := queryParams.Get("tagId")
	if tagID == "" {
		tagID = queryParams.Get("tag")
	}
	search := queryParams.Get("search")
	sortKey := queryParams.Get("sort")
	sortDir := queryParams.Get("sortDir")

	filter := domain.ContactFilter{
		Channel:  channelFilter,
		TagID:    tagID,
		Search:   search,
		SortKey:  sortKey,
		SortDir:  sortDir,
		Page:     page,
		PageSize: pageSize,
	}

	contacts, err := h.useCase.GetAllContacts(r.Context(), tenantID, filter)
	if err != nil {
		log.Printf("[ContactHandler] ListContacts error for tenant %s filter=%+v: %v\n", tenantID, filter, err)
		utils.WriteError(w, http.StatusInternalServerError, "Error streaming query collection results")
		return
	}

	utils.WriteJSON(w, http.StatusOK, contacts)
}

// ExportContacts handles: GET /api/v1/contacts/export
func (h *ContactHandler) ExportContacts(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	// Gate CSV streaming exports for Growth and Agency tiers
	if h.subRepo != nil {
		sub, err := h.subRepo.GetByTenantID(r.Context(), tenantID)
		if err == nil && sub != nil && sub.PlanTier == domain.PlanStarter {
			utils.WriteError(w, http.StatusPaymentRequired, "CSV streaming exports require a Growth or Agency subscription plan.")
			return
		}
	}

	queryParams := r.URL.Query()
	channelFilter := queryParams.Get("channel")
	tagID := queryParams.Get("tagId")
	if tagID == "" {
		tagID = queryParams.Get("tag")
	}
	search := queryParams.Get("search")

	filter := domain.ContactFilter{
		Channel: channelFilter,
		TagID:   tagID,
		Search:  search,
	}

	fileName := fmt.Sprintf("messagerail-audience-%s.csv", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Accel-Buffering", "no")

	// Prepend UTF-8 BOM for Excel compatibility
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	csvWriter := csv.NewWriter(w)

	headers := []string{
		"Contact ID",
		"First Name",
		"Last Name",
		"Platform Channel",
		"Routing Value / Address",
		"Acquisition Source",
		"Status",
		"Assigned Tags",
		"Date Added (UTC)",
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
	_ = h.useCase.StreamContacts(r.Context(), tenantID, filter, func(c *domain.Contact, tagNames string) error {
		row := []string{
			c.ID,
			c.FirstName,
			c.LastName,
			c.Channel,
			c.RoutingValue,
			c.Source,
			c.Status,
			tagNames,
			c.CreatedAt.UTC().Format(time.RFC3339),
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
