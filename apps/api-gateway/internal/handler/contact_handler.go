package handler

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

// ContactHandler wraps our business domain port to handle REST network routing
type ContactHandler struct {
	useCase domain.ContactUseCase
}

// NewContactHandler is our explicit dependency injection constructor for transport control
func NewContactHandler(useCase domain.ContactUseCase) *ContactHandler {
	return &ContactHandler{useCase: useCase}
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
		if errors.Is(err, domain.ErrInvalidContact) {
			utils.WriteError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to provision new audience model")
		return
	}

	utils.WriteJSON(w, http.StatusCreated, payload)
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
