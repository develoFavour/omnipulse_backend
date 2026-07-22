package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

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

	contacts, err := h.useCase.GetAllContacts(r.Context(), tenantID, channelFilter, page, pageSize)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Error streaming query collection results")
		return
	}

	utils.WriteJSON(w, http.StatusOK, contacts)
}
