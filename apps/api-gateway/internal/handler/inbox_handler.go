package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/usecase"
	"omnipulse/apps/api-gateway/internal/utils"
)

type InboxHandler struct {
	inboxUseCase usecase.InboxUseCase
}

func NewInboxHandler(inboxUseCase usecase.InboxUseCase) *InboxHandler {
	return &InboxHandler{inboxUseCase: inboxUseCase}
}

func (h *InboxHandler) ListConversations(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	offset, _ := strconv.Atoi(query.Get("offset"))

	filter := domain.ConversationFilter{
		Channel:    query.Get("channel"),
		Status:     query.Get("status"),
		Search:     query.Get("search"),
		AssignedTo: query.Get("assigned_to"),
		NeedsHuman: query.Get("needs_attention") == "true",
		Limit:      limit,
		Offset:     offset,
	}

	conversations, total, err := h.inboxUseCase.ListConversations(r.Context(), tenantID, filter)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"conversations": conversations,
		"total":         total,
		"limit":         filter.Limit,
		"offset":        filter.Offset,
	})
}

func (h *InboxHandler) GetConversation(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	conversationID := r.PathValue("id")
	if conversationID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing conversation id")
		return
	}

	conv, err := h.inboxUseCase.GetConversation(r.Context(), tenantID, conversationID)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, conv)
}

func (h *InboxHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	conversationID := r.PathValue("id")
	if conversationID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing conversation id")
		return
	}

	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	offset, _ := strconv.Atoi(query.Get("offset"))

	messages, err := h.inboxUseCase.ListMessages(r.Context(), tenantID, conversationID, limit, offset)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"messages": messages,
	})
}

func (h *InboxHandler) SendReply(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	userID, _ := r.Context().Value(UserIDKey).(string)

	conversationID := r.PathValue("id")
	if conversationID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing conversation id")
		return
	}

	var req domain.SendReplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Body == "" && (req.MediaURL == nil || *req.MediaURL == "") {
		utils.WriteError(w, http.StatusBadRequest, "Message body or media URL is required")
		return
	}

	msg, err := h.inboxUseCase.SendReply(r.Context(), tenantID, userID, conversationID, req)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, msg)
}

func (h *InboxHandler) UpdateConversation(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	conversationID := r.PathValue("id")
	if conversationID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing conversation id")
		return
	}

	var req struct {
		Status     *domain.ConversationStatus `json:"status,omitempty"`
		AssignedTo json.RawMessage            `json:"assigned_to,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Status != nil {
		if err := h.inboxUseCase.UpdateConversationStatus(r.Context(), tenantID, conversationID, *req.Status); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if len(req.AssignedTo) > 0 {
		var assignedTo *string
		if strings.TrimSpace(string(req.AssignedTo)) != "null" {
			var assignee string
			if err := json.Unmarshal(req.AssignedTo, &assignee); err != nil || strings.TrimSpace(assignee) == "" {
				utils.WriteError(w, http.StatusBadRequest, "assigned_to must be a workspace member ID or null")
				return
			}
			assignedTo = &assignee
		}
		if err := h.inboxUseCase.AssignConversation(r.Context(), tenantID, conversationID, assignedTo); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// BulkAssignConversations assigns or unassigns selected conversations atomically.
func (h *InboxHandler) BulkAssignConversations(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var req struct {
		ConversationIDs []string        `json:"conversation_ids"`
		AssignedTo      json.RawMessage `json:"assigned_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(req.ConversationIDs) == 0 {
		utils.WriteError(w, http.StatusBadRequest, "Select at least one conversation")
		return
	}

	var assignedTo *string
	if len(req.AssignedTo) > 0 && strings.TrimSpace(string(req.AssignedTo)) != "null" {
		var assignee string
		if err := json.Unmarshal(req.AssignedTo, &assignee); err != nil || strings.TrimSpace(assignee) == "" {
			utils.WriteError(w, http.StatusBadRequest, "assigned_to must be a workspace member ID or null")
			return
		}
		assignedTo = &assignee
	}

	if err := h.inboxUseCase.BulkAssignConversations(r.Context(), tenantID, req.ConversationIDs, assignedTo); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "updated",
		"updated_count": len(req.ConversationIDs),
	})
}

func (h *InboxHandler) MarkAsRead(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	conversationID := r.PathValue("id")
	if conversationID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing conversation id")
		return
	}

	if err := h.inboxUseCase.MarkConversationAsRead(r.Context(), tenantID, conversationID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "marked_as_read"})
}

func (h *InboxHandler) EditMessage(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	messageID := r.PathValue("msgID")
	if messageID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing message id")
		return
	}

	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Body == "" {
		utils.WriteError(w, http.StatusBadRequest, "body field is required")
		return
	}

	updated, err := h.inboxUseCase.EditMessage(r.Context(), tenantID, messageID, req.Body)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, updated)
}

func (h *InboxHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	messageID := r.PathValue("msgID")
	if messageID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing message id")
		return
	}

	if err := h.inboxUseCase.DeleteMessage(r.Context(), tenantID, messageID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
