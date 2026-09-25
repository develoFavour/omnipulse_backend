package handler

import (
	"encoding/json"
	"net/http"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type TemplateHandler struct {
	useCase domain.TemplateUseCase
}

func NewTemplateHandler(useCase domain.TemplateUseCase) *TemplateHandler {
	return &TemplateHandler{useCase: useCase}
}

// ListTemplates handles: GET /api/v1/templates?category=...
func (h *TemplateHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	category := r.URL.Query().Get("category")

	templates, err := h.useCase.ListTemplates(r.Context(), tenantID, category)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to list templates: "+err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, templates)
}

// GetTemplate handles: GET /api/v1/templates/{id}
func (h *TemplateHandler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing template id")
		return
	}

	template, err := h.useCase.GetTemplate(r.Context(), tenantID, id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "Template not found: "+err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, template)
}

// CreateTemplate handles: POST /api/v1/templates
func (h *TemplateHandler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var req struct {
		Title     string   `json:"title"`
		Category  string   `json:"category"`
		Body      string   `json:"body"`
		MediaURL  *string  `json:"media_url"`
		Variables []string `json:"variables"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
		return
	}

	template, err := h.useCase.CreateTemplate(
		r.Context(),
		tenantID,
		req.Title,
		req.Category,
		req.Body,
		req.MediaURL,
		req.Variables,
	)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, template)
}

// UpdateTemplate handles: PUT /api/v1/templates/{id}
func (h *TemplateHandler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing template id")
		return
	}

	var req struct {
		Title     string   `json:"title"`
		Category  string   `json:"category"`
		Body      string   `json:"body"`
		MediaURL  *string  `json:"media_url"`
		Variables []string `json:"variables"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
		return
	}

	template, err := h.useCase.UpdateTemplate(
		r.Context(),
		tenantID,
		id,
		req.Title,
		req.Category,
		req.Body,
		req.MediaURL,
		req.Variables,
	)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, template)
}

// DeleteTemplate handles: DELETE /api/v1/templates/{id}
func (h *TemplateHandler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing template id")
		return
	}

	if err := h.useCase.DeleteTemplate(r.Context(), tenantID, id); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to delete template: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
