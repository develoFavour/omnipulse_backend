package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type AIAgentHandler struct {
	repo domain.AIAgentRepository
}

func NewAIAgentHandler(repo domain.AIAgentRepository) *AIAgentHandler {
	return &AIAgentHandler{repo: repo}
}

// GetConfig handles GET /api/v1/ai-agent/config
func (h *AIAgentHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	cfg, err := h.repo.GetConfig(r.Context(), tenantID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, cfg)
}

// SaveConfig handles PUT /api/v1/ai-agent/config
func (h *AIAgentHandler) SaveConfig(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var req domain.AIAgentConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	req.TenantID = tenantID
	if req.BusinessHoursStart == "" {
		req.BusinessHoursStart = "09:00:00"
	}
	if req.BusinessHoursEnd == "" {
		req.BusinessHoursEnd = "17:00:00"
	}
	if req.Timezone == "" {
		req.Timezone = "Africa/Lagos"
	}
	if len(req.WorkDays) == 0 {
		req.WorkDays = []int{1, 2, 3, 4, 5}
	}
	if req.ConfidenceThreshold <= 0 {
		req.ConfidenceThreshold = 0.70
	}

	if err := h.repo.SaveConfig(r.Context(), &req); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "AI assistant configuration saved successfully",
		"config":  req,
	})
}

// ListKnowledgeItems handles GET /api/v1/ai-agent/knowledge
func (h *AIAgentHandler) ListKnowledgeItems(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	items, err := h.repo.ListKnowledgeItems(r.Context(), tenantID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []domain.AIKnowledgeItem{}
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"items": items,
		"total": len(items),
	})
}

// CreateKnowledgeItem handles POST /api/v1/ai-agent/knowledge
func (h *AIAgentHandler) CreateKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var req domain.AIKnowledgeItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if strings.TrimSpace(req.QuestionOrTopic) == "" || strings.TrimSpace(req.AnswerOrContent) == "" {
		utils.WriteError(w, http.StatusBadRequest, "Question/topic and answer/content cannot be empty")
		return
	}

	if req.Category == "" {
		req.Category = "faq"
	}
	req.TenantID = tenantID
	req.IsActive = true

	if err := h.repo.CreateKnowledgeItem(r.Context(), &req); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, req)
}

// UpdateKnowledgeItem handles PUT /api/v1/ai-agent/knowledge/{id}
func (h *AIAgentHandler) UpdateKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	// Extract ID from URL path (e.g. /api/v1/ai-agent/knowledge/{id})
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 {
		utils.WriteError(w, http.StatusBadRequest, "Missing knowledge item ID")
		return
	}
	itemID := pathParts[len(pathParts)-1]

	var req domain.AIKnowledgeItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	req.ID = itemID
	req.TenantID = tenantID

	if err := h.repo.UpdateKnowledgeItem(r.Context(), &req); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, req)
}

// DeleteKnowledgeItem handles DELETE /api/v1/ai-agent/knowledge/{id}
func (h *AIAgentHandler) DeleteKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 {
		utils.WriteError(w, http.StatusBadRequest, "Missing knowledge item ID")
		return
	}
	itemID := pathParts[len(pathParts)-1]

	if err := h.repo.DeleteKnowledgeItem(r.Context(), tenantID, itemID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Knowledge item deleted successfully"})
}

type ToggleConversationAIRequest struct {
	AIState         string `json:"ai_state"` // "active", "paused_by_human"
	ClearNeedsHuman bool   `json:"clear_needs_human"`
}

// ToggleConversationAI handles POST /api/v1/ai-agent/conversations/{id}/toggle
func (h *AIAgentHandler) ToggleConversationAI(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 5 {
		utils.WriteError(w, http.StatusBadRequest, "Missing conversation ID")
		return
	}
	conversationID := pathParts[len(pathParts)-2]

	var req ToggleConversationAIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	var pausedUntil *time.Time
	if req.AIState == "paused_by_human" {
		t := time.Now().Add(30 * time.Minute)
		pausedUntil = &t
	}

	needsHuman := false
	if !req.ClearNeedsHuman {
		// keep existing status or set false
		needsHuman = false
	}

	if err := h.repo.SetConversationAIState(r.Context(), tenantID, conversationID, req.AIState, pausedUntil, needsHuman); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"conversation_id": conversationID,
		"ai_state":        req.AIState,
		"paused_until":    pausedUntil,
	})
}
