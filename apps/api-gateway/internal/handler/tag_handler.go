package handler

import (
	"encoding/json"
	"net/http"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type TagHandler struct {
	useCase domain.TagUseCase
}

func NewTagHandler(useCase domain.TagUseCase) *TagHandler {
	return &TagHandler{useCase: useCase}
}

// ListTags handles: GET /api/v1/tags
func (h *TagHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	tags, err := h.useCase.ListTags(r.Context(), tenantID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to fetch tags: "+err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, tags)
}

// CreateTag handles: POST /api/v1/tags
func (h *TagHandler) CreateTag(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	tag, err := h.useCase.CreateTag(r.Context(), tenantID, req.Name, req.Color)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, tag)
}

// DeleteTag handles: DELETE /api/v1/tags/{id}
func (h *TagHandler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	tagID := r.PathValue("id")
	if tagID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing tag ID")
		return
	}

	if err := h.useCase.DeleteTag(r.Context(), tenantID, tagID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to delete tag: "+err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Tag deleted successfully"})
}

// UpdateTag handles: PUT /api/v1/tags/{id}
func (h *TagHandler) UpdateTag(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	tagID := r.PathValue("id")
	if tagID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing tag ID")
		return
	}

	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	tag, err := h.useCase.UpdateTag(r.Context(), tenantID, tagID, req.Name, req.Color)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to update tag: "+err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, tag)
}

// TagContact handles: POST /api/v1/contacts/{id}/tags
func (h *TagHandler) TagContact(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	contactID := r.PathValue("id")
	var req struct {
		TagID string `json:"tag_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if err := h.useCase.TagContact(r.Context(), tenantID, contactID, req.TagID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to assign tag: "+err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Tag assigned successfully"})
}

// UntagContact handles: DELETE /api/v1/contacts/{id}/tags/{tag_id}
func (h *TagHandler) UntagContact(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	contactID := r.PathValue("id")
	tagID := r.PathValue("tag_id")

	if err := h.useCase.UntagContact(r.Context(), tenantID, contactID, tagID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to remove tag: "+err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Tag removed successfully"})
}

// BulkTagContacts handles: POST /api/v1/tags/{id}/bulk-assign
// Body: { "action": "assign"|"remove", "contact_ids": ["uuid1","uuid2",...] }
func (h *TagHandler) BulkTagContacts(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	tagID := r.PathValue("id")
	if tagID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing tag ID")
		return
	}

	var req struct {
		Action     string   `json:"action"`
		ContactIDs []string `json:"contact_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if err := h.useCase.BulkTagContacts(r.Context(), tenantID, tagID, req.ContactIDs, req.Action); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"message": "Bulk tag operation completed",
		"count":   len(req.ContactIDs),
		"action":  req.Action,
	})
}
