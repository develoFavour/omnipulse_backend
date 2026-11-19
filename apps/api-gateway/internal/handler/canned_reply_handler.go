package handler

import (
	"encoding/json"
	"net/http"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type CannedReplyHandler struct {
	useCase domain.CannedReplyUseCase
}

func NewCannedReplyHandler(useCase domain.CannedReplyUseCase) *CannedReplyHandler {
	return &CannedReplyHandler{useCase: useCase}
}

func (h *CannedReplyHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	replies, err := h.useCase.List(r.Context(), tenantID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to list canned replies: "+err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, replies)
}

func (h *CannedReplyHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	userID, _ := r.Context().Value(UserIDKey).(string)

	var body struct {
		Shortcut string `json:"shortcut"`
		Title    string `json:"title"`
		Body     string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	reply, err := h.useCase.Create(r.Context(), tenantID, body.Shortcut, body.Title, body.Body, userID)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusCreated, reply)
}

func (h *CannedReplyHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing canned reply id")
		return
	}
	var body struct {
		Shortcut string `json:"shortcut"`
		Title    string `json:"title"`
		Body     string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	reply, err := h.useCase.Update(r.Context(), tenantID, id, body.Shortcut, body.Title, body.Body)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, reply)
}

func (h *CannedReplyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing canned reply id")
		return
	}
	if err := h.useCase.Delete(r.Context(), tenantID, id); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to delete canned reply: "+err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}
